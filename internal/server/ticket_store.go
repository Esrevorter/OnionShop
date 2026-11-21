package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TicketRow struct {
	IDStr     string
	Subject   string
	Category  string
	Status    string
	UpdatedAt string
}

type TicketMessageRow struct {
	Author    string
	Body      string
	CreatedAt string
	IsStaff   bool
}

func CreateTicket(ctx context.Context, pool *pgxpool.Pool, userID pgtype.UUID, category, subject, body string) (pgtype.UUID, error) {
	var id pgtype.UUID
	tx, err := pool.Begin(ctx)
	if err != nil {
		return id, fmt.Errorf("server: begin ticket tx: %w", err)
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		INSERT INTO tickets (user_id, category, subject, status, priority)
		VALUES ($1,$2,$3,'open','normal') RETURNING id`, userID, category, subject).Scan(&id)
	if err != nil {
		return id, fmt.Errorf("server: insert ticket: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ticket_messages (ticket_id, author_id, body, is_staff)
		VALUES ($1,$2,$3,false)`, id, userID, body); err != nil {
		return id, fmt.Errorf("server: insert first ticket message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return id, fmt.Errorf("server: commit ticket: %w", err)
	}
	return id, nil
}

func ListUserTickets(ctx context.Context, pool *pgxpool.Pool, userID pgtype.UUID) ([]TicketRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, subject, category, status, updated_at
		FROM tickets WHERE user_id=$1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("server: list tickets: %w", err)
	}
	defer rows.Close()
	var out []TicketRow
	for rows.Next() {
		var t TicketRow
		var id pgtype.UUID
		var updated time.Time
		if err := rows.Scan(&id, &t.Subject, &t.Category, &t.Status, &updated); err != nil {
			return nil, fmt.Errorf("server: scan ticket: %w", err)
		}
		t.IDStr = uuidString(id)
		t.UpdatedAt = updated.Format("2006-01-02 15:04")
		out = append(out, t)
	}
	return out, rows.Err()
}

func GetUserTicket(ctx context.Context, pool *pgxpool.Pool, id, userID pgtype.UUID) (*TicketRow, error) {
	var t TicketRow
	var tid pgtype.UUID
	var updated time.Time
	err := pool.QueryRow(ctx, `
		SELECT id, subject, category, status, updated_at
		FROM tickets WHERE id=$1 AND user_id=$2`, id, userID).
		Scan(&tid, &t.Subject, &t.Category, &t.Status, &updated)
	if err != nil {
		return nil, err
	}
	t.IDStr = uuidString(tid)
	t.UpdatedAt = updated.Format("2006-01-02 15:04")
	return &t, nil
}

func ListTicketMessages(ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) ([]TicketMessageRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.username, m.body, m.created_at, m.is_staff
		FROM ticket_messages m JOIN users u ON u.id = m.author_id
		WHERE m.ticket_id=$1 ORDER BY m.created_at`, id)
	if err != nil {
		return nil, fmt.Errorf("server: list ticket messages: %w", err)
	}
	defer rows.Close()
	var out []TicketMessageRow
	for rows.Next() {
		var m TicketMessageRow
		var created time.Time
		if err := rows.Scan(&m.Author, &m.Body, &created, &m.IsStaff); err != nil {
			return nil, fmt.Errorf("server: scan ticket message: %w", err)
		}
		m.CreatedAt = created.Format("2006-01-02 15:04")
		out = append(out, m)
	}
	return out, rows.Err()
}

// UserTicketEvent appends a message and moves the status through the
// ApplyTicket state machine (tickets.go), keeping transitions authoritative.
func UserTicketEvent(ctx context.Context, pool *pgxpool.Pool, id, userID pgtype.UUID, body string, ev TicketEvent) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("server: begin ticket event tx: %w", err)
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM tickets WHERE id=$1 AND user_id=$2`, id, userID).Scan(&current); err != nil {
		return fmt.Errorf("server: load ticket status: %w", err)
	}
	next, err := ApplyTicket(TicketStatus(current), ev)
	if err != nil {
		return err
	}
	if body != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ticket_messages (ticket_id, author_id, body, is_staff)
			VALUES ($1,$2,$3,false)`, id, userID, body); err != nil {
			return fmt.Errorf("server: insert ticket message: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tickets SET status=$1, updated_at=now() WHERE id=$2`, string(next), id); err != nil {
		return fmt.Errorf("server: update ticket status: %w", err)
	}
	return tx.Commit(ctx)
}
