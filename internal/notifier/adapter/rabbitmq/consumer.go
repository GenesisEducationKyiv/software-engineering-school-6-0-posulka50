package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/posul/github-notifier/internal/notifier/domain"
	"github.com/posul/github-notifier/internal/platform/metrics"
)

// Sender is the port the consumer hands decoded notifications to. It is
// satisfied by notifier/adapter/resend.Sender via structural typing.
type Sender interface {
	SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error
	SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error
}

// Consumer subscribes to the deliveries queue and dispatches messages to the
// underlying Sender. Permanent errors nack(requeue=false); a future commit
// can route those into a dead-letter queue.
type Consumer struct {
	conn   *amqp.Connection
	ch     *amqp.Channel
	sender Sender
	tag    string
}

const consumerPrefetch = 8

func NewConsumer(amqpURL string, sender Sender) (*Consumer, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := Declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err := ch.Qos(consumerPrefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}
	return &Consumer{conn: conn, ch: ch, sender: sender, tag: "notifier"}, nil
}

func (c *Consumer) Close() error {
	chErr := c.ch.Close()
	connErr := c.conn.Close()
	if chErr != nil {
		return chErr
	}
	return connErr
}

// Run starts consuming and blocks until ctx is canceled or the delivery
// channel closes. Cancellation triggers a basic.cancel so RabbitMQ stops
// pushing new messages; in-flight handlers finish first.
func (c *Consumer) Run(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		QueueDeliveries,
		c.tag,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("start consume: %w", err)
	}

	slog.Info("rabbitmq: consumer started", "queue", QueueDeliveries, "prefetch", consumerPrefetch)

	for {
		select {
		case <-ctx.Done():
			if err := c.ch.Cancel(c.tag, false); err != nil {
				slog.Warn("rabbitmq: cancel consumer", "error", err)
			}
			slog.Info("rabbitmq: consumer stopped")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				slog.Warn("rabbitmq: deliveries channel closed")
				return nil
			}
			c.handle(ctx, d)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) {
	routingKey := d.RoutingKey
	start := time.Now()
	result := c.dispatch(ctx, d)
	metrics.RabbitMQMessageProcessingDuration.WithLabelValues(routingKey).Observe(time.Since(start).Seconds())
	metrics.RabbitMQMessagesConsumedTotal.WithLabelValues(routingKey, result).Inc()
}

func (c *Consumer) dispatch(ctx context.Context, d amqp.Delivery) string {
	switch d.RoutingKey {
	case RoutingKeyConfirmation:
		var msg ConfirmationMessage
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal confirmation", "error", err)
			_ = d.Nack(false, false)
			return "nack"
		}
		if err := c.sender.SendConfirmation(ctx, msg.To, domain.ConfirmData{
			Repo:       msg.Repo,
			ConfirmURL: msg.ConfirmURL,
		}); err != nil {
			slog.Error("rabbitmq: send confirmation failed", "to", msg.To, "repo", msg.Repo, "error", err)
			_ = d.Nack(false, false)
			return "nack"
		}
		_ = d.Ack(false)
		slog.Info("rabbitmq: confirmation delivered", "to", msg.To, "repo", msg.Repo)
		return "ack"

	case RoutingKeyRelease:
		var msg ReleaseMessage
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal release", "error", err)
			_ = d.Nack(false, false)
			return "nack"
		}
		if err := c.sender.SendReleaseNotification(ctx, msg.To, domain.ReleaseData{
			Repo:           msg.Repo,
			TagName:        msg.TagName,
			ReleaseName:    msg.ReleaseName,
			Body:           msg.Body,
			ReleaseURL:     msg.ReleaseURL,
			UnsubscribeURL: msg.UnsubscribeURL,
		}); err != nil {
			slog.Error("rabbitmq: send release failed", "to", msg.To, "repo", msg.Repo, "tag", msg.TagName, "error", err)
			_ = d.Nack(false, false)
			return "nack"
		}
		_ = d.Ack(false)
		slog.Info("rabbitmq: release delivered", "to", msg.To, "repo", msg.Repo, "tag", msg.TagName)
		return "ack"

	default:
		slog.Warn("rabbitmq: unknown routing key", "key", d.RoutingKey)
		_ = d.Nack(false, false)
		return "unknown"
	}
}
