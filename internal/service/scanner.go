package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/repository"
)

type releaseChecker interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (*githubclient.Release, error)
}

type Scanner struct {
	repoRepo    repository.RepositoryRepository
	subRepo     repository.SubscriptionRepository
	github      releaseChecker
	emailSender email.Notifier
	baseURL     string
	interval    time.Duration
}

func NewScanner(
	repoRepo repository.RepositoryRepository,
	subRepo repository.SubscriptionRepository,
	githubClient releaseChecker,
	emailSender email.Notifier,
	baseURL string,
	interval time.Duration,
) *Scanner {
	return &Scanner{
		repoRepo:    repoRepo,
		subRepo:     subRepo,
		github:      githubClient,
		emailSender: emailSender,
		baseURL:     baseURL,
		interval:    interval,
	}
}

func (s *Scanner) RunOnce(ctx context.Context) {
	s.scan(ctx)
}

func (s *Scanner) Start(ctx context.Context) {
	log.Printf("scanner: started (interval=%s)", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.scan(ctx)

	for {
		select {
		case <-ticker.C:
			s.scan(ctx)
		case <-ctx.Done():
			log.Println("scanner: stopped")
			return
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	log.Println("scanner: running scan")

	repos, err := s.repoRepo.GetAllWithConfirmedSubscriptions(ctx)
	if err != nil {
		log.Printf("scanner: failed to fetch repositories: %v", err)
		return
	}

	log.Printf("scanner: %d repo(s) to check", len(repos))
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
				log.Println("scanner: GitHub rate limit hit, stopping scan early")
				return
			}
			if errors.Is(err, githubclient.ErrNotFound) {
				log.Printf("scanner: no releases for %s, skipping", repo.FullName)
				continue
			}
			log.Printf("scanner: error fetching release for %s: %v", repo.FullName, err)
			continue
		}

		s.processRepo(ctx, repo, release)
	}

	log.Println("scanner: scan complete")
}

func (s *Scanner) processRepo(ctx context.Context, repo *model.Repository, release *githubclient.Release) {
	if repo.LastSeenTag == nil {
		if err := s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
			log.Printf("scanner: failed to set initial last_seen_tag for %s: %v", repo.FullName, err)
		}
		return
	}

	if *repo.LastSeenTag == release.TagName {
		log.Printf("scanner: %s — no new release (last: %s)", repo.FullName, *repo.LastSeenTag)
		return
	}

	log.Printf("scanner: %s — new release %s → %s", repo.FullName, *repo.LastSeenTag, release.TagName)

	subs, err := s.subRepo.GetConfirmedByRepoID(ctx, repo.ID)
	if err != nil {
		log.Printf("scanner: failed to fetch subscribers for %s: %v", repo.FullName, err)
		return
	}

	for _, sub := range subs {
		unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", s.baseURL, sub.UnsubscribeToken)
		err := s.emailSender.SendReleaseNotification(sub.Email, email.ReleaseData{
			Repo:           repo.FullName,
			TagName:        release.TagName,
			ReleaseName:    release.Name,
			Body:           release.Body,
			ReleaseURL:     release.HTMLURL,
			UnsubscribeURL: unsubURL,
		})
		if err != nil {
			log.Printf("scanner: failed to notify %s about %s@%s: %v",
				sub.Email, repo.FullName, release.TagName, err)
		} else {
			log.Printf("scanner: notified %s → %s %s", sub.Email, repo.FullName, release.TagName)
		}
	}

	if err := s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
		log.Printf("scanner: failed to update last_seen_tag for %s: %v", repo.FullName, err)
	}
}
