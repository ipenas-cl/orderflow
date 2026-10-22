# ADR 0003: An orchestrated saga, owned by the orders service

- Status: accepted
- Date: 2026-10-22

## Context
Placing an order spans four services: inventory (reserve), payments (charge), fulfillment (pick, pack, ship) and the order itself. No single ACID transaction covers them all. When a step fails, the steps that already succeeded must be undone.

## Options
- **Choreography.** Each service reacts to the others' events. Coupling is low, but the flow is implicit and spread across services, and compensations are hard to reason about once there are more than three steps.
- **Orchestration.** One component sends commands and reacts to replies, and the flow lives in one place.

## Decision
Orchestration. The orchestrator is the `Order` aggregate plus `orders/app.HandleMessage`.
- The order's state machine (`PENDING → STOCK_RESERVED → PAID → SHIPPED → DELIVERED`, or `CANCELLED`/`REFUNDED`) *is* the saga state, stored in the same row with optimistic locking.
- `Order.Cancel()` returns the compensations that the steps already completed require (`ReleaseStock`, `RefundPayment`). The orchestrator emits them through the outbox.
- Participants (inventory, payments, fulfillment) stay simple and reply with events.
- Messages that are out of order or stale (an illegal transition) are logged and acknowledged, never retried forever.
- Every step is appended to the order timeline, which the backoffice renders.

| Failure | Compensation |
|---|---|
| Stock rejected | none (nothing reserved) |
| Payment declined, or PSP down after retries | release stock |
| Carrier rejects the shipment (after payment) | release stock + refund payment |

## Consequences
- The flow is readable in one file and testable with in-memory adapters.
- The orders service becomes the coordination point, but it is stateless apart from Postgres and scales horizontally.
- Future work: a saga timeout sweeper (cancel orders stuck in a step for more than N minutes).
