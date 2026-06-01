package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/repository"
	"github.com/posul/github-notifier/internal/service"
)

type mockRepoRepo struct {
	repos        map[string]*model.Repository // fullName -> repo (fullName used as ID in tests)
	lastSeenTags map[string]string            // repo ID -> tag
}

func newMockRepoRepo() *mockRepoRepo {
	return &mockRepoRepo{
		repos:        make(map[string]*model.Repository),
		lastSeenTags: make(map[string]string),
	}
}

func (m *mockRepoRepo) GetOrCreate(_ context.Context, fullName string) (*model.Repository, error) {
	if r, ok := m.repos[fullName]; ok {
		return r, nil
	}
	r := &model.Repository{ID: fullName, FullName: fullName}
	m.repos[fullName] = r
	return r, nil
}

func (m *mockRepoRepo) GetAllWithConfirmedSubscriptions(_ context.Context) ([]*model.Repository, error) {
	var result []*model.Repository
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
	subs           map[string]*model.Subscription
	byConfirmToken map[string]*model.Subscription
	byUnsubToken   map[string]*model.Subscription
	confirmedIDs   map[string]bool
	createErr      error
}

func newMockSubRepo() *mockSubRepo {
	return &mockSubRepo{
		subs:           make(map[string]*model.Subscription),
		byConfirmToken: make(map[string]*model.Subscription),
		byUnsubToken:   make(map[string]*model.Subscription),
		confirmedIDs:   make(map[string]bool),
	}
}

func (m *mockSubRepo) Create(_ context.Context, sub *model.Subscription) error {
	if m.createErr != nil {
		return m.createErr
	}
	for _, s := range m.subs {
		if s.Email == sub.Email && s.RepoID == sub.RepoID {
			return repository.ErrAlreadyExists
		}
	}
	m.subs[sub.ID] = sub
	m.byConfirmToken[sub.ConfirmToken] = sub
	m.byUnsubToken[sub.UnsubscribeToken] = sub
	return nil
}

func (m *mockSubRepo) GetByConfirmToken(_ context.Context, token string) (*model.Subscription, error) {
	if sub, ok := m.byConfirmToken[token]; ok {
		return sub, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockSubRepo) GetByUnsubscribeToken(_ context.Context, token string) (*model.Subscription, error) {
	if sub, ok := m.byUnsubToken[token]; ok {
		return sub, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockSubRepo) GetByEmail(_ context.Context, emailAddr string) ([]*model.Subscription, error) {
	var result []*model.Subscription
	for _, s := range m.subs {
		if s.Email == emailAddr && m.confirmedIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSubRepo) GetConfirmedByRepoID(_ context.Context, repoID string) ([]*model.Subscription, error) {
	var result []*model.Subscription
	for _, s := range m.subs {
		if s.RepoID == repoID && m.confirmedIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSubRepo) Confirm(_ context.Context, id string) error {
	if _, ok := m.subs[id]; !ok {
		return repository.ErrNotFound
	}
	m.confirmedIDs[id] = true
	return nil
}

func (m *mockSubRepo) Delete(_ context.Context, id string) error {
	sub, ok := m.subs[id]
	if !ok {
		return repository.ErrNotFound
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

type mockEmail struct {
	confirmCalled bool
	confirmErr    error
}

func (m *mockEmail) SendConfirmation(_ context.Context, _ string, _ email.ConfirmData) error {
	m.confirmCalled = true
	return m.confirmErr
}

func (m *mockEmail) SendReleaseNotification(_ context.Context, _ string, _ email.ReleaseData) error {
	return nil
}

type testSvc struct {
	*service.SubscribeUseCase
	*service.ConfirmUseCase
	*service.UnsubscribeUseCase
	*service.GetSubscriptionsUseCase
}

func newSvc(repoRepo *mockRepoRepo, subRepo *mockSubRepo, gh *mockGitHub, em *mockEmail) *testSvc {
	return &testSvc{
		SubscribeUseCase:        service.NewSubscribeUseCase(repoRepo, subRepo, gh, em, "http://localhost:8080"),
		ConfirmUseCase:          service.NewConfirmUseCase(subRepo),
		UnsubscribeUseCase:      service.NewUnsubscribeUseCase(subRepo),
		GetSubscriptionsUseCase: service.NewGetSubscriptionsUseCase(subRepo),
	}
}

func TestSubscribe_Success(t *testing.T) {
	repoRepo := newMockRepoRepo()
	subRepo := newMockSubRepo()
	em := &mockEmail{}
	svc := newSvc(repoRepo, subRepo, &mockGitHub{}, em)

	if err := svc.Subscribe(context.Background(), "user@example.com", "golang/go"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(subRepo.subs) != 1 {
		t.Fatalf("expected 1 subscription in repo, got %d", len(subRepo.subs))
	}
	if !em.confirmCalled {
		t.Error("expected confirmation email to be sent")
	}
}

func TestSubscribe_InvalidEmail(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockEmail{})
	err := svc.Subscribe(context.Background(), "not-an-email", "golang/go")
	if !errors.Is(err, service.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	cases := []string{"justarepo", "", "too/many/slashes", "/noleft", "noright/"}
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockEmail{})
	for _, tc := range cases {
		err := svc.Subscribe(context.Background(), "user@example.com", tc)
		if !errors.Is(err, service.ErrInvalidRepo) {
			t.Errorf("repo=%q: expected ErrInvalidRepo, got %v", tc, err)
		}
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{err: githubclient.ErrNotFound}, &mockEmail{})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, service.ErrRepoNotFound) {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestSubscribe_RateLimit(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{err: githubclient.ErrRateLimit}, &mockEmail{})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, service.ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
}

func TestSubscribe_Duplicate(t *testing.T) {
	repoRepo := newMockRepoRepo()
	subRepo := newMockSubRepo()
	svc := newSvc(repoRepo, subRepo, &mockGitHub{}, &mockEmail{})

	_ = svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if !errors.Is(err, service.ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestConfirm_Success(t *testing.T) {
	repoRepo := newMockRepoRepo()
	subRepo := newMockSubRepo()
	svc := newSvc(repoRepo, subRepo, &mockGitHub{}, &mockEmail{})
	_ = svc.Subscribe(context.Background(), "user@example.com", "golang/go")

	var confirmToken string
	for _, s := range subRepo.subs {
		confirmToken = s.ConfirmToken
	}

	if err := svc.Confirm(context.Background(), confirmToken); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	var id string
	for _, s := range subRepo.subs {
		id = s.ID
	}
	if !subRepo.confirmedIDs[id] {
		t.Error("expected subscription to be confirmed")
	}
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockEmail{})
	err := svc.Confirm(context.Background(), "nonexistent-token")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUnsubscribe_Success(t *testing.T) {
	repoRepo := newMockRepoRepo()
	subRepo := newMockSubRepo()
	svc := newSvc(repoRepo, subRepo, &mockGitHub{}, &mockEmail{})
	_ = svc.Subscribe(context.Background(), "user@example.com", "golang/go")

	var unsubToken string
	for _, s := range subRepo.subs {
		unsubToken = s.UnsubscribeToken
	}

	if err := svc.Unsubscribe(context.Background(), unsubToken); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(subRepo.subs) != 0 {
		t.Error("expected subscription to be deleted")
	}
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockEmail{})
	err := svc.Unsubscribe(context.Background(), "bad-token")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetSubscriptions_InvalidEmail(t *testing.T) {
	svc := newSvc(newMockRepoRepo(), newMockSubRepo(), &mockGitHub{}, &mockEmail{})
	_, err := svc.GetSubscriptions(context.Background(), "notanemail")
	if !errors.Is(err, service.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
}

func TestGetSubscriptions_ReturnsOnlyConfirmed(t *testing.T) {
	repoRepo := newMockRepoRepo()
	subRepo := newMockSubRepo()
	svc := newSvc(repoRepo, subRepo, &mockGitHub{}, &mockEmail{})

	_ = svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	_ = svc.Subscribe(context.Background(), "user@example.com", "gin-gonic/gin")

	var firstConfirmToken string
	for _, s := range subRepo.subs {
		if s.Repo == "golang/go" {
			firstConfirmToken = s.ConfirmToken
		}
	}
	_ = svc.Confirm(context.Background(), firstConfirmToken)

	subs, err := svc.GetSubscriptions(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 confirmed subscription, got %d", len(subs))
	}
	if subs[0].Repo != "golang/go" {
		t.Errorf("expected golang/go, got %s", subs[0].Repo)
	}
}
