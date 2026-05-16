//go:build integration

package integration_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/posul/github-notifier/internal/email"
	"github.com/posul/github-notifier/internal/handler"
	"github.com/posul/github-notifier/internal/repository"
	"github.com/posul/github-notifier/internal/service"
)

// stubGitHub satisfies the unexported service.repoChecker interface via duck typing.
type stubGitHub struct{ err error }

func (s *stubGitHub) CheckRepo(_ context.Context, _, _ string) error { return s.err }

// stubEmail implements email.Notifier and records outgoing addresses without sending.
type stubEmail struct {
	confirmations []string
	err           error
}

func (s *stubEmail) SendConfirmation(_ context.Context, to string, _ email.ConfirmData) error {
	if s.err != nil {
		return s.err
	}
	s.confirmations = append(s.confirmations, to)
	return nil
}

func (s *stubEmail) SendReleaseNotification(_ context.Context, _ string, _ email.ReleaseData) error {
	return nil
}

// testServer wraps httptest.Server with the stubs used in each test.
type testServer struct {
	*httptest.Server
	gh *stubGitHub
	em *stubEmail
}

// startPostgres launches a postgres:16-alpine container and returns the connection DSN.
// The container is terminated automatically when tb finishes.
func startPostgres(tb testing.TB) string {
	tb.Helper()
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithLogger(testcontainers.TestLogger(tb)),
	)
	testcontainers.CleanupContainer(tb, c)
	if err != nil {
		tb.Fatalf("startPostgres: %v", err)
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		tb.Fatalf("get connection string: %v", err)
	}
	return dsn
}

// applyMigrations runs all SQL migrations from cmd/server/migrations against dsn.
func applyMigrations(tb testing.TB, dsn string) {
	tb.Helper()

	_, callerFile, _, _ := runtime.Caller(0)
	absDir, err := filepath.Abs(filepath.Join(filepath.Dir(callerFile), "..", "cmd", "server", "migrations"))
	if err != nil {
		tb.Fatalf("resolve migrations dir: %v", err)
	}

	// Build a file:// URL that works on both Unix and Windows.
	slash := filepath.ToSlash(absDir)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash // Windows: "C:/..." → "/C:/..."
	}
	source := "file://" + slash

	m, err := migrate.New(source, dsn)
	if err != nil {
		tb.Fatalf("create migrator: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			tb.Logf("migrate close source: %v", srcErr)
		}
		if dbErr != nil {
			tb.Logf("migrate close db: %v", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		tb.Fatalf("apply migrations: %v", err)
	}
}

// newTestServer creates an httptest.Server backed by a real PostgreSQL database.
// GitHub validation and email delivery are replaced with in-memory stubs.
// The server and database container are cleaned up when tb finishes.
func newTestServer(tb testing.TB) *testServer {
	tb.Helper()

	dsn := startPostgres(tb)
	applyMigrations(tb, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		tb.Fatalf("create pool: %v", err)
	}
	tb.Cleanup(pool.Close)

	gh := &stubGitHub{}
	em := &stubEmail{}

	repoRepo := repository.NewPostgresRepoRepository(pool)
	subRepo := repository.NewPostgresRepository(pool)
	svc := service.NewSubscriptionService(repoRepo, subRepo, gh, em, "http://test")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.New(svc)

	api := r.Group("/api")
	api.POST("/subscribe", h.Subscribe)
	api.GET("/confirm/:token", h.Confirm)
	api.GET("/unsubscribe/:token", h.Unsubscribe)
	api.GET("/subscriptions", h.GetSubscriptions)

	srv := httptest.NewServer(r)
	tb.Cleanup(srv.Close)

	return &testServer{Server: srv, gh: gh, em: em}
}
