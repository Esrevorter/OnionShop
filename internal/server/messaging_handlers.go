package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MessagesData struct {
	AuthData
	Threads []ThreadRow
}

type ThreadData struct {
	AuthData
	ThreadID  string
	OtherName string
	HasOrder  bool
	Messages  []MessageRow
}

func (a *App) registerMessagingRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/messages", anyUser(http.HandlerFunc(a.handleMessagesInbox)))
	r.Method(http.MethodGet, "/messages/start", anyUser(http.HandlerFunc(a.handleMessageStart)))
	r.Method(http.MethodGet, "/messages/{id}", anyUser(http.HandlerFunc(a.handleMessageThread)))
	r.Method(http.MethodPost, "/messages/{id}/reply", anyUser(http.HandlerFunc(a.handleMessageReply)))
}

func (a *App) msgPool() (*pgxpool.Pool, bool) {
	p, ok := a.deps.Pool.(*pgxpool.Pool)
	return p, ok
}

func (a *App) handleMessagesInbox(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.msgPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	threads, err := ListThreadsForUser(r.Context(), pool, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	a.renderAs(w, "messages", http.StatusOK, MessagesData{AuthData: a.baseData(r), Threads: threads})
}

// handleMessageStart opens (or creates) the thread for an order and redirects
// into it. Participants are derived from the order so neither side can spoof.
func (a *App) handleMessageStart(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.msgPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	orderStr := strings.TrimSpace(r.URL.Query().Get("order"))
	orderID, ok := parseUUID(orderStr)
	if !ok {
		http.Redirect(w, r, "/messages", http.StatusSeeOther)
		return
	}
	var customer, vendorUser pgtype.UUID
	err := pool.QueryRow(r.Context(), `
		SELECT o.customer_id, v.user_id
		FROM orders o JOIN vendors v ON v.id = o.vendor_id
		WHERE o.id=$1 AND (o.customer_id=$2 OR v.user_id=$2)`, orderID, uid).Scan(&customer, &vendorUser)
	if err != nil {
		http.Redirect(w, r, "/messages", http.StatusSeeOther)
		return
	}
	other := vendorUser
	if uuidString(uid) == uuidString(vendorUser) {
		other = customer
	}
	tid, err := EnsureThread(r.Context(), pool, uid, other, orderID)
	if err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/messages/"+uuidString(tid), http.StatusSeeOther)
}

func (a *App) handleMessageThread(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.msgPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tid, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mine, err := ThreadParticipant(r.Context(), pool, tid, uid)
	if err != nil || !mine {
		http.NotFound(w, r)
		return
	}
	msgs, err := ListThreadMessages(r.Context(), pool, tid, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	MarkThreadRead(r.Context(), pool, tid, uid)
	var otherName string
	var hasOrder bool
	_ = pool.QueryRow(r.Context(), `
		SELECT o.username, t.order_ref IS NOT NULL
		FROM threads t JOIN users o ON o.id = (CASE WHEN t.participant_a=$2 THEN t.participant_b ELSE t.participant_a END)
		WHERE t.id=$1`, tid, uid).Scan(&otherName, &hasOrder)
	a.renderAs(w, "message_thread", http.StatusOK, ThreadData{
		AuthData: a.baseData(r), ThreadID: uuidString(tid), OtherName: otherName, HasOrder: hasOrder, Messages: msgs,
	})
}

func (a *App) handleMessageReply(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.msgPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tid, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mine, err := ThreadParticipant(r.Context(), pool, tid, uid)
	if err != nil || !mine {
		http.NotFound(w, r)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "message") {
		http.Redirect(w, r, "/messages/"+uuidString(tid), http.StatusSeeOther)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if len(body) < 1 || len(body) > 5000 {
		http.Redirect(w, r, "/messages/"+uuidString(tid), http.StatusSeeOther)
		return
	}
	if err := AppendMessage(r.Context(), pool, tid, uid, body); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/messages/"+uuidString(tid), http.StatusSeeOther)
}
