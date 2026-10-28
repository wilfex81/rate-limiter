package limiter

import (
	"sync"
	"time"
)

type FixedWindow struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	windowStart time.Time
	count       int
}

func (f *FixedWindow) Allow() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	diff := now.Sub(f.windowStart)

	// Reset window if elapsed time >= window duration
	if diff >= f.window {
		f.windowStart = now
		f.count = 0
	}

	// Deny if the limit already reached
	if f.count >= f.limit {
		return false
	}

	// Allow and increment
	f.count++
	return true
}

// can be accessed by packages outside without altering the limit
func NewFixedWindow(limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{limit: limit, window: window}
}
