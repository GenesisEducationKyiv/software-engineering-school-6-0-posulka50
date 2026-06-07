package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posul/github-notifier/internal/service"
)

// Unsubscribe handles GET /api/unsubscribe/:token and removes a subscription.
func (h *Handler) Unsubscribe(c *gin.Context) {
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		slog.Warn("unsubscribe: invalid token format", "token", token)
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidToken})
		return
	}

	err := h.unsubscriber.Unsubscribe(c.Request.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			slog.Warn("unsubscribe: token not found", "token", token)
			c.JSON(http.StatusNotFound, errorResponse{msgTokenNotFound})
		default:
			slog.Error("unsubscribe: internal error", "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	slog.Info("unsubscribe: token removed", "token", token)
	c.JSON(http.StatusOK, messageResponse{msgUnsubscribeSuccess})
}
