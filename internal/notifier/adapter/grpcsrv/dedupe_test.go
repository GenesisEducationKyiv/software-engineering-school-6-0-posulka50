package grpcsrv

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDedupe_Seen_AfterMark(t *testing.T) {
	d := NewDedupe(10, time.Hour)
	d.Mark("saga-1")
	if !d.Seen("saga-1") {
		t.Fatal("expected Seen to return true after Mark")
	}
	if d.Seen("saga-2") {
		t.Error("expected Seen to return false for unmarked saga")
	}
}

func TestDedupe_ExpiredEntries_AreNotSeen(t *testing.T) {
	d := NewDedupe(10, 100*time.Millisecond)
	now := time.Now()
	d.now = func() time.Time { return now }
	d.Mark("saga-1")

	// Advance virtual clock past TTL.
	d.now = func() time.Time { return now.Add(200 * time.Millisecond) }
	if d.Seen("saga-1") {
		t.Error("expected expired entry to not be seen")
	}
	// Expired entry should also be removed from the underlying map.
	if _, ok := d.entries["saga-1"]; ok {
		t.Error("expected expired entry to be evicted on Seen")
	}
}

func TestDedupe_MaxSize_EvictsOldest(t *testing.T) {
	d := NewDedupe(3, time.Hour)
	base := time.Now()
	tick := 0
	d.now = func() time.Time {
		t := base.Add(time.Duration(tick) * time.Second)
		tick++
		return t
	}

	d.Mark("oldest")
	d.Mark("middle")
	d.Mark("newest")
	d.Mark("overflow") // should evict "oldest"

	if d.Seen("oldest") {
		t.Error("expected oldest entry to be evicted")
	}
	for _, id := range []string{"middle", "newest", "overflow"} {
		if !d.Seen(id) {
			t.Errorf("expected %q to be retained", id)
		}
	}
}

func TestDedupe_MaxSize_PrefersExpiredEviction(t *testing.T) {
	d := NewDedupe(3, 100*time.Millisecond)
	base := time.Now()
	d.now = func() time.Time { return base }

	d.Mark("stale-1")
	d.Mark("stale-2")
	d.Mark("fresh")

	// Move time past TTL — stale-1 and stale-2 are expired, fresh's age too,
	// but advancing further keeps the relative order. Add another mark at a
	// later virtual time: expired entries should be evicted preferentially.
	d.now = func() time.Time { return base.Add(200 * time.Millisecond) }
	d.Mark("survivor")

	if d.Seen("stale-1") || d.Seen("stale-2") || d.Seen("fresh") {
		t.Error("expected expired entries to be removed when capacity is reached")
	}
	if !d.Seen("survivor") {
		t.Error("expected fresh survivor entry to remain")
	}
}

func TestDedupe_TryClaim_ConcurrentWinnerIsUnique(t *testing.T) {
	d := NewDedupe(1000, time.Hour)
	const workers = 32
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if d.TryClaim("saga-x") {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", got)
	}
}

func TestDedupe_TryClaim_SecondCallLosesUntilRelease(t *testing.T) {
	d := NewDedupe(10, time.Hour)
	if !d.TryClaim("saga-1") {
		t.Fatal("first claim should win")
	}
	if d.TryClaim("saga-1") {
		t.Fatal("second claim should lose while first is live")
	}
	d.Release("saga-1")
	if !d.TryClaim("saga-1") {
		t.Fatal("claim after release should succeed")
	}
}

func TestDedupe_TryClaim_ReclaimsAfterTTL(t *testing.T) {
	d := NewDedupe(10, 100*time.Millisecond)
	now := time.Now()
	d.now = func() time.Time { return now }
	if !d.TryClaim("saga-1") {
		t.Fatal("initial claim should win")
	}
	d.now = func() time.Time { return now.Add(200 * time.Millisecond) }
	if !d.TryClaim("saga-1") {
		t.Fatal("claim after TTL expiry should win")
	}
}

func TestDedupe_ConcurrentAccess(t *testing.T) {
	d := NewDedupe(1000, time.Hour)
	const workers = 16
	const perWorker = 200

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := "saga-" + strconv.Itoa(w*perWorker+i)
				d.Mark(id)
				d.Seen(id)
			}
		}(w)
	}
	wg.Wait()
}
