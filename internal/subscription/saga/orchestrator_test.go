package saga_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/posul/github-notifier/internal/subscription/domain"
	"github.com/posul/github-notifier/internal/subscription/saga"
)

type fakePublisher struct {
	calls []struct {
		sagaID, to, repo, confirmURL string
	}
	err error
}

func (f *fakePublisher) SendConfirmationCommand(_ context.Context, sagaID, to, repo, confirmURL string) error {
	f.calls = append(f.calls, struct {
		sagaID, to, repo, confirmURL string
	}{sagaID, to, repo, confirmURL})
	return f.err
}

type fakeSagaStore struct {
	sagas     map[string]*domain.Saga
	createErr error
}

func newFakeSagaStore() *fakeSagaStore {
	return &fakeSagaStore{sagas: make(map[string]*domain.Saga)}
}

func (f *fakeSagaStore) Create(_ context.Context, s *domain.Saga) error {
	if f.createErr != nil {
		return f.createErr
	}
	cp := *s
	f.sagas[s.ID] = &cp
	return nil
}

func (f *fakeSagaStore) Get(_ context.Context, id string) (*domain.Saga, error) {
	s, ok := f.sagas[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSagaStore) markTerminal(id string, state domain.SagaState, reason *string) error {
	s, ok := f.sagas[id]
	if !ok {
		return domain.ErrNotFound
	}
	if s.State != domain.SagaStatePending {
		// Mirrors the SQL guard: only pending sagas transition.
		return domain.ErrNotFound
	}
	s.State = state
	s.LastError = reason
	now := time.Now().UTC()
	s.CompletedAt = &now
	return nil
}

func (f *fakeSagaStore) MarkCompleted(_ context.Context, id string) error {
	return f.markTerminal(id, domain.SagaStateCompleted, nil)
}

func (f *fakeSagaStore) MarkCompensated(_ context.Context, id string, reason string) error {
	r := reason
	return f.markTerminal(id, domain.SagaStateCompensated, &r)
}

func (f *fakeSagaStore) MarkTimedOut(_ context.Context, id string) error {
	r := "timeout"
	return f.markTerminal(id, domain.SagaStateTimedOut, &r)
}

func (f *fakeSagaStore) GetPendingOlderThan(_ context.Context, threshold time.Time) ([]*domain.Saga, error) {
	var out []*domain.Saga
	for _, s := range f.sagas {
		if s.State == domain.SagaStatePending && s.StartedAt.Before(threshold) {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

type fakeSubStore struct {
	deleted   []string
	deleteErr error
}

func (f *fakeSubStore) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func newSubscription() *domain.Subscription {
	return &domain.Subscription{
		ID:    "sub-1",
		Email: "user@example.com",
		Repo:  "golang/go",
	}
}

// seedPendingSaga puts a pending saga row into the fake store, mimicking what
// SubscriptionRepository.CreateWithSaga does atomically alongside the
// subscription insert in production.
func seedPendingSaga(t *testing.T, sagas *fakeSagaStore, sub *domain.Subscription) string {
	t.Helper()
	s := domain.NewSaga(sub.ID)
	if err := sagas.Create(context.Background(), s); err != nil {
		t.Fatalf("seed pending saga: %v", err)
	}
	return s.ID
}

func TestPublish_HappyPath(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)

	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	if err := o.Publish(context.Background(), sagaID, sub, "https://example/confirm/x"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sagaID != sagaID || call.to != sub.Email || call.repo != sub.Repo || call.confirmURL != "https://example/confirm/x" {
		t.Errorf("publish call mismatch: %+v", call)
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStatePending {
		t.Errorf("expected saga pending after publish, got %s", got.State)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("Publish must not touch subscriptions on success, got deletes: %v", subs.deleted)
	}
}

func TestPublish_Fails_CompensatesInline(t *testing.T) {
	pub := &fakePublisher{err: errors.New("broker down")}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)

	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	if err := o.Publish(context.Background(), sagaID, sub, "url"); err == nil {
		t.Fatal("expected error from Publish when broker publish fails")
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompensated {
		t.Errorf("expected compensated after inline HandleFailed, got %s", got.State)
	}
	// Inline compensation must delete the orphaned subscription so a
	// user retry immediately succeeds.
	if len(subs.deleted) != 1 || subs.deleted[0] != sub.ID {
		t.Errorf("expected subscription %q deleted inline, got %v", sub.ID, subs.deleted)
	}
}

func TestHandleSent_MarksCompleted(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)
	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	if err := o.HandleSent(context.Background(), sagaID); err != nil {
		t.Fatalf("HandleSent: %v", err)
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompleted {
		t.Errorf("expected completed, got %s", got.State)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("HandleSent must not delete subscription, got: %v", subs.deleted)
	}
}

func TestHandleSent_DuplicateEvent_NoError(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)
	sagaID := seedPendingSaga(t, sagas, newSubscription())
	_ = o.HandleSent(context.Background(), sagaID)

	if err := o.HandleSent(context.Background(), sagaID); err != nil {
		t.Errorf("duplicate HandleSent should be no-op, got %v", err)
	}
}

func TestHandleFailed_CompensatesAndDeletesSubscription(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)
	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	if err := o.HandleFailed(context.Background(), sagaID, "resend 500"); err != nil {
		t.Fatalf("HandleFailed: %v", err)
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompensated {
		t.Errorf("expected compensated, got %s", got.State)
	}
	if got.LastError == nil || *got.LastError != "resend 500" {
		t.Errorf("expected last_error=\"resend 500\", got %v", got.LastError)
	}
	if len(subs.deleted) != 1 || subs.deleted[0] != sub.ID {
		t.Errorf("expected subscription %q deleted, got %v", sub.ID, subs.deleted)
	}
}

func TestHandleFailed_AlreadyCompleted_NoOp(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{}
	o := saga.New(pub, sagas, subs)
	sagaID := seedPendingSaga(t, sagas, newSubscription())
	_ = o.HandleSent(context.Background(), sagaID)

	if err := o.HandleFailed(context.Background(), sagaID, "late timeout"); err != nil {
		t.Errorf("late HandleFailed should be no-op, got %v", err)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("must not delete subscription after success, got: %v", subs.deleted)
	}
}

func TestHandleFailed_DeleteFails_RetrySucceeds(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := &fakeSubStore{deleteErr: errors.New("db blip")}
	o := saga.New(pub, sagas, subs)
	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	// First delivery: Delete blows up. The orchestrator must NOT flip the
	// saga to compensated — otherwise the redelivered event would short-circuit
	// on the state guard and the subscription would stay orphaned.
	if err := o.HandleFailed(context.Background(), sagaID, "resend 500"); err == nil {
		t.Fatal("expected error when subscription delete fails")
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStatePending {
		t.Fatalf("saga must stay pending when delete fails, got %s", got.State)
	}

	// Broker redelivers; the DB has recovered.
	subs.deleteErr = nil
	if err := o.HandleFailed(context.Background(), sagaID, "resend 500"); err != nil {
		t.Fatalf("retry HandleFailed: %v", err)
	}
	got, _ = sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompensated {
		t.Errorf("expected compensated after retry, got %s", got.State)
	}
	if len(subs.deleted) != 2 || subs.deleted[1] != sub.ID {
		t.Errorf("expected retried delete of %q, got %v", sub.ID, subs.deleted)
	}
}

func TestHandleFailed_UnknownSaga_NoError(t *testing.T) {
	o := saga.New(&fakePublisher{}, newFakeSagaStore(), &fakeSubStore{})
	if err := o.HandleFailed(context.Background(), "ghost-id", "x"); err != nil {
		t.Errorf("HandleFailed for unknown saga should be no-op, got %v", err)
	}
}
