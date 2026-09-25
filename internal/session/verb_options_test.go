package session

import (
	"testing"
)

// TestSetOptionStillTakesTheBareSpelling guards a break dressed up as a fix. The
// runtime setter has always taken "border_style" as well as the full path, and
// validation that only knew the long form would have made the short one an error
// for the first time.
func TestSetOptionStillTakesTheBareSpelling(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "bare")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"set-option","params":{"session":"bare","key":"border_style","value":"double"}}`))
	// Normalised on the way in, so one option never becomes two entries that can
	// disagree.
	if res["key"] != "appearance.border_style" {
		t.Errorf("key = %v, want the full path", res["key"])
	}

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"bare","key":"border_style"}}`))
	if got["value"] != "double" {
		t.Errorf("value = %v, want double", got["value"])
	}
}
