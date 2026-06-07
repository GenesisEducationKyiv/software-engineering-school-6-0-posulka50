package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/posul/github-notifier/internal/subscription/app"
	"github.com/posul/github-notifier/internal/subscription/domain"
)

type subscriptionResponse struct {
	Email       string  `json:"email"`
	Repo        string  `json:"repo"`
	Confirmed   bool    `json:"confirmed"`
	LastSeenTag *string `json:"last_seen_tag,omitempty"`
}

func toResponse(s *domain.Subscription) subscriptionResponse {
	return subscriptionResponse{
		Email:       s.Email,
		Repo:        s.Repo,
		Confirmed:   s.Confirmed,
		LastSeenTag: s.LastSeenTag,
	}
}

// GetSubscriptions handles GET /api/subscriptions and returns confirmed subscriptions for an email.
func (h *Handler) GetSubscriptions(c *gin.Context) {
	emailAddr := c.Query("email")
	if emailAddr == "" {
		c.JSON(http.StatusBadRequest, errorResponse{msgEmailRequired})
		return
	}

	subs, err := h.subscriptionLister.GetSubscriptions(c.Request.Context(), emailAddr)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrInvalidEmail):
			c.JSON(http.StatusBadRequest, errorResponse{err.Error()})
		default:
			slog.Error("subscriptions: internal error", "email", emailAddr, "error", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	resp := make([]subscriptionResponse, len(subs))
	for i, s := range subs {
		resp[i] = toResponse(s)
	}
	c.JSON(http.StatusOK, resp)
}
