//go:build integration

package integration_test

import (
	"context"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/posul/github-notifier/internal/email"
	"github.com/posul/github-notifier/internal/handler"
	"github.com/posul/github-notifier/internal/repository"
	"github.com/posul/github-notifier/internal/service"
)

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	ctx := context.Background()

	c, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("startPostgres: %v", err)
		return 1
	}
	defer c.Terminate(ctx)

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}

	runMigrations(dsn)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("create pool: %v", err)
		return 1
	}
	defer pool.Close()
	sharedPool = pool

	return m.Run()
}

func runMigrations(dsn string) {
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("runtime.Caller failed: cannot determine source file path")
	}
	absDir, err := filepath.Abs(filepath.Join(filepath.Dir(callerFile), "..", "cmd", "server", "migrations"))
	if err != nil {
		log.Fatalf("resolve migrations dir: %v", err)
	}

	d, err := iofs.New(os.DirFS(absDir), ".")
	if err != nil {
		log.Fatalf("create iofs source: %v", err)
	}

	mi, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		log.Fatalf("create migrator: %v", err)
	}
	defer mi.Close()

	if err := mi.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("apply migrations: %v", err)
	}
}

type stubGitHub struct{ err error }

func (s *stubGitHub) CheckRepo(_ context.Context, _, _ string) error { return s.err }

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

type testServer struct {
	*httptest.Server
	gh   *stubGitHub
	em   *stubEmail
	pool *pgxpool.Pool
}

// newTestServer wires up an httptest.Server backed by the shared PostgreSQL instance.
// Tables are truncated before each test to ensure isolation.
func newTestServer(tb testing.TB) *testServer {
	tb.Helper()

	if _, err := sharedPool.Exec(context.Background(), "TRUNCATE repositories CASCADE"); err != nil {
		tb.Fatalf("truncate: %v", err)
	}

	gh := &stubGitHub{}
	em := &stubEmail{}

	repoRepo := repository.NewPostgresRepoRepository(sharedPool)
	subRepo := repository.NewPostgresRepository(sharedPool)

	subscriber := service.NewSubscribeUseCase(repoRepo, subRepo, gh, em, "http://test")
	confirmer := service.NewConfirmUseCase(subRepo)
	unsubscriber := service.NewUnsubscribeUseCase(subRepo)
	lister := service.NewGetSubscriptionsUseCase(subRepo)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.New(subscriber, confirmer, unsubscriber, lister)

	api := r.Group("/api")
	api.POST("/subscribe", h.Subscribe)
	api.GET("/confirm/:token", h.Confirm)
	api.GET("/unsubscribe/:token", h.Unsubscribe)
	api.GET("/subscriptions", h.GetSubscriptions)

	srv := httptest.NewServer(r)
	tb.Cleanup(srv.Close)

	return &testServer{Server: srv, gh: gh, em: em, pool: sharedPool}
}
