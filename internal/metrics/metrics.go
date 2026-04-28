package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests processed.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	EmailsSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emails_sent_total",
		Help: "Total number of emails sent by type (confirmation, release).",
	}, []string{"type"})

	GithubAPIRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_requests_total",
		Help: "Total GitHub API requests made.",
	}, []string{"endpoint", "status"})
)
