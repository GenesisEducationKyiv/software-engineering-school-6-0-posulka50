# GitHub Release Notifier

A Go service that lets users subscribe to email notifications whenever a new release appears on any GitHub repository.

**Live demo:** https://github-notifier-case.posulka.site

---

## Architecture

Single-process monolith with three logical components:

| Component | Responsibility |
|-----------|---------------|
| **API** | HTTP REST endpoints (Gin) — subscription management |
| **Scanner** | Background goroutine — periodically polls GitHub for new releases |
| **Notifier** | Sends HTML emails via Resend HTTP API (confirmation + release alerts) |

```
cmd/server/main.go          ← wires everything together
internal/
  config/                   ← env-based configuration
  model/                    ← data models
  repository/               ← PostgreSQL data access
  github/                   ← GitHub REST API client (with Redis cache)
  email/                    ← Resend HTTP email sender
  service/
    subscription.go         ← business logic: subscribe / confirm / unsubscribe
    scanner.go              ← release polling loop
  handler/                  ← HTTP handlers (one file per endpoint)
  middleware/               ← API key auth, Prometheus metrics
migrations/                 ← SQL migrations (golang-migrate)
static/                     ← single-page web UI
```

---

## API

Follows the Swagger contract at `swagger.yaml`.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/subscribe` | Subscribe an email to a GitHub repository |
| `GET` | `/api/confirm/:token` | Confirm subscription via emailed token |
| `GET` | `/api/unsubscribe/:token` | Unsubscribe via token |
| `GET` | `/api/subscriptions?email=` | List confirmed subscriptions for an email |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Prometheus metrics |

### Error codes

| HTTP | Reason |
|------|--------|
| 400 | Invalid email or invalid `owner/repo` format |
| 404 | GitHub repository not found |
| 409 | Email already subscribed to this repository |
| 429 | GitHub API rate limit exceeded |

### Examples

**Subscribe:**
```bash
curl -X POST https://github-notifier-case.posulka.site/api/subscribe \
  -H "Content-Type: application/json" \
  -d '{"email":"you@example.com","repo":"golang/go"}'
```

**Get subscriptions:**
```bash
curl "https://github-notifier-case.posulka.site/api/subscriptions?email=you@example.com"
```

**Confirm** (sent via email link):
```
GET /api/confirm/{token}
```

**Unsubscribe** (link in every release email):
```
GET /api/unsubscribe/{token}
```

---

## How it works

### Subscription flow
1. `POST /api/subscribe` — validates input, checks GitHub API for repo existence, creates an unconfirmed record, sends a confirmation email.
2. `GET /api/confirm/:token` — sets `confirmed=true`. Only confirmed subscriptions receive release notifications.
3. `GET /api/unsubscribe/:token` — deletes the subscription. Every release email contains an unsubscribe link.

### Release scanning
- A background goroutine ticks every `SCAN_INTERVAL` (default `1h`).
- On each tick it fetches all repos with confirmed subscribers and calls GitHub API once per repo.
- **First scan:** stores `last_seen_tag` silently — no email sent, preventing a flood of old releases on signup.
- **New release detected:** sends notification to all confirmed subscribers, then updates `last_seen_tag` for the repo (single DB write regardless of subscriber count).
- **Rate limit hit (429):** scan stops early and resumes on the next tick.

### Redis caching
GitHub API responses are cached in Redis (TTL 10 minutes):
- `github:repo:{owner}/{repo}` — repo existence check (cached: repo existence doesn't change between requests)

`GetLatestRelease` is intentionally **not cached** — the scanner must always fetch fresh data from GitHub to detect new releases correctly. Caching releases would cause the scanner to miss new releases if `SCAN_INTERVAL` is less than the cache TTL.

Service works without Redis — caching gracefully disabled if unavailable.

### Email
HTML emails are sent via [Resend](https://resend.com) HTTP API — no SMTP ports required. Templates use GitHub dark theme and include:
- **Confirmation email** — button + fallback link to confirm subscription
- **Release notification** — tag, release name, notes, link to GitHub, unsubscribe link

---

## Quick start

### With Docker Compose

```bash
cp .env.example .env
# fill in RESEND_API_KEY, EMAIL_FROM, and optionally GITHUB_TOKEN
docker compose up --build
```

### Local development

```bash
docker compose up db redis -d

cp .env.example .env
go run ./cmd/server
```

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgres://...localhost/github_notifier` | PostgreSQL DSN |
| `REDIS_URL` | `redis://localhost:6379` | Redis URL (optional, caching) |
| `GITHUB_TOKEN` | _(empty)_ | GitHub PAT — raises rate limit from 60 to 5 000 req/h |
| `RESEND_API_KEY` | — | [Resend](https://resend.com) API key |
| `EMAIL_FROM` | — | Verified sender address (e.g. `noreply@yourdomain.com`) |
| `BASE_URL` | `http://localhost:8080` | Used in confirmation/unsubscribe links |
| `SCAN_INTERVAL` | `1h` | Release polling interval (e.g. `10m`, `6h`) |
| `API_KEY` | _(empty)_ | Optional — `APIKeyAuth` middleware available for future internal routes |

---

## Running tests

```bash
go test ./...
# verbose + race detector:
go test -v -race ./...
```

Unit tests cover the full service layer (subscription logic + scanner) via in-memory mocks — no database or network required.

---

## Migrations

Run automatically on startup via [golang-migrate](https://github.com/golang-migrate/migrate).
Files in `migrations/` follow `{version}_{title}.{up|down}.sql` naming convention.

---

## Extras implemented

| Feature | Details |
|---------|---------|
| Live deployment | Railway — https://github-notifier-case.posulka.site |
| Web UI | Single-page subscription form at `/` |
| Redis caching | GitHub API responses cached with 10-min TTL |
| Prometheus metrics | `/metrics` endpoint |
| GitHub Actions CI | Lint + test + build on every push |
