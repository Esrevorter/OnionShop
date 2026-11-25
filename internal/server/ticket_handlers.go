package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

var ticketCategories = map[string]bool{
	"general": true, "order_problem": true, "vendor_report": true,
	"payment_issue": true, "bug_bounty": true, "account_recovery": true,
}

type TicketsData struct {
	AuthData
	Tickets []TicketRow
}

type TicketDetailData struct {
	AuthData
	Ticket   TicketRow
	Messages []TicketMessageRow
}

func (a *App) registerTicketRoutes(r chi.Router) {
	anyUser := a.RequireRole("customer", "vendor", "mod", "admin")
	r.Method(http.MethodGet, "/tickets", anyUser(http.HandlerFunc(a.handleTicketsList)))
	r.Method(http.MethodGet, "/tickets/new", anyUser(http.HandlerFunc(a.handleTicketNew)))
	r.Method(http.MethodPost, "/tickets/new", anyUser(http.HandlerFunc(a.handleTicketCreate)))
	r.Method(http.MethodGet, "/tickets/{id}", anyUser(http.HandlerFunc(a.handleTicketDetail)))
	r.Method(http.MethodPost, "/tickets/{id}/reply", anyUser(http.HandlerFunc(a.handleTicketReply)))
	r.Method(http.MethodPost, "/tickets/{id}/close", anyUser(http.HandlerFunc(a.handleTicketClose)))
}

func (a *App) handleTicketsList(w http.ResponseWriter, r *http.Request) {
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
	tickets, err := ListUserTickets(r.Context(), pool, uid)
	if err != nil {
		writeGenericError(w)
		return
	}
	a.renderAs(w, "tickets", http.StatusOK, TicketsData{AuthData: a.baseData(r), Tickets: tickets})
}

func (a *App) handleTicketNew(w http.ResponseWriter, r *http.Request) {
	data := struct {
		AuthData
	}{AuthData: a.baseData(r)}
	if data.Captcha = a.newChallenge(w, r, "ticket"); data.Captcha == nil {
		return
	}
	a.renderAs(w, "ticket_new", http.StatusOK, data)
}

func (a *App) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
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
	if honeypotTriggered(r) || !a.requireFriction(w, r, "ticket") {
		http.Redirect(w, r, "/tickets/new", http.StatusSeeOther)
		return
	}
	category := strings.TrimSpace(r.FormValue("category"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := strings.TrimSpace(r.FormValue("body"))
	if !ticketCategories[category] || len(subject) < 3 || len(subject) > 200 || len(body) < 1 || len(body) > 10000 {
		http.Redirect(w, r, "/tickets/new", http.StatusSeeOther)
		return
	}
	id, err := CreateTicket(r.Context(), pool, uid, category, subject, body)
	if err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/tickets/"+uuidString(id), http.StatusSeeOther)
}

func (a *App) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
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
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ticket, err := GetUserTicket(r.Context(), pool, id, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msgs, err := ListTicketMessages(r.Context(), pool, id)
	if err != nil {
		writeGenericError(w)
		return
	}
	a.renderAs(w, "ticket_detail", http.StatusOK, TicketDetailData{AuthData: a.baseData(r), Ticket: *ticket, Messages: msgs})
}

func (a *App) handleTicketReply(w http.ResponseWriter, r *http.Request) {
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
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if honeypotTriggered(r) || !a.requireFriction(w, r, "ticket") {
		http.Redirect(w, r, "/tickets/"+uuidString(id), http.StatusSeeOther)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if len(body) < 1 || len(body) > 10000 {
		http.Redirect(w, r, "/tickets/"+uuidString(id), http.StatusSeeOther)
		return
	}
	if err := UserTicketEvent(r.Context(), pool, id, uid, body, TEventUserReplies); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/tickets/"+uuidString(id), http.StatusSeeOther)
}

func (a *App) handleTicketClose(w http.ResponseWriter, r *http.Request) {
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
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if honeypotTriggered(r) {
		http.Redirect(w, r, "/tickets/"+uuidString(id), http.StatusSeeOther)
		return
	}
	if err := UserTicketEvent(r.Context(), pool, id, uid, "", TEventUserCloses); err != nil {
		writeGenericError(w)
		return
	}
	http.Redirect(w, r, "/tickets", http.StatusSeeOther)
}
