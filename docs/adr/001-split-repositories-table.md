# ADR-001: Extract repositories into a separate table

**Date:** xxxx-xx-xx  
**Status:** Accepted

---

## Context

The initial schema stored the full repository name (`repo TEXT`) and the last observed release tag (`last_seen_tag TEXT`) directly on the `subscriptions` table.

```
subscriptions
─────────────────────────────────────────────────────
id | email | repo         | last_seen_tag | confirmed …
─────────────────────────────────────────────────────
1  | a@x   | owner/repo-a | v1.2.0        | true
2  | b@x   | owner/repo-a | v1.2.0        | true
3  | c@x   | owner/repo-a | v1.2.0        | true
```

This layout caused three concrete problems.

**Redundant writes on each scan.** When the scanner detects a new release it must update `last_seen_tag` for every subscriber row of that repository, not once. For a repository with N confirmed subscribers, one release event produces N `UPDATE` statements touching unrelated rows.

**Risk of partial updates.** If the scanner updates subscriber rows individually and crashes mid-loop, some rows carry the new tag and others carry the old one. The next scan run sees mixed state and may re-notify part of the audience for the same release.

**Composite unique constraint couples entity concerns.** The `UNIQUE (email, repo)` constraint on `subscriptions` conflates two independent facts — who subscribed and which repository is tracked. Adding repository-level metadata (e.g. description, homepage, stars) would require denormalising it into every subscriber row.

---

## Decision

Introduce a dedicated `repositories` table and replace the `repo TEXT` column with a foreign key `repo_id TEXT REFERENCES repositories(id)`.

```
repositories
─────────────────────────────────────
id | full_name     | last_seen_tag
─────────────────────────────────────
r1 | owner/repo-a  | v1.2.0

subscriptions
──────────────────────────────────────────────────────────
id | email | repo_id | confirmed | confirm_token | …
──────────────────────────────────────────────────────────
1  | a@x   | r1      | true      | …
2  | b@x   | r1      | true      | …
3  | c@x   | r1      | true      | …
```

`last_seen_tag` is owned exclusively by `repositories`. The scanner issues a single `UPDATE repositories SET last_seen_tag = $1 WHERE id = $2` per repository regardless of subscriber count.

The migration (`000002_split_repos.up.sql`) handles the in-place transition:

1. Creates the `repositories` table.
2. Back-fills it from `subscriptions`, using `MAX(last_seen_tag)` per `repo` to avoid re-notifying already-seen releases.
3. Adds `repo_id` as a nullable column, populates it via a `JOIN`, then sets it `NOT NULL`.
4. Drops the now-redundant `repo` and `last_seen_tag` columns from `subscriptions`.

The migration is fully reversible (`000002_split_repos.down.sql`).

---

## Consequences

**Positive**

- One `UPDATE` per scan cycle per repository, independent of subscriber count.
- Eliminates the partial-update anomaly; `last_seen_tag` is an atomic single-row write.
- Repository metadata can be extended without touching `subscriptions`.
- `ON DELETE CASCADE` on the foreign key keeps orphaned rows from accumulating if a repository record is ever removed.

**Negative**

- Queries that need both subscriber details and the repository name now require a `JOIN`. All `SELECT` statements in `PostgresRepository` and `PostgresRepoRepository` were updated accordingly.
- The `GetOrCreate` upsert pattern (`ON CONFLICT (full_name) DO UPDATE`) must be called before a subscription can be inserted, adding one extra round-trip on the subscribe path.
