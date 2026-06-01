package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	"github.com/posul/github-notifier/internal/config"
	"github.com/posul/github-notifier/internal/email"
	githubclient "github.com/posul/github-notifier/internal/github"
	"github.com/posul/github-notifier/internal/handler"
	"github.com/posul/github-notifier/internal/middleware"
	"github.com/posul/github-notifier/internal/repository"
	"github.com/posul/github-notifier/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil {
		log.Printf("warn: .env file not found or unreadable (%v), using environment variables", err)
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
	log.Println("migrations applied")

	redisClient := initRedis(cfg.RedisURL)

	subscribeUC, confirmUC, unsubscribeUC, getSubsUC, scanner := setupServices(dbPool, redisClient, cfg)

	router := newRouter(cfg, handler.New(subscribeUC, confirmUC, unsubscribeUC, getSubsUC))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scanner.Start(ctx)

	srv := newServer(cfg.Port, router)

	go func() {
		log.Printf("server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down gracefully...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced to shutdown: %v", err)
	}
	log.Println("server stopped")
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

	log.Println("database connected")
	return pool, nil
}

func initRedis(redisURL string) *redis.Client {
	if redisURL == "" {
		return nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("warn: invalid REDIS_URL: %v", err)
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
		log.Printf("warn: redis not available (%v), caching disabled", err)
		return nil
	}

	log.Println("redis connected")
	return client
}

func newRouter(cfg *config.Config, h *handler.Handler) *gin.Engine {
	gin.SetMode(cfg.GinMode)

	router := gin.New()
	router.Use(gin.Recovery())
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

func setupServices(dbPool *pgxpool.Pool, redisClient *redis.Client, cfg *config.Config) (
	*service.SubscribeUseCase,
	*service.ConfirmUseCase,
	*service.UnsubscribeUseCase,
	*service.GetSubscriptionsUseCase,
	*service.Scanner,
) {
	repoRepo := repository.NewPostgresRepoRepository(dbPool)
	subRepo := repository.NewPostgresRepository(dbPool)
	repoCheckerClient := githubclient.NewRepoCheckerClient(cfg.GitHubToken)
	releaseFetcherClient := githubclient.NewReleaseFetcherClient(cfg.GitHubToken)
	emailSender := email.NewSender(cfg.ResendAPIKey, cfg.EmailFrom, email.NewTemplateRenderer())

	var checker githubclient.RepoChecker = repoCheckerClient
	var fetcher githubclient.ReleaseFetcher = releaseFetcherClient
	if redisClient != nil {
		checker = githubclient.NewCachedRepoChecker(repoCheckerClient, redisClient)
		fetcher = githubclient.NewCachedReleaseFetcher(releaseFetcherClient, redisClient)
	}

	subscribeUC := service.NewSubscribeUseCase(repoRepo, subRepo, checker, emailSender, cfg.BaseURL)
	confirmUC := service.NewConfirmUseCase(subRepo)
	unsubscribeUC := service.NewUnsubscribeUseCase(subRepo)
	getSubsUC := service.NewGetSubscriptionsUseCase(subRepo)

	scanInterval, err := time.ParseDuration(cfg.ScanInterval)
	if err != nil {
		log.Printf("warn: invalid SCAN_INTERVAL %q, defaulting to 1h", cfg.ScanInterval)
		scanInterval = time.Hour
	}
	scanner := service.NewScanner(repoRepo, subRepo, fetcher, emailSender, cfg.BaseURL, scanInterval)

	return subscribeUC, confirmUC, unsubscribeUC, getSubsUC, scanner
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
			log.Printf("migrate close source error: %v", sourceErr)
		}
		if dbErr != nil {
			log.Printf("migrate close db error: %v", dbErr)
		}
	}()

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
