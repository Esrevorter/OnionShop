-- 0004_support_messaging.sql — support ticketing and private messaging
-- (ARCHITECTURE.md §7–8). Message/ticket POSTs additionally require CSRF +
-- single-use CAPTCHA tokens; CAPTCHA state lives in Redis, not here.
BEGIN;

-- ---------------------------------------------------------------------------
-- Support tickets (§7)
-- ---------------------------------------------------------------------------
CREATE TABLE tickets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    order_ref   UUID REFERENCES orders(id),            -- optional link to an order
    category    TEXT NOT NULL CHECK (category IN
                ('general','order_problem','vendor_report','payment_issue',
                 'bug_bounty','account_recovery')),
    subject     TEXT NOT NULL CHECK (char_length(subject) BETWEEN 3 AND 200),
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN
                ('open','answered','user_replied','resolved','closed')),
    priority    TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN
                ('low','normal','high','urgent')),
    assigned_to UUID REFERENCES users(id),             -- staff member who claimed it
    dispute_id  UUID REFERENCES disputes(id),          -- set when escalated (§7)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tickets_status_idx  ON tickets(status, priority, updated_at);
CREATE INDEX tickets_user_idx    ON tickets(user_id, created_at DESC);
CREATE INDEX tickets_assigned_idx ON tickets(assigned_to) WHERE assigned_to IS NOT NULL;

CREATE TABLE ticket_messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id  UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL CHECK (char_length(body) <= 10000),
    is_staff   BOOLEAN NOT NULL DEFAULT false,          -- denormalized from author role
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ticket_messages_ticket_idx ON ticket_messages(ticket_id, created_at);

-- ---------------------------------------------------------------------------
-- Private messaging (§8)
-- ---------------------------------------------------------------------------
CREATE TABLE threads (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    participant_a UUID NOT NULL REFERENCES users(id),
    participant_b UUID NOT NULL REFERENCES users(id),
    order_ref     UUID REFERENCES orders(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (participant_a < participant_b)               -- one thread per unordered pair
);

CREATE UNIQUE INDEX threads_pair_idx ON threads(participant_a, participant_b);

CREATE TABLE messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id  UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    sender_id  UUID NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 5000),
    -- URLs inside body are rendered as inert text by the template layer; never auto-linked.
    reported   BOOLEAN NOT NULL DEFAULT false,          -- "report message" -> abuse ticket
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX messages_thread_idx ON messages(thread_id, created_at);
CREATE INDEX messages_unread_idx ON messages(thread_id, read_at) WHERE read_at IS NULL;

COMMIT;
