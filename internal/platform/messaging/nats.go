package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

// Bus is the NATS JetStream adapter (publisher + durable consumers).
type Bus struct {
	nc  *nats.Conn
	js  jetstream.JetStream
	log *slog.Logger
	ccs []jetstream.ConsumeContext
}

// Connect dials NATS and makes sure the stream exists.
func Connect(ctx context.Context, url, name string, log *slog.Logger) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name(name), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       contracts.Stream,
		Subjects:   []string{"orderflow.>"},
		Storage:    jetstream.FileStorage,
		Duplicates: 2 * time.Minute, // dedupe window for Nats-Msg-Id
		MaxAge:     7 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	return &Bus{nc: nc, js: js, log: log}, nil
}

// Publish sends data with msgID as Nats-Msg-Id, so relay retries after a crash
// are deduplicated by the broker.
func (b *Bus) Publish(ctx context.Context, subject, msgID string, data []byte) error {
	_, err := b.js.PublishMsg(ctx, &nats.Msg{Subject: subject, Data: data}, jetstream.WithMsgID(msgID))
	return err
}

// Subscribe starts a durable pull consumer. Failures are NAKed with an
// exponential delay; after MaxDeliver attempts the message is terminated and
// counted as dead-lettered.
func (b *Bus) Subscribe(ctx context.Context, durable string, subjects []string, h Handler) error {
	cons, err := b.js.CreateOrUpdateConsumer(ctx, contracts.Stream, jetstream.ConsumerConfig{
		Durable:        durable,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        30 * time.Second,
		MaxDeliver:     8,
		MaxAckPending:  256,
	})
	if err != nil {
		return err
	}
	tracer := otel.Tracer("orderflow/messaging")
	cc, err := cons.Consume(func(m jetstream.Msg) {
		var env Envelope
		if err := json.Unmarshal(m.Data(), &env); err != nil {
			b.log.Error("poison message", "subject", m.Subject(), "err", err)
			observability.ConsumerMessages.WithLabelValues(durable, "poison").Inc()
			_ = m.Term()
			return
		}
		mctx, span := tracer.Start(env.Context(context.Background()), "consume "+env.Subject,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination.name", env.Subject),
				attribute.String("messaging.message.id", env.ID),
				attribute.String("orderflow.correlation_id", env.CorrelationID),
			))
		defer span.End()
		mctx, cancel := context.WithTimeout(mctx, 20*time.Second)
		defer cancel()
		if err := h(mctx, env); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			md, _ := m.Metadata()
			attempt := uint64(1)
			if md != nil {
				attempt = md.NumDelivered
			}
			if attempt >= 8 {
				b.log.ErrorContext(mctx, "message dead-lettered", "consumer", durable, "subject", env.Subject, "id", env.ID, "err", err)
				observability.ConsumerMessages.WithLabelValues(durable, "dead_letter").Inc()
				_ = m.Term()
				return
			}
			delay := time.Duration(1<<min(attempt, 6)) * 100 * time.Millisecond
			b.log.WarnContext(mctx, "handler failed, will redeliver", "consumer", durable, "subject", env.Subject, "attempt", attempt, "delay", delay, "err", err)
			observability.ConsumerMessages.WithLabelValues(durable, "retry").Inc()
			_ = m.NakWithDelay(delay)
			return
		}
		observability.ConsumerMessages.WithLabelValues(durable, "ok").Inc()
		_ = m.Ack()
	})
	if err != nil {
		return err
	}
	b.ccs = append(b.ccs, cc)
	return nil
}

// Healthy is a readiness check.
func (b *Bus) Healthy(context.Context) error {
	if b.nc.Status() != nats.CONNECTED {
		return errors.New("nats not connected: " + b.nc.Status().String())
	}
	return nil
}

// Close drains consumers (in-flight messages finish) and the connection.
func (b *Bus) Close() {
	for _, cc := range b.ccs {
		cc.Drain()
	}
	for _, cc := range b.ccs {
		<-cc.Closed()
	}
	_ = b.nc.Drain()
}
