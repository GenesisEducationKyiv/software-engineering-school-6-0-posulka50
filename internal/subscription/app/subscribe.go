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
	// CreateWithSaga atomically inserts the subscription and its pending saga
	// journal row so a crash between the two cannot orphan the subscription.
	CreateWithSaga(ctx context.Context, sub *domain.Subscription, s *domain.Saga) error
	Delete(ctx context.Context, id string) error
	ExistsByEmailAndRepoID(ctx context.Context, email, repoID string) (bool, error)
}

type repoChecker interface {
	CheckRepo(ctx context.Context, owner, repo string) error
}

// sagaPublisher is the orchestrator entry point used by Subscribe. Satisfied
// by *saga.Orchestrator via structural typing. The saga row is already in the
// DB (persisted by CreateWithSaga) by the time Publish is called; Publish
// only dispatches the command and runs inline compensation on failure.
type sagaPublisher interface {
	Publish(ctx context.Context, sagaID string, sub *domain.Subscription, confirmURL string) error
}

type SubscribeUseCase struct {
	repos   subscriptionRepoStore
	subs    subscribeSubStore
	github  repoChecker
	saga    sagaPublisher
	baseURL string
}

func NewSubscribeUseCase(
	repos subscriptionRepoStore,
	subs subscribeSubStore,
	githubClient repoChecker,
	saga sagaPublisher,
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
	sagaEntity := domain.NewSaga(sub.ID)

	slog.InfoContext(ctx, "subscription: creating subscription", "email", emailAddr, "repo", repoName)
	if err := uc.subs.CreateWithSaga(ctx, sub, sagaEntity); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("create subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", uc.baseURL, sub.ConfirmToken)
	if err := uc.saga.Publish(ctx, sagaEntity.ID, sub, confirmURL); err != nil {
		// The orchestrator has already run inline compensation on publish
		// failure; if that failed too, the saga stays pending and the
		// timeout sweeper will finish the delete.
		return fmt.Errorf("publish subscribe saga: %w", err)
	}

	metrics.SubscriptionsCreatedTotal.Inc()
	return nil
}
