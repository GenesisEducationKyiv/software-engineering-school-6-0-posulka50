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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
	notifierdomain "github.com/posul/github-notifier/internal/notifier/domain"
	releasepostgres "github.com/posul/github-notifier/internal/release/adapter/postgres"
	subscriptionhttp "github.com/posul/github-notifier/internal/subscription/adapter/http"
	subscriptionpostgres "github.com/posul/github-notifier/internal/subscription/adapter/postgres"
	subscriptionrabbitmq "github.com/posul/github-notifier/internal/subscription/adapter/rabbitmq"
	subscriptionapp "github.com/posul/github-notifier/internal/subscription/app"
	"github.com/posul/github-notifier/internal/subscription/saga"
)

var (
	sharedPool *pgxpool.Pool
	sharedAMQP string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("start postgres: %v", err)
		return 1
	}
	defer pg.Terminate(ctx)

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
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

	rmq, err := tcrabbitmq.Run(ctx, "rabbitmq:3.13-management-alpine")
	if err != nil {
		log.Printf("start rabbitmq: %v", err)
		return 1
	}
	defer rmq.Terminate(ctx)

	amqpURL, err := rmq.AmqpURL(ctx)
	if err != nil {
		log.Printf("get rabbitmq url: %v", err)
		return 1
	}
	sharedAMQP = amqpURL

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

// stubEmail satisfies rabbitmq.Sender. It is driven by the broker consumer in
// each test, so callers must use waitForConfirmations to observe deliveries
// rather than reading the slice directly (Publish -> consume is async).
type stubEmail struct {
	mu            sync.Mutex
	confirmations []string
	err           error
}

func (s *stubEmail) SendConfirmation(_ context.Context, to string, _ notifierdomain.ConfirmData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.confirmations = append(s.confirmations, to)
	return nil
}

func (s *stubEmail) SendReleaseNotification(_ context.Context, _ string, _ notifierdomain.ReleaseData) error {
	return nil
}

func (s *stubEmail) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *stubEmail) Confirmations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.confirmations...)
}

func (s *stubEmail) waitForConfirmations(tb testing.TB, n int) []string {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := s.Confirmations()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			tb.Fatalf("timeout waiting for %d confirmations, got %d", n, len(got))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type testServer struct {
	*httptest.Server
	gh   *stubGitHub
	em   *stubEmail
	pool *pgxpool.Pool
}

// newTestServer wires the full Subscribe saga end-to-end against the shared
// RabbitMQ + Postgres containers:
//
//   - App side: SubscribeUseCase -> Orchestrator -> Publisher -> command queue.
//   - Notifier side (in-test): rabbitmq.Consumer -> stubEmail.SendConfirmation
//     -> Publisher -> reply event queue.
//   - App side again: subscription/adapter/rabbitmq.RepliesConsumer -> Orchestrator.HandleSent / HandleFailed.
//
// Tables and all three saga queues are reset before each test for isolation.
// Tests observe outcomes via stubEmail (for sent confirmations) and the DB
// (for compensation effects on subscriptions / saga state).
func newTestServer(tb testing.TB) *testServer {
	tb.Helper()

	if _, err := sharedPool.Exec(context.Background(), "TRUNCATE repositories, subscription_sagas CASCADE"); err != nil {
		tb.Fatalf("truncate: %v", err)
	}
	purgeQueues(tb)

	gh := &stubGitHub{}
	em := &stubEmail{}

	publisher, err := rabbitmq.NewPublisher(sharedAMQP)
	if err != nil {
		tb.Fatalf("create publisher: %v", err)
	}
	tb.Cleanup(func() {
		if err := publisher.Close(); err != nil {
			tb.Logf("publisher close: %v", err)
		}
	})

	// Notifier side: consumes both legacy deliveries and saga commands,
	// publishes reply events via the same publisher. Marker is nil because
	// the integration test does not exercise the sync-retry / gRPC dedupe
	// path — that is covered by the grpcsrv unit tests.
	consumer, err := rabbitmq.NewConsumer(sharedAMQP, em, publisher, nil)
	if err != nil {
		tb.Fatalf("create consumer: %v", err)
	}
	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(consumerCtx); err != nil {
			tb.Logf("consumer run: %v", err)
		}
	}()
	tb.Cleanup(func() {
		cancelConsumer()
		<-consumerDone
		if err := consumer.Close(); err != nil {
			tb.Logf("consumer close: %v", err)
		}
	})

	gin.SetMode(gin.TestMode)

	repoRepo := releasepostgres.NewRepoRepository(sharedPool)
	subRepo := subscriptionpostgres.NewSubscriptionRepository(sharedPool)
	sagaRepo := subscriptionpostgres.NewSagaRepository(sharedPool)

	// nil syncRetrier is safe here: the integration test does not spin up a
	// TimeoutSweeper, so AttemptSyncRetry is never called. The base URL is
	// only used to build a confirmation link on the sync-retry path, so a
	// placeholder is fine.
	orchestrator := saga.New(publisher, sagaRepo, subRepo, nil, "http://integration-test")

	// App side: consumes saga reply events, drives orchestrator transitions.
	replies, err := subscriptionrabbitmq.NewRepliesConsumer(sharedAMQP, orchestrator)
	if err != nil {
		tb.Fatalf("create replies consumer: %v", err)
	}
	repliesCtx, cancelReplies := context.WithCancel(context.Background())
	repliesDone := make(chan struct{})
	go func() {
		defer close(repliesDone)
		if err := replies.Run(repliesCtx); err != nil {
			tb.Logf("replies consumer run: %v", err)
		}
	}()
	tb.Cleanup(func() {
		cancelReplies()
		<-repliesDone
		if err := replies.Close(); err != nil {
			tb.Logf("replies consumer close: %v", err)
		}
	})

	subscriber := subscriptionapp.NewSubscribeUseCase(repoRepo, subRepo, gh, orchestrator, "http://test")
	confirmer := subscriptionapp.NewConfirmUseCase(subRepo)
	unsubscriber := subscriptionapp.NewUnsubscribeUseCase(subRepo)
	lister := subscriptionapp.NewGetSubscriptionsUseCase(subRepo)

	r := gin.New()
	h := subscriptionhttp.New(subscriber, confirmer, unsubscriber, lister)

	api := r.Group("/api")
	api.POST("/subscribe", h.Subscribe)
	api.GET("/confirm/:token", h.Confirm)
	api.GET("/unsubscribe/:token", h.Unsubscribe)
	api.GET("/subscriptions", h.GetSubscriptions)

	srv := httptest.NewServer(r)
	tb.Cleanup(srv.Close)

	return &testServer{Server: srv, gh: gh, em: em, pool: sharedPool}
}

// purgeQueues drops any messages prior tests left in the shared queues so the
// new consumers cannot pick up stale work. All three saga-related queues are
// purged to avoid cross-test pollution.
func purgeQueues(tb testing.TB) {
	tb.Helper()
	conn, err := amqp.Dial(sharedAMQP)
	if err != nil {
		tb.Fatalf("dial rabbitmq for purge: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		tb.Fatalf("open channel for purge: %v", err)
	}
	defer ch.Close()
	if err := rabbitmq.Declare(ch); err != nil {
		tb.Fatalf("declare topology for purge: %v", err)
	}
	for _, q := range []string{rabbitmq.QueueDeliveries, rabbitmq.QueueSagaCommands, rabbitmq.QueueSagaEvents} {
		if _, err := ch.QueuePurge(q, false); err != nil {
			tb.Fatalf("purge queue %q: %v", q, err)
		}
	}
}
