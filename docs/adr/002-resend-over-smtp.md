# ADR-002: Use Resend API instead of direct SMTP

**Date:** xxxx-xx-xx  
**Status:** Accepted

---

## Context

The application sends two categories of transactional email: subscription confirmations and release notifications. A delivery mechanism had to be chosen before the email layer could be implemented.

The two realistic options were:

**Option A — Direct SMTP** (self-hosted relay or plain `net/smtp`)  
Connect to an SMTP server, authenticate with credentials, and deliver messages using the standard SMTP protocol.

**Option B — Transactional email API** (HTTP REST service such as Resend, Postmark, or SendGrid)  
Serialize each message as JSON and `POST` it to an HTTPS endpoint. The provider's infrastructure handles queuing, retry, DKIM signing, and delivery.

The evaluation criteria were: implementation simplicity, deliverability, operational burden, and cost at small scale.

---

## Evaluation

| Criterion | SMTP | Resend API |
|---|---|---|
| Go dependency footprint | Needs SMTP client library or `net/smtp` with manual TLS setup | Standard `net/http` already present in the codebase |
| Deliverability | Requires manual SPF/DKIM/DMARC configuration; high risk of inbox placement issues without a warmed-up sending domain | Provider handles DKIM signing and reputation; inbox placement is managed by Resend |
| Retry / queuing | Caller is responsible for handling temporary SMTP failures and retries | Handled transparently by the provider |
| Port and firewall exposure | Port 587 (STARTTLS) or 465 (SMTPS) must be reachable from the container network; blocked by many cloud providers | Single outbound HTTPS connection on port 443, universally allowed |
| Error visibility | SMTP status codes are numeric and coarse | HTTP status codes with JSON error bodies; easier to log and act on |
| Free tier | Depends on provider; self-hosted has no cost but requires a server | Resend free tier covers 3 000 emails/month and 100/day — sufficient for development and early production |
| Vendor lock-in | SMTP is a universal protocol | Tied to Resend's API contract; migration requires implementing a different `Notifier` |

---

## Decision

Use the **Resend HTTP API** as the email delivery backend.

The `email.Sender` type implements the `email.Notifier` interface using a plain `*http.Client`. No additional library is introduced. Authentication is a single `Authorization: Bearer <key>` header. The API key is injected via the `RESEND_API_KEY` environment variable and never appears in source code.

The `Notifier` interface is deliberately narrow:

```go
type Notifier interface {
    SendConfirmation(ctx context.Context, to string, data ConfirmData) error
    SendReleaseNotification(ctx context.Context, to string, data ReleaseData) error
}
```

Replacing Resend with any other provider (Postmark, SendGrid, or a self-hosted relay) requires only a new struct that satisfies this interface; no changes to `SubscriptionService` or `Scanner` are needed.

---

## Consequences

**Positive**

- Zero extra Go dependencies for the email layer.
- Deliverability is handled by Resend's infrastructure, including DKIM/SPF and reputation management.
- Port 443 is the only egress requirement — compatible with all cloud and Docker networking configurations.
- The `Notifier` interface decouples the rest of the codebase from the concrete delivery mechanism.

**Negative**

- An internet connection to `api.resend.com` is required at runtime; integration tests that invoke `Sender` directly cannot run fully offline.
- The free tier cap (100 emails/day) constrains load testing and high-volume scenarios without upgrading to a paid plan.
- Resend is a relatively young service; long-term availability is less proven than SMTP relays from established providers.
