-- db/query/messages.sql — buyer↔vendor private messaging (ARCHITECTURE.md §8).
-- Threads are keyed by the unordered participant pair (threads_pair_idx); URLs
-- inside bodies are rendered as inert text by the template layer.

-- name: GetOrCreateThread :one
-- Participants must be passed ordered (a < b) to match the pair CHECK/index.
WITH ins AS (
    INSERT INTO threads (participant_a, participant_b, order_ref)
    VALUES ($1, $2, $3)
    ON CONFLICT (participant_a, participant_b) DO NOTHING
    RETURNING *
)
SELECT * FROM ins
UNION ALL
SELECT * FROM threads
WHERE participant_a = $1 AND participant_b = $2
LIMIT 1;

-- name: GetThread :one
SELECT * FROM threads WHERE id = $1;

-- name: ListUserThreads :many
SELECT t.* FROM threads t
WHERE t.participant_a = $1 OR t.participant_b = $1
ORDER BY t.created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: SendMessage :one
INSERT INTO messages (thread_id, sender_id, body)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListThreadMessages :many
SELECT * FROM messages WHERE thread_id = $1 ORDER BY created_at
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: MarkMessageRead :exec
UPDATE messages SET read_at = now()
WHERE id = $1 AND thread_id = $2 AND sender_id <> $3 AND read_at IS NULL;

-- name: CountUnreadMessages :one
SELECT count(*) AS unread FROM messages
WHERE thread_id = $1 AND read_at IS NULL AND sender_id <> $2;

-- name: ReportMessage :exec
UPDATE messages SET reported = true WHERE id = $1;
