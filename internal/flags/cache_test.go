package flags

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

type fakeSource struct {
	mu      sync.Mutex
	flags   map[game.ID]store.Flag
	err     error
	reads   int
	onRead  func()
	release chan struct{}
}

func (f *fakeSource) FeatureFlag(_ context.Context, id game.ID) (store.Flag, error) {
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.onRead != nil {
		f.onRead()
	}
	return f.flags[id], f.err
}

func (f *fakeSource) set(id game.ID, on bool, version int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flags[id] = store.Flag{Game: id, DoublePoints: on, Version: version}
}

type clock struct{ now time.Time }

func (c *clock) read() time.Time { return c.now }

func setup(t *testing.T, ttl time.Duration) (*Cache, *fakeSource, *clock) {
	t.Helper()
	source := &fakeSource{flags: map[game.ID]store.Flag{}}
	source.set(game.Rhythm, false, 1)
	source.set(game.Reflex, false, 1)
	at := &clock{now: time.Unix(1800000000, 0)}
	cache, err := NewCache(source, ttl, at.read)
	if err != nil {
		t.Fatal(err)
	}
	return cache, source, at
}

func read(t *testing.T, cache *Cache, id game.ID) store.Flag {
	t.Helper()
	flag, err := cache.Current(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return flag
}

func TestChangeIsSeenExactlyAtTheStalenessBound(t *testing.T) {
	cache, source, at := setup(t, 5*time.Second)
	if flag := read(t, cache, game.Rhythm); flag.DoublePoints {
		t.Fatalf("first read: %+v", flag)
	}
	source.set(game.Rhythm, true, 2)
	at.now = at.now.Add(5*time.Second - time.Nanosecond)
	if flag := read(t, cache, game.Rhythm); flag.Version != 1 {
		t.Fatalf("inside the TTL the cached value is reused: %+v", flag)
	}
	if source.reads != 1 {
		t.Fatalf("reads inside the TTL = %d", source.reads)
	}
	at.now = at.now.Add(time.Nanosecond)
	if flag := read(t, cache, game.Rhythm); !flag.DoublePoints || flag.Version != 2 {
		t.Fatalf("at the TTL the change must be visible: %+v", flag)
	}
}

// The age counts from when the read began. A slow read must not extend the bound.
func TestBoundCountsFromTheStartOfTheRead(t *testing.T) {
	cache, source, at := setup(t, 5*time.Second)
	source.onRead = func() { at.now = at.now.Add(4 * time.Second) } // the read itself takes 4s
	read(t, cache, game.Rhythm)
	source.onRead = nil
	at.now = at.now.Add(time.Second) // 5s after the read began, 1s after it ended
	read(t, cache, game.Rhythm)
	if source.reads != 2 {
		t.Fatalf("reads=%d; the second call should have refreshed", source.reads)
	}
}

func TestFailedRefreshNeverServesAnExpiredValue(t *testing.T) {
	cache, source, at := setup(t, 5*time.Second)
	read(t, cache, game.Rhythm)
	source.err = errors.New("database down")
	at.now = at.now.Add(5 * time.Second)
	if flag, err := cache.Current(context.Background(), game.Rhythm); err == nil {
		t.Fatalf("expected an error, got expired value %+v", flag)
	}
	// A failed refresh must not count as fresh either.
	if _, err := cache.Current(context.Background(), game.Rhythm); err == nil {
		t.Fatal("second call inside the TTL of the failed refresh should also fail")
	}
	source.err = nil
	source.set(game.Rhythm, true, 2)
	if flag := read(t, cache, game.Rhythm); !flag.DoublePoints {
		t.Fatalf("after recovery: %+v", flag)
	}
}

func TestConcurrentCallersShareOneRefresh(t *testing.T) {
	cache, source, _ := setup(t, 5*time.Second)
	source.release = make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Current(context.Background(), game.Reflex); err != nil {
				t.Error(err)
			}
		}()
	}
	close(source.release)
	wg.Wait()
	if source.reads != 1 {
		t.Fatalf("reads=%d, want 1", source.reads)
	}
}

func TestGamesAreCachedSeparately(t *testing.T) {
	cache, source, _ := setup(t, 5*time.Second)
	read(t, cache, game.Rhythm)
	source.set(game.Reflex, true, 2)
	if flag := read(t, cache, game.Reflex); !flag.DoublePoints {
		t.Fatalf("reflex: %+v", flag)
	}
	if flag := read(t, cache, game.Rhythm); flag.DoublePoints {
		t.Fatalf("rhythm: %+v", flag)
	}
}

func TestZeroTTLReadsEveryTime(t *testing.T) {
	cache, source, _ := setup(t, 0)
	read(t, cache, game.Rhythm)
	read(t, cache, game.Rhythm)
	if source.reads != 2 {
		t.Fatalf("reads=%d", source.reads)
	}
}

func TestTTLLimits(t *testing.T) {
	if _, err := NewCache(&fakeSource{}, MaxTTL+time.Nanosecond, nil); err == nil {
		t.Fatal("a TTL above the maximum must be refused")
	}
	if _, err := NewCache(&fakeSource{}, -time.Second, nil); err == nil {
		t.Fatal("a negative TTL must be refused")
	}
	if _, err := NewCache(nil, time.Second, nil); err == nil {
		t.Fatal("a nil source must be refused")
	}
	for raw, want := range map[string]string{"": "", "0s": "", "60s": "", "61s": "between", "-1s": "between", "soon": "MINIPLAY_FLAG_TTL"} {
		t.Setenv("MINIPLAY_FLAG_TTL", raw)
		ttl, err := TTLFromEnv()
		if want == "" && err != nil {
			t.Errorf("%q: %v", raw, err)
		}
		if want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%q: ttl=%s err=%v", raw, ttl, err)
		}
		if raw == "" && ttl != DefaultTTL {
			t.Errorf("default ttl=%s", ttl)
		}
	}
}
