package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/metrics"
	"github.com/posul/github-notifier/internal/model"
)

type releaseChecker interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (*githubclient.Release, error)
}

type scannerRepoStore interface {
	GetAllWithConfirmedSubscriptions(ctx context.Context) ([]*model.Repository, error)
	UpdateLastSeenTag(ctx context.Context, id string, tag string) error
}

type scannerSubStore interface {
	GetConfirmedByRepoID(ctx context.Context, repoID string) ([]*model.Subscription, error)
}

type releaseSender interface {
	SendReleaseNotification(ctx context.Context, to string, data email.ReleaseData) error
}

// Scanner polls GitHub for new releases and notifies subscribers by email.
type Scanner struct {
	repos       scannerRepoStore
	subs        scannerSubStore
	github      releaseChecker
	emailSender releaseSender
	baseURL     string
	interval    time.Duration
}

func NewScanner(
	repos scannerRepoStore,
	subs scannerSubStore,
	githubClient releaseChecker,
	emailSender releaseSender,
	baseURL string,
	interval time.Duration,
) *Scanner {
	return &Scanner{
		repos:       repos,
		subs:        subs,
		github:      githubClient,
		emailSender: emailSender,
		baseURL:     baseURL,
		interval:    interval,
	}
}

func (s *Scanner) RunOnce(ctx context.Context) {
	s.scan(ctx)
}

// Start runs the scanner in a loop, scanning immediately and then on each interval tick.
// It blocks until ctx is canceled.
func (s *Scanner) Start(ctx context.Context) {
	slog.Info("scanner: started", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.scan(ctx)

	for {
		select {
		case <-ticker.C:
			s.scan(ctx)
		case <-ctx.Done():
			slog.Info("scanner: stopped")
			return
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	slog.Info("scanner: running scan")
	start := time.Now()

	repos, err := s.repos.GetAllWithConfirmedSubscriptions(ctx)
	if err != nil {
		slog.Error("scanner: failed to fetch repositories", "error", err)
		metrics.ScannerRunsTotal.WithLabelValues("error").Inc()
		return
	}

	slog.Info("scanner: repos to check", "count", len(repos))
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}

		parts := strings.SplitN(repo.FullName, "/", 2)
		if len(parts) != 2 {
			continue
		}
		owner, repoName := parts[0], parts[1]

		release, err := s.github.GetLatestRelease(ctx, owner, repoName)
		if err != nil {
			if errors.Is(err, githubclient.ErrRateLimit) {
				slog.Warn("scanner: GitHub rate limit hit, stopping scan early")
				return
			}
			if errors.Is(err, githubclient.ErrNotFound) {
				slog.Debug("scanner: no releases for repo", "repo", repo.FullName)
				continue
			}
			slog.Error("scanner: error fetching release", "repo", repo.FullName, "error", err)
			continue
		}

		s.processRepo(ctx, repo, release)
	}

	metrics.ScannerRunsTotal.WithLabelValues("success").Inc()
	metrics.ScannerDuration.Observe(time.Since(start).Seconds())
	slog.Info("scanner: scan complete")
}

func (s *Scanner) processRepo(ctx context.Context, repo *model.Repository, release *githubclient.Release) {
	if repo.LastSeenTag == nil {
		if err := s.repos.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
			slog.Error("scanner: failed to set initial last_seen_tag", "repo", repo.FullName, "error", err)
		}
		return
	}

	if *repo.LastSeenTag == release.TagName {
		slog.Debug("scanner: no new release", "repo", repo.FullName, "tag", *repo.LastSeenTag)
		return
	}

	log.Printf("scanner: %s — new release %s -> %s", repo.FullName, *repo.LastSeenTag, release.TagName)

	subs, err := s.subs.GetConfirmedByRepoID(ctx, repo.ID)
	if err != nil {
		slog.Error("scanner: failed to fetch subscribers", "repo", repo.FullName, "error", err)
		return
	}

	for _, sub := range subs {
		unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", s.baseURL, sub.UnsubscribeToken)
		err = s.emailSender.SendReleaseNotification(ctx, sub.Email, email.ReleaseData{
			Repo:           repo.FullName,
			TagName:        release.TagName,
			ReleaseName:    release.Name,
			Body:           release.Body,
			ReleaseURL:     release.HTMLURL,
			UnsubscribeURL: unsubURL,
		})
		if err != nil {
			slog.Error("scanner: failed to notify subscriber",
				"email", sub.Email, "repo", repo.FullName, "tag", release.TagName, "error", err)
		} else {
			log.Printf("scanner: notified %s -> %s %s", sub.Email, repo.FullName, release.TagName)
		}
	}

	if err := s.repos.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
		slog.Error("scanner: failed to update last_seen_tag", "repo", repo.FullName, "error", err)
	}
}
