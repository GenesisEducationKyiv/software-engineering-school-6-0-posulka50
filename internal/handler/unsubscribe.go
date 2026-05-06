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
		c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: msgInvalidToken})
		return
	}

	err := h.svc.Unsubscribe(c.Request.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			log.Printf("unsubscribe: token not found: %s", token)
			c.JSON(http.StatusNotFound, gin.H{jsonKeyError: msgTokenNotFound})
		default:
			log.Printf("unsubscribe: internal error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{jsonKeyError: msgInternalError})
		}
		return
	}

	log.Printf("unsubscribe: token %s removed", token)
	c.JSON(http.StatusOK, gin.H{jsonKeyMessage: msgUnsubscribeSuccess})
}
