package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/posul/github-notifier/internal/notifier/adapter/grpcsrv"
	"github.com/posul/github-notifier/internal/notifier/adapter/rabbitmq"
	"github.com/posul/github-notifier/internal/notifier/adapter/resend"
	"github.com/posul/github-notifier/internal/notifier/adapter/templates"
	"github.com/posul/github-notifier/internal/platform/logctx"
	"github.com/posul/github-notifier/internal/platform/middleware"
	notifierv1 "github.com/posul/github-notifier/proto/gen/notifier/v1"
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
	grpcPort := getEnv("NOTIFIER_GRPC_PORT", "50051")
	resendKey := os.Getenv("RESEND_API_KEY")
	resendURL := getEnv("RESEND_API_URL", "https://api.resend.com/emails")
	emailFrom := os.Getenv("EMAIL_FROM")
	ginMode := getEnv("GIN_MODE", "release")
	brokerURL := os.Getenv("BROKER_URL")
	if brokerURL == "" {
		return errors.New("BROKER_URL is required: notifier consumes notifications from rabbitmq")
	}

	sender := resend.NewSender(resendKey, emailFrom, resendURL, templates.NewRenderer())

	// Dedupe is shared between the RabbitMQ consumer and the gRPC server so a
	// sweeper-triggered sync retry does not re-send an email the async path
	// already delivered.
	dedupe := grpcsrv.NewDefaultDedupe()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()

	// The notifier needs its own publisher to emit Subscribe-saga reply events
	// (confirmation_sent / confirmation_failed) back to the orchestrator.
	replies, err := dialPublisherWithRetry(brokerURL)
	if err != nil {
		return fmt.Errorf("connect rabbitmq publisher: %w", err)
	}
	defer func() {
		if err := replies.Close(); err != nil {
			slog.Warn("notifier: replies publisher close error", "error", err)
		}
	}()

	consumer, err := dialConsumerWithRetry(brokerURL, sender, replies, dedupe)
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

	grpcLis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", grpcPort, err)
	}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(grpcsrv.NewValidationUnaryInterceptor()))
	notifierv1.RegisterEmailNotifierServiceServer(grpcServer, grpcsrv.NewServer(sender, dedupe))
	grpcDone := make(chan struct{})
	go func() {
		defer close(grpcDone)
		slog.Info("notifier: grpc listening", "port", grpcPort)
		if err := grpcServer.Serve(grpcLis); err != nil {
			slog.Error("notifier: grpc server error", "error", err)
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

	go func() {
		<-shutdownCtx.Done()
		grpcServer.Stop()
	}()
	grpcServer.GracefulStop()

	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		slog.Warn("notifier: grpc did not stop in time")
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

func dialConsumerWithRetry(brokerURL string, sender rabbitmq.Sender, replies rabbitmq.ReplyPublisher, marker rabbitmq.Marker) (*rabbitmq.Consumer, error) {
	var lastErr error
	for attempt := 1; attempt <= brokerDialAttempts; attempt++ {
		c, err := rabbitmq.NewConsumer(brokerURL, sender, replies, marker)
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

func dialPublisherWithRetry(brokerURL string) (*rabbitmq.Publisher, error) {
	var lastErr error
	for attempt := 1; attempt <= brokerDialAttempts; attempt++ {
		p, err := rabbitmq.NewPublisher(brokerURL)
		if err == nil {
			slog.Info("notifier: rabbitmq publisher connected", "attempt", attempt)
			return p, nil
		}
		lastErr = err
		slog.Warn("notifier: rabbitmq publisher dial failed, retrying", "attempt", attempt, "error", err)
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
