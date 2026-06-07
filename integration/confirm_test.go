//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfirm_Success(t *testing.T) {
	srv := newTestServer(t)
	subscribeOK(t, srv, "user@example.com", "golang/go")

	confirmToken, _ := getTokens(t, srv, "user@example.com")
	require.False(t, isConfirmed(t, srv, "user@example.com"), "should be unconfirmed before confirm")

	resp := doGet(t, srv, "/api/confirm/"+confirmToken)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Contains(t, body["message"], "confirmed")

	assert.True(t, isConfirmed(t, srv, "user@example.com"), "should be confirmed after confirm")
}

func TestConfirm_InvalidToken(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/confirm/not-a-uuid")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/confirm/"+uuid.NewString())
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
