package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/repository"
)

type subscriptionRepoStore interface {
	GetOrCreate(ctx context.Context, fullName string) (*model.Repository, error)
}

type subscriptionStore interface {
	Create(ctx context.Context, sub *model.Subscription) error
	GetByConfirmToken(ctx context.Context, token string) (*model.Subscription, error)
	GetByUnsubscribeToken(ctx context.Context, token string) (*model.Subscription, error)
	GetByEmail(ctx context.Context, email string) ([]*model.Subscription, error)
	Confirm(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error)
}

type confirmationSender interface {
	SendConfirmation(ctx context.Context, to string, data email.ConfirmData) error
}

var repoRegex = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+$`)

var (
	ErrInvalidEmail  = errors.New("invalid email format")
	ErrInvalidRepo   = errors.New("invalid repository format, expected owner/repo")
	ErrRepoNotFound  = errors.New("repository not found on GitHub")
	ErrAlreadyExists = errors.New("email already subscribed to this repository")
	ErrNotFound      = errors.New("not found")
	ErrRateLimit     = errors.New("GitHub API rate limit exceeded, try again later")
)

type repoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

type SubscriptionService struct {
	repoRepo    subscriptionRepoStore
	subRepo     subscriptionStore
	github      repoChecker
	emailSender confirmationSender
	baseURL     string
}

func NewSubscriptionService(
	repoRepo subscriptionRepoStore,
	subRepo subscriptionStore,
	githubClient repoChecker,
	emailSender confirmationSender,
	baseURL string,
) *SubscriptionService {
	return &SubscriptionService{
		repoRepo:    repoRepo,
		subRepo:     subRepo,
		github:      githubClient,
		emailSender: emailSender,
		baseURL:     baseURL,
	}
}

// Subscribe validates the email and repo, creates a pending subscription, and sends a confirmation email.
func (s *SubscriptionService) Subscribe(ctx context.Context, emailAddr, repoName string) error {
	if !isValidEmail(emailAddr) {
		return ErrInvalidEmail
	}
	if !repoRegex.MatchString(repoName) {
		return ErrInvalidRepo
	}

	parts := strings.SplitN(repoName, "/", 2)
	owner, repo := parts[0], parts[1]

	if err := s.github.CheckRepo(ctx, owner, repo); err != nil {
		switch {
		case errors.Is(err, githubclient.ErrNotFound):
			return ErrRepoNotFound
		case errors.Is(err, githubclient.ErrRateLimit):
			return ErrRateLimit
		default:
			return fmt.Errorf("check github repo: %w", err)
		}
	}

	repoRecord, err := s.repoRepo.GetOrCreate(ctx, repoName)
	if err != nil {
		return fmt.Errorf("get or create repository: %w", err)
	}

	exists, err := s.subRepo.ExistsByEmailAndRepoID(ctx, emailAddr, repoRecord.ID)
	if err != nil {
		return fmt.Errorf("check subscription exists: %w", err)
	}
	if exists {
		return ErrAlreadyExists
	}

	sub := model.NewSubscription(emailAddr, repoRecord.ID, repoName)

	log.Printf("service: creating subscription %s → %s", emailAddr, repoName)
	if err := s.subRepo.Create(ctx, sub); err != nil {
		if errors.Is(err, repository.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", s.baseURL, sub.ConfirmToken)
	if err := s.emailSender.SendConfirmation(ctx, emailAddr, email.ConfirmData{
		Repo:       repoName,
		ConfirmURL: confirmURL,
	}); err != nil {
		_ = s.subRepo.Delete(ctx, sub.ID)
		return fmt.Errorf("send confirmation email: %w", err)
	}

	return nil
}

func (s *SubscriptionService) Confirm(ctx context.Context, token string) error {
	sub, err := s.subRepo.GetByConfirmToken(ctx, token)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("get subscription by confirm token: %w", err)
	}

	if err := s.subRepo.Confirm(ctx, sub.ID); err != nil {
		return fmt.Errorf("confirm subscription: %w", err)
	}
	log.Printf("service: confirmed subscription id=%s", sub.ID)
	return nil
}

func (s *SubscriptionService) Unsubscribe(ctx context.Context, token string) error {
	sub, err := s.subRepo.GetByUnsubscribeToken(ctx, token)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("get subscription by unsubscribe token: %w", err)
	}

	if err := s.subRepo.Delete(ctx, sub.ID); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	log.Printf("service: deleted subscription id=%s email=%s", sub.ID, sub.Email)
	return nil
}

func (s *SubscriptionService) GetSubscriptions(ctx context.Context, emailAddr string) ([]*model.Subscription, error) {
	if !isValidEmail(emailAddr) {
		return nil, ErrInvalidEmail
	}

	subs, err := s.subRepo.GetByEmail(ctx, emailAddr)
	if err != nil {
		return nil, fmt.Errorf("get subscriptions: %w", err)
	}
	if subs == nil {
		subs = []*model.Subscription{}
	}
	return subs, nil
}

func isValidEmail(addr string) bool {
	parts := strings.Split(addr, "@")
	if len(parts) != 2 {
		return false
	}
	local, domain := parts[0], parts[1]
	return len(local) > 0 && strings.Contains(domain, ".") && len(domain) > 2
}
