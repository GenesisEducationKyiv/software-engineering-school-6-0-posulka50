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

	slog.Info("subscribe", "email", email, "repo", repo)

	err := h.subscriber.Subscribe(c.Request.Context(), email, repo)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrInvalidEmail),
			errors.Is(err, app.ErrInvalidRepo):
			slog.Warn("subscribe: validation error", "error", err)
			c.JSON(http.StatusBadRequest, errorResponse{err.Error()})
		case errors.Is(err, app.ErrRepoNotFound):
			slog.Warn("subscribe: repo not found", "repo", repo)
			c.JSON(http.StatusNotFound, errorResponse{err.Error()})
		case errors.Is(err, app.ErrAlreadyExists):
			slog.Warn("subscribe: already exists", "email", email, "repo", repo)
			c.JSON(http.StatusConflict, errorResponse{err.Error()})
		case errors.Is(err, app.ErrRateLimit):
			slog.Warn("subscribe: github rate limit hit")
			c.JSON(http.StatusTooManyRequests, errorResponse{err.Error()})
		default:
			slog.Error("subscribe: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.Info("subscribe: confirmation email sent", "email", email, "repo", repo)
	c.JSON(http.StatusOK, messageResponse{msgSubscribeSuccess})
}
