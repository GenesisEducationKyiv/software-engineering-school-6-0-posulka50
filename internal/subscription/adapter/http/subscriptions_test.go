package httpapi_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/posul/github-notifier/internal/subscription/app"
	"github.com/posul/github-notifier/internal/subscription/domain"
)

func TestGetSubscriptions_Success(t *testing.T) {
	svc := &mockService{}
	subs := []*domain.Subscription{
		{Email: "user@example.com", Repo: "golang/go", Confirmed: true},
		{Email: "user@example.com", Repo: "gin-gonic/gin", Confirmed: true},
	}
	svc.On("GetSubscriptions", mock.Anything, "user@example.com").Return(subs, nil)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/subscriptions?email=user@example.com", nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "golang/go")
	assert.Contains(t, w.Body.String(), "gin-gonic/gin")
	svc.AssertExpectations(t)
}

func TestGetSubscriptions_EmptyList(t *testing.T) {
	svc := &mockService{}
	svc.On("GetSubscriptions", mock.Anything, "user@example.com").Return([]*domain.Subscription{}, nil)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/subscriptions?email=user@example.com", nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
	svc.AssertExpectations(t)
}

func TestGetSubscriptions_MissingEmail(t *testing.T) {
	svc := &mockService{}

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/subscriptions", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "GetSubscriptions")
}

func TestGetSubscriptions_InvalidEmail(t *testing.T) {
	svc := &mockService{}
	svc.On("GetSubscriptions", mock.Anything, "bad-email").Return(nil, app.ErrInvalidEmail)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/subscriptions?email=bad-email", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertExpectations(t)
}

func TestGetSubscriptions_InternalError(t *testing.T) {
	svc := &mockService{}
	svc.On("GetSubscriptions", mock.Anything, "user@example.com").Return(nil, errors.New("db error"))

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/subscriptions?email=user@example.com", nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	svc.AssertExpectations(t)
}
