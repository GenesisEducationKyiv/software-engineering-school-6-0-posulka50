package httpapi

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
	msgSubscribeSuccess     = "Subscription accepted. Check your inbox shortly to confirm."
	msgConfirmSuccess       = "Subscription confirmed successfully"
	msgUnsubscribeSuccess   = "Unsubscribed successfully"
)

type Handler struct {
	subscriber         Subscriber
	confirmer          Confirmer
	unsubscriber       Unsubscriber
	subscriptionLister SubscriptionLister
}

func New(
	subscriber Subscriber,
	confirmer Confirmer,
	unsubscriber Unsubscriber,
	subscriptionLister SubscriptionLister,
) *Handler {
	return &Handler{
		subscriber:         subscriber,
		confirmer:          confirmer,
		unsubscriber:       unsubscriber,
		subscriptionLister: subscriptionLister,
	}
}
