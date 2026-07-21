// Package logctx propagates a per-request correlation id through
// context.Context and injects it into every structured log record.
//
// The flow is: the RequestID HTTP middleware puts the id on the request
// context via WithRequestID; downstream code that already passes ctx into
// slog.*Context calls (or that the access logger calls with c.Request.Context())
// picks it up automatically through the Handler wrapper installed in main.
package logctx

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

var requestIDKey ctxKey

const requestIDAttr = "request_id"

// WithRequestID returns a child context that carries the given request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request id stored on ctx, or "" if none is set.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Handler wraps another slog.Handler and, on each Handle call, adds a
// request_id attribute pulled from the record's context. Records emitted
// without a context (or outside a request) are passed through untouched.
type Handler struct {
	inner slog.Handler
}

// NewHandler wraps inner so every record gets a request_id attribute when
// one is present on the context.
func NewHandler(inner slog.Handler) *Handler {
	return &Handler{inner: inner}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String(requestIDAttr, id))
	}
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}
