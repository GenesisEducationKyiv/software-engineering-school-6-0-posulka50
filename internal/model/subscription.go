package model

import "time"

type Repository struct {
	ID          string    `json:"id"`
	FullName    string    `json:"full_name"`
	LastSeenTag *string   `json:"last_seen_tag,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

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
