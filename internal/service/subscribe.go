package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/repository"
)

var repoRegex = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+$`)

type subscriptionRepoStore interface {
	GetOrCreate(ctx context.Context, fullName string) (*model.Repository, error)
}

type subscribeSubStore interface {
	Create(ctx context.Context, sub *model.Subscription) error
	Delete(ctx context.Context, id string) error
	ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error)
}

type repoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

type confirmationSender interface {
	SendConfirmation(ctx context.Context, to string, data email.ConfirmData) error
}

type SubscribeUseCase struct {
	repos       subscriptionRepoStore
	subs        subscribeSubStore
	github      repoChecker
	emailSender confirmationSender
	baseURL     string
}

func NewSubscribeUseCase(
	repos subscriptionRepoStore,
	subs subscribeSubStore,
	githubClient repoChecker,
	emailSender confirmationSender,
	baseURL string,
) *SubscribeUseCase {
	return &SubscribeUseCase{
		repos:       repos,
		subs:        subs,
		github:      githubClient,
		emailSender: emailSender,
		baseURL:     baseURL,
	}
}

func (uc *SubscribeUseCase) Subscribe(ctx context.Context, emailAddr, repoName string) error {
	if !isValidEmail(emailAddr) {
		return ErrInvalidEmail
	}
	if !repoRegex.MatchString(repoName) {
		return ErrInvalidRepo
	}

	parts := strings.SplitN(repoName, "/", 2)
	owner, repo := parts[0], parts[1]

	if err := uc.github.CheckRepo(ctx, owner, repo); err != nil {
		switch {
		case errors.Is(err, githubclient.ErrNotFound):
			return ErrRepoNotFound
		case errors.Is(err, githubclient.ErrRateLimit):
			return ErrRateLimit
		default:
			return fmt.Errorf("check github repo: %w", err)
		}
	}

	repoRecord, err := uc.repos.GetOrCreate(ctx, repoName)
	if err != nil {
		return fmt.Errorf("get or create repository: %w", err)
	}

	exists, err := uc.subs.ExistsByEmailAndRepoID(ctx, emailAddr, repoRecord.ID)
	if err != nil {
		return fmt.Errorf("check subscription exists: %w", err)
	}
	if exists {
		return ErrAlreadyExists
	}

	sub := model.NewSubscription(emailAddr, repoRecord.ID, repoName)

	slog.Info("service: creating subscription", "email", emailAddr, "repo", repoName)
	if err := uc.subs.Create(ctx, sub); err != nil {
		if errors.Is(err, repository.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", uc.baseURL, sub.ConfirmToken)
	if err := uc.emailSender.SendConfirmation(ctx, emailAddr, email.ConfirmData{
		Repo:       repoName,
		ConfirmURL: confirmURL,
	}); err != nil {
		_ = uc.subs.Delete(ctx, sub.ID)
		return fmt.Errorf("send confirmation email: %w", err)
	}

	return nil
}
