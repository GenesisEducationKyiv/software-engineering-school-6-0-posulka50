//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSubscriptions_Empty(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/subscriptions?email=nobody@example.com")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var subs []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&subs))
	assert.Empty(t, subs)
}

func TestGetSubscriptions_UnconfirmedNotReturned(t *testing.T) {
	srv := newTestServer(t)
	subscribeOK(t, srv, "user@example.com", "golang/go")
	// subscription exists but is not confirmed

	resp := doGet(t, srv, "/api/subscriptions?email=user@example.com")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var subs []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&subs))
	assert.Empty(t, subs)
}

func TestGetSubscriptions_ReturnsConfirmed(t *testing.T) {
	srv := newTestServer(t)
	subscribeOK(t, srv, "user@example.com", "golang/go")

	confirmToken, _ := getTokens(t, srv, "user@example.com")
	resp := doGet(t, srv, "/api/confirm/"+confirmToken)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp2 := doGet(t, srv, "/api/subscriptions?email=user@example.com")
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var subs []map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&subs))
	require.Len(t, subs, 1)
	assert.Equal(t, "golang/go", subs[0]["repo"])
	assert.Equal(t, true, subs[0]["confirmed"])
}

func TestGetSubscriptions_MultipleRepos(t *testing.T) {
	srv := newTestServer(t)
	subscribeOK(t, srv, "user@example.com", "golang/go")
	subscribeOK(t, srv, "user@example.com", "gin-gonic/gin")

	// confirm both
	for _, repo := range []string{"golang/go", "gin-gonic/gin"} {
		var confirmToken string
		err := srv.pool.QueryRow(t.Context(),
			"SELECT s.confirm_token FROM subscriptions s JOIN repositories r ON r.id = s.repo_id WHERE s.email = $1 AND r.full_name = $2",
			"user@example.com", repo,
		).Scan(&confirmToken)
		require.NoError(t, err)

		r := doGet(t, srv, "/api/confirm/"+confirmToken)
		r.Body.Close()
		require.Equal(t, http.StatusOK, r.StatusCode)
	}

	resp := doGet(t, srv, "/api/subscriptions?email=user@example.com")
	defer resp.Body.Close()

	var subs []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&subs))
	assert.Len(t, subs, 2)
}

func TestGetSubscriptions_InvalidEmail(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/subscriptions?email=notanemail")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetSubscriptions_MissingEmailParam(t *testing.T) {
	srv := newTestServer(t)

	resp := doGet(t, srv, "/api/subscriptions")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
