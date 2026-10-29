package limiter

import (
	"errors"
	"math"
	"sync"
	"time"
)

type TokenBucket struct {
	mu       sync.Mutex
	capacity float64
	rate     float64 // tokens added per second
	tokens   float64
	last     time.Time
	now      func() time.Time
}

func NewTokenBucket(capacity int, rate float64) (*TokenBucket, error) {
	return newTokenBucket(capacity, rate, time.Now)
}

func newTokenBucket(capacity int, rate float64, now func() time.Time) (*TokenBucket, error) {
	if capacity < 1 {
		return nil, errors.New("capacity must be at least 1")
	}
	if rate <= 0 {
		return nil, errors.New("rate must be positive")
	}
	return &TokenBucket{
		capacity: float64(capacity),
		rate:     rate,
		tokens:   float64(capacity), // starts full
		last:     now(),
		now:      now,
	}, nil
}

func (b *TokenBucket) Allow() (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed < 0 {
		elapsed = 0 // clock went backwards, ignore it
	}
	b.last = now

	b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.rate)

	if b.tokens < 1 {
		wait := (1 - b.tokens) / b.rate
		return false, time.Duration(wait * float64(time.Second))
	}
	b.tokens--
	return true, 0
}
