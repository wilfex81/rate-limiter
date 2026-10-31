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

	"github.com/wilfex81/rate-limiter/internal/limiter"
	"github.com/wilfex81/rate-limiter/internal/middleware"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	algo := flag.String("algo", "token", "fixed, sliding window or token")
	limit := flag.Int("limit", 10, "requests per window (fixed, sliding) or burst size (token)")
	window := flag.Duration("window", time.Minute, "window length (fixed, sliding)")
	rate := flag.Float64("rate", 1, "tokens added per second (token)")
	flag.Parse()

	newLimiter, idleTTL, err := factory(*algo, *limit, *window, *rate)
	if err != nil {
		log.Fatal(err)
	}
	store := limiter.NewStore(newLimiter, idleTTL, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong\n"))
	})
	handler := middleware.RateLimit(store, middleware.ClientIP)(mux)

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("listening on %s (algo=%s)", *addr, *algo)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	store.Close()
	log.Println("shutdown complete")
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
