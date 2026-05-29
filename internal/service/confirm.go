package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/repository"
)

type confirmSubStore interface {
	GetByConfirmToken(ctx context.Context, token string) (*model.Subscription, error)
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
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("get subscription by confirm token: %w", err)
	}

	if err := uc.subs.Confirm(ctx, sub.ID); err != nil {
		return fmt.Errorf("confirm subscription: %w", err)
	}
	log.Printf("service: confirmed subscription id=%s", sub.ID)
	return nil
}
