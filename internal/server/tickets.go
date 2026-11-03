package server

import (
	"errors"
	"fmt"
)

// Support-ticket status transitions (ARCHITECTURE.md §7) as pure logic, in
// the same spirit as escrow.go: exhaustively unit-testable without a DB.
// The DB CHECK constraint on tickets.status (migration 0004) is the second
// line of defense; staff actions additionally require a reason that lands in
// the audit log — enforced by the Phase 7 handlers, enumerated here.
//
// Documented flow: open → answered → user_replied → resolved → closed.
// Practical shortcuts encoded below:
//   - a user can reply to an OPEN ticket too (first message may be the only
//     one; staff claiming it moves it forward via StaffAnswers);
//   - staff may answer repeatedly while waiting on the user;
//   - resolved → closed (user or expiry), but a user reply REOPENS a resolved
//     ticket (back to open) instead of silently dead-ending them;
//   - closed is terminal; escalation into a dispute is orthogonal (escrow
//     machine owns that), so no event here models it.

// TicketStatus matches the tickets.status CHECK set in migration 0004.
type TicketStatus string

const (
	TicketOpen        TicketStatus = "open"
	TicketAnswered    TicketStatus = "answered"
	TicketUserReplied TicketStatus = "user_replied"
	TicketResolved    TicketStatus = "resolved"
	TicketClosed      TicketStatus = "closed"
)

// TicketEvent names one actor action. Actor column distinguishes staff vs
// user rows in ticket_messages; events keep that authority check explicit.
type TicketEvent string

const (
	TEventStaffAnswers   TicketEvent = "staff_answers"   // requires claim/reason -> audit
	TEventUserReplies    TicketEvent = "user_replies"
	TEventStaffResolves  TicketEvent = "staff_resolves"  // mandatory reason
	TEventUserConfirms   TicketEvent = "user_confirms"   // user accepts resolution
	TEventUserCloses     TicketEvent = "user_closes"     // user withdraws
	TEventStaffCloses    TicketEvent = "staff_closes"    // spam/abuse path, mandatory reason
	TEventExpiryCloses   TicketEvent = "expiry_closes"   // cron: resolved untouched for N days
)

// ErrTicketIllegalTransition marks refused moves. Handlers render a generic
// 4xx and re-show the queue; nothing here ever auto-forced a status.
var ErrTicketIllegalTransition = errors.New("server: illegal ticket transition")

var ticketTransitions = map[TicketStatus]map[TicketEvent]TicketStatus{
	TicketOpen: {
		TEventStaffAnswers: TicketAnswered,
		TEventUserReplies:  TicketOpen, // still awaiting first staff answer
		TEventStaffCloses:  TicketClosed,
		TEventUserCloses:   TicketClosed,
	},
	TicketAnswered: {
		TEventUserReplies:   TicketUserReplied,
		TEventStaffAnswers:  TicketAnswered, // follow-up while waiting
		TEventStaffResolves: TicketResolved,
		TEventStaffCloses:   TicketClosed,
	},
	TicketUserReplied: {
		TEventStaffAnswers:  TicketAnswered,
		TEventStaffResolves: TicketResolved,
		TEventStaffCloses:   TicketClosed,
		TEventUserCloses:    TicketClosed,
	},
	TicketResolved: {
		TEventUserReplies:  TicketOpen, // reopen: never dead-end a paying customer
		TEventUserConfirms: TicketClosed,
		TEventExpiryCloses: TicketClosed,
		TEventStaffAnswers: TicketResolved, // informational note keeps state
	},
	TicketClosed: {}, // terminal
}

// ApplyTicket computes the next ticket status, or an error wrapping
// ErrTicketIllegalTransition. Unknown statuses/events fail closed.
func ApplyTicket(current TicketStatus, event TicketEvent) (TicketStatus, error) {
	evs, ok := ticketTransitions[current]
	if !ok || event == "" {
		return "", fmt.Errorf("%w: %q/%q", ErrTicketIllegalTransition, current, event)
	}
	next, ok := evs[event]
	if !ok {
		return "", fmt.Errorf("%w: %q from %q", ErrTicketIllegalTransition, event, current)
	}
	return next, nil
}

// IsStaffEvent reports which transitions need the staff role + a mandatory
// reason written to the audit log before they may fire (Phase 7 handlers call
// this BEFORE touching the DB).
func IsStaffEvent(e TicketEvent) bool {
	switch e {
	case TEventStaffAnswers, TEventStaffResolves, TEventStaffCloses:
		return true
	}
	return false
}

// RequiresReason lists events whose audit entry must carry a non-empty reason
// (SECURITY_AND_OPSEC.md §3: every privileged action is explainable later).
func RequiresReason(e TicketEvent) bool {
	switch e {
	case TEventStaffResolves, TEventStaffCloses:
		return true
	}
	return false
}

// IsValidTicketStatus reports membership in the tickets.status CHECK set.
func IsValidTicketStatus(s TicketStatus) bool {
	_, ok := ticketTransitions[s]
	return ok
}
