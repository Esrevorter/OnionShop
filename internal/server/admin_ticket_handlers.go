package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (a *App) registerAdminTicketRoutes(r chi.Router) {
	staff := a.RequireRole("mod", "admin")
	r.With(staff).Get("/admin/tickets", a.handleAdminTickets)
	r.With(staff).Get("/admin/tickets/{id}", a.handleAdminTicketDetail)
	r.With(staff).Post("/admin/tickets/{id}/reply", a.handleAdminTicketReply)
	r.With(staff).Post("/admin/tickets/{id}/resolve", a.handleAdminTicketResolve)
	r.With(staff).Post("/admin/tickets/{id}/close", a.handleAdminTicketClose)
}

func (a *App) handleAdminTickets(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	data := struct {
		AuthData
		Tickets []TicketRow
	}{AuthData: a.baseData(r)}
	rows, err := pool.Query(r.Context(), `
		SELECT t.id, t.subject, t.category, t.status, t.updated_at
		FROM tickets t ORDER BY t.updated_at DESC LIMIT 200`)
	if err != nil {
		log.Printf("server: admin tickets list failed: %v", err)
		a.renderAs(w, "admin_tickets", http.StatusOK, data)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t TicketRow
		var id interface{}
		var updated interface{}
		if err := rows.Scan(&id, &t.Subject, &t.Category, &t.Status, &updated); err != nil {
			continue
		}
		t.IDStr = formatUUID(id)
		t.UpdatedAt = formatTime(updated)
		data.Tickets = append(data.Tickets, t)
	}
	a.renderAs(w, "admin_tickets", http.StatusOK, data)
}

func (a *App) handleAdminTicketDetail(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.NotFound(w, r)
		return
	}
	data := struct {
		AuthData
		Ticket   TicketRow
		Messages []TicketMessageRow
	}{AuthData: a.baseData(r)}
	if data.Captcha = a.newChallenge(w, r, "admin"); data.Captcha == nil {
		return
	}
	var tid interface{}
	var updated interface{}
	err := pool.QueryRow(r.Context(), `
		SELECT id, subject, category, status, updated_at FROM tickets WHERE id=$1`, id).
		Scan(&tid, &data.Ticket.Subject, &data.Ticket.Category, &data.Ticket.Status, &updated)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.Ticket.IDStr = formatUUID(tid)
	data.Ticket.UpdatedAt = formatTime(updated)
	msgs, err := ListTicketMessages(r.Context(), pool, id)
	if err == nil {
		data.Messages = msgs
	}
	a.renderAs(w, "admin_ticket_detail", http.StatusOK, data)
}

func (a *App) handleAdminTicketReply(w http.ResponseWriter, r *http.Request) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/admin/tickets", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/tickets", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if len(body) < 1 || len(body) > 10000 {
		http.Redirect(w, r, "/admin/tickets/"+id.String(), http.StatusSeeOther)
		return
	}
	tx, err := pool.Begin(r.Context())
	if err != nil {
		writeGenericError(w)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO ticket_messages (ticket_id, author_id, body, is_staff) VALUES ($1,$2,$3,true)`, id, uid, body); err != nil {
		writeGenericError(w)
		return
	}
	var current string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM tickets WHERE id=$1`, id).Scan(&current); err != nil {
		writeGenericError(w)
		return
	}
	next, err := ApplyTicket(TicketStatus(current), TEventStaffAnswers)
	if err != nil {
		writeGenericError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE tickets SET status=$1, updated_at=now() WHERE id=$2`, string(next), id); err != nil {
		writeGenericError(w)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/tickets/"+id.String(), http.StatusSeeOther)
}

func (a *App) handleAdminTicketResolve(w http.ResponseWriter, r *http.Request) {
	a.adminTicketStaffEvent(w, r, TEventStaffResolves, "resolved")
}

func (a *App) handleAdminTicketClose(w http.ResponseWriter, r *http.Request) {
	a.adminTicketStaffEvent(w, r, TEventStaffCloses, "closed")
}

func (a *App) adminTicketStaffEvent(w http.ResponseWriter, r *http.Request, ev TicketEvent, action string) {
	pool, ok := a.adminPool()
	if !ok {
		writeGenericError(w)
		return
	}
	uid, ok := a.currentUUID(r)
	if !ok {
		http.Redirect(w, r, "/admin/tickets", http.StatusSeeOther)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "admin") {
		http.Redirect(w, r, "/admin/tickets", http.StatusSeeOther)
		return
	}
	id := parseUUIDText(chi.URLParam(r, "id"))
	if !id.Valid {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if RequiresReason(ev) && (len(reason) < 5 || len(reason) > 4000) {
		http.Error(w, "reason required (5-4000 chars)", http.StatusBadRequest)
		return
	}
	tx, err := pool.Begin(r.Context())
	if err != nil {
		writeGenericError(w)
		return
	}
	defer tx.Rollback(r.Context())
	var current string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM tickets WHERE id=$1`, id).Scan(&current); err != nil {
		http.NotFound(w, r)
		return
	}
	next, err := ApplyTicket(TicketStatus(current), ev)
	if err != nil {
		writeGenericError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE tickets SET status=$1, updated_at=now() WHERE id=$2`, string(next), id); err != nil {
		writeGenericError(w)
		return
	}
	if RequiresReason(ev) {
		_, _ = tx.Exec(r.Context(), `
			INSERT INTO audit_log (actor_id, actor_kind, action, target, details)
			VALUES ($1,'user',$2,$3,$4)`, uid, "ticket."+string(ev), "tickets:"+id.String(), reason)
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/admin/tickets/"+id.String(), http.StatusSeeOther)
}

