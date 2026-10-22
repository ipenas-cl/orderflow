# ADR 0001: Hexagonal architecture (ports and adapters) per service

- Status: accepted
- Date: 2026-10-22

## Context
Every service talks to several kinds of infrastructure: HTTP, Postgres, NATS, and external providers (a PSP, a carrier, an identity provider). We want to test business rules without them and swap any of them without touching the rules.

## Decision
Each service has the same layout:

| Layer | Package | Depends on |
|---|---|---|
| Domain (aggregates, invariants) | `internal/<svc>/domain` | nothing |
| Ports (driven interfaces) | `internal/<svc>/ports` | domain, `platform/messaging` |
| Application (use cases, saga) | `internal/<svc>/app` | domain, ports |
| Adapters (HTTP, Postgres, memory, PSP, carrier) | `internal/<svc>/adapters/*` | everything above |
| Composition root | `cmd/<svc>` + `platform/bootstrap` | adapters |

A `UnitOfWork` port hands the use case a `Tx` with repositories, the outbox and the inbox, all bound to one database transaction.

## Consequences
- Use-case tests run against in-memory adapters and hand-written mocks in milliseconds.
- The Stripe adapter replaced nothing: it sits next to the fake PSP behind the same `PaymentProvider` port, and the resilience decorator wraps either one.
- There is a little more boilerplate (memory and Postgres repositories) than a CRUD service would need. That is acceptable for a domain with real invariants.
