package domain

// ConfirmData carries the variables needed to render and address a subscription
// confirmation email.
type ConfirmData struct {
	Repo       string
	ConfirmURL string
}

// ReleaseData carries the variables needed to render and address a release
// notification email.
type ReleaseData struct {
	Repo           string
	TagName        string
	ReleaseName    string
	Body           string
	ReleaseURL     string
	UnsubscribeURL string
}
