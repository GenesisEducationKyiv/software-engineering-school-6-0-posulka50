package handler

import (
	"context"

	"github.com/posul/github-notifier/internal/model"
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
	GetSubscriptions(ctx context.Context, email string) ([]*model.Subscription, error)
}
