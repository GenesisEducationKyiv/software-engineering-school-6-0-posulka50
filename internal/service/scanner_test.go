package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/service"
)

type mockReleaseChecker struct {
	release *githubclient.Release
	err     error
}

func (m *mockReleaseChecker) GetLatestRelease(_ context.Context, _, _ string) (*githubclient.Release, error) {
	return m.release, m.err
}

type mockNotifier struct {
	releaseEmails []string
	releaseErr    error
}

func (m *mockNotifier) SendConfirmation(_ string, _ email.ConfirmData) error { return nil }

func (m *mockNotifier) SendReleaseNotification(to string, _ email.ReleaseData) error {
	if m.releaseErr != nil {
		return m.releaseErr
	}
	m.releaseEmails = append(m.releaseEmails, to)
	return nil
}

func ptr(s string) *string { return &s }

func repoWithSubs(fullName string, lastTag *string, subEmail, subID, unsubToken string) (*mockRepoRepo, *mockSubRepo) {
	rr := newMockRepoRepo()
	sr := newMockSubRepo()

	repo := &model.Repository{ID: fullName, FullName: fullName, LastSeenTag: lastTag}
	rr.repos[fullName] = repo

	sub := &model.Subscription{
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

func newScanner(rr *mockRepoRepo, sr *mockSubRepo, gh *mockReleaseChecker, em *mockNotifier) *service.Scanner {
	return service.NewScanner(rr, sr, gh, em, "http://localhost:8080", time.Hour)
}

func TestScanner_SendsNotificationOnNewRelease(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", ptr("v1.0.0"), "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{release: &githubclient.Release{TagName: "v1.1.0", Name: "Go 1.1"}}
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
	rr, sr := repoWithSubs("golang/go", ptr("v1.0.0"), "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{release: &githubclient.Release{TagName: "v1.0.0"}}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no notifications, got %v", em.releaseEmails)
	}
}

func TestScanner_SetsInitialTagWithoutNotifying(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", nil, "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{release: &githubclient.Release{TagName: "v1.0.0"}}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no notifications on first scan, got %v", em.releaseEmails)
	}
	if rr.lastSeenTags["golang/go"] != "v1.0.0" {
		t.Errorf("expected initial last_seen_tag=v1.0.0, got %q", rr.lastSeenTags["golang/go"])
	}
}

func TestScanner_StopsOnRateLimit(t *testing.T) {
	rr := newMockRepoRepo()
	sr := newMockSubRepo()

	for _, full := range []string{"owner/repo1", "owner/repo2"} {
		rr.repos[full] = &model.Repository{ID: full, FullName: full, LastSeenTag: ptr("v1.0.0")}
	}

	gh := &mockReleaseChecker{err: githubclient.ErrRateLimit}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no emails on rate limit, got %v", em.releaseEmails)
	}
}

func TestScanner_SkipsOnNoRelease(t *testing.T) {
	rr, sr := repoWithSubs("golang/go", ptr("v1.0.0"), "user@example.com", "id1", "unsub1")
	gh := &mockReleaseChecker{err: githubclient.ErrNotFound}
	em := &mockNotifier{}

	newScanner(rr, sr, gh, em).RunOnce(context.Background())

	if len(em.releaseEmails) != 0 {
		t.Errorf("expected no emails when no release found, got %v", em.releaseEmails)
	}
}
