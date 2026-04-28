package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/posul/github-notifier/internal/model"
	"github.com/posul/github-notifier/internal/service"
)

type subscriptionResponse struct {
	Email       string  `json:"email"`
	Repo        string  `json:"repo"`
	Confirmed   bool    `json:"confirmed"`
	LastSeenTag *string `json:"last_seen_tag,omitempty"`
}

func toResponse(s *model.Subscription) subscriptionResponse {
	return subscriptionResponse{
		Email:       s.Email,
		Repo:        s.Repo,
		Confirmed:   s.Confirmed,
		LastSeenTag: s.LastSeenTag,
	}
}

func (h *Handler) GetSubscriptions(c *gin.Context) {
	emailAddr := c.Query("email")
	if emailAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email query parameter is required"})
		return
	}

	subs, err := h.svc.GetSubscriptions(c.Request.Context(), emailAddr)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidEmail):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			log.Printf("subscriptions: internal error for %s: %v", emailAddr, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}

	resp := make([]subscriptionResponse, len(subs))
	for i, s := range subs {
		resp[i] = toResponse(s)
	}
	c.JSON(http.StatusOK, resp)
}
