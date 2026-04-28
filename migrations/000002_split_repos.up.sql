CREATE TABLE IF NOT EXISTS repositories (
    id            TEXT PRIMARY KEY,
    full_name     TEXT NOT NULL UNIQUE,
    last_seen_tag TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_repositories_full_name ON repositories (full_name);

-- Migrate existing repos; MAX(last_seen_tag) to avoid re-notifying already-seen releases
INSERT INTO repositories (id, full_name, last_seen_tag, created_at)
SELECT gen_random_uuid()::TEXT, repo, MAX(last_seen_tag), MIN(created_at)
FROM subscriptions
GROUP BY repo
ON CONFLICT (full_name) DO NOTHING;

ALTER TABLE subscriptions ADD COLUMN repo_id TEXT REFERENCES repositories(id) ON DELETE CASCADE;

UPDATE subscriptions s
SET repo_id = r.id
FROM repositories r
WHERE s.repo = r.full_name;

ALTER TABLE subscriptions ALTER COLUMN repo_id SET NOT NULL;

ALTER TABLE subscriptions DROP COLUMN repo;
ALTER TABLE subscriptions DROP COLUMN last_seen_tag;

CREATE INDEX IF NOT EXISTS idx_subscriptions_repo_id ON subscriptions (repo_id);
