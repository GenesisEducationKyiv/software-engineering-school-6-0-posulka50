package saga

import (
	"context"
	"log/slog"
	"time"

	"github.com/posul/github-notifier/internal/subscription/domain"
)

// sweeperStore is the slice of the saga store the sweeper needs.
type sweeperStore interface {
	GetPendingOlderThan(ctx context.Context, threshold time.Time) ([]*domain.Saga, error)
}

// failHandler routes timed-out sagas through the same compensation path as
// real failure events, so the user-visible outcome is identical regardless
// of whether the notifier reported failure or simply went silent.
type failHandler interface {
	HandleFailed(ctx context.Context, sagaID, reason string) error
}

// TimeoutSweeper is the safety net for Subscribe sagas whose reply event
// never arrived (notifier crashed mid-send, broker dropped the event, etc.).
// It runs as a background goroutine, periodically scanning for pending sagas
// older than the configured timeout and routing them through the standard
// failed-event compensation.
type TimeoutSweeper struct {
	sagas    sweeperStore
	handler  failHandler
	timeout  time.Duration
	interval time.Duration
}

func NewTimeoutSweeper(sagas sweeperStore, handler failHandler, timeout, interval time.Duration) *TimeoutSweeper {
	return &TimeoutSweeper{
		sagas:    sagas,
		handler:  handler,
		timeout:  timeout,
		interval: interval,
	}
}

// Run blocks until ctx is canceled, sweeping on each interval tick. The
// first sweep runs immediately so a restart does not have to wait an entire
// interval before catching sagas left in pending from the previous process.
func (s *TimeoutSweeper) Run(ctx context.Context) {
	slog.Info("saga: timeout sweeper started", "timeout", s.timeout, "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("saga: timeout sweeper stopped")
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *TimeoutSweeper) sweep(ctx context.Context) {
	threshold := time.Now().UTC().Add(-s.timeout)
	stuck, err := s.sagas.GetPendingOlderThan(ctx, threshold)
	if err != nil {
		slog.ErrorContext(ctx, "saga: sweeper fetch failed", "error", err)
		return
	}
	if len(stuck) == 0 {
		return
	}

	slog.WarnContext(ctx, "saga: sweeping stuck sagas", "count", len(stuck))
	for _, saga := range stuck {
		if ctx.Err() != nil {
			return
		}
		if err := s.handler.HandleFailed(ctx, saga.ID, "timeout"); err != nil {
			slog.ErrorContext(ctx, "saga: sweep compensation failed", "saga_id", saga.ID, "error", err)
		}
	}
}
