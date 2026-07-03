package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
	"github.com/posul/github-notifier/internal/platform/config"
	"github.com/posul/github-notifier/internal/platform/logctx"
	"github.com/posul/github-notifier/internal/platform/middleware"
	githubclient "github.com/posul/github-notifier/internal/release/adapter/github"
	releasepostgres "github.com/posul/github-notifier/internal/release/adapter/postgres"
	releaseapp "github.com/posul/github-notifier/internal/release/app"
	subscriptionhttp "github.com/posul/github-notifier/internal/subscription/adapter/http"
	subscriptionpostgres "github.com/posul/github-notifier/internal/subscription/adapter/postgres"
	subscriptionrabbitmq "github.com/posul/github-notifier/internal/subscription/adapter/rabbitmq"
	subscriptionapp "github.com/posul/github-notifier/internal/subscription/app"
	"github.com/posul/github-notifier/internal/subscription/saga"
)

func main() {
	slog.SetDefault(slog.New(logctx.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil {
		slog.Warn(".env file not found, using environment variables", "error", err)
	}

	cfg := config.Load()

	dbPool, err := initDB(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database init failed: %w", err)
	}
	defer dbPool.Close()

	if err = runMigrations(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrations failed: %w", err)
	}
	slog.Info("migrations applied")

	redisClient := initRedis(cfg.RedisURL)

	publisher, err := dialPublisherWithRetry(cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			slog.Warn("rabbitmq publisher close error", "error", err)
		}
	}()

	svc := setupServices(dbPool, redisClient, publisher, cfg)

	repliesConsumer, err := subscriptionrabbitmq.NewRepliesConsumer(cfg.BrokerURL, svc.orchestrator)
	if err != nil {
		return fmt.Errorf("connect saga replies consumer: %w", err)
	}
	defer func() {
		if err := repliesConsumer.Close(); err != nil {
			slog.Warn("saga replies consumer close error", "error", err)
		}
	}()

	router := newRouter(cfg, subscriptionhttp.New(svc.subscribe, svc.confirm, svc.unsubscribe, svc.getSubs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.scanner.Start(ctx)
	go svc.sweeper.Run(ctx)

	repliesDone := make(chan struct{})
	go func() {
		defer close(repliesDone)
		if err := repliesConsumer.Run(ctx); err != nil {
			slog.Error("saga replies consumer exited with error", "error", err)
		}
	}()

	srv := newServer(cfg.Port, router)

	go func() {
		slog.Info("server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// A silent replies consumer with a live HTTP server is worse than a
	// crash: /health stays green while new subscriptions accumulate as
	// pending sagas that eventually time out. Treat unexpected exit of the
	// consumer goroutine as fatal so the orchestrator restarts the process.
	select {
	case <-quit:
		slog.Info("shutting down gracefully")
	case <-repliesDone:
		slog.Error("saga replies consumer exited unexpectedly, shutting down")
	}
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server forced to shutdown", "error", err)
	}

	select {
	case <-repliesDone:
	case <-shutdownCtx.Done():
		slog.Warn("saga replies consumer did not stop in time")
	}

	slog.Info("server stopped")
	return nil
}

func initDB(databaseURL string) (*pgxpool.Pool, error) {
	dbCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	dbCfg.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(context.Background(), dbCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	slog.Info("database connected")
	return pool, nil
}

func initRedis(redisURL string) *redis.Client {
	if redisURL == "" {
		return nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		slog.Warn("invalid REDIS_URL", "error", err)
		return nil
	}

	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second

	client := redis.NewClient(opts)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		slog.Warn("redis not available, caching disabled", "error", err)
		return nil
	}

	slog.Info("redis connected")
	return client
}

func newRouter(cfg *config.Config, h *subscriptionhttp.Handler) *gin.Engine {
	gin.SetMode(cfg.GinMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())
	router.Use(middleware.Logger())
	router.Use(middleware.Prometheus())

	api := router.Group("/api")
	{
		api.POST("/subscribe", h.Subscribe)
		api.GET("/confirm/:token", h.Confirm)
		api.GET("/unsubscribe/:token", h.Unsubscribe)
		api.GET("/subscriptions", h.GetSubscriptions)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	router.GET("/", func(c *gin.Context) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	return router
}

func newServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

type appServices struct {
	subscribe    *subscriptionapp.SubscribeUseCase
	confirm      *subscriptionapp.ConfirmUseCase
	unsubscribe  *subscriptionapp.UnsubscribeUseCase
	getSubs      *subscriptionapp.GetSubscriptionsUseCase
	scanner      *releaseapp.Scanner
	orchestrator *saga.Orchestrator
	sweeper      *saga.TimeoutSweeper
}

func setupServices(dbPool *pgxpool.Pool, redisClient *redis.Client, publisher *rabbitmq.Publisher, cfg *config.Config) *appServices {
	repoRepo := releasepostgres.NewRepoRepository(dbPool)
	subRepo := subscriptionpostgres.NewSubscriptionRepository(dbPool)
	sagaRepo := subscriptionpostgres.NewSagaRepository(dbPool)
	repoCheckerClient := githubclient.NewRepoCheckerClient(cfg.GitHubToken)
	releaseFetcherClient := githubclient.NewReleaseFetcherClient(cfg.GitHubToken)

	var checker githubclient.RepoChecker = repoCheckerClient
	var fetcher githubclient.ReleaseFetcher = releaseFetcherClient
	if redisClient != nil {
		checker = githubclient.NewCachedRepoChecker(repoCheckerClient, redisClient)
		fetcher = githubclient.NewCachedReleaseFetcher(releaseFetcherClient, redisClient)
	}

	orchestrator := saga.New(publisher, sagaRepo, subRepo)

	subscribeUC := subscriptionapp.NewSubscribeUseCase(repoRepo, subRepo, checker, orchestrator, cfg.BaseURL)
	confirmUC := subscriptionapp.NewConfirmUseCase(subRepo)
	unsubscribeUC := subscriptionapp.NewUnsubscribeUseCase(subRepo)
	getSubsUC := subscriptionapp.NewGetSubscriptionsUseCase(subRepo)

	scanInterval, err := time.ParseDuration(cfg.ScanInterval)
	if err != nil {
		slog.Warn("invalid SCAN_INTERVAL, defaulting to 1h", "value", cfg.ScanInterval)
		scanInterval = time.Hour
	}
	scanner := releaseapp.NewScanner(repoRepo, subRepo, fetcher, publisher, cfg.BaseURL, scanInterval)

	sagaTimeout, err := time.ParseDuration(cfg.SagaTimeout)
	if err != nil {
		slog.Warn("invalid SAGA_TIMEOUT, defaulting to 5m", "value", cfg.SagaTimeout)
		sagaTimeout = 5 * time.Minute
	}
	sweepInterval, err := time.ParseDuration(cfg.SagaSweepInterval)
	if err != nil {
		slog.Warn("invalid SAGA_SWEEP_INTERVAL, defaulting to 30s", "value", cfg.SagaSweepInterval)
		sweepInterval = 30 * time.Second
	}
	sweeper := saga.NewTimeoutSweeper(sagaRepo, orchestrator, sagaTimeout, sweepInterval)

	return &appServices{
		subscribe:    subscribeUC,
		confirm:      confirmUC,
		unsubscribe:  unsubscribeUC,
		getSubs:      getSubsUC,
		scanner:      scanner,
		orchestrator: orchestrator,
		sweeper:      sweeper,
	}
}

const (
	brokerDialAttempts = 15
	brokerDialDelay    = 2 * time.Second
)

// dialPublisherWithRetry tolerates the broker still booting alongside the
// server in docker-compose: it retries the initial dial before failing.
func dialPublisherWithRetry(brokerURL string) (*rabbitmq.Publisher, error) {
	var lastErr error
	for attempt := 1; attempt <= brokerDialAttempts; attempt++ {
		p, err := rabbitmq.NewPublisher(brokerURL)
		if err == nil {
			slog.Info("rabbitmq publisher connected", "attempt", attempt)
			return p, nil
		}
		lastErr = err
		slog.Warn("rabbitmq publisher dial failed, retrying", "attempt", attempt, "error", err)
		time.Sleep(brokerDialDelay)
	}
	return nil, fmt.Errorf("after %d attempts: %w", brokerDialAttempts, lastErr)
}

func runMigrations(databaseURL string) error {
	d, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil {
			slog.Warn("migrate close source error", "error", sourceErr)
		}
		if dbErr != nil {
			slog.Warn("migrate close db error", "error", dbErr)
		}
	}()

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
