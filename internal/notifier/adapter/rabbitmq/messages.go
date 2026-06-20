package rabbitmq

// ConfirmationMessage is the JSON payload published under routing key
// notification.confirmation.
type ConfirmationMessage struct {
	To         string `json:"to"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

// ReleaseMessage is the JSON payload published under routing key
// notification.release.
type ReleaseMessage struct {
	To             string `json:"to"`
	Repo           string `json:"repo"`
	TagName        string `json:"tag_name"`
	ReleaseName    string `json:"release_name"`
	Body           string `json:"body"`
	ReleaseURL     string `json:"release_url"`
	UnsubscribeURL string `json:"unsubscribe_url"`
}

// SendConfirmationCommand is the Subscribe-saga command published by the
// subscription orchestrator under routing key
// notification.command.send_confirmation. The notifier consumes it, performs
// the Resend call, and replies with a ConfirmationSentEvent or
// ConfirmationFailedEvent carrying the same SagaID for correlation.
type SendConfirmationCommand struct {
	SagaID     string `json:"saga_id"`
	To         string `json:"to"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

// ConfirmationSentEvent is the success reply emitted by the notifier under
// routing key notification.event.confirmation_sent.
type ConfirmationSentEvent struct {
	SagaID string `json:"saga_id"`
}

// ConfirmationFailedEvent is the failure reply emitted by the notifier under
// routing key notification.event.confirmation_failed. Reason is a short
// human-readable description (e.g. the upstream error) used for saga
// last_error and logging; the orchestrator does not parse it.
type ConfirmationFailedEvent struct {
	SagaID string `json:"saga_id"`
	Reason string `json:"reason"`
}
