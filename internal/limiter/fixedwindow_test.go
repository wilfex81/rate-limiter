package limiter

import (
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
