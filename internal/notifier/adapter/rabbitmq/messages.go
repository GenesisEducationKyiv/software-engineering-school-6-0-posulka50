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
