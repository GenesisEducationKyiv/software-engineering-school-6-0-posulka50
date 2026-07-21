# Application Architecture

> Two Go services communicating over RabbitMQ (async) and gRPC (sync-retry
> fallback), backed by PostgreSQL and Redis. Deployed as a small `docker-compose`
> stack with a Prometheus/Grafana/ELK observability plane.

---

## Table of Contents

- [1. Overview](#1-overview)
- [2. High-Level Topology](#2-high-level-topology)
- [3. Services](#3-services)
- [4. Communication Patterns](#4-communication-patterns)
- [5. Data Stores](#5-data-stores)
- [6. Subscribe Saga](#6-subscribe-saga)
- [7. Release Notification Flow](#7-release-notification-flow)
- [8. Internal Package Layout](#8-internal-package-layout)
- [9. Configuration](#9-configuration)
- [10. Observability](#10-observability)
- [11. Deployment Topology](#11-deployment-topology)

---

## 1. Overview

The application delivers **release notifications** for GitHub repositories to
subscribed email addresses. A user subscribes via HTTP, confirms via a tokenised
email link (double opt-in), and thereafter receives an email each time the
tracked repository publishes a new release.

The system is split into two deployable Go binaries:

| Binary | Package | Responsibility |
|---|---|---|
| `server` | `cmd/server` | HTTP API, subscription domain, saga orchestrator, release scanner |
| `notifier` | `cmd/notifier` | Email delivery via Resend, exposed as an AMQP consumer and a gRPC service |

The split exists because email delivery is I/O bound and independently scalable
from the subscription/scanner workload, and because the Subscribe use case is a
natural fit for an orchestrated saga (see [Section 6](#6-subscribe-saga)).

---

## 2. High-Level Topology

_Diagram placeholder: `docs/img/application-architecture.png` (to be attached
after this document is reviewed)._

Textually, the runtime looks like this:

```
                   +--------+
                   |  User  |
                   +---+----+
                       |  HTTP (subscribe / confirm / unsubscribe)
                       v
+-------------------------------------------+           +--------------+
|              server (cmd/server)          |  gRPC     |  notifier    |
|                                           |---------->|  (cmd/       |
|  - HTTP router (Gin)                      |  sync     |   notifier)  |
|  - SubscribeUseCase                       |  retry    |              |
|  - Saga Orchestrator + TimeoutSweeper     |           |  - AMQP      |
|  - Release Scanner (goroutine)            |           |    consumer  |
|                                           |  AMQP     |  - gRPC      |
|                                           |<--------->|    server    |
+---------+---------+-------------+---------+  commands |  - Resend    |
          |         |             |         |  & events |    adapter   |
          | SQL     | Redis       | AMQP    |           |  - Dedupe    |
          v         v             v         |           +------+-------+
      +-------+  +-------+  +----------+    |                  | HTTPS
      | Post- |  | Redis |  | RabbitMQ |----+                  v
      | greSQL|  | cache |  | (topic   |                  +---------+
      +-------+  +-------+  |  exchange|                  | Resend  |
                            +----------+                  |   API   |
                                                          +---------+
                    +--------+
                    | GitHub |<---- HTTPS (repo check, latest release)
                    |  REST  |         from server
                    +--------+
```

Key edges:

- **User to server**: HTTP JSON API (Gin).
- **server to GitHub**: outbound HTTPS, cached in Redis.
- **server to RabbitMQ**: publishes saga commands and release-delivery
  messages; consumes saga reply events.
- **RabbitMQ to notifier**: notifier consumes saga commands and release
  deliveries; publishes saga reply events back.
- **server to notifier (gRPC)**: used only by the `TimeoutSweeper` as a
  synchronous last-chance retry before compensation (see
  [Section 6](#6-subscribe-saga)).
- **notifier to Resend**: outbound HTTPS to the Resend transactional email API.

---

## 3. Services

### 3.1 `server` (subscription + scanner)

Entry point: `cmd/server/main.go`.

Responsibilities:

- Serve the public HTTP API (`/api/subscribe`, `/api/confirm/:token`,
  `/api/unsubscribe/:token`, `/api/subscriptions`) plus `/health` and
  `/metrics`.
- Own the subscription and repository tables in PostgreSQL.
- Run the **Subscribe saga orchestrator** and its **timeout sweeper** as
  background goroutines. The orchestrator publishes `SendConfirmationCommand`
  messages and reacts to reply events (`ConfirmationSent`,
  `ConfirmationFailed`).
- Run the **release scanner** goroutine, which polls GitHub for new release
  tags and publishes `notification.release` messages onto RabbitMQ for the
  notifier to deliver.
- Hold a gRPC client to the notifier; used only by the sweeper's sync-retry
  path.

Startup dependencies: PostgreSQL (must be up), RabbitMQ (retried on dial),
notifier gRPC (dial only; failure is deferred). Redis is optional; without it
the GitHub-existence cache is disabled and every check hits the live API.

### 3.2 `notifier` (email delivery)

Entry point: `cmd/notifier/main.go`.

Responsibilities:

- **AMQP consumer** for two queues:
  - `notifier.deliveries` (`notification.release`): render and send release
    notification emails; ack on success, nack-without-requeue on permanent
    failure.
  - `notifier.commands` (`notification.command.send_confirmation`): render and
    send the saga's confirmation email; **always ack** after publishing the
    reply event, so retry policy lives with the saga rather than the broker.
- **gRPC server** implementing `EmailNotifierService.SendConfirmation`. This
  is the sync counterpart to the AMQP command path and shares the same
  `Sender` and `Dedupe`.
- **Dedupe** (in-memory bounded map keyed by `saga_id`) so that a sweeper's
  sync retry does not send a duplicate email when the AMQP path already
  delivered.
- **Reply publisher**: the notifier owns its own publisher to emit
  `notification.event.confirmation_sent` / `notification.event.confirmation_failed`
  back to the orchestrator.

The notifier is stateless: everything durable lives on the server side or in
RabbitMQ. Restarting it drops in-flight in-memory dedupe state, which is
acceptable because Resend has no idempotency key and the primary duplicate
protection is the AMQP-side ack-only-after-reply semantics.

---

## 4. Communication Patterns

### 4.1 Synchronous HTTP (user to server)

Standard REST-ish JSON. All write endpoints validate input at the boundary
before touching the DB. The API-key middleware is wired but not currently
enforced on any route.

### 4.2 Asynchronous AMQP (server and notifier)

A single **topic exchange** `notifications` routes to three durable queues.
Bindings and routing keys:

| Queue | Binding | Routing keys | Producer | Consumer |
|---|---|---|---|---|
| `notifier.deliveries` | `notification.*` | `notification.release` | scanner (server) | notifier |
| `notifier.commands` | `notification.command.*` | `notification.command.send_confirmation` | saga orchestrator (server) | notifier |
| `app.saga.events` | `notification.event.*` | `notification.event.confirmation_sent`, `notification.event.confirmation_failed` | notifier | saga orchestrator (server) |

The single-segment binding on `notifier.deliveries` matches only the legacy
release routing key; the saga's three-segment keys land exclusively in the
saga queues, so there is no cross-delivery.

Topology is declared idempotently on **both** sides at startup, so whichever
service boots first creates the exchange, queues and bindings.

Both `Publisher` and `Consumer` implement transparent channel-reopen and
dial-with-retry logic to survive broker flaps in `docker-compose` and
production restarts.

### 4.3 Synchronous gRPC (server to notifier)

Proto definitions in `proto/notifier/v1/notification.proto`, generated code
under `proto/gen/notifier/v1`. The service exposes a single RPC:

```
rpc SendConfirmation(SendConfirmationRequest) returns (SendConfirmationResponse)
```

The RPC is invoked **only** from the `TimeoutSweeper` inside
`saga.Orchestrator.AttemptSyncRetry`. It is not the primary happy path; it
exists so that a saga stuck in `pending` past its timeout can be rescued
without immediately compensating (deleting the user's just-created
subscription). The notifier-side `Dedupe` guarantees that the sync retry does
not send a duplicate if the async path had already delivered.

---

## 5. Data Stores

### 5.1 PostgreSQL

Schema is managed by `golang-migrate` and applied automatically at server
startup from `cmd/server/migrations`.

**`repositories`** (`000001` + `000002`) — one row per tracked GitHub
repository; carries `last_seen_tag`, updated after each successful scan.

**`subscriptions`** (`000001` + `000002`) — one row per (email, repo) pair;
carries `confirmed`, `confirm_token`, `unsubscribe_token`. `repo_id` is a
foreign key into `repositories` with `ON DELETE CASCADE`.

**`subscription_sagas`** (`000003`) — one row per Subscribe saga instance,
tracking state (`pending`, `completed`, `compensated`, `timed_out`),
`last_error`, `started_at`, `completed_at`. Indexed on `(state, started_at)`
for the sweeper's `GetPendingOlderThan` query.

The subscription row and its saga row are inserted in the **same
transaction**, so no orphaned subscription can exist without a journal entry.

### 5.2 Redis

Redis is used as a short-TTL cache for two GitHub-side lookups:

- `CheckRepo` (repository existence) responses, TTL 10 min.
- `GetLatestRelease` responses (added in the split of scanner adapters), TTL
  short enough that new releases surface within one scan cycle.

Both cached fetchers degrade to live GitHub calls if Redis is unreachable at
startup or during runtime; the service does not depend on Redis to function.

---

## 6. Subscribe Saga

The Subscribe use case pairs two independent writes:

1. Create the subscription row locally in PostgreSQL.
2. Deliver a confirmation email through a remote service (notifier -> Resend).

These cannot share a database transaction, so the pairing is implemented as
an **orchestrated saga**. States: `pending`, `completed`, `compensated`,
`timed_out`.

### 6.1 Happy path

```
User      server (subscribe UC)      RabbitMQ       notifier            Resend
 |POST /subscribe                       |              |                   |
 |----------> validate + GH check       |              |                   |
 |            insert (sub + saga row) atomically      |                   |
 |            orchestrator.Start                       |                   |
 |            publish command --------->|              |                   |
 |<------ 200 OK                        |------------->|                   |
 |                                      |  consume     |                   |
 |                                      |              |---- send email -->|
 |                                      |<-------------|                   |
 |                                      | publish sent |                   |
 |          orchestrator.HandleSent     |              |                   |
 |          MarkCompleted (state=completed)            |                   |
```

### 6.2 Notifier reports failure

Failure event is a normal reply. The orchestrator's `HandleFailed` marks the
saga `compensated` and **deletes the orphaned pending subscription** so the
user does not see a phantom row that can never be confirmed.

### 6.3 Reply lost / notifier crashes silently

This is what the **`TimeoutSweeper`** is for. It scans for sagas in `pending`
older than the configured `SAGA_TIMEOUT` and, for each one:

1. Calls `orchestrator.AttemptSyncRetry`, which resolves the subscription
   fresh, calls `SendConfirmation` over **gRPC**, and marks the saga
   `completed` on success.
2. On any gRPC failure, falls through to `HandleFailed` (compensation).

Because the notifier's `Dedupe` is keyed by `saga_id` and marked **before**
the reply is published, a sync retry after a lost-reply event is a no-op on
the email side. If the async and sync paths race, the SQL guard on
`MarkCompleted` (only updates rows where `state = 'pending'`) turns the loser
into a benign no-op.

### 6.4 Publish failure at `Start`

If the initial `SendConfirmationCommand` publish fails, the just-created saga
row is marked compensated and the subscription row is deleted by the caller;
the user sees a clean failure and can retry.

### 6.5 Idempotency

- **`MarkCompleted`, `MarkCompensated`** guard on `state = 'pending'`, so
  duplicate reply events (RabbitMQ redelivery) are no-ops.
- **Dedupe** on `saga_id` guards duplicate emails when the two delivery paths
  race.

---

## 7. Release Notification Flow

Scanner loop (`internal/release/app/scanner.go`):

```
every SCAN_INTERVAL:
    repos = repos.GetAllWithConfirmedSubscriptions()
    for repo in repos:
        release = github.GetLatestRelease(repo)         # 429 -> abort cycle
        if release.tag == repo.last_seen_tag: continue
        if repo.last_seen_tag == nil:
            repos.UpdateLastSeenTag(release.tag); continue    # baseline
        subs = subs.GetConfirmedByRepoID(repo.id)
        for sub in subs:
            publisher.PublishRelease(notification.release{...})
        repos.UpdateLastSeenTag(release.tag)
```

Delivery is **at-least-once**: `last_seen_tag` is only updated after the
publish loop, so a server crash mid-loop re-publishes on the next cycle. The
notifier acks each release message individually; a permanent Resend failure
nacks-without-requeue, dropping that single delivery from the queue rather
than blocking others.

Rate-limit safety: on a GitHub 429 (or 403 with rate-limit headers) the
scanner aborts the current cycle immediately and resumes on the next tick.

---

## 8. Internal Package Layout

Each service follows a hexagonal (ports-and-adapters) split. The top-level
domain modules under `internal/` are:

```
internal/
  subscription/          - Subscribe / confirm / unsubscribe use cases
    domain/              - Subscription + Saga entities, domain errors
    app/                 - Use cases (SubscribeUseCase, ConfirmUseCase, ...)
    adapter/http/        - Gin handlers (JSON in/out)
    adapter/postgres/    - subscription + saga repositories
    saga/                - Orchestrator, TimeoutSweeper, RepliesConsumer, Retrier

  release/               - Repository scanning + GitHub client
    domain/              - Repository entity
    app/                 - Scanner
    adapter/github/      - REST client, RepoChecker, ReleaseFetcher (+ cached variants)
    adapter/postgres/    - repository repository

  notifier/              - Email delivery
    domain/              - Email data types (ConfirmData, ReleaseData)
    adapter/rabbitmq/    - Publisher, Consumer, topology, message types
    adapter/grpcsrv/     - gRPC server + Dedupe
    adapter/resend/      - Resend HTTP client
    adapter/templates/   - HTML template renderer + embed

  platform/              - Cross-cutting infrastructure
    config/              - env-var config loader
    logctx/              - slog handler that pulls request_id from context
    metrics/             - Prometheus collectors
    middleware/          - Gin middleware (RequestID, Logger, Prometheus, Auth)
```

Rules of thumb the code follows:

- **`domain`** holds pure Go types and errors; no imports from `app` or
  `adapter`.
- **`app`** holds use cases and orchestration; depends on `domain` and on
  narrow interfaces declared *in `app`* which adapters implement.
- **`adapter`** is the only layer allowed to import third-party clients
  (`pgx`, `amqp091`, `grpc`, `resend`, `gin`, `redis`).
- Interfaces are declared on the **consumer** side (Go idiom); adapters
  satisfy them structurally.

`cmd/server/main.go` is the only place that stitches concrete adapters into
use cases (`setupServices`). `cmd/notifier/main.go` does the same on the
notifier side.

---

## 9. Configuration

All configuration is by environment variable (`internal/platform/config`).
The `.env` file is loaded on boot if present; env vars win.

Key variables:

| Variable | Consumer | Purpose |
|---|---|---|
| `DATABASE_URL` | server | PostgreSQL DSN |
| `REDIS_URL` | server | Optional; disables cache when unset/unreachable |
| `BROKER_URL` | server, notifier | RabbitMQ AMQP URL |
| `NOTIFIER_GRPC_ADDR` | server | Sweeper's sync-retry target |
| `NOTIFIER_GRPC_PORT` | notifier | gRPC listen port |
| `GITHUB_TOKEN` | server | Optional; raises rate limit to 5 000/h |
| `RESEND_API_KEY`, `EMAIL_FROM`, `RESEND_API_URL` | notifier | Resend credentials |
| `BASE_URL` | server | Used to build confirmation and unsubscribe links |
| `SCAN_INTERVAL` | server | Scanner tick (default `1h`) |
| `SAGA_TIMEOUT`, `SAGA_SWEEP_INTERVAL` | server | Sweeper tuning |
| `API_KEY` | server | Optional; middleware wired but not enforced |
| `PORT`, `GIN_MODE` | both | HTTP listen port, Gin mode |

---

## 10. Observability

- **Metrics**: both services expose `/metrics` (Prometheus text format) via
  `promhttp`. Instrumented: HTTP request counts and latency histograms,
  scanner runs and duration, releases detected, subscriptions created,
  saga-related counters.
- **Logs**: structured JSON via `slog`, wrapped by `logctx.Handler` which
  injects the per-request `request_id` (set by the `RequestID` middleware).
  Shipped by **Filebeat** into **Elasticsearch** and viewed in **Kibana**.
- **Dashboards**: **Grafana** is provisioned from `monitoring/grafana` and
  reads from the **Prometheus** instance configured in
  `monitoring/prometheus/prometheus.yml`.
- **Health**: both services expose `/health` used by compose probes.

---

## 11. Deployment Topology

Reference deployment is `docker-compose.yml`. Nine services:

| Service | Image | Role |
|---|---|---|
| `app` | built from `Dockerfile` with `TARGET=server` | HTTP + saga + scanner |
| `notifier` | built from `Dockerfile` with `TARGET=notifier` | Email delivery |
| `db` | `postgres:16-alpine` | Primary store |
| `redis` | `redis:7-alpine` | Optional cache |
| `rabbitmq` | `rabbitmq:3.13-management-alpine` | Broker |
| `prometheus` | `prom/prometheus` | Metrics store |
| `grafana` | `grafana/grafana` | Metrics dashboards |
| `elasticsearch`, `kibana`, `filebeat` | Elastic stack | Log pipeline |

The two Go binaries are produced from a single multi-stage `Dockerfile`
parameterised by the `TARGET` build arg (`server` or `notifier`), so both
containers ship from one build definition.

---

## Appendix: Diagram

The rendered architecture diagram will be added at
`docs/img/application-architecture.png` (draw.io source under
`docs/img/draw.io/`). Once attached, replace the placeholder in
[Section 2](#2-high-level-topology) with an image reference.
