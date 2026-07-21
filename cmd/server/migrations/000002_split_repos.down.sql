ALTER TABLE subscriptions ADD COLUMN repo TEXT;
ALTER TABLE subscriptions ADD COLUMN last_seen_tag TEXT;

UPDATE subscriptions s
SET repo = r.full_name, last_seen_tag = r.last_seen_tag
FROM repositories r
WHERE s.repo_id = r.id;

ALTER TABLE subscriptions ALTER COLUMN repo SET NOT NULL;

ALTER TABLE subscriptions DROP COLUMN repo_id;

DROP INDEX IF EXISTS idx_subscriptions_repo_id;
DROP INDEX IF EXISTS idx_repositories_full_name;
DROP TABLE IF EXISTS repositories;
