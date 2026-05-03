package main

import (
	"context"
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
	_ "github.com/golang-migrate/migrate/v4/source/file"
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
	_ = godotenv.Load()

	cfg := config.Load()

	dbPool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := dbPool.Ping(context.Background()); err != nil {
		log.Fatalf("database ping failed: %v", err)
	}
	log.Println("database connected")

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}
	log.Println("migrations applied")

	defer dbPool.Close()

	var redisClient *redis.Client
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err == nil {
			redisClient = redis.NewClient(opts)
			if err := redisClient.Ping(context.Background()).Err(); err != nil {
				log.Printf("warn: redis not available (%v), caching disabled", err)
				redisClient = nil
			} else {
				log.Println("redis connected")
			}
		} else {
			log.Printf("warn: invalid REDIS_URL: %v", err)
		}
	}

	repoRepo := repository.NewPostgresRepoRepository(dbPool)
	subRepo := repository.NewPostgresRepository(dbPool)
	ghClient := githubclient.NewClient(cfg.GitHubToken, redisClient)
	emailSender := email.NewSender(cfg.ResendAPIKey, cfg.EmailFrom)

	subService := service.NewSubscriptionService(repoRepo, subRepo, ghClient, emailSender, cfg.BaseURL)

	scanInterval, err := time.ParseDuration(cfg.ScanInterval)
	if err != nil {
		log.Printf("warn: invalid SCAN_INTERVAL %q, defaulting to 1h", cfg.ScanInterval)
		scanInterval = time.Hour
	}
	scanner := service.NewScanner(repoRepo, subRepo, ghClient, emailSender, cfg.BaseURL, scanInterval)

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.Prometheus())

	h := handler.New(subService)

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

	router.StaticFile("/", "./static/index.html")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scanner.Start(ctx)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
}

func runMigrations(databaseURL string) error {
	m, err := migrate.New("file://migrations", databaseURL)
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

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
