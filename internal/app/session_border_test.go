package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSessionBorderAnswersTwoStrengths: the focused pane keeps the frame, so
// the unfocused border is the same hue pulled back toward the ground rather
// than a second colour. A caller that got one colour would have to know how far
// to pull it back, and two callers would disagree.
func TestSessionBorderAnswersTwoStrengths(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.Settings.SessionColors = true
	m.Settings.SessionBorder = true
	m.SessionName = "work"

	focused, unfocused, ok := m.sessionBorderTint()
	if !ok {
		t.Fatal("the setting is on and a session is attached, but no border colour came back")
	}
	if focused == nil || unfocused == nil {
		t.Fatalf("a nil border colour: focused %v, unfocused %v", focused, unfocused)
	}
	if focused == unfocused {
		t.Error("the focused and unfocused borders are the same colour, so the frame no longer says which pane has focus")
	}
}

// TestSessionBorderNeedsSessionColours. The border reads the same colour the
// rail does, so turning the colours off has to turn the borders off with them
// rather than leaving them on a colour nothing else is using.
func TestSessionBorderNeedsSessionColours(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.Settings.SessionColors = false
	m.Settings.SessionBorder = true
	m.SessionName = "work"

	if _, _, ok := m.sessionBorderTint(); ok {
		t.Error("pane borders are tinted while session colours are off")
	}
}

// TestSessionBorderNeedsASession: before a session is attached there is no
// colour to carry, and the border must fall back rather than draw nothing.
func TestSessionBorderNeedsASession(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.Settings.SessionColors = true
	m.Settings.SessionBorder = true
	m.SessionName = ""

	if _, _, ok := m.sessionBorderTint(); ok {
		t.Error("a border colour came back for no session")
	}
}
