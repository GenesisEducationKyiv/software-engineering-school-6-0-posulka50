# GitHub Release Notifier

A Go service that lets users subscribe to email notifications whenever a new release appears on any GitHub repository.

**Live demo:** https://github-notifier-case.posulka.site

---

## Architecture

For architecture, data model, sequence diagrams, and design decisions see [docs/system-design-document.md](docs/system-design-document.md).

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
| `API_KEY` | _(empty)_ | When set, write and list endpoints require `X-API-Key` header |

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
