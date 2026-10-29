package limiter

import (
	"errors"
	"sync"
	"time"
)

type FixedWindow struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	now         func() time.Time
	windowStart time.Time
	count       int
}

func NewFixedWindow(limit int, window time.Duration) (*FixedWindow, error) {
	return newFixedWindow(limit, window, time.Now)
}

func newFixedWindow(limit int, window time.Duration, now func() time.Time) (*FixedWindow, error) {
	if limit < 1 {
		return nil, errors.New("limit must be at least 1")
	}
	if window <= 0 {
		return nil, errors.New("window must be positive")
	}
	return &FixedWindow{limit: limit, window: window, now: now}, nil
}

func (f *FixedWindow) Allow() (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	if now.Sub(f.windowStart) >= f.window {
		f.windowStart = now
		f.count = 0
	}
	if f.count >= f.limit {
		return false, f.windowStart.Add(f.window).Sub(now)
	}
	f.count++
	return true, 0
}
