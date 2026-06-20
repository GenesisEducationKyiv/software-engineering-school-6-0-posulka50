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

// sagaStore persists Subscribe saga state.
type sagaStore interface {
	Create(ctx context.Context, s *domain.Saga) error
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

// Start records a new pending saga for the given subscription and publishes
// the SendConfirmationCommand to the notifier. If the publish fails, the
// just-created saga row is removed so the caller sees a clean failure and
// can roll back its own subscription insert; otherwise the caller's
// subscription stays in place until a reply (or the timeout sweeper) drives
// the next transition.
func (o *Orchestrator) Start(ctx context.Context, sub *domain.Subscription, confirmURL string) (string, error) {
	s := domain.NewSaga(sub.ID)
	if err := o.sagas.Create(ctx, s); err != nil {
		return "", fmt.Errorf("create saga: %w", err)
	}

	if err := o.commands.SendConfirmationCommand(ctx, s.ID, sub.Email, sub.Repo, confirmURL); err != nil {
		// Best-effort cleanup: the saga never went live, so mark it
		// compensated to keep the journal honest. Do not delete the
		// subscription here — Start has not taken ownership of it yet; the
		// caller decides what to do with its own insert.
		if markErr := o.sagas.MarkCompensated(ctx, s.ID, "publish_failed: "+err.Error()); markErr != nil {
			slog.ErrorContext(ctx, "saga: cleanup mark compensated failed", "saga_id", s.ID, "error", markErr)
		}
		return s.ID, fmt.Errorf("publish saga command: %w", err)
	}

	slog.InfoContext(ctx, "saga: started", "saga_id", s.ID, "subscription_id", sub.ID)
	return s.ID, nil
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

	if err := o.sagas.MarkCompensated(ctx, sagaID, reason); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Raced with another worker; the winner will do the delete.
			return nil
		}
		return fmt.Errorf("mark saga compensated: %w", err)
	}

	if err := o.subs.Delete(ctx, s.SubscriptionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("compensate delete subscription: %w", err)
	}

	slog.InfoContext(ctx, "saga: compensated", "saga_id", sagaID, "subscription_id", s.SubscriptionID, "reason", reason)
	return nil
}
