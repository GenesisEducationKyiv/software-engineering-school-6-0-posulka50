CREATE TABLE IF NOT EXISTS subscriptions (
    id                TEXT PRIMARY KEY,
    email             TEXT NOT NULL,
    repo              TEXT NOT NULL,
    confirmed         BOOLEAN NOT NULL DEFAULT FALSE,
    confirm_token     TEXT NOT NULL UNIQUE,
    unsubscribe_token TEXT NOT NULL UNIQUE,
    last_seen_tag     TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscriptions_email_repo_unique UNIQUE (email, repo)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_email             ON subscriptions (email);
CREATE INDEX IF NOT EXISTS idx_subscriptions_confirmed         ON subscriptions (confirmed);
CREATE INDEX IF NOT EXISTS idx_subscriptions_confirm_token     ON subscriptions (confirm_token);
CREATE INDEX IF NOT EXISTS idx_subscriptions_unsubscribe_token ON subscriptions (unsubscribe_token);
