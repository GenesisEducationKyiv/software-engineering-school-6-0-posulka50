//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// doGet sends a GET request to the test server and returns the response.
func doGet(tb testing.TB, srv *testServer, path string) *http.Response {
	tb.Helper()
	resp, err := http.Get(srv.URL + path)
	require.NoError(tb, err)
	return resp
}

// subscribeOK subscribes email to repo and asserts a 200 response.
func subscribeOK(tb testing.TB, srv *testServer, email, repo string) {
	tb.Helper()
	body := fmt.Sprintf(`{"email":%q,"repo":%q}`, email, repo)
	resp := post(tb, srv, body)
	resp.Body.Close()
	require.Equal(tb, http.StatusOK, resp.StatusCode)
}

// getTokens returns the confirm and unsubscribe tokens for the given email from the DB.
func getTokens(tb testing.TB, srv *testServer, email string) (confirmToken, unsubToken string) {
	tb.Helper()
	err := srv.pool.QueryRow(
		context.Background(),
		"SELECT confirm_token, unsubscribe_token FROM subscriptions WHERE email = $1",
		email,
	).Scan(&confirmToken, &unsubToken)
	require.NoError(tb, err)
	return
}

// isConfirmed reports whether the subscription for email is confirmed in the DB.
func isConfirmed(tb testing.TB, srv *testServer, email string) bool {
	tb.Helper()
	var confirmed bool
	err := srv.pool.QueryRow(
		context.Background(),
		"SELECT confirmed FROM subscriptions WHERE email = $1",
		email,
	).Scan(&confirmed)
	require.NoError(tb, err)
	return confirmed
}

// subscriptionCount returns the number of subscriptions for email in the DB.
func subscriptionCount(tb testing.TB, srv *testServer, email string) int {
	tb.Helper()
	var count int
	err := srv.pool.QueryRow(
		context.Background(),
		"SELECT COUNT(*) FROM subscriptions WHERE email = $1",
		email,
	).Scan(&count)
	require.NoError(tb, err)
	return count
}
