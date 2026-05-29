package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP RED metrics
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests processed.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// Email metrics
	EmailsSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emails_sent_total",
		Help: "Total number of emails sent by type (confirmation, release).",
	}, []string{"type"})

	EmailSendErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "email_send_errors_total",
		Help: "Total email send failures by type.",
	}, []string{"type"})

	// GitHub API metrics
	GithubAPIRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_requests_total",
		Help: "Total GitHub API requests made.",
	}, []string{"endpoint", "status"})

	// Subscription lifecycle RED metrics
	SubscriptionsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "subscriptions_created_total",
		Help: "Total subscription requests successfully created (unconfirmed).",
	})

	SubscriptionsConfirmedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "subscriptions_confirmed_total",
		Help: "Total subscriptions confirmed via token.",
	})

	SubscriptionsRemovedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "subscriptions_removed_total",
		Help: "Total subscriptions removed via unsubscribe token.",
	})

	// Scanner RED metrics
	ScannerRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scanner_runs_total",
		Help: "Total scanner runs by result status.",
	}, []string{"status"})

	ScannerDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "scanner_run_duration_seconds",
		Help:    "Duration of a full scanner run in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	ReleasesDetectedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "releases_detected_total",
		Help: "Total new releases detected across all tracked repositories.",
	})
)
