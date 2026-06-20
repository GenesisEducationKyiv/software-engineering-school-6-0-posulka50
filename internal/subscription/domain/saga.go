package domain

import (
	"time"

	"github.com/google/uuid"
)

// SagaState is the lifecycle stage of a Subscribe saga.
type SagaState string

const (
	SagaStatePending     SagaState = "pending"
	SagaStateCompleted   SagaState = "completed"
	SagaStateCompensated SagaState = "compensated"
	SagaStateTimedOut    SagaState = "timed_out"
)

// Saga is the journal entry for a single Subscribe saga instance. It tracks
// the lifecycle of the confirmation-email distributed transaction between the
// subscription service (orchestrator) and the notifier service (participant).
type Saga struct {
	ID             string
	SubscriptionID string
	State          SagaState
	LastError      *string
	StartedAt      time.Time
	CompletedAt    *time.Time
}

// NewSaga returns a fresh pending saga bound to the given subscription.
func NewSaga(subscriptionID string) *Saga {
	return &Saga{
		ID:             uuid.New().String(),
		SubscriptionID: subscriptionID,
		State:          SagaStatePending,
		StartedAt:      time.Now().UTC(),
	}
}
