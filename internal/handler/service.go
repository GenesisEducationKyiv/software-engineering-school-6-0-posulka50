package handler

import (
	"context"

	"github.com/posul/github-notifier/internal/model"
)

type Service interface {
	Subscribe(ctx context.Context, email, repo string) error
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
	GetSubscriptions(ctx context.Context, email string) ([]*model.Subscription, error)
}
