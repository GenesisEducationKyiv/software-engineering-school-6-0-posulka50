package domain

import "time"

// Repository is a GitHub repository tracked for new releases.
type Repository struct {
	ID          string    `json:"id"`
	FullName    string    `json:"full_name"`
	LastSeenTag *string   `json:"last_seen_tag,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
