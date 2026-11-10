package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type DisputeRecord struct {
	ID         pgtype.UUID
	OrderID    pgtype.UUID
	OpenedBy   pgtype.UUID
	Reason     string
	Resolution *string
	ResolvedBy *pgtype.UUID
	ResolvedAt *time.Time
	CreatedAt  time.Time
}

type DisputeMessageRecord struct {
	ID        int64
	DisputeID pgtype.UUID
	AuthorID  pgtype.UUID
	Body      string
	CreatedAt time.Time
}

type DisputeStore interface {
	CreateDispute(ctx context.Context, orderID, openedBy pgtype.UUID, reason string) (*DisputeRecord, error)
	GetDispute(ctx context.Context, disputeID pgtype.UUID) (*DisputeRecord, error)
	ListDisputesByOrder(ctx context.Context, orderID pgtype.UUID) ([]DisputeRecord, error)
	AddDisputeMessage(ctx context.Context, disputeID, authorID pgtype.UUID, body string) error
	ListDisputeMessages(ctx context.Context, disputeID pgtype.UUID) ([]DisputeMessageRecord, error)
	ResolveDispute(ctx context.Context, disputeID, resolvedBy pgtype.UUID, resolution string) error
}

func (a *App) RegisterDisputeRoutes(r chi.Router, store DisputeStore) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	modOnly := a.RequireRole("mod", "admin")

	r.Method(http.MethodPost, "/orders/{id}/dispute", anyUser(http.HandlerFunc(a.handleOpenDispute)))
	r.Method(http.MethodGet, "/disputes/{id}", anyUser(http.HandlerFunc(a.handleViewDispute)))
	r.Method(http.MethodPost, "/disputes/{id}/messages", anyUser(http.HandlerFunc(a.handleAddDisputeMessage)))
	r.Method(http.MethodPost, "/disputes/{id}/resolve", modOnly(http.HandlerFunc(a.handleResolveDispute)))
}

func (a *App) handleOpenDispute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	orderIDStr := chi.URLParam(r, "id")
	reason := strings.TrimSpace(r.FormValue("reason"))
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil || reason == "" {
		writeGenericError(w)
		return
	}

	oid, ok := parseUUID(orderIDStr)
	if !ok {
		writeGenericError(w)
		return
	}

	if ds, ok := a.vendor.(DisputeStore); ok {
		_, err := ds.CreateDispute(ctx, oid, *uid, reason)
		if err != nil {
			writeGenericError(w)
			return
		}
	} else {
		writeGenericError(w)
		return
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (a *App) handleViewDispute(w http.ResponseWriter, r *http.Request) {
	data := a.baseData(r)
	a.render(w, "dispute", http.StatusOK, data)
}

func (a *App) handleAddDisputeMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	disputeIDStr := chi.URLParam(r, "id")
	body := strings.TrimSpace(r.FormValue("body"))
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil || body == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	did, ok := parseUUID(disputeIDStr)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if ds, ok := a.vendor.(DisputeStore); ok {
		if err := ds.AddDisputeMessage(ctx, did, *uid, body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/disputes/"+disputeIDStr, http.StatusSeeOther)
}

func (a *App) handleResolveDispute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	disputeIDStr := chi.URLParam(r, "id")
	resolution := r.FormValue("resolution") // released or refunded
	uid := currentUserUUID(ctx, a.sm)
	if uid == nil || (resolution != "released" && resolution != "refunded") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	did, ok := parseUUID(disputeIDStr)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if ds, ok := a.vendor.(DisputeStore); ok {
		if err := ds.ResolveDispute(ctx, did, *uid, resolution); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if a.auth != nil {
			_ = a.auth.AuditAppend(ctx, *uid, "user", "dispute.resolve", "disputes:"+disputeIDStr, `{"resolution":"`+resolution+`"}`)
		}
	}

	http.Redirect(w, r, "/disputes/"+disputeIDStr, http.StatusSeeOther)
}
