//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	githubclient "github.com/posul/github-notifier/internal/github"
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

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Contains(t, body["message"], "Subscription successful")

	assert.Equal(t, []string{"user@example.com"}, srv.em.confirmations)
}

func TestSubscribe_InvalidEmail(t *testing.T) {
	srv := newTestServer(t)

	resp := post(t, srv, `{"email":"not-an-email","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Empty(t, srv.em.confirmations)
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
			assert.Empty(t, srv.em.confirmations)
		})
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	srv := newTestServer(t)
	srv.gh.err = githubclient.ErrNotFound

	resp := post(t, srv, `{"email":"user@example.com","repo":"nobody/norepo"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Empty(t, srv.em.confirmations)
}

func TestSubscribe_RateLimit(t *testing.T) {
	srv := newTestServer(t)
	srv.gh.err = githubclient.ErrRateLimit

	resp := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Empty(t, srv.em.confirmations)
}

func TestSubscribe_Duplicate(t *testing.T) {
	srv := newTestServer(t)

	resp1 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	resp1.Body.Close()
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	resp2 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
	assert.Len(t, srv.em.confirmations, 1)
}

func TestSubscribe_EmailSenderFails_RollsBack(t *testing.T) {
	srv := newTestServer(t)
	srv.em.err = errors.New("resend unavailable")

	resp := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Subscription must have been deleted (rollback): a second attempt should succeed
	// once we restore the email sender.
	srv.em.err = nil
	resp2 := post(t, srv, `{"email":"user@example.com","repo":"golang/go"}`)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
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
