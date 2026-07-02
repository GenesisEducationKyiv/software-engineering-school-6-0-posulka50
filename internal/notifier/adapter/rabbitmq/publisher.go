package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/posul/github-notifier/internal/notifier/domain"
	"github.com/posul/github-notifier/internal/platform/metrics"
)

// Publisher writes notification messages to the notifications exchange. It
// satisfies the subscription confirmationSender and release releaseSender
// ports via structural typing.
type Publisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
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
	return &Publisher{conn: conn, ch: ch}, nil
}

func (p *Publisher) Close() error {
	chErr := p.ch.Close()
	connErr := p.conn.Close()
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
	pubErr := p.ch.PublishWithContext(
		ctx,
		ExchangeNotifications,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	metrics.RabbitMQPublishDuration.WithLabelValues(routingKey).Observe(time.Since(start).Seconds())

	if pubErr != nil {
		metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "error").Inc()
		return fmt.Errorf("publish %s: %w", routingKey, pubErr)
	}
	metrics.RabbitMQMessagesPublishedTotal.WithLabelValues(routingKey, "ok").Inc()
	return nil
}
