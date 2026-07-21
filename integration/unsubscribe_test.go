//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestUnsubscribe_Success(t *testing.T) {
	srv := newTestServer(t)
	subscribeOK(t, srv, "user@example.com", "golang/go")

	_, unsubToken := getTokens(t, srv, "user@example.com")
	assert.Equal(t, 1, subscriptionCount(t, srv, "user@example.com"))

	resp := doGet(t, srv, "/api/unsubscribe/"+unsubToken)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Contains(t, body["message"], "Unsubscribed")

	assert.Equal(t, 0, subscriptionCount(t, srv, "user@example.com"))
}

func TestUnsubscribe_InvalidToken(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/unsubscribe/not-a-uuid")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/unsubscribe/"+uuid.NewString())
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
