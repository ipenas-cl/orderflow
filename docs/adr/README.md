# Architecture Decision Records

| # | Decision |
|---|---|
| [0001](0001-hexagonal-architecture.md) | Hexagonal architecture per service |
| [0002](0002-transactional-outbox.md) | Transactional outbox + idempotent inbox instead of dual writes |
| [0003](0003-saga-orchestration.md) | Orchestrated saga owned by orders |
| [0004](0004-nats-jetstream.md) | NATS JetStream as the broker |
| [0005](0005-authentication-and-rbac.md) | OIDC/JWT (Auth0) behind an auth port, RBAC + audit |
| [0006](0006-payments-provider-and-webhooks.md) | Payment provider port, idempotency keys, Stripe webhooks |
