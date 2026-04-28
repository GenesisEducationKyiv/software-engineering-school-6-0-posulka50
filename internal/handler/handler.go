package handler

import (
	"github.com/posul/github-notifier/internal/service"
)

type Handler struct {
	svc *service.SubscriptionService
}

func New(svc *service.SubscriptionService) *Handler {
	return &Handler{svc: svc}
}
