# Runbooks

## Error budget burn
1. Grafana → *orderflow · services & SLOs*: find which `service`/`route` produces the 5xx.
2. Jaeger: search the service for `error=true`, and follow the trace across messages (the trace context travels in the envelope).
3. If it started with a deploy, roll back (`helm rollback orderflow`).

## Circuit open
`PaymentProviderCircuitOpen`: the PSP is failing, and orders are being cancelled with stock released (by design).
1. Check the PSP's status page. Locally: `curl localhost:8090/admin/chaos`.
2. The breaker half-opens after 10 s and closes on its own after a successful probe.
3. If the outage is long, consider pausing order intake (rate limit to 0) instead of cancelling orders.

## Outbox backlog
`OutboxPublishFailing`: the relay cannot reach NATS.
1. `kubectl -n messaging get pods`, and the NATS `/healthz`.
2. Pending rows: `SELECT count(*) FROM outbox WHERE published_at IS NULL;`. They drain automatically once NATS is back, with no data lost.

## Dead letters
A message exhausted 8 deliveries. Find it in logs (`"msg":"message dead-lettered"`, with subject and id), fix the cause, and re-publish it from the producing service's outbox (`UPDATE outbox SET published_at = NULL WHERE id = '<id>'`). Consumers are idempotent.
