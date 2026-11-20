package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ThreadRow struct {
	ID        pgtype.UUID
	OtherID   pgtype.UUID
	OtherName string
	HasOrder  bool
	LastBody  string
	LastAt    time.Time
	Unread    int64
}

type MessageRow struct {
	ID        pgtype.UUID
	SenderID  pgtype.UUID
	Sender    string
	Body      string
	CreatedAt time.Time
	Mine      bool
}

func orderedPair(a, b pgtype.UUID) (pgtype.UUID, pgtype.UUID) {
	if uuidString(a) < uuidString(b) {
		return a, b
	}
	return b, a
}

// EnsureThread returns the pair thread, creating it if needed and attaching the
// order reference on first use.
func EnsureThread(ctx context.Context, pool *pgxpool.Pool, a, b, orderRef pgtype.UUID) (pgtype.UUID, error) {
	p, q := orderedPair(a, b)
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO threads (participant_a, participant_b, order_ref)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id`, p, q, orderRef).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("server: create thread: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM threads WHERE participant_a=$1 AND participant_b=$2`, p, q).Scan(&id); err != nil {
		return pgtype.UUID{}, fmt.Errorf("server: load thread: %w", err)
	}
	if orderRef.Valid {
		_, _ = pool.Exec(ctx, `UPDATE threads SET order_ref=$1 WHERE id=$2 AND order_ref IS NULL`, orderRef, id)
	}
	return id, nil
}

func AppendMessage(ctx context.Context, pool *pgxpool.Pool, threadID, senderID pgtype.UUID, body string) error {
	_, err := pool.Exec(ctx, `INSERT INTO messages (thread_id, sender_id, body) VALUES ($1,$2,$3)`, threadID, senderID, body)
	if err != nil {
		return fmt.Errorf("server: append message: %w", err)
	}
	return nil
}

func ListThreadsForUser(ctx context.Context, pool *pgxpool.Pool, uid pgtype.UUID) ([]ThreadRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id, o.id, o.username, t.order_ref IS NOT NULL,
		       COALESCE((SELECT m.body FROM messages m WHERE m.thread_id=t.id ORDER BY m.created_at DESC LIMIT 1),''),
		       COALESCE((SELECT m.created_at FROM messages m WHERE m.thread_id=t.id ORDER BY m.created_at DESC LIMIT 1), t.created_at),
		       (SELECT COUNT(*) FROM messages m WHERE m.thread_id=t.id AND m.sender_id <> $1 AND m.read_at IS NULL)
		FROM threads t
		JOIN users o ON o.id = (CASE WHEN t.participant_a = $1 THEN t.participant_b ELSE t.participant_a END)
		WHERE t.participant_a = $1 OR t.participant_b = $1
		ORDER BY 6 DESC`, uid)
	if err != nil {
		return nil, fmt.Errorf("server: list threads: %w", err)
	}
	defer rows.Close()
	var out []ThreadRow
	for rows.Next() {
		var t ThreadRow
		if err := rows.Scan(&t.ID, &t.OtherID, &t.OtherName, &t.HasOrder, &t.LastBody, &t.LastAt, &t.Unread); err != nil {
			return nil, fmt.Errorf("server: scan thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func ThreadParticipant(ctx context.Context, pool *pgxpool.Pool, threadID, uid pgtype.UUID) (bool, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM threads WHERE id=$1 AND (participant_a=$2 OR participant_b=$2)`, threadID, uid).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func ListThreadMessages(ctx context.Context, pool *pgxpool.Pool, threadID, viewer pgtype.UUID) ([]MessageRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.id, m.sender_id, u.username, m.body, m.created_at
		FROM messages m JOIN users u ON u.id = m.sender_id
		WHERE m.thread_id=$1 ORDER BY m.created_at`, threadID)
	if err != nil {
		return nil, fmt.Errorf("server: list messages: %w", err)
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var sid pgtype.UUID
		if err := rows.Scan(&m.ID, &sid, &m.Sender, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("server: scan message: %w", err)
		}
		m.SenderID = sid
		m.Mine = uuidString(sid) == uuidString(viewer)
		out = append(out, m)
	}
	return out, rows.Err()
}

func MarkThreadRead(ctx context.Context, pool *pgxpool.Pool, threadID, reader pgtype.UUID) {
	_, _ = pool.Exec(ctx, `UPDATE messages SET read_at = now() WHERE thread_id=$1 AND sender_id <> $2 AND read_at IS NULL`, threadID, reader)
}

func CountUnreadForUser(ctx context.Context, pool *pgxpool.Pool, uid pgtype.UUID) int64 {
	var n int64
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM messages m JOIN threads t ON t.id = m.thread_id
		WHERE (t.participant_a=$1 OR t.participant_b=$1) AND m.sender_id <> $1 AND m.read_at IS NULL`, uid).Scan(&n)
	return n
}
