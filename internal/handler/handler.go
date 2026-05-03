package handler

import (
	"github.com/posul/github-notifier/internal/service"
)

const (
	jsonKeyError     = "error"
	jsonKeyMessage   = "message"
	msgInternalError = "internal server error"
)

// Handler holds the HTTP handler methods for the subscription API.
type Handler struct {
	svc *service.SubscriptionService
}

// New creates a new Handler with the given SubscriptionService.
func New(svc *service.SubscriptionService) *Handler {
	return &Handler{svc: svc}
}
