package httpapi_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/posul/github-notifier/internal/subscription/app"
)

func TestSubscribe_Success(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "golang/go").Return(nil)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"golang/go"}`))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Subscription successful")
	svc.AssertExpectations(t)
}

func TestSubscribe_InvalidJSON(t *testing.T) {
	svc := &mockService{}

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{invalid json}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Subscribe")
}

func TestSubscribe_MissingFields(t *testing.T) {
	svc := &mockService{}

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"","repo":""}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Subscribe")
}

func TestSubscribe_InvalidEmail(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "bad-email", "golang/go").Return(app.ErrInvalidEmail)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"bad-email","repo":"golang/go"}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertExpectations(t)
}

func TestSubscribe_InvalidRepo(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "badrepo").Return(app.ErrInvalidRepo)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"badrepo"}`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertExpectations(t)
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "nobody/norepo").Return(app.ErrRepoNotFound)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"nobody/norepo"}`))

	assert.Equal(t, http.StatusNotFound, w.Code)
	svc.AssertExpectations(t)
}

func TestSubscribe_AlreadyExists(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "golang/go").Return(app.ErrAlreadyExists)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"golang/go"}`))

	assert.Equal(t, http.StatusConflict, w.Code)
	svc.AssertExpectations(t)
}

func TestSubscribe_RateLimit(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "golang/go").Return(app.ErrRateLimit)

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"golang/go"}`))

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	svc.AssertExpectations(t)
}

func TestSubscribe_InternalError(t *testing.T) {
	svc := &mockService{}
	svc.On("Subscribe", mock.Anything, "user@example.com", "golang/go").Return(errors.New("db error"))

	w := doRequest(newTestRouter(svc), http.MethodPost, "/api/subscribe",
		strings.NewReader(`{"email":"user@example.com","repo":"golang/go"}`))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	svc.AssertExpectations(t)
}
