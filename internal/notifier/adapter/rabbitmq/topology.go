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
)

// Declare creates the exchange, queues and bindings used by publisher and
// consumer. It is idempotent and safe to call from either side at startup so
// whichever boots first sets up the topology.
//
// The single-segment binding "notification.*" matches only the legacy
// confirmation/release routing keys; the saga's three-segment keys
// ("notification.command.*", "notification.event.*") land exclusively in the
// dedicated saga queues, so there is no cross-delivery.
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

	queues := []struct {
		name    string
		binding string
	}{
		{QueueDeliveries, BindingKeyDeliveries},
		{QueueSagaCommands, BindingKeySagaCommands},
		{QueueSagaEvents, BindingKeySagaEvents},
	}
	for _, q := range queues {
		if _, err := ch.QueueDeclare(q.name, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %q: %w", q.name, err)
		}
		if err := ch.QueueBind(q.name, q.binding, ExchangeNotifications, false, nil); err != nil {
			return fmt.Errorf("bind queue %q to %q: %w", q.name, ExchangeNotifications, err)
		}
	}

	return nil
}
