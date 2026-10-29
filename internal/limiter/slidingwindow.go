package limiter

import (
	"errors"
	"sync"
	"time"
)

// Removes the edge burst of FixedWindow
// Weighs the previous window by how much of it still overlaps
type SlidingWindow struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	now         func() time.Time
	windowStart time.Time
	prev, curr  int
}

func NewSlidingWindow(limit int, window time.Duration) (*SlidingWindow, error) {
	return newSlidingWindow(limit, window, time.Now)
}

func newSlidingWindow(limit int, window time.Duration, now func() time.Time) (*SlidingWindow, error) {
	if limit < 1 {
		return nil, errors.New("limit must be atleast 1")
	}
	if window <= 0 {
		return nil, errors.New("window must be positive")
	}
	return &SlidingWindow{limit: limit, window: window, now: now}, nil

}

func (s *SlidingWindow) Allow() (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.windowStart.IsZero() {
		s.windowStart = now // first call ever
	}

	elapsed := now.Sub(s.windowStart)
	if elapsed >= s.window {
		passed := int(elapsed / s.window) // whole window that went by
		if passed == 1 {
			s.prev = s.curr
		} else {
			s.prev = 0 // idle for 2+ windows, the old count no longer matters
		}
		s.curr = 0
		s.windowStart = s.windowStart.Add(time.Duration(passed) * s.window)
	}

	fraction := float64(now.Sub(s.windowStart)) / float64(s.window)
	estimate := float64(s.prev)*(1-fraction) + float64(s.curr)

	if estimate >= float64(s.limit) {
		return false, s.windowStart.Add(s.window).Sub(now)
	}
	s.curr++
	return true, 0
}
