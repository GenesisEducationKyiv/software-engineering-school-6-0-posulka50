package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
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
//
// Run drives a supervisor loop: when the connection or delivery channel
// closes unexpectedly, the consumer re-dials with exponential backoff and
// resumes consuming. Graceful shutdown is triggered by canceling ctx.
type Consumer struct {
	url    string
	sender Sender
	tag    string

	conn *amqp.Connection
	ch   *amqp.Channel
}

const consumerPrefetch = 8

var errSessionEnded = errors.New("rabbitmq consumer session ended")

func NewConsumer(amqpURL string, sender Sender) (*Consumer, error) {
	c := &Consumer{url: amqpURL, sender: sender, tag: "notifier"}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Consumer) dial() error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open channel: %w", err)
	}
	if err := Declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	if err := ch.Qos(consumerPrefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("set qos: %w", err)
	}
	c.conn = conn
	c.ch = ch
	return nil
}

func (c *Consumer) closeSession() {
	if c.ch != nil {
		_ = c.ch.Close()
		c.ch = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Consumer) Close() error {
	c.closeSession()
	return nil
}

// Run consumes messages until ctx is canceled. On connection loss it
// re-dials with exponential backoff instead of giving up.
func (c *Consumer) Run(ctx context.Context) error {
	backoff := initialReconnectBackoff
	for {
		if ctx.Err() != nil {
			c.closeSession()
			return nil
		}
		if c.ch == nil {
			for {
				if ctx.Err() != nil {
					return nil
				}
				if err := c.dial(); err == nil {
					slog.Info("rabbitmq: consumer reconnected")
					backoff = initialReconnectBackoff
					break
				} else {
					slog.Warn("rabbitmq: consumer reconnect failed", "error", err, "retry_in", backoff)
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(backoff):
					}
					backoff = nextBackoff(backoff)
				}
			}
		}

		err := c.runSession(ctx)
		if ctx.Err() != nil {
			c.closeSession()
			return nil
		}
		slog.Warn("rabbitmq: consumer session ended, reconnecting", "error", err)
		c.closeSession()
	}
}

func (c *Consumer) runSession(ctx context.Context) error {
	closeCh := c.conn.NotifyClose(make(chan *amqp.Error, 1))
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
		case amqErr := <-closeCh:
			return fmt.Errorf("%w: %v", errSessionEnded, amqErr)
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("%w: deliveries channel closed", errSessionEnded)
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
