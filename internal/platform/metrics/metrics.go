package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const labelStatus = "status"

// defaultLatencyBuckets covers sub-millisecond responses up to long-running
// background work (two minutes). Prometheus DefBuckets top out at 10s, which
// is too low for the scanner and gives no resolution between 1s and 10s.
var defaultLatencyBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005,
	0.0075, 0.01, 0.015, 0.025, 0.05,
	0.075, 0.1, 0.15, 0.2, 0.3, 0.4, 0.5,
	0.6, 0.7, 0.8, 0.9, 1.0, 1.2, 1.4, 1.6, 1.8, 2.0,
	2.5, 3.5, 5, 7.5, 10, 15, 20, 30, 60, 120,
}

var (
	// HTTP RED metrics
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests processed.",
	}, []string{"method", "path", labelStatus})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: defaultLatencyBuckets,
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
	}, []string{"endpoint", labelStatus})

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
	}, []string{labelStatus})

	ScannerDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "scanner_run_duration_seconds",
		Help:    "Duration of a full scanner run in seconds.",
		Buckets: defaultLatencyBuckets,
	})

	ReleasesDetectedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "releases_detected_total",
		Help: "Total new releases detected across all tracked repositories.",
	})
)
