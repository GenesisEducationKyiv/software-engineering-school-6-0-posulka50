//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// doGet sends a GET request to the test server and returns the response.
func doGet(tb testing.TB, srv *testServer, path string) *http.Response {
	tb.Helper()
	resp, err := http.Get(srv.URL + path)
	require.NoError(tb, err)
	return resp
}

// subscribeOK subscribes email to repo, asserts a 202 response, and waits
// for the saga to reach a completed state so follow-up DB reads (tokens,
// confirmations) observe the post-saga world rather than a transient one.
func subscribeOK(tb testing.TB, srv *testServer, email, repo string) {
	tb.Helper()
	body := fmt.Sprintf(`{"email":%q,"repo":%q}`, email, repo)
	resp := post(tb, srv, body)
	resp.Body.Close()
	require.Equal(tb, http.StatusAccepted, resp.StatusCode)
	waitForSagaState(tb, srv, email, "completed")
}

// waitForSagaState polls for the saga attached to the given email's
// subscription to reach the desired state. Used to synchronize against the
// async reply-event flow before asserting DB invariants.
func waitForSagaState(tb testing.TB, srv *testServer, email, state string) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		err := srv.pool.QueryRow(context.Background(),
			`SELECT s.state
			 FROM subscription_sagas s
			 JOIN subscriptions sub ON sub.id = s.subscription_id
			 WHERE sub.email = $1
			 ORDER BY s.started_at DESC
			 LIMIT 1`, email).Scan(&got)
		if err == nil && got == state {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tb.Fatalf("saga for %q did not reach state %q in time (last seen %q)", email, state, got)
}

// waitForSubscriptionCount polls until subscription rows for email match the
// expected count. Used to observe compensation-driven deletions.
func waitForSubscriptionCount(tb testing.TB, srv *testServer, email string, want int) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		got = subscriptionCount(tb, srv, email)
		if got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tb.Fatalf("subscription count for %q did not reach %d in time (last %d)", email, want, got)
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
