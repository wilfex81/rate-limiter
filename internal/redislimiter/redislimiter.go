package redislimiter

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// The whole check runs inside Redis as one script.
// Redis runs one script at a time, so two server instances can never both spend the same token

var script = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])

local data = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = capacity
  ts = now
end

local elapsed = math.max(0, now - ts) / 1000
tokens = math.min(capacity, tokens + elapsed * rate)

local allowed = 0
local wait_ms = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  wait_ms = math.ceil((1 - tokens) / rate * 1000)
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], math.ceil(capacity / rate * 1000) + 1000)
return {allowed, wait_ms}
`)

// state lives in Redis
// Shared by every instance
type TokenBucket struct {
	rdb      redis.Scripter
	prefix   string
	capacity int
	rate     float64
	timeout  time.Duration
	failOpen bool
	lastLog  atomic.Int64
}

// New Creates a limiter.
// failOpen decides what happens when Redis cannot be reached
// true lets requests through, false rejects them
func New(rdb redis.Scripter, capacity int, rate float64, failOpen bool) (*TokenBucket, error) {
	if capacity < 1 {
		return nil, errors.New("capacity must be atleast 1")
	}
	if rate <= 0 {
		return nil, errors.New("rate must be positive")
	}

	return &TokenBucket{
		rdb:      rdb,
		prefix:   "r1:",
		capacity: capacity,
		rate:     rate,
		timeout:  200 * time.Millisecond,
		failOpen: failOpen,
	}, nil

}

// Improve debugging redis outage
func (t *TokenBucket) logError(err error) {
	now := time.Now().Unix()
	last := t.lastLog.Load()
	if now-last >= 10 && t.lastLog.CompareAndSwap(last, now) {
		log.Printf("redislimiter: %v (failOpen=%v)", err, t.failOpen)
	}
}

// Allow has the same shape as limiter.Store.Allow
// No need to chaneg the middleware as it plugs into the middleware's Allower interface
func (t *TokenBucket) Allow(key string) (bool, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	res, err := script.Run(ctx, t.rdb, []string{t.prefix + key}, t.capacity, t.rate).Int64Slice()
	if err == nil && len(res) != 2 {
		err = errors.New("unexpected script result")
	}
	if err != nil {
		t.logError(err)
		return t.failOpen, 0
	}
	if res[0] == 1 {
		return true, 0
	}
	return false, time.Duration(res[1]) * time.Millisecond
}
