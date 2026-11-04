package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/wilfex81/rate-limiter/internal/limiter"
	"github.com/wilfex81/rate-limiter/internal/middleware"
	"github.com/wilfex81/rate-limiter/internal/redislimiter"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	backend := flag.String("backend", "memory", "where counters live: memory or redis")
	redisAddr := flag.String("redis", "localhost:6379", "redis address (backend redis)")
	failOpen := flag.Bool("fail-open", true, "let requests trough when redis is unreachable")
	algo := flag.String("algo", "token", "fixed, sliding window or token")
	limit := flag.Int("limit", 10, "requests per window (fixed, sliding) or burst size (token)")
	window := flag.Duration("window", time.Minute, "window length (fixed, sliding)")
	rate := flag.Float64("rate", 1, "tokens added per second (token)")
	flag.Parse()

	allower, closeFn, err := buildAllower(*backend, *algo, *limit, *window, *rate, *redisAddr, *failOpen)
	if err != nil {
		log.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(chimw.Recoverer)

	// never rate limit health checks.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	// rate limit is now in effect
	r.Group(func(r chi.Router) {
		r.Use(middleware.RateLimit(allower, middleware.ClientIP))
		r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("pong\n"))
		})
	})

	srv := &http.Server{Addr: *addr, Handler: r, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("listening on %s (backend=%s algo=%s)", *addr, *backend, *algo)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	closeFn()
	log.Println("shutdown complete")

}

// creates the counter backend and returns a function to close it
func buildAllower(backend, algo string, limit int, window time.Duration, rate float64,
	redisAddr string, failOpen bool) (middleware.Allower, func(), error) {
	switch backend {
	case "memory":
		newLimiter, idleTTL, err := factory(algo, limit, window, rate)
		if err != nil {
			return nil, nil, err
		}
		store := limiter.NewStore(newLimiter, idleTTL, time.Minute)
		return store, store.Close, nil

	case "redis":
		if algo != "token" {
			return nil, nil, errors.New("the redis backend only supports -algo token")
		}
		rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
		rl, err := redislimiter.New(rdb, limit, rate, failOpen)
		if err != nil {
			rdb.Close()
			return nil, nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("warning: cannot reach redis at %s: %v", redisAddr, err)
		}
		return rl, func() { rdb.Close() }, nil
	}
	return nil, nil, fmt.Errorf("unknown backend %q", backend)

}

// factory validates the settings once at startup, then returns a function
// that builds a fresh limiter for each new client, plus how long a client
// must be idle before it is safe to forget
func factory(algo string, limit int, window time.Duration, rate float64) (func() limiter.Limiter, time.Duration, error) {
	switch algo {
	case "fixed":
		if _, err := limiter.NewFixedWindow(limit, window); err != nil {
			return nil, 0, err
		}
		return func() limiter.Limiter {
			l, _ := limiter.NewFixedWindow(limit, window)
			return l
		}, window, nil
	case "sliding":
		if _, err := limiter.NewSlidingWindow(limit, window); err != nil {
			return nil, 0, err
		}
		return func() limiter.Limiter {
			l, _ := limiter.NewSlidingWindow(limit, window)
			return l
		}, 2 * window, nil
	case "token":
		if _, err := limiter.NewTokenBucket(limit, rate); err != nil {
			return nil, 0, err
		}
		refill := time.Duration(float64(limit) / rate * float64(time.Second))
		return func() limiter.Limiter {
			l, _ := limiter.NewTokenBucket(limit, rate)
			return l
		}, refill, nil
	}
	return nil, 0, fmt.Errorf("unknown algorithm %q", algo)
}
