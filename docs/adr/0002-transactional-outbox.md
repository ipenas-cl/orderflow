# ADR 0002: Transactional outbox and idempotent inbox instead of dual writes

- Status: accepted
- Date: 2026-10-22

## Context
A use case must change state *and* emit a message (for example, the order is created and `inventory.cmd.reserve` goes out). Writing to Postgres and then publishing to the broker is a dual write: a crash or a broker outage between the two steps loses the message or publishes a message for a rolled-back change.

## Options
1. **Dual write** (commit, then publish). Simple, but it loses or invents messages under failure.
2. **Distributed transaction (2PC/XA).** NATS does not support it, and it couples availability.
3. **CDC (Debezium) on the outbox table.** Robust, but it adds Kafka Connect to operate.
4. **Transactional outbox with a polling relay.** The message row commits with the state change, and a relay publishes it.

## Decision
Option 4.
- Use cases call `tx.Outbox.Add(env)` inside the same transaction as the aggregate change (`internal/platform/pg/pg.go`).
- `outbox.Relay` (`internal/platform/outbox/relay.go`) polls with `FOR UPDATE SKIP LOCKED`, so several replicas can run it safely. It publishes with `Nats-Msg-Id = envelope id`, which lets JetStream deduplicate a re-publish after a crash within the 2-minute window, and then marks the rows as published.
- Delivery is **at least once**. Every consumer records `(consumer, msg_id)` in an `inbox` table **in the same transaction** as its side effects, so redeliveries are no-ops.
- W3C trace context travels inside the envelope, so one trace spans producer → outbox → broker → consumer.

## Consequences
- Publish latency is up to one poll interval (200 ms by default). Switching to CDC later only changes the relay; the use cases stay the same.
- The outbox and inbox tables need retention jobs (delete published rows older than N days).
- Handlers must be written to be idempotent. Compensations are idempotent too (releasing stock twice is a no-op).
