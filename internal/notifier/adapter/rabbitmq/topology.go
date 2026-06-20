// Package rabbitmq holds the RabbitMQ-backed publisher and consumer that move
// notification work between cmd/server and cmd/notifier.
package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeNotifications = "notifications"

	// Fire-and-forget delivery queue (legacy confirmation + release paths).
	QueueDeliveries        = "notifier.deliveries"
	BindingKeyDeliveries   = "notification.*"
	RoutingKeyConfirmation = "notification.confirmation"
	RoutingKeyRelease      = "notification.release"

	// Subscribe-saga: notifier consumes commands here.
	QueueSagaCommands             = "notifier.commands"
	BindingKeySagaCommands        = "notification.command.*"
	RoutingKeyCmdSendConfirmation = "notification.command.send_confirmation"

	// Subscribe-saga: app (orchestrator) consumes reply events here.
	QueueSagaEvents                   = "app.saga.events"
	BindingKeySagaEvents              = "notification.event.*"
	RoutingKeyEventConfirmationSent   = "notification.event.confirmation_sent"
	RoutingKeyEventConfirmationFailed = "notification.event.confirmation_failed"

	// Dead-letter topology. Messages nacked with requeue=false by the
	// consumer (unmarshal errors, sender failures, unknown routing keys)
	// are routed here instead of being silently dropped. Ops can inspect
	// or replay from ExchangeDeadLetter / QueueDeadLetters.
	ExchangeDeadLetter = "notifications.dlx"
	QueueDeadLetters   = "notifier.dead_letters"
)

// Declare creates the exchange, queues and bindings used by publisher and
// consumer, plus the dead-letter exchange and queue that hold rejected
// messages. It is idempotent and safe to call from either side at startup so
// whichever boots first sets up the topology.
//
// The single-segment binding "notification.*" matches only the legacy
// confirmation/release routing keys; the saga's three-segment keys
// ("notification.command.*", "notification.event.*") land exclusively in the
// dedicated saga queues, so there is no cross-delivery.
//
// Note: adding x-dead-letter-exchange to an existing queue is a
// PRECONDITION_FAILED because queue arguments are immutable. If upgrading
// a running deployment, delete the existing business queues once (they will
// be recreated with the new args on next startup).
func Declare(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(
		ExchangeNotifications,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare exchange %q: %w", ExchangeNotifications, err)
	}

	if err := ch.ExchangeDeclare(
		ExchangeDeadLetter,
		"fanout",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare exchange %q: %w", ExchangeDeadLetter, err)
	}

	if _, err := ch.QueueDeclare(
		QueueDeadLetters,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare queue %q: %w", QueueDeadLetters, err)
	}
	if err := ch.QueueBind(
		QueueDeadLetters,
		"",
		ExchangeDeadLetter,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind queue %q to %q: %w", QueueDeadLetters, ExchangeDeadLetter, err)
	}

	queues := []struct {
		name    string
		binding string
	}{
		{QueueDeliveries, BindingKeyDeliveries},
		{QueueSagaCommands, BindingKeySagaCommands},
		{QueueSagaEvents, BindingKeySagaEvents},
	}
	businessQueueArgs := amqp.Table{"x-dead-letter-exchange": ExchangeDeadLetter}
	for _, q := range queues {
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, businessQueueArgs); err != nil {
			return fmt.Errorf("declare queue %q: %w", q.name, err)
		}
		if err := ch.QueueBind(q.name, q.binding, ExchangeNotifications, false, nil); err != nil {
			return fmt.Errorf("bind queue %q to %q: %w", q.name, ExchangeNotifications, err)
		}
	}

	return nil
}
