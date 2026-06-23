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

const validToken = "550e8400-e29b-41d4-a716-446655440000"

func TestConfirm_Success(t *testing.T) {
	svc := &mockService{}
	svc.On("Confirm", mock.Anything, validToken).Return(nil)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/confirm/"+validToken, nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "confirmed")
	svc.AssertExpectations(t)
}

func TestConfirm_InvalidTokenFormat(t *testing.T) {
	svc := &mockService{}

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/confirm/not-a-uuid", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	svc.AssertNotCalled(t, "Confirm")
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc := &mockService{}
	svc.On("Confirm", mock.Anything, validToken).Return(app.ErrNotFound)

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/confirm/"+validToken, nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	svc.AssertExpectations(t)
}

func TestConfirm_InternalError(t *testing.T) {
	svc := &mockService{}
	svc.On("Confirm", mock.Anything, validToken).Return(errors.New("db error"))

	w := doRequest(newTestRouter(svc), http.MethodGet, "/api/confirm/"+validToken, nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	svc.AssertExpectations(t)
}
