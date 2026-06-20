package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
)

// replyHandler is the orchestrator surface used by the reply consumer.
// Defined locally so the consumer can be tested with a fake.
type replyHandler interface {
	HandleSent(ctx context.Context, sagaID string) error
	HandleFailed(ctx context.Context, sagaID, reason string) error
}

// RepliesConsumer subscribes to the app.saga.events queue and dispatches
// notifier reply events to the orchestrator. It owns its own AMQP connection
// and channel so the app's outbound publisher and the inbound replies live
// on independent channels.
type RepliesConsumer struct {
	conn    *amqp.Connection
	ch      *amqp.Channel
	handler replyHandler
	tag     string
}

const repliesPrefetch = 8

func NewRepliesConsumer(amqpURL string, handler replyHandler) (*RepliesConsumer, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := rabbitmq.Declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err := ch.Qos(repliesPrefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}
	return &RepliesConsumer{conn: conn, ch: ch, handler: handler, tag: "app.saga"}, nil
}

func (c *RepliesConsumer) Close() error {
	chErr := c.ch.Close()
	connErr := c.conn.Close()
	if chErr != nil {
		return chErr
	}
	return connErr
}

// Run consumes saga reply events until ctx is canceled or the delivery
// channel closes.
func (c *RepliesConsumer) Run(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		rabbitmq.QueueSagaEvents,
		c.tag,
		false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("start consume saga events: %w", err)
	}
	slog.Info("saga: replies consumer started", "queue", rabbitmq.QueueSagaEvents, "prefetch", repliesPrefetch)

	for {
		select {
		case <-ctx.Done():
			if err := c.ch.Cancel(c.tag, false); err != nil {
				slog.Warn("saga: cancel replies consumer", "error", err)
			}
			slog.Info("saga: replies consumer stopped")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				slog.Warn("saga: replies channel closed")
				return nil
			}
			c.handle(ctx, d)
		}
	}
}

func (c *RepliesConsumer) handle(ctx context.Context, d amqp.Delivery) {
	switch d.RoutingKey {
	case rabbitmq.RoutingKeyEventConfirmationSent:
		var ev rabbitmq.ConfirmationSentEvent
		if err := json.Unmarshal(d.Body, &ev); err != nil {
			slog.Error("saga: unmarshal confirmation_sent", "error", err)
			_ = d.Nack(false, false)
			return
		}
		if err := c.handler.HandleSent(ctx, ev.SagaID); err != nil {
			// Transient (e.g. DB blip): requeue so we retry on next delivery.
			slog.Error("saga: handle confirmation_sent", "saga_id", ev.SagaID, "error", err)
			_ = d.Nack(false, true)
			return
		}
		_ = d.Ack(false)

	case rabbitmq.RoutingKeyEventConfirmationFailed:
		var ev rabbitmq.ConfirmationFailedEvent
		if err := json.Unmarshal(d.Body, &ev); err != nil {
			slog.Error("saga: unmarshal confirmation_failed", "error", err)
			_ = d.Nack(false, false)
			return
		}
		if err := c.handler.HandleFailed(ctx, ev.SagaID, ev.Reason); err != nil {
			slog.Error("saga: handle confirmation_failed", "saga_id", ev.SagaID, "error", err)
			_ = d.Nack(false, true)
			return
		}
		_ = d.Ack(false)

	default:
		slog.Warn("saga: unknown routing key", "key", d.RoutingKey)
		_ = d.Nack(false, false)
	}
}
