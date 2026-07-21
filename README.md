# GitHub Release Notifier

A Go service that lets users subscribe to email notifications whenever a new release appears on any GitHub repository.

**Live demo:** https://github-notifier-case.posulka.site

---

## Architecture

For architecture, data model, sequence diagrams, and design decisions see [docs/system-design-document.md](docs/system-design-document.md).

### Subscribe saga

Confirmation email delivery spans two services (`app` orchestrator, `notifier`
participant) and is implemented as an orchestrated saga over RabbitMQ:

1. `POST /api/subscribe` inserts the subscription + a `pending` saga row, then
   publishes a `SendConfirmationCommand` and returns `202 Accepted`.
2. `notifier` consumes the command, calls Resend, and publishes a reply event
   (`confirmation_sent` or `confirmation_failed`) carrying the saga id.
3. `app` consumes the reply: on success the saga is marked `completed`; on
   failure the orchestrator **compensates** by deleting the orphaned pending
   subscription so a retry succeeds rather than colliding with a row the user
   can never confirm.
4. A timeout sweeper (`SAGA_TIMEOUT`, default `5m`) catches any saga left in
   `pending` long enough to count as a lost reply. Before compensating it
   first attempts one **synchronous gRPC retry** against
   `notifier.EmailNotifierService.SendConfirmation` (see ADR-003). On RPC
   success the saga is marked `completed` and the subscription is preserved;
   on failure the sweeper falls through to compensation. The notifier
   deduplicates by saga id in memory, so a retry that races the original
   async delivery does not send a second email.

Saga state lives in the `subscription_sagas` table and is independent of the
domain row; reply handlers are idempotent (`WHERE state='pending'` guard), so
broker redeliveries are no-ops.

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
| `POST` | `/api/subscribe` | Subscribe an email to a GitHub repository. Returns `202 Accepted` — confirmation email is dispatched asynchronously via the Subscribe saga (see below). |
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
| `BROKER_URL` | `amqp://guest:guest@localhost:5672/` | RabbitMQ connection string between app and notifier |
| `SAGA_TIMEOUT` | `5m` | How long a Subscribe saga may stay `pending` before the sweeper acts |
| `SAGA_SWEEP_INTERVAL` | `30s` | How often the sweeper scans for stuck sagas |
| `NOTIFIER_GRPC_ADDR` | `localhost:50051` | App-side address of the notifier's gRPC listener (sweeper sync-retry target) |
| `NOTIFIER_GRPC_PORT` | `50051` | Notifier-side listen port for the gRPC server |

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

## gRPC contract (buf)

The `app -> notifier` Subscribe-saga sync-retry call has a gRPC contract
defined in
[`proto/notifier/v1/notification.proto`](proto/notifier/v1/notification.proto)
and managed with [buf](https://buf.build). Generated Go code is committed to
`proto/gen/` so the repository builds without buf installed.

**Wiring.** The notifier exposes
`EmailNotifierService.SendConfirmation` on `NOTIFIER_GRPC_PORT`
(default `50051`) alongside its RabbitMQ consumer; both share an in-memory
dedupe cache keyed by `saga_id` so a sync retry that races the original
async delivery does not produce a duplicate email. The app dials
`NOTIFIER_GRPC_ADDR` on startup and uses the resulting client from the
`TimeoutSweeper` last-chance retry path (see ADR-003).

Install the proto tooling once:

```bash
make proto-tools
# or manually:
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Lint and regenerate:

```bash
make proto-lint   # buf lint
make proto-gen    # buf generate -> proto/gen/...
```

---

## Extras implemented

| Feature | Details |
|---------|---------|
| Live deployment | Railway — https://github-notifier-case.posulka.site |
| Web UI | Single-page subscription form at `/` |
| Redis caching | GitHub API responses cached with 10-min TTL |
| Prometheus metrics | `/metrics` endpoint |
| GitHub Actions CI | Lint + test + build on every push |
