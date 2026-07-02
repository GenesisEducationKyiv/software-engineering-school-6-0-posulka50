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

// Publisher writes notification messages to the notifications exchange. It
// satisfies the subscription confirmationSender and release releaseSender
// ports via structural typing.
//
// The channel is put in confirm mode and every publish waits for the broker's
// ack before returning, so callers only see success once the message is
// safely accepted by RabbitMQ (not merely written to the TCP buffer).
// Messages are published with mandatory=true; unroutable returns are drained
// on a background goroutine and reported as the "unroutable" status.
type Publisher struct {
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns chan amqp.Return
	done    chan struct{}
}

func NewPublisher(amqpURL string) (*Publisher, error) {
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
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}

	p := &Publisher{
		conn:    conn,
		ch:      ch,
		returns: ch.NotifyReturn(make(chan amqp.Return, 16)),
		done:    make(chan struct{}),
	}
	go p.drainReturns()
	return p, nil
}

func (p *Publisher) drainReturns() {
	defer close(p.done)
	for r := range p.returns {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(r.RoutingKey, "unroutable").Inc()
		slog.Error("rabbitmq message returned as unroutable",
			"exchange", r.Exchange,
			"routing_key", r.RoutingKey,
			"reply_code", r.ReplyCode,
			"reply_text", r.ReplyText,
		)
	}
}

func (p *Publisher) Close() error {
	chErr := p.ch.Close()
	connErr := p.conn.Close()
	<-p.done
	if chErr != nil {
		return chErr
	}
	return connErr
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

	start := time.Now()
	confirm, pubErr := p.ch.PublishWithDeferredConfirmWithContext(
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
