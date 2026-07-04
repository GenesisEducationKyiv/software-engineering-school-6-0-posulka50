package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/posul/github-notifier/internal/subscription/app"
)

// Unsubscribe handles GET /api/unsubscribe/:token and removes a subscription.
func (h *Handler) Unsubscribe(c *gin.Context) {
	ctx := c.Request.Context()
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		slog.WarnContext(ctx, "unsubscribe: invalid token format")
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidToken})
		return
	}

	err := h.unsubscriber.Unsubscribe(ctx, token)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNotFound):
			slog.WarnContext(ctx, "unsubscribe: token not found")
			c.JSON(http.StatusNotFound, errorResponse{msgTokenNotFound})
		default:
			slog.ErrorContext(ctx, "unsubscribe: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.InfoContext(ctx, "unsubscribe: token removed")
	c.JSON(http.StatusOK, messageResponse{msgUnsubscribeSuccess})
}
