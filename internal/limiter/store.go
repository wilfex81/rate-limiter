package limiter

import (
	"sync"
	"time"
)

type entry struct {
	limiter  Limiter
	lastSeen time.Time
}

type Store struct {
	mu         sync.Mutex
	entries    map[string]*entry
	newLimiter func() Limiter
	idleTTL    time.Duration
	now        func() time.Time

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// NewStore creates a store. newLimiter builds the limiter for a new key.
//
// idleTTL must be at least as long as a limiter needs to fully recover.
// A brand new limiter starts fresh, so dropping a client sooner than that
// would let it reset its own limit by going quiet.
//
// cleanupEvery is how often idle clients are swept. 0 means no background
// goroutine, which lets tests call sweep by hand.

func NewStore(newLimiter func() Limiter, idleTTL, cleanupEvery time.Duration) *Store {
	return newStoreWithClock(newLimiter, idleTTL, cleanupEvery, time.Now)
}

func newStoreWithClock(newLimiter func() Limiter, idleTTL, cleanupEvery time.Duration, now func() time.Time) *Store {
	s := &Store{
		entries:    make(map[string]*entry),
		newLimiter: newLimiter,
		idleTTL:    idleTTL,
		now:        now,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	if cleanupEvery > 0 {
		go s.cleanupLoop(cleanupEvery)
	} else {
		close(s.done)
	}
	return s
}

func (s *Store) Allow(key string) (bool, time.Duration) {
	s.mu.Lock()
	e, ok := s.entries[key]
	if !ok {
		e = &entry{limiter: s.newLimiter()}
		s.entries[key] = e
	}
	e.lastSeen = s.now()
	s.mu.Unlock()

	return e.limiter.Allow()
}

// Len - How many clients are currently tracked
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// sweep - removes clients idle for longer than idleTTl and returns how many
func (s *Store) sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := s.now().Add(-s.idleTTL)
	removed := 0
	for k, e := range s.entries {
		if e.lastSeen.Before(cutoff) {
			delete(s.entries, k)
			removed++
		}
	}
	return removed
}

func (s *Store) cleanupLoop(every time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.sweep()
		case <-s.stop:
			return
		}
	}
}

// stops background cleanup
func (s *Store) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
	})
	<-s.done
}
