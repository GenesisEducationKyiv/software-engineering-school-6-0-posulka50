package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/posul/github-notifier/internal/release/adapter/github"
	"github.com/posul/github-notifier/internal/release/app"
	"github.com/posul/github-notifier/internal/release/domain"
	subscriptiondomain "github.com/posul/github-notifier/internal/subscription/domain"
)

type mockRepoRepo struct {
	repos        map[string]*domain.Repository
	lastSeenTags map[string]string
}

func newMockRepoRepo() *mockRepoRepo {
	return &mockRepoRepo{
		repos:        make(map[string]*domain.Repository),
		lastSeenTags: make(map[string]string),
	}
}

func (m *mockRepoRepo) GetAllWithConfirmedSubscriptions(_ context.Context) ([]*domain.Repository, error) {
	var result []*domain.Repository
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
	subs         map[string]*subscriptiondomain.Subscription
	confirmedIDs map[string]bool
}

func newMockSubRepo() *mockSubRepo {
	return &mockSubRepo{
		subs:         make(map[string]*subscriptiondomain.Subscription),
		confirmedIDs: make(map[string]bool),
	}
}

func (m *mockSubRepo) GetConfirmedByRepoID(_ context.Context, repoID string) ([]*subscriptiondomain.Subscription, error) {
	var result []*subscriptiondomain.Subscription
	for _, s := range m.subs {
		if s.RepoID == repoID && m.confirmedIDs[s.ID] {
			result = append(result, s)
		}
	}
	return result, nil
}

type mockReleaseChecker struct {
	release *github.Release
	err     error
}

func (m *mockReleaseChecker) GetLatestRelease(_ context.Context, _, _ string) (*github.Release, error) {
	return m.release, m.err
}

type mockNotifier struct {
	releaseEmails []string
	releaseErr    error
}

func (m *mockNotifier) SendReleaseNotification(_ context.Context, to, _, _, _, _, _, _ string) error {
	if m.releaseErr != nil {
		return m.releaseErr
	}
	m.releaseEmails = append(m.releaseEmails, to)
	return nil
}

const initialTag = "v1.0.0"

func ptr(s string) *string { return &s }

func repoWithSubs(fullName string, lastTag *string, subEmail, subID, unsubToken string) (*mockRepoRepo, *mockSubRepo) {
	rr := newMockRepoRepo()
	sr := newMockSubRepo()

	repo := &domain.Repository{ID: fullName, FullName: fullName, LastSeenTag: lastTag}
	rr.repos[fullName] = repo

	sub := &subscriptiondomain.Subscription{
		ID:               subID,
		RepoID:           fullName,
		Repo:             fullName,
		Email:            subEmail,
		Confirmed:        true,
		UnsubscribeToken: unsubToken,
	}
	sr.subs[subID] = sub
	sr.confirmedIDs[subID] = true

	return rr, sr
}

func newScanner(rr *mockRepoRepo, sr *mockSubRepo, gh *mockReleaseChecker, em *mockNotifier) *app.Scanner {
	return app.NewScanner(rr, sr, gh, em, "http://localhost:8080", time.Hour)
}

func TestScanner_SendsNotificationOnNewRelease(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", ptr(initialTag), "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{release: &github.Release{TagName: "v1.1.0", Name: "Go 1.1"}}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 1 || em.releaseEmails[0] != "user@example.com" {
		t.Fatalf("expected notification to user@example.com, got %v", em.releaseEmails)
	}
	if rr.lastSeenTags["golang/go"] != "v1.1.0" {
		t.Errorf("expected last_seen_tag=v1.1.0, got %q", rr.lastSeenTags["golang/go"])
	}
}

func TestScanner_NoNotificationWhenTagUnchanged(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", ptr(initialTag), "user@example.com", "id2", "unsub2")
	gh := &mockReleaseChecker{release: &github.Release{TagName: initialTag}}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no notifications, got %v", em.releaseEmails)
	}
}

func TestScanner_SetsInitialTagWithoutNotifying(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", nil, "other@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{release: &github.Release{TagName: initialTag}}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no notifications on first scan, got %v", em.releaseEmails)
	}
	if rr.lastSeenTags["golang/go"] != initialTag {
		t.Errorf("expected initial last_seen_tag=v1.0.0, got %q", rr.lastSeenTags["golang/go"])
	}
}

func TestScanner_StopsOnRateLimit(t *testing.T) {
	rr := newMockRepoRepo()
	sr := newMockSubRepo()

	for _, full := range []string{"owner/repo1", "owner/repo2"} {
		rr.repos[full] = &domain.Repository{ID: full, FullName: full, LastSeenTag: ptr(initialTag)}
	}

	gh := &mockReleaseChecker{err: github.ErrRateLimit}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no emails on rate limit, got %v", em.releaseEmails)
	}
}

func TestScanner_SkipsOnNoRelease(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", ptr(initialTag), "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{err: github.ErrNotFound}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no emails when no release found, got %v", em.releaseEmails)
	}
}
