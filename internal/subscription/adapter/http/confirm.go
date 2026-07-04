package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/posul/github-notifier/internal/subscription/app"
)

// Confirm handles GET /api/confirm/:token and activates a pending subscription.
func (h *Handler) Confirm(c *gin.Context) {
	ctx := c.Request.Context()
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		slog.WarnContext(ctx, "confirm: invalid token format")
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidToken})
		return
	}

	err := h.confirmer.Confirm(ctx, token)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNotFound):
			slog.WarnContext(ctx, "confirm: token not found")
			c.JSON(http.StatusNotFound, errorResponse{msgTokenNotFound})
		default:
			slog.ErrorContext(ctx, "confirm: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.InfoContext(ctx, "confirm: subscription confirmed")
	c.JSON(http.StatusOK, messageResponse{msgConfirmSuccess})
}
