package limiter

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func fixedFactory(limit int, window time.Duration, now func() time.Time) func() Limiter {
	return func() Limiter {
		fw, err := newFixedWindow(limit, window, now)
		if err != nil {
			panic(err)
		}
		return fw
	}
}

func TestStoreKeysAreIsolated(t *testing.T) {
	clock := newFakeClock()
	s := newStoreWithClock(fixedFactory(2, time.Minute, clock.Now), time.Hour, 0, clock.Now)
	defer s.Close()

	ok, _ := s.Allow("alice")
	assert.True(t, ok)
	ok, _ = s.Allow("alice")
	assert.True(t, ok)
	ok, _ = s.Allow("alice")
	assert.False(t, ok) // alice is out
	ok, _ = s.Allow("bob")
	assert.True(t, ok) // bob is unaffected
}

func TestStoreSweepRemovesOnlyIdleClients(t *testing.T) {
	clock := newFakeClock()
	s := newStoreWithClock(fixedFactory(5, time.Minute, clock.Now), 10*time.Minute, 0, clock.Now)
	defer s.Close()

	s.Allow("old")
	clock.Advance(8 * time.Minute)
	s.Allow("recent")
	clock.Advance(5 * time.Minute) // old idle 13m, recent idle 5m

	assert.Equal(t, 1, s.sweep())
	assert.Equal(t, 1, s.Len())
	assert.Equal(t, 0, s.sweep())
}

func TestStoreActivityKeepsClientAlive(t *testing.T) {
	clock := newFakeClock()
	s := newStoreWithClock(fixedFactory(100, time.Minute, clock.Now), 10*time.Minute, 0, clock.Now)
	defer s.Close()

	for i := 0; i < 5; i++ {
		s.Allow("busy")
		clock.Advance(6 * time.Minute) // never idle for 10 minutes
		assert.Equal(t, 0, s.sweep())
	}
	assert.Equal(t, 1, s.Len())
}

func TestStoreBackgroundCleanup(t *testing.T) {
	s := NewStore(fixedFactory(1, time.Minute, time.Now), 20*time.Millisecond, 10*time.Millisecond)
	defer s.Close()

	s.Allow("x")
	assert.Eventually(t, func() bool { return s.Len() == 0 },
		time.Second, 10*time.Millisecond)
}

func TestStoreCloseTwice(t *testing.T) {
	s := NewStore(fixedFactory(1, time.Minute, time.Now), time.Minute, time.Millisecond)
	s.Close()
	s.Close() // must not panic
}

func TestStoreConcurrent(t *testing.T) {
	s := NewStore(func() Limiter {
		tb, _ := NewTokenBucket(10, 0.000001)
		return tb
	}, time.Hour, time.Millisecond)
	defer s.Close()

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if ok, _ := s.Allow("k" + strconv.Itoa(i%5)); ok {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(50), allowed.Load()) // 5 keys, 10 tokens each
}

func BenchmarkStoreAllow(b *testing.B) {
	s := NewStore(fixedFactory(1_000_000, time.Hour, time.Now), time.Hour, time.Minute)
	defer s.Close()

	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = "key" + strconv.Itoa(i)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			s.Allow(keys[i%len(keys)])
			i++
		}
	})
}