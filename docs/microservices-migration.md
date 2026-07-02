# hw5 to hw6: Microservices migration

This document is a structured changelog for the migration from the single-binary
monolith (last seen on `main`) to the two-service layout that ships on
`hw6-microservices`. It is intentionally a **diff doc**, not a replacement for
[`system-design-document.md`](system-design-document.md) — the SDD describes
the *state*, this document explains the *change* and the *reasoning* behind
how it was split.

---

## 1. Context

The hw5 codebase compiled into a single binary. One process owned everything:
HTTP API, business logic, the scanner goroutine, and the Resend client.
Internal packages were grouped by **technical kind** (`config`, `model`,
`repository`, `service`, `handler`, `middleware`) rather than by domain.

hw6's task: extract a separate **notifier** service responsible for outbound
email, reachable from the monolith over HTTP, while keeping the migration
bisectable — every intermediate commit must compile and pass tests.

Two orthogonal threads of work fall out of that task:

1. **Restructure** the monolith's package layout from technical buckets to
   hexagonal-style bounded contexts so that "extracting a service" reduces to
   "moving an existing folder to its own binary". This is cheap to do *before*
   the split and expensive afterwards.
2. **Split** the notifier out and wire the monolith to call it over HTTP,
   then layer in the operational concerns (compose service, healthchecks,
   internal auth, env separation, test harness changes).

Doing 1 before 2 means each later commit touches small, obvious surface area.
Doing them interleaved would have produced large noisy diffs that conflate
"moving code" with "changing behaviour".

---

## 2. Before / After

### Package layout

```
hw5 (monolith)                       hw6 (two services, three bounded contexts)
─────────────                        ────────────────────────────────────────
cmd/server/                          cmd/server/                    ← monolith binary
internal/                            cmd/notifier/                  ← new binary
  config/                            internal/
  model/                               platform/                    ← cross-cutting
  repository/                          ├─ config/
  github/                              ├─ metrics/
  email/                               └─ middleware/ (+ auth)
  service/                             subscription/                ← bounded context
    subscription.go                    ├─ domain/
    scanner.go                         ├─ app/                       (use cases)
  handler/                             └─ adapter/{http,postgres}/   (driving + driven)
  middleware/                          release/                     ← bounded context
                                       ├─ domain/
                                       ├─ app/                       (scanner)
                                       └─ adapter/{github,postgres}/
                                       notifier/                    ← bounded context
                                       ├─ domain/
                                       └─ adapter/
                                         ├─ http/       (driving — receives commands)
                                         ├─ httpclient/ (driven — used by monolith)
                                         ├─ resend/     (driven — Resend HTTP API)
                                         └─ templates/  (driven — HTML rendering)
```

Net effect: each context owns its `domain` (entities, errors), its `app` (use
cases / orchestrators), and its `adapter/*` (port implementations). Cross-cutting
concerns moved to `internal/platform/`. The `notifier` context is the only one
with both a *driving* adapter (`http/handler.go`, used by `cmd/notifier`) and a
*driven* adapter (`httpclient/client.go`, used by `cmd/server`) — that
duality is what makes "split into a service" mechanical.

### Runtime topology

```
hw5                                  hw6
─────                                ───
[ server ]                           [ server ]            [ notifier ]
  ├─ HTTP API                          ├─ HTTP API           ├─ HTTP API (/v1/notifications/*)
  ├─ scanner (goroutine)               ├─ scanner            ├─ Resend client
  ├─ Resend client                     └─ notifier.httpclient    ↓
  └─ PostgreSQL                              ↓
                                            HTTP (X-Internal-Token)
                                            ↓
                                       [ notifier ]
```

The monolith no longer touches Resend directly. The scanner and subscribe flow
both go through `notifier/adapter/httpclient`, which is the *only* thing the
monolith knows about email delivery.

### Docker Compose

| Service | hw5 | hw6 |
|---|---|---|
| `app` | builds `cmd/server` | builds `cmd/server`, depends on `notifier` (healthy) |
| `notifier` | — | new; builds `cmd/notifier`, exposes 8081, has `/health` healthcheck |
| `db`, `redis` | as before | as before |
| `prometheus` scrape | only `app` | `app` + `notifier` |

---

## 3. Migration roadmap

Commits are listed in chronological order. Each one was kept small enough that
`go build ./... && go test ./...` stays green before pushing.

### Phase A — Restructure in place (no behaviour change)

| # | Commit | What it did | Why this order |
|---|---|---|---|
| 1 | `af538bd refactor(platform): move config, metrics, middleware to internal/platform` | Carved out cross-cutting code first | Other contexts will import `platform`; moving it first means later moves don't pull it along |
| 2 | `32c7386 refactor(notifier): extract email package into internal/notifier` | `internal/email/` becomes `internal/notifier/adapter/{resend,templates}/` plus `internal/notifier/domain/` | The thing we're about to split into its own binary gets its own bounded context first |
| 3 | `39003e1 refactor(release): extract release domain into internal/release` | Scanner + GitHub client + repository data access | Scanner is the second-largest piece and the heaviest user of the now-shared notifier port |
| 4 | `36514dd refactor(subscription): extract subscription domain into its own module` | Subscribe / confirm / unsubscribe + sub repository | Pure rename / move, but completes the three-context layout |

After Phase A, the monolith looks like a microservice system that just happens
to run in one binary. No tests change semantics; many simply move with their
packages.

### Phase B — Split out the notifier binary

| # | Commit | What it did | Why this order |
|---|---|---|---|
| 5 | `a3fdbf5 feat(notifier): add cmd/notifier binary with HTTP API` | New `cmd/notifier/main.go`, new `notifier/adapter/http/handler.go` (driving adapter exposing `/v1/notifications/{confirmation,release}`) | Stand up the recipient first so the next commit has something to call |
| 6 | `201610e feat(notifier): switch monolith to HTTP transport` | New `notifier/adapter/httpclient/client.go`; monolith now constructs `httpclient.Client` instead of `resend.Sender`; subscribe/scanner use cases unchanged (their ports are satisfied structurally) | This is the *only* behaviour-changing commit in the split. Keeping it isolated means a regression bisects straight here |
| 7 | `ade64da chore(infra): add notifier service to compose + Prometheus scrape` | Compose service, scrape target, env vars | Infra catches up with code; previously you could only run the split locally with two `go run` invocations |
| 8 | `16507ed test: run notifier alongside server under test` | Integration harness boots a second `httptest.Server` for the notifier handler so tests exercise the real HTTP transport | Without this, integration tests would silently keep using the in-process emailSender and never validate the wire format |

### Phase C — Operational hardening

| # | Commit | What it did | Why |
|---|---|---|---|
| 9  | `0994c89 feat(middleware): add InternalAuth for service-to-service auth` | `platform/middleware/auth.go` — checks `X-Internal-Token` header | The notifier's API is exposed to the compose network; an unauthenticated path would let any pod blast emails |
| 10 | `bda85bb feat(config): add NotifierAuthToken loaded from NOTIFIER_INTERNAL_TOKEN` | Config plumbing for both sides | Token must reach both binaries; lands before either side enforces it |
| 11 | `e23f130 feat(notifier): require X-Internal-Token on /v1/notifications` | Notifier rejects unauthenticated requests | Enforcement; only safe after #10 (otherwise tests/dev break) |
| 12 | `fb4a61b feat(notifier/client): send X-Internal-Token to notifier` | httpclient attaches the header | Closes the loop — calls succeed end-to-end |
| 13 | `c928652 chore(env): split .env.example into per-service files` | Distinct env files for app vs notifier | Two binaries should not share a single env example; the notifier doesn't need `DATABASE_URL`, the app doesn't need `RESEND_API_KEY` |

### Phase D — Boundary cleanup

| # | Commit | What it did | Why |
|---|---|---|---|
| 14 | `fa0b300 fix(integration): update httpclient.NewClient call after signature change` | Test wiring drift | Trivial follow-up after auth token plumbing |
| 15 | `59d89e7 refactor(subscription): drop notifier/domain import from port` | The subscription use case's `confirmationSender` port now takes primitives (`to, repo, confirmURL string`) instead of `notifierdomain.ConfirmData` | A cross-context import was the last seam tying `subscription` to `notifier`; removing it makes the bounded contexts truly independent. The httpclient assembles the DTO internally. |
| 16 | `e33271e refactor(release): drop notifier/domain import from port` | Same treatment for the scanner's release-sender port | Symmetric cleanup; release no longer knows what `notifier/domain` looks like either |
| 17 | `3517fbc fix(compose): gate app on notifier healthcheck instead of service_started` | Compose `depends_on` strengthened from `service_started` to `service_healthy` after adding a `wget /health` healthcheck on the notifier | Closes a latent startup race the reviewer flagged |

---

## 4. Key design decisions

### 4.1 Hexagonal layout, three bounded contexts

Going to `domain/app/adapter` per context (rather than continuing with
`model/service/repository`) costs more files but earns three things that mattered
for this migration:

- **Mechanical extraction.** When `notifier/adapter/resend/` exists, lifting it
  into its own binary is a `git mv` of `cmd/notifier/main.go` and a few
  imports. There is no entangled `service/email.go` to surgically slice apart.
- **Small ports.** Use cases declare narrow interfaces (`confirmationSender`,
  `releaseSender`, `repoChecker`, etc.) rather than depending on concrete
  packages. The HTTP client is one of many possible implementations — the
  switch from `resend.Sender` to `httpclient.Client` in commit 6 required
  zero changes inside `subscription/app` or `release/app`.
- **Clear ownership.** Each context owns its tables (`subscriptions` for
  subscription, `repositories` for release), its domain errors, and its
  external clients. No "god service" remains.

### 4.2 HTTP over a broker (for hw6)

The notifier is reached over **HTTP**, not via a queue. This was deliberate for
hw6 even though it couples availability:

- Adds the minimum number of new moving parts (no broker, no codec, no
  consumer lifecycle). A reviewer can read the whole call path in two files
  (`httpclient/client.go` + `http/handler.go`).
- Makes the request/response model identical to the monolith's previous
  in-process call — no semantic surprise.
- Leaves the broker question open for hw7 (which is exactly where it landed,
  as `feat(notifier): switch monolith to HTTP transport` shows it was
  acknowledged as transitional).

The cost — an outage of the notifier blocks subscribe and release flows — was
considered acceptable for a learning step, and is the motivation handed off
to the hw7/hw8 work.

### 4.3 Driving vs driven adapters in the same package

The notifier context has both `adapter/http/handler.go` (driving — *receives*
HTTP) and `adapter/httpclient/client.go` (driven — *sends* HTTP). They look
symmetric, but their roles are opposite:

- `http/handler.go` lives behind `cmd/notifier`'s router; nothing in the
  monolith imports it.
- `httpclient/client.go` lives inside the monolith binary; nothing in the
  notifier imports it.

Keeping them under one bounded context (`notifier/`) makes the contract
self-documenting: changing the wire format means editing two files that sit
next to each other, instead of hunting across services.

### 4.4 Ports as primitive parameters (Phase D)

Originally the `confirmationSender` port took `notifierdomain.ConfirmData`.
That worked, but it forced `subscription` to import `notifier/domain` — a
cross-context dependency that would block extracting subscription into its
own binary later. Commits 15-16 collapse the DTO into primitive args; the
adapter rebuilds the struct on the other side.

Trade-off: marginally noisier signatures (`to, repo, confirmURL string`)
in exchange for a cleaner dependency graph. Worth it because the contexts
are now genuinely independent.

### 4.5 Internal auth as a separate phase

Auth landed in four small commits (#9-12) rather than one because the order
matters: introduce the middleware, plumb the config, enforce on the server,
attach on the client. Squashed, any in-between push would have broken either
tests (notifier rejects unauthenticated calls before the client started
sending the header) or production (header sent before notifier knows the
secret). The four-step ordering kept every commit green.

---

## 5. Trade-offs and what was deliberately *not* done

| Choice | Trade-off | Deferred to |
|---|---|---|
| HTTP transport between services | Availability of subscribe = min(monolith, notifier). Sync coupling. | hw7 (message broker) |
| Per-subscriber notification still fire-and-forget | If the notifier returns 5xx for one subscriber, the scanner logs and moves on; `last_seen_tag` advances regardless, so a downed Resend silently loses notifications for that release | hw8 (saga / outbox in subscription path) |
| Notifier `/health` is a 200-always endpoint | Doesn't verify Resend reachability or template renderer health | Could be enhanced when needed |
| Single shared `internal/platform/middleware/auth.go` for both API key and internal token | Two unrelated authentication concerns in one file | Tolerable while small |
| Notifier has no DB | Replies are not persisted; a notifier crash mid-send loses state | Acceptable for stateless sender; persistence pushed back to caller side |

---

## 6. Verification

After every commit on this branch:

```bash
go build ./...
go test ./...
go test -tags=integration ./integration/...    # needs Docker (testcontainers)
```

For the runtime split:

```bash
docker compose up --build
# observe: notifier reaches "healthy" before app starts;
# subscribe form returns 200; confirmation email arrives via the notifier path
```
