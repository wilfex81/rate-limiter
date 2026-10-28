package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFixedWindowBlockAfterLimit(t *testing.T) {
	fw := &FixedWindow{limit: 3, window: time.Minute}

	want := []bool{true, true, true, false}
	for i, w := range want {
		got := fw.Allow()
		if got != w {
			t.Errorf("call %d: got %v, want %v", i+1, got, w)
		}
	}
}

func TestFixedWindowConcurrent(t *testing.T) {
	fw := &FixedWindow{limit: 100, window: time.Hour}

	var allowed atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if fw.Allow() {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 100 {
		t.Errorf("allowed %d, want 100", allowed.Load())
	}
}
