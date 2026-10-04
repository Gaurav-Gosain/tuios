package app

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestSetProgramFPS pins the one thing frame_rate.go assumes about Bubble Tea
// that its API does not promise: an unexported int field fps that the frame
// ticker starts from, which NewProgram clamps to 120. A release that renames it
// would leave every max_fps above 120 drawing at 120 again, with nothing else
// failing, so this is the test that notices. The same holds for the ticker
// field a change while running resets.
func TestSetProgramFPS(t *testing.T) {
	p := tea.NewProgram(nil, tea.WithFPS(240), tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard))
	fps := reflect.ValueOf(p).Elem().FieldByName("fps")
	if !fps.IsValid() {
		t.Fatal("tea.Program has no fps field; setProgramFPS cannot lift the 120 clamp")
	}
	if got := fps.Int(); got != 120 {
		t.Fatalf("NewProgram left fps at %d for WithFPS(240); the clamp moved, so re-check MaxFPSCap and setProgramFPS", got)
	}
	// The ticker is what a change while running resets.
	if tick := reflect.ValueOf(p).Elem().FieldByName("ticker"); !tick.IsValid() || tick.Type() != reflect.TypeFor[*time.Ticker]() {
		t.Fatal("tea.Program has no *time.Ticker field ticker; a max_fps change would wait for the next start")
	}
	if !setProgramFPS(p, 240) {
		t.Fatal("setProgramFPS did not reach the fps field")
	}
	if got := fps.Int(); got != 240 {
		t.Fatalf("fps is %d after setProgramFPS(240)", got)
	}
}
