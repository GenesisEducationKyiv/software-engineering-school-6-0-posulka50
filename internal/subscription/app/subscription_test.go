package app_test

import (
	"context"
	"errors"
	"testing"

	githubclient "github.com/posul/github-notifier/internal/release/adapter/github"
	releasedomain "github.com/posul/github-notifier/internal/release/domain"
	"github.com/posul/github-notifier/internal/subscription/app"
	"github.com/posul/github-notifier/internal/subscription/domain"
)

type mockRepoRepo struct {
	repos        map[string]*releasedomain.Repository // fullName -> repo (fullName used as ID in tests)
	lastSeenTags map[string]string                    // repo ID -> tag
}

func newMockRepoRepo() *mockRepoRepo {
	return &mockRepoRepo{
		repos:        make(map[string]*releasedomain.Repository),
		lastSeenTags: make(map[string]string),
	}
}

func (m *mockRepoRepo) GetOrCreate(_ context.Context, fullName string) (*releasedomain.Repository, error) {
	if r, ok := m.repos[fullName]; ok {
		return r, nil
	}
	r := &releasedomain.Repository{ID: fullName, FullName: fullName}
	m.repos[fullName] = r
	return r, nil
}

func (m *mockRepoRepo) GetAllWithConfirmedSubscriptions(_ context.Context) ([]*releasedomain.Repository, error) {
	var result []*releasedomain.Repository
	for _, r := range m.repos {
		result = append(result, r)
	}
	return result, nil
}

func (m *mockRepoRepo) UpdateLastSeenTag(_ context.Context, id, tag string) error {
	m.lastSeenTags[id] = tag
	for _, r := range m.repos {
		if r.ID == id {
			t := tag
			r.LastSeenTag = &t
		}
	}
	return nil
}

type mockSubRepo struct {
	subs           map[string]*domain.Subscription
	byConfirmToken map[string]*domain.Subscription
	byUnsubToken   map[string]*domain.Subscription
	confirmedIDs   map[string]bool
	createErr      error
}

func newMockSubRepo() *mockSubRepo {
	return &mockSubRepo{
		subs:           make(map[string]*domain.Subscription),
		byConfirmToken: make(map[string]*domain.Subscription),
		byUnsubToken:   make(map[string]*domain.Subscription),
		confirmedIDs:   make(map[string]bool),
	}
}

func (m *mockSubRepo) Create(_ context.Context, sub *domain.Subscription) error {
	if m.createErr != nil {
		return m.createErr
	}
	for _, s := range m.subs {
		if s.Email == sub.Email && s.RepoID == sub.RepoID {
			return domain.ErrAlreadyExists
		}
	}
	m.subs[sub.ID] = sub
	m.byConfirmToken[sub.ConfirmToken] = sub
	m.byUnsubToken[sub.UnsubscribeToken] = sub
	return nil
}

func (m *mockSubRepo) GetByConfirmToken(_ context.Context, token string) (*domain.Subscription, error) {
	if sub, ok := m.byConfirmToken[token]; ok {
		return sub, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockSubRepo) GetByUnsubscribeToken(_ context.Context, token string) (*domain.Subscription, error) {
	if sub, ok := m.byUnsubToken[token]; ok {
		return sub, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockSubRepo) GetByEmail(_ context.Context, emailAddr string) ([]*domain.Subscription, error) {
	var result []*domain.Subscription
	for _, s := range m.subs {
		if s.Email == emailAddr && m.confirmedIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSubRepo) GetConfirmedByRepoID(_ context.Context, repoID string) ([]*domain.Subscription, error) {
	var result []*domain.Subscription
	for _, s := range m.subs {
		if s.RepoID == repoID && m.confirmedIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSubRepo) Confirm(_ context.Context, id string) error {
	if _, ok := m.subs[id]; !ok {
		return domain.ErrNotFound
	}
	m.confirmedIDs[id] = true
	return nil
}

func (m *mockSubRepo) Delete(_ context.Context, id string) error {
	sub, ok := m.subs[id]
	if !ok {
		return domain.ErrNotFound
	}
	delete(m.byConfirmToken, sub.ConfirmToken)
	delete(m.byUnsubToken, sub.UnsubscribeToken)
	delete(m.subs, id)
	return nil
}

func (m *mockSubRepo) ExistsByEmailAndRepoID(_ context.Context, emailAddr, repoID string) (bool, error) {
	for _, s := range m.subs {
		if s.Email == emailAddr && s.RepoID == repoID {
			return true, nil
		}
	}
	return false, nil
}

type mockGitHub struct {
	err error
}

func (m *mockGitHub) CheckRepo(_ context.Context, _, _ string) error {
	return m.err
}

type mockSaga struct {
	startCalled bool
	startErr    error
}

func (m *mockSaga) Start(_ context.Context, _ *domain.Subscription, _ string) (string, error) {
	m.startCalled = true
	if m.startErr != nil {
		return "", m.startErr
	}
	return "saga-id", nil
}

func newSubscribeUC(repos *mockRepoRepo, subs *mockSubRepo, gh *mockGitHub, sg *mockSaga) *app.SubscribeUseCase {
	return app.NewSubscribeUseCase(repos, subs, gh, sg, "http://localhost:8080")
}

func TestSubscribe_Success(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	sg := &mockSaga{}
	uc := newSubscribeUC(repos, subs, &mockGitHub{}, sg)

	if err := uc.Subscribe(context.Background(), "user@example.com", "golang/go"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(subs.subs) != 1 {
		t.Fatalf("expected 1 subscription in repo, got %d", len(subs.subs))
	}
	if !sg.startCalled {
		t.Error("expected saga to be started")
	}
}

// TestSubscribe_SagaStartFailureDeletesSubscription verifies the local
// rollback path: when the orchestrator cannot publish the saga command, the
// just-created subscription is removed so a retry will not return 409.
func TestSubscribe_SagaStartFailureDeletesSubscription(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	sg := &mockSaga{startErr: errors.New("publish failed")}
	uc := newSubscribeUC(repos, subs, &mockGitHub{}, sg)

	err := uc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if len(subs.subs) != 0 {
		t.Errorf("expected subscription to be deleted on saga start failure, got %d", len(subs.subs))
	}
}

func TestSubscribe_InvalidEmail(t *testing.T) {
	uc := newSubscribeUC(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockSaga{})
	err := uc.Subscribe(context.Background(), "not-an-email", "golang/go")
	if !errors.Is(err, app.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	cases := []string{"justarepo", "", "too/many/slashes", "/noleft", "noright/"}
	uc := newSubscribeUC(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockSaga{})
	for _, tc := range cases {
		err := uc.Subscribe(context.Background(), "user@example.com", tc)
		if !errors.Is(err, app.ErrInvalidRepo) {
			t.Errorf("repo=%q: expected ErrInvalidRepo, got %v", tc, err)
		}
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	uc := newSubscribeUC(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{err: githubclient.ErrNotFound}, &mockSaga{})
	err := uc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, app.ErrRepoNotFound) {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestSubscribe_RateLimit(t *testing.T) {
	uc := newSubscribeUC(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{err: githubclient.ErrRateLimit}, &mockSaga{})
	err := uc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, app.ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
}

func TestSubscribe_Duplicate(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	uc := newSubscribeUC(repos, subs, &mockGitHub{}, &mockSaga{})

	_ = uc.Subscribe(context.Background(), "user@example.com", "golang/go")
	err := uc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, app.ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestConfirm_Success(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	_ = newSubscribeUC(repos, subs, &mockGitHub{}, &mockSaga{}).Subscribe(context.Background(), "user@example.com", "golang/go")

	var confirmToken string
	for _, s := range subs.subs {
		confirmToken = s.ConfirmToken
	}

	if err := app.NewConfirmUseCase(subs).Confirm(context.Background(), confirmToken); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var id string
	for _, s := range subs.subs {
		id = s.ID
	}
	if !subs.confirmedIDs[id] {
		t.Error("expected subscription to be confirmed")
	}
}

func TestConfirm_TokenNotFound(t *testing.T) {
	err := app.NewConfirmUseCase(newMockSubRepo()).Confirm(context.Background(), "nonexistent-token")
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUnsubscribe_Success(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	_ = newSubscribeUC(repos, subs, &mockGitHub{}, &mockSaga{}).Subscribe(context.Background(), "user@example.com", "golang/go")

	var unsubToken string
	for _, s := range subs.subs {
		unsubToken = s.UnsubscribeToken
	}

	if err := app.NewUnsubscribeUseCase(subs).Unsubscribe(context.Background(), unsubToken); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(subs.subs) != 0 {
		t.Error("expected subscription to be deleted")
	}
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	err := app.NewUnsubscribeUseCase(newMockSubRepo()).Unsubscribe(context.Background(), "bad-token")
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetSubscriptions_InvalidEmail(t *testing.T) {
	_, err := app.NewGetSubscriptionsUseCase(newMockSubRepo()).GetSubscriptions(context.Background(), "notanemail")
	if !errors.Is(err, app.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestGetSubscriptions_ReturnsOnlyConfirmed(t *testing.T) {
	repos := newMockRepoRepo()
	subs := newMockSubRepo()
	subscribeUC := newSubscribeUC(repos, subs, &mockGitHub{}, &mockSaga{})

	_ = subscribeUC.Subscribe(context.Background(), "user@example.com", "golang/go")
	_ = subscribeUC.Subscribe(context.Background(), "user@example.com", "gin-gonic/gin")

	var firstConfirmToken string
	for _, s := range subs.subs {
		if s.Repo == "golang/go" {
			firstConfirmToken = s.ConfirmToken
		}
	}
	_ = app.NewConfirmUseCase(subs).Confirm(context.Background(), firstConfirmToken)

	result, err := app.NewGetSubscriptionsUseCase(subs).GetSubscriptions(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 confirmed subscription, got %d", len(result))
	}
	if result[0].Repo != "golang/go" {
		t.Errorf("expected golang/go, got %s", result[0].Repo)
	}
}
