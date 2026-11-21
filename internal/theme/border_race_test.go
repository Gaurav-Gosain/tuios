package theme

import (
	"sync"
	"testing"
)

// TestTheBorderOverridesSurviveConcurrentSessions.
//
// It has to be run with -race to mean anything, which is how CI runs it and
// how it was caught.
//
// Negative control: dropping the lock from SetBorderOverrides makes this
// report a race under -race.
func TestTheBorderOverridesSurviveConcurrentSessions(t *testing.T) {
	t.Cleanup(func() { SetBorderOverrides(BorderOverrides{}) })

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if i%2 == 0 {
				SetBorderOverrides(BorderOverrides{Focused: "#89b4fa", Unfocused: "#45475a"})
			} else {
				SetBorderOverrides(BorderOverrides{})
			}
		})
	}
	for range 8 {
		wg.Go(func() {
			_ = BorderFocusedWindow()
			_ = BorderUnfocused()
			_ = BorderFocusedTerminal()
		})
	}
	wg.Wait()
}
