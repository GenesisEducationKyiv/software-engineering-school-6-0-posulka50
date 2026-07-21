// Package saga implements the Subscribe orchestrated saga: it owns the
// distributed transaction that pairs a subscription record (in app's DB) with
// a confirmation email delivered by the notifier service. The orchestrator
// persists saga state transitions, dispatches commands over RabbitMQ, reacts
// to reply events with success/compensation transitions, and exposes a
// last-chance synchronous retry over gRPC for the TimeoutSweeper.
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

// subStore is the slice of the subscription repository the orchestrator needs:
// Delete for compensation (orphaned pending subscription) and GetByID for the
// sweeper's sync retry path (needs email + repo + confirm token to rebuild
// the confirmation URL).
type subStore interface {
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*domain.Subscription, error)
}

// syncRetrier is the gRPC client wrapper used by AttemptSyncRetry. Satisfied
// by *Retrier; injected as an interface so tests can stub the gRPC call.
type syncRetrier interface {
	Retry(ctx context.Context, sagaID, to, repo, confirmURL string) error
}

// Orchestrator owns the Subscribe saga lifecycle.
type Orchestrator struct {
	commands commandPublisher
	sagas    sagaStore
	subs     subStore
	retrier  syncRetrier
	baseURL  string
}

func New(commands commandPublisher, sagas sagaStore, subs subStore, retrier syncRetrier, baseURL string) *Orchestrator {
	return &Orchestrator{commands: commands, sagas: sagas, subs: subs, retrier: retrier, baseURL: baseURL}
}

// Publish dispatches the SendConfirmationCommand for an already-persisted
// pending saga. The saga row and its subscription are inserted atomically by
// the use case before this call, so failure only needs to compensate the
// downstream side: on publish error we drive the compensation inline (delete
// subscription + mark compensated); if that inline path itself errors, the
// saga stays pending and the timeout sweeper finishes the compensation later
// — so a lost publish never leaves an orphaned subscription behind.
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

// AttemptSyncRetry is the TimeoutSweeper's last-chance path: before
// compensating a stuck saga, it calls the notifier directly over gRPC. On a
// successful RPC the saga is moved to completed (idempotently, via the SQL
// guard) and the user's subscription is preserved. On any failure the error
// is returned so the caller can fall through to compensation.
//
// Concurrency: an async reply may land between the RPC success and the
// MarkCompleted write. The SQL guard turns the loser into ErrNotFound, which
// we treat as a benign no-op rather than a retry failure.
func (o *Orchestrator) AttemptSyncRetry(ctx context.Context, sagaID string) error {
	s, err := o.sagas.Get(ctx, sagaID)
	if err != nil {
		return fmt.Errorf("get saga: %w", err)
	}
	if s.State != domain.SagaStatePending {
		slog.InfoContext(ctx, "saga: sync retry skipped, saga not pending", "saga_id", sagaID, "state", s.State)
		return nil
	}

	sub, err := o.subs.GetByID(ctx, s.SubscriptionID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", o.baseURL, sub.ConfirmToken)
	if err := o.retrier.Retry(ctx, sagaID, sub.Email, sub.Repo, confirmURL); err != nil {
		return fmt.Errorf("sync retry: %w", err)
	}

	if err := o.sagas.MarkCompleted(ctx, sagaID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Raced with an async reply: the saga is already in a terminal
			// state. The email was sent twice (mitigated by notifier dedupe);
			// the saga journal is consistent.
			slog.InfoContext(ctx, "saga: sync retry succeeded but saga already terminal", "saga_id", sagaID)
			return nil
		}
		return fmt.Errorf("mark saga completed: %w", err)
	}
	slog.InfoContext(ctx, "saga: rescued via sync retry", "saga_id", sagaID, "subscription_id", sub.ID)
	return nil
}

// HandleFailed runs the compensation: delete the orphaned pending subscription
// and mark the saga compensated. Deletes first so that a partial success
// (delete OK, mark fails) is safely retried by the broker/sweeper: the saga
// stays pending and the whole compensation runs again; Delete treats
// ErrNotFound as success, so a retry after a partial success is a no-op.
// Idempotent.
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
