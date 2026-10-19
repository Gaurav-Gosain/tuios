package surface

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := NewLimiter(PatchesPerSecond, PatchesPerSecond)
	now := time.Unix(0, 0)
	n := 0
	for range 150 {
		if l.Allow(now) {
			n++
		}
	}
	if n != PatchesPerSecond {
		t.Errorf("allowed %d patches at once, want %d", n, PatchesPerSecond)
	}
	if !l.Allow(now.Add(20 * time.Millisecond)) {
		t.Error("no patch allowed after the bucket refilled")
	}
	toast := NewLimiter(1/ToastInterval.Seconds(), 1)
	if !toast.Allow(now) || toast.Allow(now.Add(time.Second)) || !toast.Allow(now.Add(2*time.Second)) {
		t.Error("the toast limit is not one per two seconds")
	}
}
