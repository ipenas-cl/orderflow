# ADR 0004: NATS JetStream as the message broker

- Status: accepted
- Date: 2026-10-22

## Context
We need durable, at-least-once delivery, durable consumers, redelivery with backoff, deduplication and low operational overhead, both locally and on Kubernetes.

## Options
| | Kafka / Redpanda | NATS JetStream | SQS/SNS |
|---|---|---|---|
| Ops footprint | heavier (brokers, partitions) | single small binary, Helm chart | managed, AWS only |
| Per-message ack/nak with delay | no (offsets) | yes | yes (visibility timeout) |
| Publish deduplication | idempotent producer | `Nats-Msg-Id` window | FIFO only |
| Replay / retention | excellent | good (stream limits) | none |
| Local dev | heavier | `nats -js` | LocalStack |

## Decision
NATS JetStream. There is one stream, `ORDERFLOW`, on `orderflow.>`, and each service has a durable pull consumer filtered to its subjects, with `MaxDeliver = 8` and NAKs with exponential delay. Messages that exhaust their retries are terminated and counted (`orderflow_consumer_messages_total{result="dead_letter"}`), which raises an alert.

## Consequences
- Per-message acknowledgement fits command/reply sagas better than offset-based consumption.
- For analytics-style replay at very high volume we would add Kafka later. The `outbox.Publisher` and `messaging.Handler` abstractions keep that change inside the adapter.
