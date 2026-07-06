// Package rabbitmq is the subscription service's AMQP-side adapter: it turns
// Subscribe-saga reply events into calls on the orchestrator port. It lives
// here (adapter layer) rather than in internal/subscription/saga so the saga
// package stays free of transport imports.
package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	broker "github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
)

// ReplyHandler is the orchestrator surface this adapter drives. Satisfied by
// *saga.Orchestrator via structural typing; declared here so the adapter owns
// the port it depends on.
type ReplyHandler interface {
	HandleSent(ctx context.Context, sagaID string) error
	HandleFailed(ctx context.Context, sagaID, reason string) error
}

// RepliesConsumer subscribes to the app.saga.events queue and dispatches
// notifier reply events to the orchestrator. It owns its own AMQP connection
// and channel so the app's outbound publisher and the inbound replies live
// on independent channels.
type RepliesConsumer struct {
	url     string
	handler ReplyHandler
	tag     string

	conn *amqp.Connection
	ch   *amqp.Channel
}

const (
	repliesPrefetch = 8

	initialReconnectBackoff = 500 * time.Millisecond
	maxReconnectBackoff     = 30 * time.Second
)

var errRepliesChannelClosed = errors.New("saga replies channel closed")

func NewRepliesConsumer(amqpURL string, handler ReplyHandler) (*RepliesConsumer, error) {
	c := &RepliesConsumer{url: amqpURL, handler: handler, tag: "app.saga"}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *RepliesConsumer) dial() error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open channel: %w", err)
	}
	if err := broker.Declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	if err := ch.Qos(repliesPrefetch, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("set qos: %w", err)
	}
	c.conn, c.ch = conn, ch
	return nil
}

func (c *RepliesConsumer) closeSession() {
	if c.ch != nil {
		_ = c.ch.Close()
		c.ch = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *RepliesConsumer) Close() error {
	c.closeSession()
	return nil
}

// Run drives a supervisor loop: on session end (broker restart, network
// blip, channel close) it re-dials with exponential backoff and resumes
// consuming, instead of silently exiting and leaving the saga replies
// unrouted while /health still reports the app as up. Returns nil only on
// ctx cancellation.
func (c *RepliesConsumer) Run(ctx context.Context) error {
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
					slog.Info("saga: replies consumer reconnected")
					backoff = initialReconnectBackoff
					break
				} else {
					slog.Warn("saga: replies reconnect failed", "error", err, "retry_in", backoff)
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
		slog.Warn("saga: replies session ended, reconnecting", "error", err)
		c.closeSession()
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	n := cur * 2
	if n > maxReconnectBackoff {
		return maxReconnectBackoff
	}
	return n
}

// runSession consumes deliveries until the channel closes or ctx is canceled.
// A channel close returns errRepliesChannelClosed so the supervisor knows to
// reconnect; ctx cancellation returns nil.
func (c *RepliesConsumer) runSession(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		broker.QueueSagaEvents,
		c.tag,
		false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("start consume saga events: %w", err)
	}
	slog.Info("saga: replies consumer started", "queue", broker.QueueSagaEvents, "prefetch", repliesPrefetch)

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
				return errRepliesChannelClosed
			}
			c.handle(ctx, d)
		}
	}
}

func (c *RepliesConsumer) handle(ctx context.Context, d amqp.Delivery) {
	switch d.RoutingKey {
	case broker.RoutingKeyEventConfirmationSent:
		var ev broker.ConfirmationSentEvent
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

	case broker.RoutingKeyEventConfirmationFailed:
		var ev broker.ConfirmationFailedEvent
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
