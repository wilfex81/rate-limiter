package redislimiter

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// the point of this whole package: two instances, one shared limit
func TestSharedAcrossInstances(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR to run, for example localhost:6379")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })

	// two limiters, as if they belonged to two server instances
	a, err := New(rdb, 5, 0.001, false)
	assert.NoError(t, err)
	b, err := New(rdb, 5, 0.001, false)
	assert.NoError(t, err)

	key := "test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { rdb.Del(context.Background(), "rl:"+key) })

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		l := a
		if i%2 == 1 {
			l = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow(key); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(5), allowed.Load()) // 20 tries across both, exactly 5 allowed
}

func TestRetryAfterWhenBlocked(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("set REDIS_ADDR to run, for example localhost:6379")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { rdb.Close() })

	l, _ := New(rdb, 1, 1, false) // 1 token, refills 1 per second
	key := "retry-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { rdb.Del(context.Background(), "rl:"+key) })

	ok, _ := l.Allow(key)
	assert.True(t, ok)
	ok, retry := l.Allow(key)
	assert.False(t, ok)
	assert.Greater(t, retry, time.Duration(0))
	assert.LessOrEqual(t, retry, time.Second)
}

// no Redis needed: the failure mode is a decision, so both choices should be tested
func TestRedisDown(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1", // nothing listens here
		DialTimeout: 50 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { rdb.Close() })

	open, _ := New(rdb, 5, 1, true)
	closed, _ := New(rdb, 5, 1, false)

	ok, _ := open.Allow("k")
	assert.True(t, ok)
	ok, _ = closed.Allow("k")
	assert.False(t, ok)
}

func TestInvalidArguments(t *testing.T) {
	_, err := New(nil, 0, 1, false)
	assert.Error(t, err)
	_, err = New(nil, 1, 0, false)
	assert.Error(t, err)
}

func TestDebugAllow(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip()
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	l, err := New(rdb, 5, 0.1, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		ok, retry := l.Allow("debug-allow")
		t.Logf("request %d: allowed=%v retry=%v", i, ok, retry)
	}
	rdb.Del(context.Background(), "rl:debug-allow")
}