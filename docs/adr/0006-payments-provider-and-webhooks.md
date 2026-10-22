# ADR 0006: Payment provider port (Stripe and a fake PSP), idempotency keys and verified webhooks

- Status: accepted
- Date: 2026-10-22

## Context
Payments must never double-charge, must survive a flaky PSP, and must reflect changes made outside our system (a refund issued from the Stripe dashboard).

## Decision
1. **Port:** `PaymentProvider{Authorize, Refund}`, with two adapters:
   - `adapters/stripe`: the REST API directly, with no SDK. It creates and confirms a PaymentIntent (test mode, `pm_card_visa`) and handles Refunds. Keys come only from the environment (`STRIPE_SECRET_KEY`, see `.env.example`).
   - `adapters/provider.FakeClient`, calling `cmd/payprovider`, a PSP simulator whose latency, 5xx rate, decline rate and outage switch can be changed at runtime for chaos runs.
2. **Resilience decorator** (`adapters/provider/resilient.go`) wraps either adapter: retry with exponential backoff and full jitter → bulkhead → circuit breaker → per-attempt timeout. Declines (`402`/`card_error`) are business outcomes: they are not retried and do not trip the breaker.
3. **Idempotency:**
   - The public API requires `Idempotency-Key`. A replay returns the stored payment with `Idempotent-Replayed: true`, and reusing a key with a different body returns `422`.
   - Saga charges use the key `order-<id>`, so there is at most one charge per order whatever the redeliveries.
   - The key is forwarded to the PSP (Stripe honours `Idempotency-Key` for 24 h), so a crash between the charge and our commit is safe.
   - The provider is called outside the DB transaction, and the result is committed with the outbox event and the inbox mark.
4. **Transient failure semantics:** the public API returns `503` + `Retry-After` and does not persist anything, so the client can retry. The saga records a definitive `FAILED` payment, which triggers compensation.
5. **Webhooks:**
   - `POST /v1/webhooks/stripe` verifies `Stripe-Signature` (HMAC-SHA256 over `t.payload`, constant-time compare, 5-minute replay window) before parsing.
   - The event id goes through the inbox, so Stripe's retries are no-ops.
   - Handling failures return `500` so Stripe retries with backoff.
   - `charge.refunded` emits `PaymentRefunded`, and the order timeline shows it.

## Consequences
- Local and CI runs never need Stripe. With test keys plus `stripe listen --forward-to localhost:8081/v1/webhooks/stripe`, the same code runs against real Stripe.
- Partial refunds and 3DS (`requires_action`) are out of scope and are treated as declines for now.
