// Package flags gives the score service a cached view of the feature_flags
// table with a hard limit on how stale that view can be.
package flags

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

const (
	// DefaultTTL is how long a read is reused unless MINIPLAY_FLAG_TTL says otherwise.
	DefaultTTL = 5 * time.Second
	// MaxTTL caps the setting, so a typo cannot turn an event off an hour late.
	MaxTTL = 60 * time.Second
	// readTimeout bounds one table read, and so the wait of any caller queued behind it.
	readTimeout = 2 * time.Second
)

// TTLFromEnv reads MINIPLAY_FLAG_TTL, a Go duration such as "5s". Zero disables
// caching and reads the table on every call.
func TTLFromEnv() (time.Duration, error) {
	raw := os.Getenv("MINIPLAY_FLAG_TTL")
	if raw == "" {
		return DefaultTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("MINIPLAY_FLAG_TTL: %w", err)
	}
	if ttl < 0 || ttl > MaxTTL {
		return 0, fmt.Errorf("MINIPLAY_FLAG_TTL must be between 0 and %s", MaxTTL)
	}
	return ttl, nil
}

// Source reads the current flag from the table.
type Source interface {
	FeatureFlag(context.Context, game.ID) (store.Flag, error)
}

type entry struct {
	mu        sync.Mutex
	flag      store.Flag
	readStart time.Time
	loaded    bool
}

// Cache reuses a read for at most ttl, measured from when that read started. A
// change committed at time C is therefore returned by every call from C+ttl
// onward: any cached value still in use by then began its read after C.
//
// There is no fallback to an expired value. If a refresh fails, Current returns
// the error and the score service answers 503, so the bound holds during a
// database outage. The run token stays usable for a retry.
type Cache struct {
	source  Source
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[game.ID]*entry
}

// NewCache checks the TTL against MaxTTL. A nil clock means time.Now.
func NewCache(source Source, ttl time.Duration, now func() time.Time) (*Cache, error) {
	if source == nil {
		return nil, errors.New("flag cache needs a source")
	}
	if ttl < 0 || ttl > MaxTTL {
		return nil, fmt.Errorf("flag cache TTL must be between 0 and %s", MaxTTL)
	}
	if now == nil {
		now = time.Now
	}
	return &Cache{source: source, ttl: ttl, now: now, entries: make(map[game.ID]*entry)}, nil
}

// Current returns the game's flag. Concurrent callers for one game share a single
// refresh, so an expiry under load costs one read rather than one per request.
func (c *Cache) Current(ctx context.Context, id game.ID) (store.Flag, error) {
	c.mu.Lock()
	current, found := c.entries[id]
	if !found {
		current = &entry{}
		c.entries[id] = current
	}
	c.mu.Unlock()

	current.mu.Lock()
	defer current.mu.Unlock()
	started := c.now()
	if age := started.Sub(current.readStart); current.loaded && age >= 0 && age < c.ttl {
		return current.flag, nil
	}
	current.loaded = false
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	flag, err := c.source.FeatureFlag(readCtx, id)
	if err != nil {
		return store.Flag{}, err
	}
	current.flag, current.readStart, current.loaded = flag, started, true
	return flag, nil
}
