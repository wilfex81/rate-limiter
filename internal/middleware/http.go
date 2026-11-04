package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Allower - checks if a key is allowed "right now"
// limiter.Store satisfies it
// the middleware package never imports limiter
type Allower interface {
	Allow(key string) (bool, time.Duration)
}

func RateLimit(a Allower, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retry := a.Allow(keyFn(r))
			if !ok {
				secs := int(math.Ceil(retry.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP - uses the address of the connection, with the port removed.
// It deliberately ignores X-Fowarded-For: any client can fake that header
// and dodge the limit.
// Note: Only read it when the request comes from a proxy controled by client
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Returns a key funnction that trusts the X-Real-IP header
// only when the connection itself comes from on of the trusted networks.
func ClientIPBehindProxy(trusted []*net.IPNet) func(*http.Request) string {
	return func(r *http.Request) string {
		direct := ClientIP(r)
		ip := net.ParseIP(direct)
		if ip == nil {
			return direct
		}
		for _, n := range trusted {
			if n.Contains(ip) {
				if real := net.ParseIP(r.Header.Get("X-Real-IP")); real != nil {
					return real.String()
				}
				break
			}
		}
		return direct
	}
}
