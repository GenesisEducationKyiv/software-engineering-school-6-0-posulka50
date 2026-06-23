package domain

import (
	"time"

	"github.com/google/uuid"
)

// NewSubscription creates a new pending Subscription with generated tokens and current timestamp.
func NewSubscription(email, repoID, repoName string) *Subscription {
	return &Subscription{
		ID:               uuid.New().String(),
		RepoID:           repoID,
		Repo:             repoName,
		Email:            email,
		Confirmed:        false,
		ConfirmToken:     uuid.New().String(),
		UnsubscribeToken: uuid.New().String(),
		CreatedAt:        time.Now().UTC(),
	}
}

// Subscription represents a user's email subscription to a repository's releases.
type Subscription struct {
	ID               string    `json:"id"`
	RepoID           string    `json:"repo_id"`
	Repo             string    `json:"repo"`
	Email            string    `json:"email"`
	Confirmed        bool      `json:"confirmed"`
	LastSeenTag      *string   `json:"last_seen_tag"`
	ConfirmToken     string    `json:"-"`
	UnsubscribeToken string    `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}
