// Package messaging defines the message envelope, the outbox/inbox ports and a
// NATS JetStream adapter.
package messaging

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Envelope wraps every message. Trace carries W3C trace context so a trace
// continues across the outbox, the broker and the consumer.
type Envelope struct {
	ID            string            `json:"id"`
	Subject       string            `json:"subject"`
	CorrelationID string            `json:"correlation_id"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Trace         map[string]string `json:"trace,omitempty"`
	Payload       json.RawMessage   `json:"payload"`
}

// New builds an envelope and injects the current trace context.
func New(ctx context.Context, subject, correlationID string, payload any) (Envelope, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return Envelope{
		ID:            uuid.NewString(),
		Subject:       subject,
		CorrelationID: correlationID,
		OccurredAt:    time.Now().UTC(),
		Trace:         carrier,
		Payload:       b,
	}, nil
}

// Context returns ctx with the remote span context of the producer.
func (e Envelope) Context(ctx context.Context) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(e.Trace))
}

// Decode unmarshals the payload.
func (e Envelope) Decode(v any) error { return json.Unmarshal(e.Payload, v) }

// Outbox is the port used by use cases to emit messages atomically with
// their state change (same transaction).
type Outbox interface {
	Add(ctx context.Context, env Envelope) error
}

// Inbox is the port used by consumers for idempotency. MarkProcessed returns
// false when the message was already handled by this consumer.
type Inbox interface {
	MarkProcessed(ctx context.Context, consumer, msgID string) (bool, error)
}

// Handler processes one message.
type Handler func(ctx context.Context, env Envelope) error
