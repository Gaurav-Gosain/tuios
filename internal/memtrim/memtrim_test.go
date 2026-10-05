package memtrim

import (
	"testing"
	"testing/synctest"
)

// A window closed inside a synctest bubble arms the trim timer there. The
// runtime ties that timer to the bubble, and resetting it from any other
// goroutine is a fatal error that ends the whole test binary. The terminal
// tests close windows both inside bubbles and in ordinary cleanups, so the
// second Request has to leave a timer it did not make alone.
func TestRequestAfterABubbleArmedTheTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		Request()
	})
	Request()
	Request()
}
