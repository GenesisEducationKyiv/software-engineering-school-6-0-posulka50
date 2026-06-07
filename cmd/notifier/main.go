package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	notifierhttp "github.com/posul/github-notifier/internal/notifier/adapter/http"
	"github.com/posul/github-notifier/internal/notifier/adapter/resend"
	"github.com/posul/github-notifier/internal/notifier/adapter/templates"
	"github.com/posul/github-notifier/internal/platform/middleware"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
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

	sender := resend.NewSender(resendKey, emailFrom, resendURL, templates.NewRenderer())
	handler := notifierhttp.New(sender)

	router := newRouter(ginMode, handler)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("notifier: server forced to shutdown", "error", err)
	}
	slog.Info("notifier: stopped")
	return nil
}

func newRouter(ginMode string, h *notifierhttp.Handler) *gin.Engine {
	gin.SetMode(ginMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.Prometheus())

	v1 := r.Group("/v1/notifications")
	{
		v1.POST("/confirmation", h.Confirmation)
		v1.POST("/release", h.Release)
	}

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
