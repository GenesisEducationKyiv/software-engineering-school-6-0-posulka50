package httpapi

import (
	"context"

	"github.com/posul/github-notifier/internal/subscription/domain"
)

type Subscriber interface {
	Subscribe(ctx context.Context, email, repo string) error
}

type Confirmer interface {
	Confirm(ctx context.Context, token string) error
}

type Unsubscriber interface {
	Unsubscribe(ctx context.Context, token string) error
}

type SubscriptionLister interface {
	GetSubscriptions(ctx context.Context, email string) ([]*domain.Subscription, error)
}
