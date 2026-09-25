package theme

import (
	"sync"
	"testing"
)

// The border overrides are process-global and are written whenever a session
// applies its appearance config, which is once per client. Under the ssh
// server that is once per connection, on that connection's own goroutine, so
// two people connecting at the same moment wrote them at the same moment.
//
// What a user would have seen is one session briefly wearing another's border
// colour. What CI saw was a data race.

// TestTheBorderOverridesSurviveConcurrentSessions.
//
// It has to be run with -race to mean anything, which is how CI runs it and
// how it was caught.
//
// Negative control: dropping the lock from SetBorderOverrides makes this
// report a race under -race.
func TestTheBorderOverridesSurviveConcurrentSessions(t *testing.T) {
	t.Cleanup(func() { SetBorderOverrides("", "") })

	var wg sync.WaitGroup
	// Two connections arriving together, each applying its own appearance,
	// while a renderer asks what the border colour is.
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				SetBorderOverrides("#89b4fa", "#45475a")
			} else {
				SetBorderOverrides("", "")
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = BorderFocusedWindow()
			_ = BorderUnfocused()
			_ = BorderFocusedTerminal()
		}()
	}
	wg.Wait()
}
