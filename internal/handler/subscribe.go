package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/posul/github-notifier/internal/service"
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

	log.Printf("subscribe: %s → %s", email, repo)

	err := h.svc.Subscribe(c.Request.Context(), email, repo)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidEmail),
			errors.Is(err, service.ErrInvalidRepo):
			log.Printf("subscribe: validation error: %v", err)
			c.JSON(http.StatusBadRequest, errorResponse{err.Error()})
		case errors.Is(err, service.ErrRepoNotFound):
			log.Printf("subscribe: repo not found: %s", repo)
			c.JSON(http.StatusNotFound, errorResponse{err.Error()})
		case errors.Is(err, service.ErrAlreadyExists):
			log.Printf("subscribe: already exists: %s %s", email, repo)
			c.JSON(http.StatusConflict, errorResponse{err.Error()})
		case errors.Is(err, service.ErrRateLimit):
			log.Printf("subscribe: github rate limit hit")
			c.JSON(http.StatusTooManyRequests, errorResponse{err.Error()})
		default:
			log.Printf("subscribe: internal error: %v", err)
			c.JSON(http.StatusInternalServerError, errorResponse{msgInternalError})
		}
		return
	}

	log.Printf("subscribe: confirmation email sent to %s for %s", email, repo)
	c.JSON(http.StatusOK, messageResponse{msgSubscribeSuccess})
}
