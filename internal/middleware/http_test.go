package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wilfex81/rate-limiter/internal/limiter"
)

func newTestHandler(t *testing.T, limit int) http.Handler {
	t.Helper()
	store := limiter.NewStore(func() limiter.Limiter {
		fw, err := limiter.NewFixedWindow(limit, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return fw
	}, time.Hour, 0)
	t.Cleanup(store.Close)

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return RateLimit(store, ClientIP)(ok)
}

func send(h http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRateLimitBlocksAfterLimit(t *testing.T) {
	h := newTestHandler(t, 2)

	assert.Equal(t, 200, send(h, "1.2.3.4:5000").Code)
	assert.Equal(t, 200, send(h, "1.2.3.4:5001").Code) // same IP, new port

	rec := send(h, "1.2.3.4:5002")
	assert.Equal(t, 429, rec.Code)
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	assert.NoError(t, err)
	assert.Greater(t, secs, 0)

	assert.Equal(t, 200, send(h, "5.6.7.8:5000").Code) // another IP is unaffected
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	assert.Equal(t, "10.0.0.1", ClientIP(r))

	r.RemoteAddr = "not-an-address"
	assert.Equal(t, "not-an-address", ClientIP(r))
}
