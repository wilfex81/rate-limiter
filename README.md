# rate-limiter

An HTTP rate limiter in Go. It counts requests per client and answers `429 Too Many Requests` with a `Retry-After` header when a client goes over its limit. Three algorithms behind one interface. Counters live in memory (standard library only) or in Redis, so several instances can share one limit.

## The problem

A server has limited capacity, and any client can send requests as fast as it likes. One buggy script or one bad actor can starve everyone else. A rate limiter sits in front of the handlers and enforces a rule such as "each client gets at most N requests per time period".

## Algorithms

| Flag value | Idea | Tradeoff |
|------------|------|----------|
| `fixed` | N requests per window, the count resets when the window ends | Simple and cheap. Allows a double burst across a window boundary |
| `sliding` | Weights the previous window by how much of it still overlaps the last window of time | Removes the boundary burst. The count is an estimate |
| `token` | A bucket of tokens refills at a steady rate, each request spends one | Allows a burst up to the bucket size, then a steady rate |

The boundary burst is covered by a test: with a limit of 3 per minute, the fixed window lets 5 requests through in 2 seconds, and the sliding window lets 3.

## Quick start

```bash
go run ./cmd/server -algo fixed -limit 3 -window 1m
```

```bash
for i in 1 2 3 4 5; do curl -s -o /dev/null -w "%{http_code} " localhost:8080/ping; done; echo
# 200 200 200 429 429
curl -i localhost:8080/ping
# HTTP/1.1 429 Too Many Requests
# Retry-After: 60
```

| Flag | Default | Meaning |
|------|---------|---------|
| `-addr` | `:8080` | Address to listen on |
| `-algo` | `token` | `fixed`, `sliding` or `token` |
| `-limit` | `10` | Requests per window (`fixed`, `sliding`) or burst size (`token`) |
| `-window` | `1m` | Window length (`fixed`, `sliding`) |
| `-rate` | `1` | Tokens added per second (`token`) |
| `-backend` | `memory` | Where counters live: `memory` or `redis` |
| `-redis` | `localhost:6379` | Redis address (`-backend redis`) |
| `-fail-open` | `true` | Let requests through when Redis is unreachable |
| `-trusted-proxies` | none | Comma separated CIDRs of proxies whose `X-Real-IP` header is trusted |

Bad settings, such as a limit of 0, stop the server at startup with an error instead of failing on a request later.

## Docker

```bash
docker run -d -p 8080:8080 ghcr.io/wilfex81/rate-limiter:latest -algo fixed -limit 3 -window 1m
```

Arguments after the image name replace the default flags. The image is a multi-stage build with a static binary on Alpine, running as a non-root user. `docker stop` sends SIGTERM and the server shuts down cleanly. A GitHub Actions workflow runs `go vet` and `go test -race`, then publishes the image.

## Shared limits across instances

In memory, every process counts on its own, so two instances behind a load balancer would allow double the limit. With `-backend redis`, the counters live in Redis and every instance checks the same bucket.

```bash
docker compose up --build -d
for i in $(seq 1 8); do curl -s -w " %{http_code}\n" localhost:8080/ping; done
```

The compose file starts Redis, two app instances and nginx in front of them. Eight requests give 5 allowed and 3 blocked, and the allowed ones are answered by both instances (the response names which one).

The Redis backend supports the token bucket only. Each check is one Lua script, so two instances can never spend the same token.

If Redis is unreachable, the limiter fails open by default: requests pass, and one log line per 10 seconds says why. Start with `-fail-open=false` to reject requests instead. That choice trades availability against protection, so make it per deployment.

Behind a proxy, list it in `-trusted-proxies` (the compose file trusts only nginx's fixed address). The limiter then uses the proxy's `X-Real-IP` header as the client address, and ignores that header from everyone else.

## How it works

```
cmd/server/main.go                server wiring: flags, router (chi), shutdown
internal/limiter/limiter.go       the Limiter interface
internal/limiter/fixedwindow.go   fixed window counter
internal/limiter/slidingwindow.go sliding window counter
internal/limiter/tokenbucket.go   token bucket
internal/limiter/store.go         one limiter per client, idle cleanup
internal/middleware/http.go       http.Handler wrapper, client IP, trusted proxies
internal/redislimiter/            token bucket in Redis, one atomic Lua script
docker-compose.yml, nginx.conf    Redis, two instances and nginx, one command
```

```
request -> middleware (find the client's IP)
        -> store      (find or create that client's limiter)
        -> limiter    (allow or deny, plus how long to wait)
        -> your handler, or 429
```

Design notes:

- **One interface.** Every algorithm implements `Allow() (bool, time.Duration)`. The store and the middleware never know which algorithm is running.
- **Injected clock.** Each limiter holds a `now` function. Tests move a fake clock by hand, so no test sleeps and none are flaky.
- **Per client state.** The store keeps one limiter per key behind a mutex, and releases that lock before calling the limiter, so a busy client never blocks the others.
- **Idle cleanup.** A background goroutine removes clients that have gone quiet. A client must be idle for at least as long as its limiter needs to recover. Dropping it sooner would let a client reset its own limit by waiting.
- **Consumer side interface.** The middleware defines its own small `Allower` interface and never imports the limiter package. That is why the Redis limiter plugged in without a change to the middleware.
- **Atomic across machines.** Checking a bucket is read, compute, write. Done as separate commands, two instances could both spend the last token. The Lua script runs as one step inside Redis, and it reads Redis's own clock so instances with drifting clocks agree.
- **Self cleaning buckets.** Each Redis key expires after the time a bucket needs to refill, so there is no cleanup job.
- **Health checks are not limited.** `/healthz` sits outside the limited route group, so a busy moment never looks like an outage.

## Tests

```bash
go vet ./...
go test -race ./...
```

Covers each algorithm with a fake clock, the boundary burst, argument validation, concurrent access under the race detector, store cleanup, trusted proxy handling, and the HTTP layer with `httptest`.

The Redis tests need a running Redis and skip themselves otherwise:

```bash
docker run -d --name redis -p 6379:6379 redis:7-alpine
REDIS_ADDR=localhost:6379 go test -race -count=1 ./...
```

The main one starts two limiters against one Redis and checks that 20 attempts across both allow exactly 5. A second test covers the Redis-unreachable case for both failure modes without needing Redis.

## Benchmarks

```bash
go test -bench=. -benchmem ./internal/limiter/
```

Intel Core i3-1005G1 (4 threads), linux/amd64. Parallel benchmarks, so ns/op is time per call across all threads.

| Benchmark | ns/op | allocs/op |
|-----------|-------|-----------|
| TokenBucketAllow (one key, parallel) | 112.8 | 0 |
| StoreAllow (1,000 keys, parallel) | 185.3 | 0 |

That is roughly 8.9 million token bucket checks and 5.4 million store checks per second. Most of the time is spent waiting on mutexes.

## Known limits

- **The in-memory backend is per process.** Two instances using `-backend memory` each keep their own counts. Use `-backend redis` to share one limit.
- **The Redis backend supports the token bucket only.** Fixed and sliding windows are in-memory only.
- **Redis is a single point.** It is not replicated here. When it is unreachable the limiter fails open (default) or closed, and logs the reason at most once every 10 seconds.
- **An outage adds latency.** While Redis is unreachable, every request waits up to the 200 ms timeout before the limiter fails open or closed. A circuit breaker that stops trying for a few seconds after repeated failures would remove that cost.
- **Client identity is the connection address**, or the `X-Real-IP` header from proxies listed in `-trusted-proxies`. `X-Forwarded-For` is ignored on purpose, because any client can fake it. Under Docker Desktop, traffic from your own machine may arrive from one bridge address and count as a single client.
- One mutex guards the whole store, so it is the throughput ceiling under heavy parallel load. Splitting the map into shards, each with its own lock, would raise it.
- The sliding window's `Retry-After` is an upper bound, not an exact time.
- The store has no maximum size. A flood of unique keys grows the map until the idle cleanup removes them.
- Limits come from flags. There is no config file and no per route rules.