package domain

import "errors"

// ErrNotFound is returned by the subscription store when a record cannot be
// located by its identifier or token.
var ErrNotFound = errors.New("subscription not found")

// ErrAlreadyExists is returned by the subscription store when a subscription
// already exists for the given email + repository pair.
var ErrAlreadyExists = errors.New("subscription already exists")
