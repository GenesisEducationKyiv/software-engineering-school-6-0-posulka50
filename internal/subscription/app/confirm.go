package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/posul/github-notifier/internal/platform/metrics"
	"github.com/posul/github-notifier/internal/subscription/domain"
)

type confirmSubStore interface {
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	Confirm(ctx context.Context, id string) error
}

type ConfirmUseCase struct {
	subs confirmSubStore
}

func NewConfirmUseCase(subs confirmSubStore) *ConfirmUseCase {
	return &ConfirmUseCase{subs: subs}
}

func (uc *ConfirmUseCase) Confirm(ctx context.Context, token string) error {
	sub, err := uc.subs.GetByConfirmToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("get subscription by confirm token: %w", err)
	}

	if err := uc.subs.Confirm(ctx, sub.ID); err != nil {
		return fmt.Errorf("confirm subscription: %w", err)
	}
	metrics.SubscriptionsConfirmedTotal.Inc()
	slog.Info("subscription: confirmed", "id", sub.ID)
	return nil
}
