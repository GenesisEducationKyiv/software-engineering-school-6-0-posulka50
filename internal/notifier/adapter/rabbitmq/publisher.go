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

// Publisher writes notification messages to the notifications exchange. It
// satisfies the subscription confirmationSender and release releaseSender
// ports via structural typing.
//
// The channel is put in confirm mode and every publish waits for the broker's
// ack before returning, so callers only see success once the message is
// safely accepted by RabbitMQ (not merely written to the TCP buffer).
// Messages are published with mandatory=true; unroutable returns are drained
// on a background goroutine and reported as the "unroutable" status.
//
// A supervisor goroutine subscribes to conn.NotifyClose and re-dials with
// exponential backoff when the connection drops, so a RabbitMQ restart no
// longer leaves the publisher permanently broken. Publishes issued while the
// connection is down fail fast with ErrPublisherNotReady.
type Publisher struct {
	url string

	mu   sync.RWMutex
	conn *amqp.Connection
	ch   *amqp.Channel

	done chan struct{}
	wg   sync.WaitGroup
}

// ErrPublisherNotReady is returned by SendConfirmation / SendReleaseNotification
// when the underlying RabbitMQ connection is being re-established. Callers may
// retry.
var ErrPublisherNotReady = errors.New("rabbitmq publisher not ready")

const (
	initialReconnectBackoff = 500 * time.Millisecond
	maxReconnectBackoff     = 30 * time.Second
)

func NewPublisher(amqpURL string) (*Publisher, error) {
	p := &Publisher{url: amqpURL, done: make(chan struct{})}
	conn, ch, closeCh, returns, err := p.dial()
	if err != nil {
		return nil, err
	}
	p.setSession(conn, ch)
	p.wg.Add(1)
	go p.supervise(closeCh, returns)
	return p, nil
}

func (p *Publisher) dial() (*amqp.Connection, *amqp.Channel, chan *amqp.Error, chan amqp.Return, error) {
	conn, err := amqp.Dial(p.url)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, nil, fmt.Errorf("open channel: %w", err)
	}
	if err := Declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, nil, fmt.Errorf("enable publisher confirms: %w", err)
	}
	closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))
	returns := ch.NotifyReturn(make(chan amqp.Return, 16))
	return conn, ch, closeCh, returns, nil
}

func (p *Publisher) setSession(conn *amqp.Connection, ch *amqp.Channel) {
	p.mu.Lock()
	p.conn, p.ch = conn, ch
	p.mu.Unlock()
}

func (p *Publisher) clearSession() (*amqp.Connection, *amqp.Channel) {
	p.mu.Lock()
	conn, ch := p.conn, p.ch
	p.conn, p.ch = nil, nil
	p.mu.Unlock()
	return conn, ch
}

func (p *Publisher) supervise(closeCh chan *amqp.Error, returns chan amqp.Return) {
	defer p.wg.Done()
	for {
		drainDone := make(chan struct{})
		go func(r chan amqp.Return) {
			for x := range r {
				metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(x.RoutingKey, "unroutable").Inc()
				slog.Error("rabbitmq message returned as unroutable",
					"exchange", x.Exchange,
					"routing_key", x.RoutingKey,
					"reply_code", x.ReplyCode,
					"reply_text", x.ReplyText,
				)
			}
			close(drainDone)
		}(returns)

		select {
		case <-p.done:
			conn, ch := p.clearSession()
			if ch != nil {
				_ = ch.Close()
			}
			if conn != nil {
				_ = conn.Close()
			}
			<-drainDone
			return
		case amqErr := <-closeCh:
			slog.Warn("rabbitmq: publisher connection lost", "error", amqErr)
		}

		conn, ch := p.clearSession()
		if ch != nil {
			_ = ch.Close()
		}
		if conn != nil {
			_ = conn.Close()
		}
		<-drainDone

		backoff := initialReconnectBackoff
		for {
			select {
			case <-p.done:
				return
			default:
			}
			newConn, newCh, newCloseCh, newReturns, err := p.dial()
			if err == nil {
				p.setSession(newConn, newCh)
				closeCh, returns = newCloseCh, newReturns
				slog.Info("rabbitmq: publisher reconnected")
				break
			}
			slog.Warn("rabbitmq: publisher reconnect failed", "error", err, "retry_in", backoff)
			select {
			case <-p.done:
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff)
		}
	}
}

func nextBackoff(cur time.Duration) time.Duration {
	n := cur * 2
	if n > maxReconnectBackoff {
		return maxReconnectBackoff
	}
	return n
}

func (p *Publisher) Close() error {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.wg.Wait()
	return nil
}

func (p *Publisher) SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error {
	return p.publish(ctx, RoutingKeyConfirmation, ConfirmationMessage{
		To:         to,
		Repo:       data.Repo,
		ConfirmURL: data.ConfirmURL,
	})
}

func (p *Publisher) SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error {
	return p.publish(ctx, RoutingKeyRelease, ReleaseMessage{
		To:             to,
		Repo:           data.Repo,
		TagName:        data.TagName,
		ReleaseName:    data.ReleaseName,
		Body:           data.Body,
		ReleaseURL:     data.ReleaseURL,
		UnsubscribeURL: data.UnsubscribeURL,
	})
}

func (p *Publisher) publish(ctx context.Context, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "error").Inc()
		return fmt.Errorf("marshal %s: %w", routingKey, err)
	}

	p.mu.RLock()
	ch := p.ch
	p.mu.RUnlock()
	if ch == nil {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "error").Inc()
		return fmt.Errorf("publish %s: %w", routingKey, ErrPublisherNotReady)
	}

	start := time.Now()
	confirm, pubErr := ch.PublishWithDeferredConfirmWithContext(
		ctx,
		ExchangeNotifications,
		routingKey,
		true,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	if pubErr != nil {
		metrics.RabbitMQPublishDuration.WithLabelValues(routingKey).Observe(time.Since(start).Seconds())
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "error").Inc()
		return fmt.Errorf("publish %s: %w", routingKey, pubErr)
	}

	acked, waitErr := confirm.WaitContext(ctx)
	metrics.RabbitMQPublishDuration.WithLabelValues(routingKey).Observe(time.Since(start).Seconds())
	if waitErr != nil {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "error").Inc()
		return fmt.Errorf("await confirm %s: %w", routingKey, waitErr)
	}
	if !acked {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "nack").Inc()
		return fmt.Errorf("publish %s: broker nacked message", routingKey)
	}
	metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "ok").Inc()
	return nil
}
