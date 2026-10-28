package limiter

import (
	"math"
	"sync"
	"time"
)

type TokenBucket struct {
	mu       sync.Mutex
	capacity float64
	rate     float64
	tokens   float64
	last     time.Time
}

func NewTokenBucket(capacity int, rate float64) *TokenBucket {
	return &TokenBucket{
		capacity: float64(capacity),
		rate:     rate,
		tokens:   float64(capacity),
		last:     time.Now(),
	}
}

func (t *TokenBucket) Allow() (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(t.last).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	t.last = now

	t.tokens = math.Min(t.capacity, t.tokens+elapsed*t.rate)

	if t.tokens < 1 {
		retry := (1 - t.tokens) / t.rate
		return false, time.Duration(retry * float64(time.Second))
	}

	t.tokens--
	return true, 0
}
