package saga_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/posul/github-notifier/internal/subscription/domain"
	"github.com/posul/github-notifier/internal/subscription/saga"
)

const testBaseURL = "https://example.com"

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

// Create is not part of the sagaStore port used by the orchestrator (the saga
// row is inserted atomically alongside the subscription by the use case), but
// tests use it to seed a pending saga before exercising transitions.
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
	subs      map[string]*domain.Subscription
	deleted   []string
	deleteErr error
	getErr    error
}

func newFakeSubStore() *fakeSubStore {
	return &fakeSubStore{subs: make(map[string]*domain.Subscription)}
}

func (f *fakeSubStore) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func (f *fakeSubStore) GetByID(_ context.Context, id string) (*domain.Subscription, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	sub, ok := f.subs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *sub
	return &cp, nil
}

type fakeRetrier struct {
	calls []struct {
		sagaID, to, repo, confirmURL string
	}
	err error
}

func (f *fakeRetrier) Retry(_ context.Context, sagaID, to, repo, confirmURL string) error {
	f.calls = append(f.calls, struct {
		sagaID, to, repo, confirmURL string
	}{sagaID, to, repo, confirmURL})
	return f.err
}

func newSubscription() *domain.Subscription {
	return &domain.Subscription{
		ID:           "sub-1",
		Email:        "user@example.com",
		Repo:         "golang/go",
		ConfirmToken: "tok-confirm",
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

// newOrchestrator wraps saga.New with fake defaults so tests do not have to
// mention the retrier/baseURL wiring unless they exercise it.
func newOrchestrator(pub *fakePublisher, sagas *fakeSagaStore, subs *fakeSubStore, retrier syncRetrierIface) *saga.Orchestrator {
	return saga.New(pub, sagas, subs, retrier, testBaseURL)
}

// syncRetrierIface mirrors the unexported syncRetrier interface so both
// fakeRetrier and racingRetrier can be passed to newOrchestrator.
type syncRetrierIface interface {
	Retry(ctx context.Context, sagaID, to, repo, confirmURL string) error
}

func TestPublish_HappyPath(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})

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
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})

	sub := newSubscription()
	sagaID := seedPendingSaga(t, sagas, sub)

	if err := o.Publish(context.Background(), sagaID, sub, "url"); err == nil {
		t.Fatal("expected error from Publish when broker publish fails")
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompensated {
		t.Errorf("expected compensated after inline HandleFailed, got %s", got.State)
	}
	if len(subs.deleted) != 1 || subs.deleted[0] != sub.ID {
		t.Errorf("expected subscription %q deleted inline, got %v", sub.ID, subs.deleted)
	}
}

func TestHandleSent_MarksCompleted(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})
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
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})
	sagaID := seedPendingSaga(t, sagas, newSubscription())
	_ = o.HandleSent(context.Background(), sagaID)

	if err := o.HandleSent(context.Background(), sagaID); err != nil {
		t.Errorf("duplicate HandleSent should be no-op, got %v", err)
	}
}

func TestHandleFailed_CompensatesAndDeletesSubscription(t *testing.T) {
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})
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
	subs := newFakeSubStore()
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})
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
	subs := newFakeSubStore()
	subs.deleteErr = errors.New("db blip")
	o := newOrchestrator(pub, sagas, subs, &fakeRetrier{})
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
	o := newOrchestrator(&fakePublisher{}, newFakeSagaStore(), newFakeSubStore(), &fakeRetrier{})
	if err := o.HandleFailed(context.Background(), "ghost-id", "x"); err != nil {
		t.Errorf("HandleFailed for unknown saga should be no-op, got %v", err)
	}
}

// setupForRetry primes the fakes with a pending saga + its subscription,
// mimicking the state the TimeoutSweeper sees when it picks up a stuck saga.
func setupForRetry(t *testing.T, retrier syncRetrierIface) (*saga.Orchestrator, *fakeSagaStore, *fakeSubStore, string) {
	t.Helper()
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	sub := newSubscription()
	subs.subs[sub.ID] = sub
	o := newOrchestrator(pub, sagas, subs, retrier)
	sagaID := seedPendingSaga(t, sagas, sub)
	return o, sagas, subs, sagaID
}

func TestAttemptSyncRetry_HappyPath_MarksCompleted(t *testing.T) {
	retrier := &fakeRetrier{}
	o, sagas, subs, sagaID := setupForRetry(t, retrier)

	if err := o.AttemptSyncRetry(context.Background(), sagaID); err != nil {
		t.Fatalf("AttemptSyncRetry: %v", err)
	}

	if len(retrier.calls) != 1 {
		t.Fatalf("expected 1 retry call, got %d", len(retrier.calls))
	}
	c := retrier.calls[0]
	wantURL := testBaseURL + "/api/confirm/tok-confirm"
	if c.sagaID != sagaID || c.to != "user@example.com" || c.repo != "golang/go" || c.confirmURL != wantURL {
		t.Errorf("retry call mismatch: %+v (want url %q)", c, wantURL)
	}

	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompleted {
		t.Errorf("expected completed, got %s", got.State)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("AttemptSyncRetry must not delete subscription on success, got %v", subs.deleted)
	}
}

func TestAttemptSyncRetry_RetrierError_PropagatesAndKeepsPending(t *testing.T) {
	retrier := &fakeRetrier{err: errors.New("unavailable")}
	o, sagas, subs, sagaID := setupForRetry(t, retrier)

	err := o.AttemptSyncRetry(context.Background(), sagaID)
	if err == nil {
		t.Fatal("expected error to propagate")
	}

	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStatePending {
		t.Errorf("expected saga to stay pending on retry error, got %s", got.State)
	}
	if len(subs.deleted) != 0 {
		t.Errorf("AttemptSyncRetry must not delete subscription, got %v", subs.deleted)
	}
}

func TestAttemptSyncRetry_AlreadyCompleted_NoRetry(t *testing.T) {
	retrier := &fakeRetrier{}
	o, sagas, _, sagaID := setupForRetry(t, retrier)
	// Async reply already won the race.
	if err := o.HandleSent(context.Background(), sagaID); err != nil {
		t.Fatalf("HandleSent: %v", err)
	}

	if err := o.AttemptSyncRetry(context.Background(), sagaID); err != nil {
		t.Errorf("expected no error when saga not pending, got %v", err)
	}
	if len(retrier.calls) != 0 {
		t.Errorf("expected retrier NOT to be called when saga not pending, got %d calls", len(retrier.calls))
	}
	got, _ := sagas.Get(context.Background(), sagaID)
	if got.State != domain.SagaStateCompleted {
		t.Errorf("saga state should remain completed, got %s", got.State)
	}
}

func TestAttemptSyncRetry_UnknownSaga_ReturnsError(t *testing.T) {
	o := newOrchestrator(&fakePublisher{}, newFakeSagaStore(), newFakeSubStore(), &fakeRetrier{})

	err := o.AttemptSyncRetry(context.Background(), "ghost-id")
	if err == nil {
		t.Fatal("expected error for unknown saga")
	}
}

func TestAttemptSyncRetry_SubscriptionNotFound_ReturnsError(t *testing.T) {
	retrier := &fakeRetrier{}
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	// Subscription deliberately missing — simulates a corrupted FK scenario.
	o := newOrchestrator(pub, sagas, subs, retrier)
	sagaID := seedPendingSaga(t, sagas, newSubscription())

	if err := o.AttemptSyncRetry(context.Background(), sagaID); err == nil {
		t.Fatal("expected error when subscription is missing")
	}
	if len(retrier.calls) != 0 {
		t.Error("retrier must not be called when subscription lookup fails")
	}
}

func TestAttemptSyncRetry_RacedByAsyncReply_NoErrorOnAlreadyTerminal(t *testing.T) {
	// Simulates: sweeper reads pending; calls gRPC; between the RPC return and
	// MarkCompleted, an async reply lands and completes the saga. The
	// orchestrator's MarkCompleted returns ErrNotFound, which must be treated
	// as a benign no-op.
	pub := &fakePublisher{}
	sagas := newFakeSagaStore()
	subs := newFakeSubStore()
	sub := newSubscription()
	subs.subs[sub.ID] = sub
	sagaID := seedPendingSaga(t, sagas, sub)

	racingRetrier := &racingRetrier{store: sagas, sagaID: sagaID}
	o := newOrchestrator(pub, sagas, subs, racingRetrier)

	if err := o.AttemptSyncRetry(context.Background(), sagaID); err != nil {
		t.Errorf("expected benign no-op on race with async reply, got %v", err)
	}
}

type racingRetrier struct {
	store  *fakeSagaStore
	sagaID string
}

func (r *racingRetrier) Retry(_ context.Context, _, _, _, _ string) error {
	// Mid-RPC: simulate the async reply arriving and completing the saga.
	_ = r.store.MarkCompleted(context.Background(), r.sagaID)
	return nil
}
