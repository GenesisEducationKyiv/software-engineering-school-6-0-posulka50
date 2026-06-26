package grpcsrv

import (
	"sync"
	"time"
)

const (
	defaultDedupeMax = 10_000
	defaultDedupeTTL = time.Hour
)

// Dedupe is a bounded, TTL-based set of recently processed saga IDs used to
// suppress duplicate confirmation emails when both the async RabbitMQ path and
// the sync gRPC retry path land on the notifier for the same saga.
//
// Trade-off: the cache lives in process memory only. A notifier restart
// between the original Resend success and the gRPC retry will not be caught
// here; that edge is documented in ADR-003 and accepted.
type Dedupe struct {
	mu      sync.Mutex
	entries map[string]time.Time
	max     int
	ttl     time.Duration
	now     func() time.Time
}

// NewDedupe returns a cache with explicit bounds, intended for tests.
func NewDedupe(maxSize int, ttl time.Duration) *Dedupe {
	return &Dedupe{
		entries: make(map[string]time.Time),
		max:     maxSize,
		ttl:     ttl,
		now:     time.Now,
	}
}

// NewDefaultDedupe returns a cache sized for production: 10k entries, 1h TTL.
func NewDefaultDedupe() *Dedupe {
	return NewDedupe(defaultDedupeMax, defaultDedupeTTL)
}

// Mark records that sagaID has just been processed. If the cache is at
// capacity, expired entries are pruned first; if it is still full, the oldest
// entry is evicted to make room.
func (d *Dedupe) Mark(sagaID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.entries) >= d.max {
		d.evictExpiredLocked()
	}
	if len(d.entries) >= d.max {
		d.evictOldestLocked()
	}
	d.entries[sagaID] = d.now()
}

// Seen reports whether sagaID was marked within the TTL window. Expired
// entries are removed on access so the cache self-cleans under normal load.
func (d *Dedupe) Seen(sagaID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.entries[sagaID]
	if !ok {
		return false
	}
	if d.now().Sub(t) > d.ttl {
		delete(d.entries, sagaID)
		return false
	}
	return true
}

func (d *Dedupe) evictExpiredLocked() {
	cutoff := d.now().Add(-d.ttl)
	for k, t := range d.entries {
		if t.Before(cutoff) {
			delete(d.entries, k)
		}
	}
}

func (d *Dedupe) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, t := range d.entries {
		if first || t.Before(oldestTime) {
			oldestKey = k
			oldestTime = t
			first = false
		}
	}
	if !first {
		delete(d.entries, oldestKey)
	}
}
