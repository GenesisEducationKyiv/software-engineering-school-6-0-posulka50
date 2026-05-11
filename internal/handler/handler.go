package handler

type errorResponse struct {
	Error string `json:"error"`
}

type messageResponse struct {
	Message string `json:"message"`
}

const (
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
	svc Service
}

// New creates a new Handler with the given Service.
func New(svc Service) *Handler {
	return &Handler{svc: svc}
}
