# rate-limiter

An HTTP rate limiter in Go. It counts requests per client and answers `429 Too Many Requests` with a `Retry-After` header when a client goes over its limit. Three algorithms, one common interface, standard library only.

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

Bad settings, such as a limit of 0, stop the server at startup with an error instead of failing on a request later.

## Docker

```bash
docker run -d -p 8080:8080 ghcr.io/wilfex81/rate-limiter:latest -algo fixed -limit 3 -window 1m
```

Arguments after the image name replace the default flags. The image is a multi-stage build with a static binary on Alpine, running as a non-root user. `docker stop` sends SIGTERM and the server shuts down cleanly. A GitHub Actions workflow runs `go vet` and `go test -race`, then publishes the image.

## How it works

```
cmd/server/main.go                server wiring: flags, shutdown
internal/limiter/limiter.go       the Limiter interface
internal/limiter/fixedwindow.go   fixed window counter
internal/limiter/slidingwindow.go sliding window counter
internal/limiter/tokenbucket.go   token bucket
internal/limiter/store.go         one limiter per client, idle cleanup
internal/middleware/http.go       http.Handler wrapper, client IP
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
- **Consumer side interface.** The middleware defines its own small `Allower` interface and never imports the limiter package.

## Tests

```bash
go vet ./...
go test -race ./...
```

Covers each algorithm with a fake clock, the boundary burst, argument validation, concurrent access under the race detector, store cleanup, and the HTTP layer with `httptest`.

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

- **State lives in one process.** Two instances behind a load balancer each keep their own counts, so a client can get double the limit. Sharing counters through Redis is the next step.
- **Client identity is the connection address.** `X-Forwarded-For` is ignored on purpose, because any client can fake it. Behind a proxy you control, read it there. Under Docker Desktop, traffic from your own machine may arrive from one bridge address and count as a single client.
- One mutex guards the whole store, so it is the throughput ceiling under heavy parallel load. Splitting the map into shards, each with its own lock, would raise it.
- The sliding window's `Retry-After` is an upper bound, not an exact time.
- The store has no maximum size. A flood of unique keys grows the map until the idle cleanup removes them.
- Limits come from flags. There is no config file and no per route rules.