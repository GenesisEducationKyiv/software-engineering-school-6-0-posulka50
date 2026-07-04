package saga_test

import (
	"context"
	"testing"
	"time"

	"github.com/posul/github-notifier/internal/subscription/domain"
	"github.com/posul/github-notifier/internal/subscription/saga"
)

type recordingHandler struct {
	calls []struct {
		sagaID, reason string
	}
}

func (r *recordingHandler) HandleFailed(_ context.Context, sagaID, reason string) error {
	r.calls = append(r.calls, struct {
		sagaID, reason string
	}{sagaID, reason})
	return nil
}

// sweepOnce calls Run with a context canceled after a short delay so the
// ticker never fires; the only invocation is the immediate sweep on entry.
func sweepOnce(t *testing.T, s *saga.TimeoutSweeper) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

func TestSweep_StuckSagas_AreCompensated(t *testing.T) {
	store := newFakeSagaStore()
	// Two stale (older than threshold), one fresh, one already completed.
	now := time.Now().UTC()
	store.sagas["old-1"] = &domain.Saga{ID: "old-1", SubscriptionID: "s1", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}
	store.sagas["old-2"] = &domain.Saga{ID: "old-2", SubscriptionID: "s2", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}
	store.sagas["fresh"] = &domain.Saga{ID: "fresh", SubscriptionID: "s3", State: domain.SagaStatePending, StartedAt: now.Add(-1 * time.Second)}
	store.sagas["done"] = &domain.Saga{ID: "done", SubscriptionID: "s4", State: domain.SagaStateCompleted, StartedAt: now.Add(-10 * time.Minute)}

	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, handler, 5*time.Minute, time.Hour)
	sweepOnce(t, sweeper)

	if len(handler.calls) != 2 {
		t.Fatalf("expected 2 stuck sagas swept, got %d (%+v)", len(handler.calls), handler.calls)
	}
	seen := map[string]string{}
	for _, c := range handler.calls {
		seen[c.sagaID] = c.reason
	}
	for _, id := range []string{"old-1", "old-2"} {
		if seen[id] != "timeout" {
			t.Errorf("expected saga %q swept with reason \"timeout\", got %q", id, seen[id])
		}
	}
	if _, ok := seen["fresh"]; ok {
		t.Error("fresh saga must not be swept")
	}
	if _, ok := seen["done"]; ok {
		t.Error("completed saga must not be swept")
	}
}

func TestSweep_NoStuckSagas_NoCalls(t *testing.T) {
	store := newFakeSagaStore()
	store.sagas["fresh"] = &domain.Saga{ID: "fresh", State: domain.SagaStatePending, StartedAt: time.Now().UTC()}

	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, handler, 5*time.Minute, time.Hour)
	sweepOnce(t, sweeper)

	if len(handler.calls) != 0 {
		t.Errorf("expected no calls, got %+v", handler.calls)
	}
}
