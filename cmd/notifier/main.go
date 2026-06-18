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
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
	"github.com/posul/github-notifier/internal/notifier/adapter/resend"
	"github.com/posul/github-notifier/internal/notifier/adapter/templates"
	"github.com/posul/github-notifier/internal/platform/logctx"
	"github.com/posul/github-notifier/internal/platform/middleware"
)

const (
	brokerDialAttempts = 15
	brokerDialDelay    = 2 * time.Second
)

func main() {
	slog.SetDefault(slog.New(logctx.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	if err := run(); err != nil {
		slog.Error("notifier: startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil {
		slog.Warn(".env file not found, using environment variables", "error", err)
	}

	port := getEnv("PORT", "8081")
	resendKey := os.Getenv("RESEND_API_KEY")
	resendURL := getEnv("RESEND_API_URL", "https://api.resend.com/emails")
	emailFrom := os.Getenv("EMAIL_FROM")
	ginMode := getEnv("GIN_MODE", "release")
	brokerURL := os.Getenv("BROKER_URL")
	if brokerURL == "" {
		return errors.New("BROKER_URL is required: notifier consumes notifications from rabbitmq")
	}

	sender := resend.NewSender(resendKey, emailFrom, resendURL, templates.NewRenderer())

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()

	consumer, err := dialConsumerWithRetry(brokerURL, sender)
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(consumerCtx); err != nil {
			slog.Error("notifier: consumer exited with error", "error", err)
		}
	}()

	router := newRouter(ginMode)
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("notifier: listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("notifier: server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("notifier: shutting down gracefully")
	cancelConsumer()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("notifier: server forced to shutdown", "error", err)
	}

	select {
	case <-consumerDone:
	case <-shutdownCtx.Done():
		slog.Warn("notifier: consumer did not stop in time")
	}
	if err := consumer.Close(); err != nil {
		slog.Warn("notifier: consumer close error", "error", err)
	}

	slog.Info("notifier: stopped")
	return nil
}

func dialConsumerWithRetry(brokerURL string, sender rabbitmq.Sender) (*rabbitmq.Consumer, error) {
	var lastErr error
	for attempt := 1; attempt <= brokerDialAttempts; attempt++ {
		c, err := rabbitmq.NewConsumer(brokerURL, sender)
		if err == nil {
			slog.Info("notifier: connected to rabbitmq", "attempt", attempt)
			return c, nil
		}
		lastErr = err
		slog.Warn("notifier: rabbitmq dial failed, retrying", "attempt", attempt, "error", err)
		time.Sleep(brokerDialDelay)
	}
	return nil, fmt.Errorf("after %d attempts: %w", brokerDialAttempts, lastErr)
}

// newRouter wires only operational endpoints: /health for compose probes and
// /metrics for Prometheus. Notification delivery is broker-only.
func newRouter(ginMode string) *gin.Engine {
	gin.SetMode(ginMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Prometheus())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	return r
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
