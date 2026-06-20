package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/posul/github-notifier/internal/notifier/domain"
)

// Sender is the port the consumer hands decoded notifications to. It is
// satisfied by notifier/adapter/resend.Sender via structural typing.
type Sender interface {
	SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error
	SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error
}

// ReplyPublisher is the port used by the consumer to send Subscribe-saga
// reply events back to the orchestrator. Satisfied by *Publisher.
type ReplyPublisher interface {
	PublishConfirmationSent(ctx context.Context, sagaID string) error
	PublishConfirmationFailed(ctx context.Context, sagaID, reason string) error
}

// Consumer subscribes to the notifier's queues and dispatches messages to the
// underlying Sender. Saga commands additionally trigger reply events via
// ReplyPublisher. Permanent errors on legacy routes nack(requeue=false); saga
// commands ack after the reply is published (success or failure), so the saga
// orchestrator owns the retry/timeout decision instead of the broker.
type Consumer struct {
	conn         *amqp.Connection
	ch           *amqp.Channel
	sender       Sender
	replies      ReplyPublisher
	consumerName string
}

const consumerPrefetch = 8

func NewConsumer(amqpURL string, sender Sender, replies ReplyPublisher) (*Consumer, error) {
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
	return &Consumer{conn: conn, ch: ch, sender: sender, replies: replies, consumerName: "notifier"}, nil
}

func (c *Consumer) Close() error {
	chErr := c.ch.Close()
	connErr := c.conn.Close()
	if chErr != nil {
		return chErr
	}
	return connErr
}

// Run starts consuming from both the legacy deliveries queue and the saga
// commands queue, blocking until ctx is canceled or all delivery channels
// close. Cancellation triggers basic.cancel on each consumer so RabbitMQ
// stops pushing new messages; in-flight handlers finish first.
func (c *Consumer) Run(ctx context.Context) error {
	queues := []struct {
		queue string
		tag   string
	}{
		{QueueDeliveries, c.consumerName + ".deliveries"},
		{QueueSagaCommands, c.consumerName + ".commands"},
	}

	var wg sync.WaitGroup
	for _, q := range queues {
		deliveries, err := c.ch.Consume(q.queue, q.tag, false, false, false, false, nil)
		if err != nil {
			return fmt.Errorf("start consume %q: %w", q.queue, err)
		}
		slog.Info("rabbitmq: consumer started", "queue", q.queue, "prefetch", consumerPrefetch)

		wg.Add(1)
		go func(queue, tag string, deliveries <-chan amqp.Delivery) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					if err := c.ch.Cancel(tag, false); err != nil {
						slog.Warn("rabbitmq: cancel consumer", "queue", queue, "error", err)
					}
					slog.Info("rabbitmq: consumer stopped", "queue", queue)
					return
				case d, ok := <-deliveries:
					if !ok {
						slog.Warn("rabbitmq: deliveries channel closed", "queue", queue)
						return
					}
					c.handle(ctx, d)
				}
			}
		}(q.queue, q.tag, deliveries)
	}

	wg.Wait()
	return nil
}

func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) {
	switch d.RoutingKey {
	case RoutingKeyConfirmation:
		var msg ConfirmationMessage
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal confirmation", "error", err)
			_ = d.Nack(false, false)
			return
		}
		if err := c.sender.SendConfirmation(ctx, msg.To, domain.ConfirmData{
			Repo:       msg.Repo,
			ConfirmURL: msg.ConfirmURL,
		}); err != nil {
			slog.Error("rabbitmq: send confirmation failed", "to", msg.To, "repo", msg.Repo, "error", err)
			_ = d.Nack(false, false)
			return
		}
		_ = d.Ack(false)
		slog.Info("rabbitmq: confirmation delivered", "to", msg.To, "repo", msg.Repo)

	case RoutingKeyCmdSendConfirmation:
		var msg SendConfirmationCommand
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal send_confirmation command", "error", err)
			_ = d.Nack(false, false)
			return
		}
		sendErr := c.sender.SendConfirmation(ctx, msg.To, domain.ConfirmData{
			Repo:       msg.Repo,
			ConfirmURL: msg.ConfirmURL,
		})
		// Publish a reply event reflecting the outcome, then ack. The saga
		// orchestrator owns retry/timeout; redelivering the command would
		// produce duplicate emails since there is no idempotency key on the
		// Resend side.
		var replyErr error
		if sendErr == nil {
			replyErr = c.replies.PublishConfirmationSent(ctx, msg.SagaID)
			slog.Info("rabbitmq: confirmation sent", "saga_id", msg.SagaID, "to", msg.To, "repo", msg.Repo)
		} else {
			slog.Error("rabbitmq: send confirmation failed", "saga_id", msg.SagaID, "to", msg.To, "repo", msg.Repo, "error", sendErr)
			replyErr = c.replies.PublishConfirmationFailed(ctx, msg.SagaID, sendErr.Error())
		}
		if replyErr != nil {
			// Reply lost in transit — saga will be compensated by the timeout
			// sweeper. Still ack to avoid Resend duplication on redelivery.
			slog.Error("rabbitmq: publish saga reply failed", "saga_id", msg.SagaID, "error", replyErr)
		}
		_ = d.Ack(false)

	case RoutingKeyRelease:
		var msg ReleaseMessage
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal release", "error", err)
			_ = d.Nack(false, false)
			return
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
			return
		}
		_ = d.Ack(false)
		slog.Info("rabbitmq: release delivered", "to", msg.To, "repo", msg.Repo, "tag", msg.TagName)

	default:
		slog.Warn("rabbitmq: unknown routing key", "key", d.RoutingKey)
		_ = d.Nack(false, false)
	}
}
