package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posul/github-notifier/internal/service"
)

// Confirm handles GET /api/confirm/:token and activates a pending subscription.
func (h *Handler) Confirm(c *gin.Context) {
	token := c.Param("token")
	if _, err := uuid.Parse(token); err != nil {
		log.Printf("confirm: invalid token format: %s", token)
		c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: "invalid token"})
		return
	}

	err := h.svc.Confirm(c.Request.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			log.Printf("confirm: token not found: %s", token)
			c.JSON(http.StatusNotFound, gin.H{jsonKeyError: "token not found"})
		default:
			log.Printf("confirm: internal error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{jsonKeyError: msgInternalError})
		}
		return
	}

	log.Printf("confirm: subscription confirmed for token %s", token)
	c.JSON(http.StatusOK, gin.H{jsonKeyMessage: "Subscription confirmed successfully"})
}
