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

// TestAnOverrideIsWhatIsRead, so the lock did not cost the feature. A colour
// set is the colour the borders come back with, and clearing it goes back to
// the theme's own.
func TestAnOverrideIsWhatIsRead(t *testing.T) {
	t.Cleanup(func() { SetBorderOverrides("", "") })

	SetBorderOverrides("#89b4fa", "#45475a")
	focused, unfocused := borderOverrides()
	if focused == nil || unfocused == nil {
		t.Fatal("an override was set and reads as nothing")
	}
	if got := BorderFocusedWindow(); got != focused {
		t.Errorf("the focused border is %v, want the override %v", got, focused)
	}
	if got := BorderUnfocused(); got != unfocused {
		t.Errorf("the unfocused border is %v, want the override %v", got, unfocused)
	}

	SetBorderOverrides("", "")
	if f, u := borderOverrides(); f != nil || u != nil {
		t.Error("clearing the overrides left one behind")
	}
}
