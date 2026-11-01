-- db/query/tickets.sql — support ticketing (ARCHITECTURE.md §7). Ticket POSTs
-- additionally require CSRF + single-use CAPTCHA tokens; CAPTCHA state lives in
-- Redis, not here (migration 0004).

-- name: CreateTicket :one
INSERT INTO tickets (user_id, order_ref, category, subject, priority)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTicket :one
SELECT * FROM tickets WHERE id = $1;

-- name: ListUserTickets :many
SELECT * FROM tickets
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListOpenTickets :many
-- Staff queue: highest priority first, oldest activity on top. Backed by
-- tickets_status_idx (status, priority, updated_at).
SELECT * FROM tickets
WHERE status IN ('open','user_replied')
ORDER BY
    CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
    updated_at
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: UpdateTicketStatus :exec
UPDATE tickets SET status = $2, updated_at = now() WHERE id = $1;

-- name: AssignTicket :exec
UPDATE tickets SET assigned_to = $2, status = 'answered', updated_at = now()
WHERE id = $1 AND assigned_to IS NULL;

-- name: EscalateTicketToDispute :exec
UPDATE tickets SET dispute_id = $2, status = 'open', updated_at = now() WHERE id = $1;

-- name: AddTicketMessage :one
INSERT INTO ticket_messages (ticket_id, author_id, body, is_staff)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListTicketMessages :many
SELECT * FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at;
