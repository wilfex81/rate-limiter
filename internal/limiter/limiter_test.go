package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeClock is a clock the test moves by hand, so no test ever sleeps.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// edgeBurst sends 1 request, then 2 at 59s, then 3 at 61s, and returns
// how many of the last 5 were allowed.
func edgeBurst(l Limiter, clock *fakeClock) int {
	l.Allow() // starts the first window
	clock.Advance(59 * time.Second)
	allowed := 0
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow(); ok {
			allowed++
		}
	}
	clock.Advance(2 * time.Second)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow(); ok {
			allowed++
		}
	}
	return allowed
}

// ---- fixed window ----

func TestFixedWindowBlocksAfterLimit(t *testing.T) {
	fw, err := NewFixedWindow(3, time.Minute)
	assert.NoError(t, err)

	want := []bool{true, true, true, false}
	for i, w := range want {
		got, _ := fw.Allow()
		assert.Equal(t, w, got, "call %d", i+1)
	}
}

func TestFixedWindowResets(t *testing.T) {
	clock := newFakeClock()
	fw, _ := newFixedWindow(3, time.Minute, clock.Now)

	for i := 0; i < 3; i++ {
		fw.Allow()
	}
	ok, retry := fw.Allow()
	assert.False(t, ok)
	assert.Equal(t, time.Minute, retry) // the window just started

	clock.Advance(59 * time.Second)
	ok, retry = fw.Allow()
	assert.False(t, ok)
	assert.Equal(t, time.Second, retry)

	clock.Advance(2 * time.Second)
	ok, _ = fw.Allow()
	assert.True(t, ok) // new window
}

func TestFixedWindowEdgeBurst(t *testing.T) {
	clock := newFakeClock()
	fw, _ := newFixedWindow(3, time.Minute, clock.Now)
	// 5 requests in 2 seconds under a limit of 3 per minute: the known weakness
	assert.Equal(t, 5, edgeBurst(fw, clock))
}

// ---- token bucket ----

func TestTokenBucketBurstThenRefill(t *testing.T) {
	clock := newFakeClock()
	tb, _ := newTokenBucket(5, 1, clock.Now) // burst of 5, 1 token per second

	for i := 0; i < 5; i++ {
		ok, _ := tb.Allow()
		assert.True(t, ok, "burst %d", i+1)
	}
	ok, retry := tb.Allow()
	assert.False(t, ok)
	assert.Equal(t, time.Second, retry)

	clock.Advance(time.Second)
	ok, _ = tb.Allow()
	assert.True(t, ok) // one token came back
	ok, _ = tb.Allow()
	assert.False(t, ok)

	clock.Advance(500 * time.Millisecond)
	ok, retry = tb.Allow()
	assert.False(t, ok) // half a token is not enough
	assert.Equal(t, 500*time.Millisecond, retry)
}

func TestTokenBucketNeverExceedsCapacity(t *testing.T) {
	clock := newFakeClock()
	tb, _ := newTokenBucket(2, 10, clock.Now)

	clock.Advance(time.Hour)
	ok, _ := tb.Allow()
	assert.True(t, ok)
	ok, _ = tb.Allow()
	assert.True(t, ok)
	ok, _ = tb.Allow()
	assert.False(t, ok) // idle time does not stockpile tokens
}

// ---- sliding window ----

func TestSlidingWindowSmoothsEdgeBurst(t *testing.T) {
	clock := newFakeClock()
	sw, _ := newSlidingWindow(3, time.Minute, clock.Now)
	// same traffic as the fixed window test, but only 3 get through
	assert.Equal(t, 3, edgeBurst(sw, clock))
}

// ---- all three ----

func TestInvalidArguments(t *testing.T) {
	_, err := NewFixedWindow(0, time.Minute)
	assert.Error(t, err)
	_, err = NewFixedWindow(1, 0)
	assert.Error(t, err)
	_, err = NewSlidingWindow(0, time.Minute)
	assert.Error(t, err)
	_, err = NewTokenBucket(0, 1)
	assert.Error(t, err)
	_, err = NewTokenBucket(1, 0)
	assert.Error(t, err)
}

func TestAllLimitersBlockAfterBurst(t *testing.T) {
	clock := newFakeClock()
	fw, _ := newFixedWindow(3, time.Minute, clock.Now)
	sw, _ := newSlidingWindow(3, time.Minute, clock.Now)
	tb, _ := newTokenBucket(3, 0.05, clock.Now)

	for name, l := range map[string]Limiter{"fixed": fw, "sliding": sw, "token": tb} {
		for i := 0; i < 3; i++ {
			ok, _ := l.Allow()
			assert.True(t, ok, "%s request %d", name, i+1)
		}
		ok, retry := l.Allow()
		assert.False(t, ok, name)
		assert.Greater(t, retry, time.Duration(0), name)
	}
}

func TestConcurrentAllow(t *testing.T) {
	fw, _ := NewFixedWindow(100, time.Hour)
	sw, _ := NewSlidingWindow(100, time.Hour)
	tb, _ := NewTokenBucket(100, 0.000001)

	for name, l := range map[string]Limiter{"fixed": fw, "sliding": sw, "token": tb} {
		var allowed atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					if ok, _ := l.Allow(); ok {
						allowed.Add(1)
					}
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, int64(100), allowed.Load(), name) // 500 tries, exactly 100 allowed
	}
}

func BenchmarkTokenBucketAllow(b *testing.B) {
	tb, _ := NewTokenBucket(1_000_000_000, 1_000_000)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow()
		}
	})
}
