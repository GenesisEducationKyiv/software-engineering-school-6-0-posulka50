package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/posul/github-notifier/internal/notifier/domain"
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

// SendConfirmationCommand is the app-side orchestrator entry point: it
// publishes a Subscribe-saga command to be picked up by the notifier.
func (p *Publisher) SendConfirmationCommand(ctx context.Context, sagaID, to, repo, confirmURL string) error {
	return p.publish(ctx, RoutingKeyCmdSendConfirmation, SendConfirmationCommand{
		SagaID:     sagaID,
		To:         to,
		Repo:       repo,
		ConfirmURL: confirmURL,
	})
}

// PublishConfirmationSent is the notifier-side reply for a successful Resend
// call, routed back to the saga orchestrator.
func (p *Publisher) PublishConfirmationSent(ctx context.Context, sagaID string) error {
	return p.publish(ctx, RoutingKeyEventConfirmationSent, ConfirmationSentEvent{SagaID: sagaID})
}

// PublishConfirmationFailed is the notifier-side reply when Resend (or
// rendering) failed permanently. The orchestrator uses Reason for saga
// last_error and logs.
func (p *Publisher) PublishConfirmationFailed(ctx context.Context, sagaID, reason string) error {
	return p.publish(ctx, RoutingKeyEventConfirmationFailed, ConfirmationFailedEvent{
		SagaID: sagaID,
		Reason: reason,
	})
}

func (p *Publisher) publish(ctx context.Context, routingKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", routingKey, err)
	}
	if err := p.ch.PublishWithContext(
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
	); err != nil {
		return fmt.Errorf("publish %s: %w", routingKey, err)
	}
	return nil
}
