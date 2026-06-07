package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/platform/metrics"
	"github.com/posul/github-notifier/internal/repository"
)

type unsubscribeSubStore interface {
	GetByUnsubscribeToken(ctx context.Context, token string) (*model.Subscription, error)
	Delete(ctx context.Context, id string) error
}

type UnsubscribeUseCase struct {
	subs unsubscribeSubStore
}

func NewUnsubscribeUseCase(subs unsubscribeSubStore) *UnsubscribeUseCase {
	return &UnsubscribeUseCase{subs: subs}
}

func (uc *UnsubscribeUseCase) Unsubscribe(ctx context.Context, token string) error {
	sub, err := uc.subs.GetByUnsubscribeToken(ctx, token)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("get subscription by unsubscribe token: %w", err)
	}

	if err := uc.subs.Delete(ctx, sub.ID); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	metrics.SubscriptionsRemovedTotal.Inc()
	slog.Info("service: subscription deleted", "id", sub.ID, "email", sub.Email)
	return nil
}
