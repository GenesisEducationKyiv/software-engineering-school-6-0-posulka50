package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posul/github-notifier/internal/service"
)

// Confirm handles GET /api/confirm/:token and activates a pending subscription.
func (h *Handler) Confirm(c *gin.Context) {
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		slog.Warn("confirm: invalid token format", "token", token)
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidToken})
		return
	}

	err := h.confirmer.Confirm(c.Request.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			slog.Warn("confirm: token not found", "token", token)
			c.JSON(http.StatusNotFound, errorResponse{msgTokenNotFound})
		default:
			slog.Error("confirm: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.Info("confirm: subscription confirmed", "token", token)
	c.JSON(http.StatusOK, messageResponse{msgConfirmSuccess})
}
