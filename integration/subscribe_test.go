//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	githubclient "github.com/posul/github-notifier/internal/release/adapter/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func post(tb testing.TB, srv *testServer, body string) *http.Response {
	tb.Helper()
	resp, err := http.Post(
		srv.URL+"/api/subscribe",
		"application/json",
		bytes.NewBufferString(body),
	)
	require.NoError(tb, err)
	return resp
}

func jsonBody(tb testing.TB, resp *http.Response) map[string]string {
	tb.Helper()
	var out map[string]string
	require.NoError(tb, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestSubscribe_Success(t *testing.T) {
	srv := newTestServer(t)

	resp := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Contains(t, body["message"], "Subscription accepted")

	got := srv.em.waitForConfirmations(t, 1)
	assert.Equal(t, []string{"user@example.com"}, got)
	waitForSagaState(t, srv, "user@example.com", "completed")
}

func TestSubscribe_InvalidEmail(t *testing.T) {
	srv := newTestServer(t)

	resp := post(t, srv, `{"email":"not-an-email","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Empty(t, srv.em.Confirmations())
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	cases := []struct {
		name string
		repo string
	}{
		{"no slash", "justarepo"},
		{"empty", ""},
		{"too many slashes", "too/many/slashes"},
		{"leading slash", "/noleft"},
		{"trailing slash", "noright/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			body, _ := json.Marshal(map[string]string{"email": "user@example.com", "repo": tc.repo})

			resp, err := http.Post(srv.URL+"/api/subscribe", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Empty(t, srv.em.Confirmations())
		})
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	srv := newTestServer(t)
	srv.gh.err = githubclient.ErrNotFound

	resp := post(t, srv, `{"email":"user@example.com","repo":"nobody/norepo"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Empty(t, srv.em.Confirmations())
}

func TestSubscribe_RateLimit(t *testing.T) {
	srv := newTestServer(t)
	srv.gh.err = githubclient.ErrRateLimit

	resp := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Empty(t, srv.em.Confirmations())
}

func TestSubscribe_Duplicate(t *testing.T) {
	srv := newTestServer(t)

	resp1 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	resp1.Body.Close()
	require.Equal(t, http.StatusAccepted, resp1.StatusCode)
	srv.em.waitForConfirmations(t, 1)
	waitForSagaState(t, srv, "user@example.com", "completed")

	resp2 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
	assert.Len(t, srv.em.Confirmations(), 1)
}

// TestSubscribe_SenderFails_SagaCompensates verifies the saga compensation
// path: when the notifier reports a delivery failure, the orchestrator
// removes the orphaned pending subscription so a retry succeeds rather than
// returning 409 against a row the user cannot ever confirm.
func TestSubscribe_SenderFails_SagaCompensates(t *testing.T) {
	srv := newTestServer(t)
	srv.em.setErr(errors.New("resend unavailable"))

	resp := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Compensation runs asynchronously after the reply event arrives.
	waitForSubscriptionCount(t, srv, "user@example.com", 0)

	// Retry with a healthy sender succeeds.
	srv.em.setErr(nil)
	resp2 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp2.StatusCode)
	srv.em.waitForConfirmations(t, 1)
	waitForSagaState(t, srv, "user@example.com", "completed")
}

func TestSubscribe_MissingFields(t *testing.T) {
	srv := newTestServer(t)

	resp := post(t, srv, `{"email":"","repo":""}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSubscribe_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)

	resp := post(t, srv, `{invalid json}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
