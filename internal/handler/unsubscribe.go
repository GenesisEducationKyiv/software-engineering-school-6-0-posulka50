package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posul/github-notifier/internal/service"
)

// Unsubscribe handles GET /api/unsubscribe/:token and removes a subscription.
func (h *Handler) Unsubscribe(c *gin.Context) {
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		log.Printf("unsubscribe: invalid token format: %s", token)
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidToken})
		return
	}

	err := h.unsubscriber.Unsubscribe(c.Request.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			log.Printf("unsubscribe: token not found: %s", token)
			c.JSON(http.StatusNotFound, errorResponse{msgTokenNotFound})
		default:
			log.Printf("unsubscribe: internal error: %v", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	log.Printf("unsubscribe: token %s removed", token)
	c.JSON(http.StatusOK, messageResponse{msgUnsubscribeSuccess})
}
