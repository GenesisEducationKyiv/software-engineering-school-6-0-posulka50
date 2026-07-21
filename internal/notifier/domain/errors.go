package domain

import "errors"

// ErrUpstreamUnavailable marks a transient transport-level failure talking to
// the upstream email provider (network error, connection refused, 5xx). It is
// wrapped by the Sender adapter so callers on the gRPC boundary can surface
// it as codes.Unavailable instead of codes.Internal, matching the contract in
// proto/notifier/v1/notification.proto.
var ErrUpstreamUnavailable = errors.New("upstream email provider unavailable")
