package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/posul/github-notifier/internal/platform/metrics"
	githubclient "github.com/posul/github-notifier/internal/release/adapter/github"
	releasedomain "github.com/posul/github-notifier/internal/release/domain"
	"github.com/posul/github-notifier/internal/subscription/domain"
)

var repoRegex = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+$`)

type subscriptionRepoStore interface {
	GetOrCreate(ctx context.Context, fullName string) (*releasedomain.Repository, error)
}

type subscribeSubStore interface {
	Create(ctx context.Context, sub *domain.Subscription) error
	Delete(ctx context.Context, id string) error
	ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error)
}

type repoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

// sagaStarter is the orchestrator entry point used by Subscribe. Satisfied by
// *saga.Orchestrator via structural typing. Returning the saga ID is purely
// informational here; the use case does not track it (the orchestrator owns
// the lifecycle from this point on).
type sagaStarter interface {
	Start(ctx context.Context, sub *domain.Subscription, confirmURL string) (string, error)
}

type SubscribeUseCase struct {
	repos   subscriptionRepoStore
	subs    subscribeSubStore
	github  repoChecker
	saga    sagaStarter
	baseURL string
}

func NewSubscribeUseCase(
	repos subscriptionRepoStore,
	subs subscribeSubStore,
	githubClient repoChecker,
	saga sagaStarter,
	baseURL string,
) *SubscribeUseCase {
	return &SubscribeUseCase{
		repos:   repos,
		subs:    subs,
		github:  githubClient,
		saga:    saga,
		baseURL: baseURL,
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

	sub := domain.NewSubscription(emailAddr, repoRecord.ID, repoName)

	slog.InfoContext(ctx, "subscription: creating subscription", "email", emailAddr, "repo", repoName)
	if err := uc.subs.Create(ctx, sub); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", uc.baseURL, sub.ConfirmToken)
	if _, err := uc.saga.Start(ctx, sub, confirmURL); err != nil {
		// The saga never went live (publish failed); the orchestrator already
		// marked its journal entry compensated. The just-created subscription
		// row has no live saga to clean it up, so we delete it here.
		_ = uc.subs.Delete(ctx, sub.ID)
		return fmt.Errorf("start subscribe saga: %w", err)
	}

	metrics.SubscriptionsCreatedTotal.Inc()
	return nil
}
