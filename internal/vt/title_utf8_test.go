package vt

import (
	"testing"
	"unicode/utf8"
)

// A title is chrome: it is drawn in a rail row, a window frame and a session
// listing, and it is serialised into JSON. An invalid byte survives all of
// that as a replacement character, which draws as a tofu box and marshals as
// U+FFFD, so one bad byte from a guest put a black diamond in three places at
// once.

// TestATitleSetByAGuestLosesOnlyItsBadBytes.
//
// Driven through the emulator rather than by calling the sanitiser, so it
// covers the path a guest actually takes. The first version of this called
// the function directly, which meant reverting the call site could not fail
// it: a negative control that cannot bite is not one.
//
// Negative control: taking the OSC payload as a string leaves the title
// invalid and this fails.
func TestATitleSetByAGuestLosesOnlyItsBadBytes(t *testing.T) {
	e := NewEmulator(80, 24)
	// OSC 2 sets the window title. The payload carries a lone continuation
	// byte, which is what a stream split in the wrong place produces.
	_, _ = e.Write([]byte("\x1b]2;tui\xffos\x07"))

	got := e.title
	if got != "tuios" {
		t.Errorf("the title is %q, want the bad byte dropped and the rest kept", got)
	}
	if !utf8.ValidString(got) {
		t.Error("the title a guest set is still not valid UTF-8")
	}
}

// TestAValidTitleIsUntouched, multi-byte characters included: the guard must
// not cost a guest its emoji or its accents.
func TestAValidTitleIsUntouched(t *testing.T) {
	for _, want := range []string{
		"tuios", "café", "日本語", "✳ building", "",
	} {
		if got := sanitiseTitle([]byte(want)); got != want {
			t.Errorf("sanitiseTitle(%q) = %q", want, got)
		}
	}
}

// TestARealReplacementCharacterSurvives. U+FFFD is a character a guest may
// legitimately send, and it is three bytes rather than one bad one. Dropping
// it would be the guard overreaching.
func TestARealReplacementCharacterSurvives(t *testing.T) {
	want := "before � after"
	if got := sanitiseTitle([]byte(want)); got != want {
		t.Errorf("sanitiseTitle dropped a real U+FFFD: %q", got)
	}
}

// TestATitleOfNothingButBadBytesIsEmpty rather than a row of boxes.
func TestATitleOfNothingButBadBytesIsEmpty(t *testing.T) {
	if got := sanitiseTitle([]byte{0xff, 0xfe, 0x80}); got != "" {
		t.Errorf("the title is %q, want nothing", got)
	}
}
