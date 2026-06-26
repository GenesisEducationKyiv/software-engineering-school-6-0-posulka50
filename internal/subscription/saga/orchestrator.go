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

// subStore is the slice of the subscription repository the orchestrator needs:
// Delete for compensation (orphaned pending subscription) and GetByID for
// the sweeper's sync retry path (needs email + repo + confirm token to rebuild
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
