# Service level objectives

| SLO | SLI | Target | Window | Where |
|---|---|---|---|---|
| API availability | non-5xx / all requests (`orderflow_http_requests_total`) | 99.9% | 30 days | `deploy/prometheus/rules.yml` |
| Accept latency | p99 of `POST /v1/orders` (`orderflow_http_request_duration_seconds`) | < 300 ms | 30 days | dashboard + k6 thresholds |
| Saga completion | orders reaching a terminal state within 2 min | 99% | 30 days | `orderflow_saga_transitions_total` |
| Message freshness | outbox publish errors = 0 for 5 min | n/a | rolling | `OutboxPublishFailing` alert |

Error budget for 99.9% over 30 days: **43 minutes**. Alerting uses multi-window burn rates (Google SRE Workbook): a page at 14.4× burn over both 1 h and 5 m (2% of the budget in 1 h).

Load test thresholds in `loadtest/k6.js` encode the same targets, so a regression fails CI or a pre-release run.
