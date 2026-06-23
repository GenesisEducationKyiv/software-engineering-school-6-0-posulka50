package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/posul/github-notifier/internal/notifier/domain"
)

// Sender is the port the HTTP adapter calls to deliver notifications. It is
// implemented by notifier/adapter/resend.Sender.
type Sender interface {
	SendConfirmation(ctx context.Context, to string, data domain.ConfirmData) error
	SendReleaseNotification(ctx context.Context, to string, data domain.ReleaseData) error
}

type Handler struct {
	sender Sender
}

func New(sender Sender) *Handler {
	return &Handler{sender: sender}
}

type errorResponse struct {
	Error string `json:"error"`
}

type statusResponse struct {
	Status string `json:"status"`
}

const (
	msgInvalidRequestBody = "invalid request body"
	msgMissingFields      = "missing required fields"
	msgSendFailed         = "failed to send notification"
)

type confirmationRequest struct {
	To         string `json:"to"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

// Confirmation handles POST /v1/notifications/confirmation.
func (h *Handler) Confirmation(c *gin.Context) {
	var req confirmationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidRequestBody})
		return
	}
	if req.To == "" || req.Repo == "" || req.ConfirmURL == "" {
		c.JSON(http.StatusBadRequest, errorResponse{msgMissingFields})
		return
	}

	if err := h.sender.SendConfirmation(c.Request.Context(), req.To, domain.ConfirmData{
		Repo:       req.Repo,
		ConfirmURL: req.ConfirmURL,
	}); err != nil {
		slog.Error("notifier: send confirmation failed", "to", req.To, "repo", req.Repo, "error", err)
		c.JSON(http.StatusInternalServerError, errorResponse{msgSendFailed})
		return
	}

	slog.Info("notifier: confirmation sent", "to", req.To, "repo", req.Repo)
	c.JSON(http.StatusAccepted, statusResponse{"sent"})
}

type releaseRequest struct {
	To             string `json:"to"`
	Repo           string `json:"repo"`
	TagName        string `json:"tag_name"`
	ReleaseName    string `json:"release_name"`
	Body           string `json:"body"`
	ReleaseURL     string `json:"release_url"`
	UnsubscribeURL string `json:"unsubscribe_url"`
}

// Release handles POST /v1/notifications/release.
func (h *Handler) Release(c *gin.Context) {
	var req releaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{msgInvalidRequestBody})
		return
	}
	if req.To == "" || req.Repo == "" || req.TagName == "" || req.UnsubscribeURL == "" {
		c.JSON(http.StatusBadRequest, errorResponse{msgMissingFields})
		return
	}

	if err := h.sender.SendReleaseNotification(c.Request.Context(), req.To, domain.ReleaseData{
		Repo:           req.Repo,
		TagName:        req.TagName,
		ReleaseName:    req.ReleaseName,
		Body:           req.Body,
		ReleaseURL:     req.ReleaseURL,
		UnsubscribeURL: req.UnsubscribeURL,
	}); err != nil {
		slog.Error("notifier: send release failed", "to", req.To, "repo", req.Repo, "tag", req.TagName, "error", err)
		c.JSON(http.StatusInternalServerError, errorResponse{msgSendFailed})
		return
	}

	slog.Info("notifier: release sent", "to", req.To, "repo", req.Repo, "tag", req.TagName)
	c.JSON(http.StatusAccepted, statusResponse{"sent"})
}
