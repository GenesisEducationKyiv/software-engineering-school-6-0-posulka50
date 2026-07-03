// Package saga implements the Subscribe orchestrated saga: it owns the
// distributed transaction that pairs a subscription record (in app's DB) with
// a confirmation email delivered by the notifier service. The orchestrator
// persists saga state, dispatches commands over RabbitMQ, and reacts to
// reply events with success/compensation transitions.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/posul/github-notifier/internal/subscription/domain"
)

// commandPublisher publishes Subscribe-saga commands to the broker. Satisfied
// by *rabbitmq.Publisher via structural typing.
type commandPublisher interface {
	SendConfirmationCommand(ctx context.Context, sagaID, to, repo, confirmURL string) error
}

// sagaStore persists Subscribe saga state. The saga row itself is inserted
// atomically alongside the subscription by the use case (see
// subscription.CreateWithSaga); the orchestrator only transitions existing
// rows, so Create is not part of this port.
type sagaStore interface {
	Get(ctx context.Context, id string) (*domain.Saga, error)
	MarkCompleted(ctx context.Context, id string) error
	MarkCompensated(ctx context.Context, id string, reason string) error
	MarkTimedOut(ctx context.Context, id string) error
	GetPendingOlderThan(ctx context.Context, threshold time.Time) ([]*domain.Saga, error)
}

// subStore is the slice of the subscription repository the orchestrator needs
// for compensation (deleting an orphaned pending subscription).
type subStore interface {
	Delete(ctx context.Context, id string) error
}

// Orchestrator owns the Subscribe saga lifecycle.
type Orchestrator struct {
	commands commandPublisher
	sagas    sagaStore
	subs     subStore
}

func New(commands commandPublisher, sagas sagaStore, subs subStore) *Orchestrator {
	return &Orchestrator{commands: commands, sagas: sagas, subs: subs}
}

// Publish sends the SendConfirmationCommand for a saga row that the caller
// has already persisted atomically with its subscription (see
// SubscriptionRepository.CreateWithSaga). On publish failure the orchestrator
// runs the standard compensation inline (delete subscription, mark
// compensated); if that inline path itself errors, the saga stays pending
// and the timeout sweeper finishes the compensation later — so a lost
// publish never leaves an orphaned subscription behind.
func (o *Orchestrator) Publish(ctx context.Context, sagaID string, sub *domain.Subscription, confirmURL string) error {
	if err := o.commands.SendConfirmationCommand(ctx, sagaID, sub.Email, sub.Repo, confirmURL); err != nil {
		reason := "publish_failed: " + err.Error()
		if compErr := o.HandleFailed(ctx, sagaID, reason); compErr != nil {
			slog.ErrorContext(ctx, "saga: inline compensation after publish failure did not finish", "saga_id", sagaID, "error", compErr)
		}
		return fmt.Errorf("publish saga command: %w", err)
	}
	slog.InfoContext(ctx, "saga: published", "saga_id", sagaID, "subscription_id", sub.ID)
	return nil
}

// HandleSent transitions a pending saga to completed on the notifier's
// success reply. Idempotent: a duplicate event (re-delivered by RabbitMQ) is
// a no-op because the repository guards on state='pending'.
func (o *Orchestrator) HandleSent(ctx context.Context, sagaID string) error {
	if err := o.sagas.MarkCompleted(ctx, sagaID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			slog.InfoContext(ctx, "saga: sent ignored, saga not pending", "saga_id", sagaID)
			return nil
		}
		return fmt.Errorf("mark saga completed: %w", err)
	}
	slog.InfoContext(ctx, "saga: completed", "saga_id", sagaID)
	return nil
}

// HandleFailed runs the compensation: mark the saga compensated and delete
// the orphaned pending subscription so the user does not see a phantom
// subscription that can never be confirmed. Idempotent.
func (o *Orchestrator) HandleFailed(ctx context.Context, sagaID, reason string) error {
	s, err := o.sagas.Get(ctx, sagaID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			slog.WarnContext(ctx, "saga: failed event for unknown saga", "saga_id", sagaID)
			return nil
		}
		return fmt.Errorf("get saga: %w", err)
	}
	if s.State != domain.SagaStatePending {
		slog.InfoContext(ctx, "saga: failed ignored, saga not pending", "saga_id", sagaID, "state", s.State)
		return nil
	}

	// Delete first, then mark compensated. If Delete fails and this method
	// returns an error, the broker redelivers the reply and the saga is still
	// pending, so the whole compensation runs again — a stuck subscription
	// row cannot outlive the delivery. Delete already treats ErrNotFound as
	// success, so a retry after a partial success is a safe no-op.
	if err := o.subs.Delete(ctx, s.SubscriptionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("compensate delete subscription: %w", err)
	}

	if err := o.sagas.MarkCompensated(ctx, sagaID, reason); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Raced with another worker; the winner already flipped the state
			// (and, thanks to the ordering above, already ran the delete).
			return nil
		}
		return fmt.Errorf("mark saga compensated: %w", err)
	}

	slog.InfoContext(ctx, "saga: compensated", "saga_id", sagaID, "subscription_id", s.SubscriptionID, "reason", reason)
	return nil
}
