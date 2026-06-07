package httpapi_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/posul/github-notifier/internal/subscription/app"
)

func TestUnsubscribe_Success(t *testing.T) {
	svc := &mockService{}
	svc.On("Unsubscribe", mock.Anything, validToken).Return(nil)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/unsubscribe/"+validToken, nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Unsubscribed")
	svc.AssertExpectations(t)
}

func TestUnsubscribe_InvalidTokenFormat(t *testing.T) {
	svc := &mockService{}

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/unsubscribe/not-a-uuid", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Unsubscribe")
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc := &mockService{}
	svc.On("Unsubscribe", mock.Anything, validToken).Return(app.ErrNotFound)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/unsubscribe/"+validToken, nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	svc.AssertExpectations(t)
}

func TestUnsubscribe_InternalError(t *testing.T) {
	svc := &mockService{}
	svc.On("Unsubscribe", mock.Anything, validToken).Return(errors.New("db error"))

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/unsubscribe/"+validToken, nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	svc.AssertExpectations(t)
}
