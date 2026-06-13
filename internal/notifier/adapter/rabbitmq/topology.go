// Package rabbitmq holds the RabbitMQ-backed publisher and consumer that move
// notification work between cmd/server and cmd/notifier.
package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeNotifications = "notifications"
	QueueDeliveries       = "notifier.deliveries"
	BindingKey            = "notification.*"

	RoutingKeyConfirmation = "notification.confirmation"
	RoutingKeyRelease      = "notification.release"
)

// Declare creates the exchange, queue and binding used by publisher and
// consumer. It is idempotent and safe to call from either side at startup so
// whichever boots first sets up the topology.
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

	if _, err := ch.QueueDeclare(
		QueueDeliveries,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare queue %q: %w", QueueDeliveries, err)
	}

	if err := ch.QueueBind(
		QueueDeliveries,
		BindingKey,
		ExchangeNotifications,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind queue %q to %q: %w", QueueDeliveries, ExchangeNotifications, err)
	}

	return nil
}
