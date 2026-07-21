CREATE TABLE IF NOT EXISTS subscription_sagas (
    id              TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    state           TEXT NOT NULL CHECK (state IN ('pending','completed','compensated','timed_out')),
    last_error      TEXT,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_subscription_sagas_state_started ON subscription_sagas (state, started_at);
CREATE INDEX IF NOT EXISTS idx_subscription_sagas_subscription_id ON subscription_sagas (subscription_id);
