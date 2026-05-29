package service

import (
	"context"
	"fmt"

	"github.com/posul/github-notifier/internal/model"
)

type getSubsStore interface {
	GetByEmail(ctx context.Context, email string) ([]*model.Subscription, error)
}

type GetSubscriptionsUseCase struct {
	subs getSubsStore
}

func NewGetSubscriptionsUseCase(subs getSubsStore) *GetSubscriptionsUseCase {
	return &GetSubscriptionsUseCase{subs: subs}
}

func (uc *GetSubscriptionsUseCase) GetSubscriptions(ctx context.Context, emailAddr string) ([]*model.Subscription, error) {
	if !isValidEmail(emailAddr) {
		return nil, ErrInvalidEmail
	}

	subs, err := uc.subs.GetByEmail(ctx, emailAddr)
	if err != nil {
		return nil, fmt.Errorf("get subscriptions: %w", err)
	}
	if subs == nil {
		subs = []*model.Subscription{}
	}
	return subs, nil
}
