package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/posul/github-notifier/internal/subscription/app"
)

// Subscribe handles POST /api/subscribe and creates a new pending subscription.
func (h *Handler) Subscribe(c *gin.Context) {
	ctx := c.Request.Context()
	var req struct {
		Email string `json:"email"`
		Repo  string `json:"repo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidRequestBody})
		return
	}

	email := req.Email
	repo := req.Repo

	if email == "" || repo == "" {
		c.JSON(http.StatusBadRequest, errorResponse{msgEmailAndRepoRequired})
		return
	}

	slog.InfoContext(ctx, "subscribe", "email", email, "repo", repo)

	err := h.subscriber.Subscribe(ctx, email, repo)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrInvalidEmail),
			errors.Is(err, app.ErrInvalidRepo):
			slog.WarnContext(ctx, "subscribe: validation error", "error", err)
			c.JSON(http.StatusBadRequest, errorResponse{err.Error()})
		case errors.Is(err, app.ErrRepoNotFound):
			slog.WarnContext(ctx, "subscribe: repo not found", "repo", repo)
			c.JSON(http.StatusNotFound, errorResponse{err.Error()})
		case errors.Is(err, app.ErrAlreadyExists):
			slog.WarnContext(ctx, "subscribe: already exists", "email", email, "repo", repo)
			c.JSON(http.StatusConflict, errorResponse{err.Error()})
		case errors.Is(err, app.ErrRateLimit):
			slog.WarnContext(ctx, "subscribe: github rate limit hit")
			c.JSON(http.StatusTooManyRequests, errorResponse{err.Error()})
		default:
			slog.ErrorContext(ctx, "subscribe: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.InfoContext(ctx, "subscribe: confirmation email sent", "email", email, "repo", repo)
	c.JSON(http.StatusOK, messageResponse{msgSubscribeSuccess})
}
