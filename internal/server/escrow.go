package server

import (
	"errors"
	"fmt"
)

// Escrow state machine (ARCHITECTURE.md §4) as PURE logic — the highest-risk
// invariant in the marketplace lives here, decoupled from Postgres/BTCPay so
// CI can exhaustively test every transition without infrastructure
// (TASKS.md "Cross-Cutting": escrow transitions first).
//
// The DB enforces the same graph a second time: migration 0002's CHECK keeps
// status within this set, and Phase 6's service layer routes every write
// through NextEscrowState before touching SQL. Payment confirmation arrives by
// BTCPay POLLING (never webhooks — the hidden service is unreachable inbound,
// ARCHITECTURE.md §2); poll results are applied with idempotency keys, so a
// duplicate observation must be an accepted no-op rather than an error. That
// is why Apply(PaymentConfirmed) on a non-awaiting state returns (state, nil).

// EscrowState is one node of the machine; values match the orders.status
// CHECK constraint in db/migrations/0002_orders_escrow.up.sql verbatim.
type EscrowState string

const (
	StateAwaitingPayment EscrowState = "awaiting_payment"
	StateInEscrow        EscrowState = "in_escrow"
	StateShipped         EscrowState = "shipped"
	StateDisputed        EscrowState = "disputed"
	StateReleased        EscrowState = "released"
	StateRefunded        EscrowState = "refunded"
	StateCancelled       EscrowState = "cancelled"
)

// EscrowEvent drives a transition. Names describe intent, not transport:
// PaymentConfirmed comes from the poller, AutoRelease from the cron, etc.
type EscrowEvent string

const (
	EventPaymentConfirmed       EscrowEvent = "payment_confirmed"
	EventUnderpaymentObserved   EscrowEvent = "underpayment_observed"
	EventOverpaymentObserved    EscrowEvent = "overpayment_observed"
	EventCancelRequested        EscrowEvent = "cancel_requested" // unpaid expiry / pre-ship cancel
	EventVendorMarksShipped     EscrowEvent = "vendor_marks_shipped"
	EventCustomerConfirms       EscrowEvent = "customer_confirms"
	EventAutoRelease            EscrowEvent = "auto_release" // 14-day cron, UTC from Postgres
	EventDisputeOpened          EscrowEvent = "dispute_opened"
	EventDisputeResolvedRelease EscrowEvent = "dispute_resolved_release"
	EventDisputeResolvedRefund  EscrowEvent = "dispute_resolved_refund"
)

// ErrEscrowIllegalTransition marks a request the machine refuses. Handlers
// surface it as a generic 4xx; it must never be papered over, because every
// legal path is enumerated below.
var ErrEscrowIllegalTransition = errors.New("server: illegal escrow transition")

// IsTerminal reports whether no event can ever move the order again.
func (s EscrowState) IsTerminal() bool {
	return s == StateReleased || s == StateRefunded || s == StateCancelled
}

// escrowTransitions is the complete legal graph, straight from
// ARCHITECTURE.md §4 including the failure paths:
//
//	awaiting_payment -> in_escrow            (payment confirmed via poll)
//	awaiting_payment -> cancelled            (expiry timer, unpaid)
//	in_escrow        -> shipped              (vendor marks shipped)
//	in_escrow        -> refunded             (customer cancels before shipment)
//	in_escrow        -> disputed             (dispute opened)
//	shipped          -> released             (customer confirms OR 14-day cron)
//	shipped          -> disputed             (dispute opened)
//	disputed         -> released | refunded  (mod resolves either way)
var escrowTransitions = map[EscrowState]map[EscrowEvent]EscrowState{
	StateAwaitingPayment: {
		EventPaymentConfirmed:     StateInEscrow,
		EventUnderpaymentObserved: StateAwaitingPayment, // stays put: delta-invoice option (§4)
		EventCancelRequested:      StateCancelled,
	},
	StateInEscrow: {
		EventVendorMarksShipped: StateShipped,
		EventCancelRequested:    StateRefunded,
		EventDisputeOpened:      StateDisputed,
	},
	StateShipped: {
		EventCustomerConfirms: StateReleased,
		EventAutoRelease:      StateReleased,
		EventDisputeOpened:    StateDisputed,
	},
	StateDisputed: {
		EventDisputeResolvedRelease: StateReleased,
		EventDisputeResolvedRefund:  StateRefunded,
	},
	// Terminal states map to nothing explicit; Apply handles the two
	// idempotent replay events below before consulting this table.
	StateReleased:  {},
	StateRefunded:  {},
	StateCancelled: {},
}

// OverpaymentFlagged is the outcome attached to the overpayment observation:
// the state NEVER advances automatically (manual review only, §4), but the
// caller must know it needs human attention.
type OverpaymentFlagged struct {
	State EscrowState
}

// Apply computes the next state for (current, event). Rules that mirror the
// deployment reality:
//   - PaymentConfirmed on an already-confirmed order is an idempotent no-op
//     (duplicate/late poll replays happen; double-crediting must not);
//   - OverpaymentObserved never changes state — it flags for manual review;
//   - everything else must be an edge in escrowTransitions or it errors.
func Apply(current EscrowState, event EscrowEvent) (EscrowState, error) {
	if !IsValidState(current) || event == "" {
		return "", fmt.Errorf("%w: %q/%q", ErrEscrowIllegalTransition, current, event)
	}
	switch event {
	case EventOverpaymentObserved:
		if current == StateAwaitingPayment || current == StateInEscrow {
			return current, nil // flag for manual review, never auto-release
		}
		return "", fmt.Errorf("%w: %q on terminal %q", ErrEscrowIllegalTransition, event, current)
	case EventPaymentConfirmed:
		if current == StateInEscrow {
			return StateInEscrow, nil // poll replay: idempotent no-op
		}
	}
	if next, ok := escrowTransitions[current][event]; ok {
		return next, nil
	}
	return "", fmt.Errorf("%w: %q from %q", ErrEscrowIllegalTransition, event, current)
}

// LegalEvents lists the events accepted from one state (used by UIs to render
// only actionable buttons — plain forms, no JS guessing).
func LegalEvents(current EscrowState) []EscrowEvent {
	evs := make([]EscrowEvent, 0, len(escrowTransitions[current])+2)
	for e := range escrowTransitions[current] {
		evs = append(evs, e)
	}
	switch current {
	case StateAwaitingPayment, StateInEscrow:
		evs = append(evs, EventOverpaymentObserved)
	}
	return evs
}

// IsValidState reports membership in the orders.status CHECK set.
func IsValidState(s EscrowState) bool {
	switch s {
	case StateAwaitingPayment, StateInEscrow, StateShipped, StateDisputed,
		StateReleased, StateRefunded, StateCancelled:
		return true
	}
	return false
}
