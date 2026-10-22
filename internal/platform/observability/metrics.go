package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_http_requests_total", Help: "HTTP requests by route and status code.",
	}, []string{"service", "method", "route", "code"})
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "orderflow_http_request_duration_seconds", Help: "HTTP latency.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"service", "method", "route"})
	SagaOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_saga_transitions_total", Help: "Order saga transitions by resulting status.",
	}, []string{"status"})
	OutboxPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_outbox_published_total", Help: "Outbox records published.",
	}, []string{"service"})
	OutboxErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_outbox_publish_errors_total", Help: "Outbox publish failures.",
	}, []string{"service"})
	ConsumerMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_consumer_messages_total", Help: "Consumed messages by result (ok, retry, duplicate, dead_letter, poison).",
	}, []string{"consumer", "result"})
	ProviderCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_provider_calls_total", Help: "Outbound provider calls by result.",
	}, []string{"provider", "op", "result"})
	BreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "orderflow_circuit_breaker_state", Help: "0 closed, 1 half-open, 2 open.",
	}, []string{"name"})
	BulkheadRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_bulkhead_rejected_total", Help: "Calls rejected by a full bulkhead.",
	}, []string{"name"})
	RateLimited = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orderflow_rate_limited_total", Help: "Requests rejected with 429.",
	}, []string{"service"})
)
