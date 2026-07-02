package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

// ReplyPublisher is the port used by the consumer to send Subscribe-saga
// reply events back to the orchestrator. Satisfied by *Publisher.
type ReplyPublisher interface {
	PublishConfirmationSent(ctx context.Context, sagaID string) error
	PublishConfirmationFailed(ctx context.Context, sagaID, reason string) error
}

// Consumer subscribes to the notifier's queues and dispatches messages to the
// underlying Sender. Saga commands additionally trigger reply events via
// ReplyPublisher. Permanent errors on legacy routes nack(requeue=false) into
// the dead-letter exchange; saga commands ack after the reply is published
// (success or failure), so the saga orchestrator owns the retry/timeout
// decision instead of the broker.
//
// Run drives a supervisor loop: when the connection or any delivery channel
// closes unexpectedly, the consumer re-dials with exponential backoff and
// resumes consuming from every business queue. Graceful shutdown is
// triggered by canceling ctx.
type Consumer struct {
	url          string
	sender       Sender
	replies      ReplyPublisher
	consumerName string

	conn *amqp.Connection
	ch   *amqp.Channel
}

const (
	consumerPrefetch = 8

	resultAck     = "ack"
	resultNack    = "nack"
	resultUnknown = "unknown"
)

var errSessionEnded = errors.New("rabbitmq consumer session ended")

func NewConsumer(amqpURL string, sender Sender, replies ReplyPublisher) (*Consumer, error) {
	c := &Consumer{
		url:          amqpURL,
		sender:       sender,
		replies:      replies,
		consumerName: "notifier",
	}
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

// Run consumes from the legacy deliveries queue and the saga commands queue
// until ctx is canceled. On connection loss it re-dials with exponential
// backoff instead of giving up.
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

type sessionQueue struct {
	queue, tag string
}

func (c *Consumer) sessionQueues() []sessionQueue {
	return []sessionQueue{
		{QueueDeliveries, c.consumerName + ".deliveries"},
		{QueueSagaCommands, c.consumerName + ".commands"},
	}
}

func (c *Consumer) runSession(ctx context.Context) error {
	closeCh := c.conn.NotifyClose(make(chan *amqp.Error, 1))
	queues := c.sessionQueues()

	deliveryChans, err := c.startConsumers(queues)
	if err != nil {
		return err
	}

	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer cancelSession()

	closedCh := make(chan string, len(queues))
	var wg sync.WaitGroup
	for i, q := range queues {
		wg.Add(1)
		go c.consumeQueue(ctx, sessionCtx, &wg, q.queue, deliveryChans[i], closedCh)
	}

	sessErr := c.awaitSessionEnd(ctx, queues, closeCh, closedCh)
	cancelSession()
	wg.Wait()

	if ctx.Err() != nil {
		slog.Info("rabbitmq: consumer stopped")
		return nil
	}
	return sessErr
}

func (c *Consumer) startConsumers(queues []sessionQueue) ([]<-chan amqp.Delivery, error) {
	deliveryChans := make([]<-chan amqp.Delivery, 0, len(queues))
	for _, q := range queues {
		deliveries, err := c.ch.Consume(q.queue, q.tag, false, false, false, false, nil)
		if err != nil {
			return nil, fmt.Errorf("start consume %q: %w", q.queue, err)
		}
		slog.Info("rabbitmq: consumer started", "queue", q.queue, "prefetch", consumerPrefetch)
		deliveryChans = append(deliveryChans, deliveries)
	}
	return deliveryChans, nil
}

func (c *Consumer) consumeQueue(ctx, sessionCtx context.Context, wg *sync.WaitGroup, queue string, deliveries <-chan amqp.Delivery, closedCh chan<- string) {
	defer wg.Done()
	for {
		select {
		case <-sessionCtx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				select {
				case closedCh <- queue:
				default:
				}
				return
			}
			c.handle(ctx, d)
		}
	}
}

func (c *Consumer) awaitSessionEnd(ctx context.Context, queues []sessionQueue, closeCh <-chan *amqp.Error, closedCh <-chan string) error {
	select {
	case <-ctx.Done():
		for _, q := range queues {
			if err := c.ch.Cancel(q.tag, false); err != nil {
				slog.Warn("rabbitmq: cancel consumer", "queue", q.queue, "error", err)
			}
		}
		return nil
	case amqErr := <-closeCh:
		if amqErr == nil {
			return errSessionEnded
		}
		return fmt.Errorf("%w: %w", errSessionEnded, amqErr)
	case queue := <-closedCh:
		return fmt.Errorf("%w: deliveries channel closed for %q", errSessionEnded, queue)
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
	case RoutingKeyCmdSendConfirmation:
		var msg SendConfirmationCommand
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal send_confirmation command", "error", err)
			_ = d.Nack(false, false)
			return resultNack
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
		return resultAck

	case RoutingKeyRelease:
		var msg ReleaseMessage
		if err := json.Unmarshal(d.Body, &msg); err != nil {
			slog.Error("rabbitmq: unmarshal release", "error", err)
			_ = d.Nack(false, false)
			return resultNack
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
			return resultNack
		}
		_ = d.Ack(false)
		slog.Info("rabbitmq: release delivered", "to", msg.To, "repo", msg.Repo, "tag", msg.TagName)
		return resultAck

	default:
		slog.Warn("rabbitmq: unknown routing key", "key", d.RoutingKey)
		_ = d.Nack(false, false)
		return resultUnknown
	}
}
