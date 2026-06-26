# ADR-003: Sync gRPC retry as last-chance path before saga compensation

**Date:** 2026-06-26
**Status:** Accepted

---

## Context

The Subscribe saga (ADR-implicit, see `internal/subscription/saga`) pairs a
subscription row in the app database with a confirmation email delivered by
the `notifier` service. Communication is asynchronous over RabbitMQ:

1. The orchestrator publishes `SendConfirmationCommand`.
2. The notifier consumes it, calls Resend, publishes a reply event
   (`confirmation_sent` / `confirmation_failed`).
3. The orchestrator's reply consumer transitions the saga to `completed` or
   compensates by deleting the orphaned pending subscription.

A `TimeoutSweeper` ran every `SAGA_SWEEP_INTERVAL` and treated any saga still
pending after `SAGA_TIMEOUT` as a failure: it compensated immediately,
deleting the subscription. This is the right behaviour when the notifier
genuinely never sent the email, but it is harmful in the more common timeout
shape — the email *was* sent and the reply was simply lost in transit
(broker hiccup, notifier crash after Resend success). The user sees their
account silently disappear despite already having a delivered confirmation
link in their inbox.

We also have a new gRPC contract on the notifier
(`proto/notifier/v1/notification.proto`) defined in HW9. The contract is
intentionally synchronous: caller learns the outcome from the gRPC status.

## Decision

Wire gRPC as a **synchronous last-chance retry** invoked by the sweeper
before compensation. The async path remains primary:

- `Orchestrator.AttemptSyncRetry(ctx, sagaID)` loads the saga, joins to the
  subscription, rebuilds the confirmation URL, and calls
  `notifier.EmailNotifierService.SendConfirmation` over gRPC. On a successful
  RPC it marks the saga `completed` (the SQL `WHERE state='pending'` guard
  makes the write idempotent against a late async reply).
- `TimeoutSweeper` calls `AttemptSyncRetry` for every stuck saga. A nil
  error rescues the saga and the loop continues. Any error falls through to
  the existing `HandleFailed` compensation path, with the reason now
  embedding the gRPC error for post-mortem.

The notifier exposes the gRPC server alongside the RabbitMQ consumer; both
share an in-process `Dedupe` cache keyed by `saga_id`. The RabbitMQ
handler records into the cache after every successful Resend call; the
gRPC handler checks the cache before sending and returns `OK` if a
prior delivery is recorded.

## Alternatives Considered

**Replace the saga with a fully synchronous gRPC subscribe flow.** This is
what the gRPC contract's docstring suggests at first reading. Rejected
because it discards the architectural work in HW8 and weakens the system: a
hung notifier would now block `POST /subscribe` requests and burn worker
threads. Async with eventual consistency is the right primary path.

**Use gRPC as a publish-time fallback when the broker dials fail.** Smaller
change but the trigger is synthetic — RabbitMQ outages are rare and the saga
already handles them by returning an error to the HTTP handler. The sweeper
retry trigger is a real, repeatedly observed failure mode (notifier crash
between Resend call and reply publish), so the synchronous path actually
earns its keep.

**Persistent seen-set on the notifier (Redis or Postgres).** Would close the
crash-restart dedupe gap. Rejected for HW9: notifier has no database today,
adding one for a single defensive cache is disproportionate, and the failure
mode it protects against (duplicate confirmation email after a notifier
restart inside the saga-timeout window) is benign for the user.

## Consequences

**Positive**

- A common timeout shape — email delivered, reply lost — now resolves to a
  preserved subscription instead of compensation. The user keeps the
  account they confirmed.
- Compensation reasons include the gRPC error string (`last_error` column on
  `subscription_sagas`), making post-mortem queries more actionable.
- The gRPC contract introduced in HW9 has a concrete consumer, not just a
  toolchain demo.

**Negative — accepted trade-offs**

- **Duplicate confirmation email risk in the crash-restart window.** If the
  notifier processed the async command successfully but crashed before
  publishing the reply *and* before the in-memory dedupe entry could
  protect the gRPC retry, the sweeper will trigger a second send. The cache
  is in-process only; it does not survive a notifier restart. Sending the
  confirmation twice is harmless (the link is idempotent on the app side)
  but cosmetically annoying. Mitigation deferred until we either add a
  persistent seen-set or move dedupe upstream of Resend.
- **One extra dependency in the app process.** The orchestrator now holds a
  long-lived `grpc.ClientConn` to the notifier. Connection failure at app
  startup blocks boot; this is intentional — a missing notifier is a
  service-level outage worth surfacing immediately.
