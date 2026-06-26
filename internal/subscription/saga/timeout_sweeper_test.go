package saga_test

import (
	"context"
	"errors"
	"strings"
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

// recordingAttempter answers AttemptSyncRetry from a per-saga map; missing
// entries return the default error so the sweeper falls through to compensate.
type recordingAttempter struct {
	results map[string]error
	calls   []string
}

func newRecordingAttempter() *recordingAttempter {
	return &recordingAttempter{results: make(map[string]error)}
}

func (a *recordingAttempter) AttemptSyncRetry(_ context.Context, sagaID string) error {
	a.calls = append(a.calls, sagaID)
	if err, ok := a.results[sagaID]; ok {
		return err
	}
	return errors.New("unavailable")
}

// sweepOnce calls Run with a context canceled after a short delay so the
// ticker never fires; the only invocation is the immediate sweep on entry.
func sweepOnce(t *testing.T, s *saga.TimeoutSweeper) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

func TestSweep_RetrySucceeds_NoCompensation(t *testing.T) {
	store := newFakeSagaStore()
	now := time.Now().UTC()
	store.sagas["old-1"] = &domain.Saga{ID: "old-1", SubscriptionID: "s1", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}

	attempter := newRecordingAttempter()
	attempter.results["old-1"] = nil // sync retry succeeds
	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, attempter, handler, 5*time.Minute, time.Hour)

	sweepOnce(t, sweeper)

	if len(attempter.calls) != 1 || attempter.calls[0] != "old-1" {
		t.Errorf("expected single AttemptSyncRetry on old-1, got %+v", attempter.calls)
	}
	if len(handler.calls) != 0 {
		t.Errorf("expected NO compensation when retry succeeds, got %+v", handler.calls)
	}
}

func TestSweep_RetryFails_FallsThroughToCompensation(t *testing.T) {
	store := newFakeSagaStore()
	now := time.Now().UTC()
	store.sagas["old-1"] = &domain.Saga{ID: "old-1", SubscriptionID: "s1", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}

	attempter := newRecordingAttempter()
	attempter.results["old-1"] = errors.New("notifier unavailable")
	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, attempter, handler, 5*time.Minute, time.Hour)

	sweepOnce(t, sweeper)

	if len(attempter.calls) != 1 {
		t.Fatalf("expected 1 retry attempt, got %d", len(attempter.calls))
	}
	if len(handler.calls) != 1 {
		t.Fatalf("expected 1 compensation call, got %d", len(handler.calls))
	}
	got := handler.calls[0]
	if got.sagaID != "old-1" {
		t.Errorf("compensated wrong saga: %q", got.sagaID)
	}
	if !strings.HasPrefix(got.reason, "timeout_after_grpc_retry_failed:") {
		t.Errorf("expected reason prefix \"timeout_after_grpc_retry_failed:\", got %q", got.reason)
	}
	if !strings.Contains(got.reason, "notifier unavailable") {
		t.Errorf("expected reason to embed retry error, got %q", got.reason)
	}
}

func TestSweep_MixedSagas_OnlyStuckAreOffered(t *testing.T) {
	store := newFakeSagaStore()
	now := time.Now().UTC()
	store.sagas["old-1"] = &domain.Saga{ID: "old-1", SubscriptionID: "s1", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}
	store.sagas["old-2"] = &domain.Saga{ID: "old-2", SubscriptionID: "s2", State: domain.SagaStatePending, StartedAt: now.Add(-10 * time.Minute)}
	store.sagas["fresh"] = &domain.Saga{ID: "fresh", SubscriptionID: "s3", State: domain.SagaStatePending, StartedAt: now.Add(-1 * time.Second)}
	store.sagas["done"] = &domain.Saga{ID: "done", SubscriptionID: "s4", State: domain.SagaStateCompleted, StartedAt: now.Add(-10 * time.Minute)}

	attempter := newRecordingAttempter()
	attempter.results["old-1"] = nil                      // rescued
	attempter.results["old-2"] = errors.New("still down") // compensated
	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, attempter, handler, 5*time.Minute, time.Hour)

	sweepOnce(t, sweeper)

	if len(attempter.calls) != 2 {
		t.Fatalf("expected 2 retry attempts, got %d (%+v)", len(attempter.calls), attempter.calls)
	}
	for _, id := range attempter.calls {
		if id == "fresh" || id == "done" {
			t.Errorf("retry attempt on saga that should be skipped: %q", id)
		}
	}
	if len(handler.calls) != 1 || handler.calls[0].sagaID != "old-2" {
		t.Errorf("expected only old-2 to be compensated, got %+v", handler.calls)
	}
}

func TestSweep_NoStuckSagas_NoCalls(t *testing.T) {
	store := newFakeSagaStore()
	store.sagas["fresh"] = &domain.Saga{ID: "fresh", State: domain.SagaStatePending, StartedAt: time.Now().UTC()}

	attempter := newRecordingAttempter()
	handler := &recordingHandler{}
	sweeper := saga.NewTimeoutSweeper(store, attempter, handler, 5*time.Minute, time.Hour)
	sweepOnce(t, sweeper)

	if len(attempter.calls) != 0 {
		t.Errorf("expected no retry attempts, got %+v", attempter.calls)
	}
	if len(handler.calls) != 0 {
		t.Errorf("expected no compensation, got %+v", handler.calls)
	}
}
