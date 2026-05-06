package handler

import (
	"github.com/posul/github-notifier/internal/service"
)

const (
	jsonKeyError   = "error"
	jsonKeyMessage = "message"

	msgInternalError        = "internal server error"
	msgInvalidToken         = "invalid token"
	msgTokenNotFound        = "token not found"
	msgInvalidRequestBody   = "invalid request body"
	msgEmailAndRepoRequired = "email and repo are required"
	msgEmailRequired        = "email query parameter is required"
	msgSubscribeSuccess     = "Subscription successful. Confirmation email sent."
	msgConfirmSuccess       = "Subscription confirmed successfully"
	msgUnsubscribeSuccess   = "Unsubscribed successfully"
)

// Handler holds the HTTP handler methods for the subscription API.
type Handler struct {
	svc *service.SubscriptionService
}

// New creates a new Handler with the given SubscriptionService.
func New(svc *service.SubscriptionService) *Handler {
	return &Handler{svc: svc}
}
