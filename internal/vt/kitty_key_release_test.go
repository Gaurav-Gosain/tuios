package vt

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestEncodeKeyReleaseNeedsTheFlag checks that a release is only ever spelled
// for a pane that asked for event types. Every other pane reads a release as
// another press, so sending one would double the keystroke.
func TestEncodeKeyReleaseNeedsTheFlag(t *testing.T) {
	key := KeyPressEvent{Code: 'a', Text: "a"}
	for _, flags := range []int{
		0,
		ansi.KittyDisambiguateEscapeCodes,
		ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportAlternateKeys,
	} {
		if got := EncodeKeyReleaseCSIu(key, flags); got != "" {
			t.Errorf("flags %d: release = %q, want nothing", flags, got)
		}
	}
	const withEventTypes = ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportEventTypes
	if got := EncodeKeyReleaseCSIu(key, withEventTypes); got != "\x1b[97;1:3u" {
		t.Errorf("release = %q, want %q", got, "\x1b[97;1:3u")
	}
}

// TestPressAndReleaseNameTheSameKey walks the keys that have a spelling of their
// own and checks the release ends the key the press started. Both encoders read
// one table so the two can never drift, and this is what says so.
func TestPressAndReleaseNameTheSameKey(t *testing.T) {
	const flags = ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportEventTypes |
		ansi.KittyReportAllKeysAsEscapeCodes

	keys := []struct {
		name string
		code rune
	}{
		{"enter", KeyEnter}, {"tab", KeyTab}, {"backspace", KeyBackspace},
		{"escape", KeyEscape}, {"space", KeySpace}, {"up", KeyUp}, {"down", KeyDown},
		{"left", KeyLeft}, {"right", KeyRight}, {"home", KeyHome}, {"end", KeyEnd},
		{"insert", KeyInsert}, {"delete", KeyDelete}, {"pgup", KeyPgUp},
		{"pgdown", KeyPgDown}, {"f1", KeyF1}, {"f4", KeyF4}, {"f5", KeyF5},
		{"f12", KeyF12}, {"letter", 'q'},
	}

	for _, k := range keys {
		t.Run(k.name, func(t *testing.T) {
			// Modified, so the press carries its key number and terminator too;
			// unmodified letter- and tilde-terminated presses are spelled in the
			// bare legacy form that has no number to compare.
			key := KeyPressEvent{Code: k.code, Mod: ModCtrl}
			press := EncodeKeyCSIu(key, flags)
			release := EncodeKeyReleaseCSIu(key, flags)
			if press == "" || release == "" {
				t.Fatalf("press = %q, release = %q, want both spelled", press, release)
			}
			if press[len(press)-1] != release[len(release)-1] {
				t.Errorf("press %q and release %q end with different keys", press, release)
			}
			num, _, _ := strings.Cut(strings.TrimPrefix(press, "\x1b["), ";")
			if !strings.HasPrefix(strings.TrimPrefix(release, "\x1b["), num+";") {
				t.Errorf("press %q and release %q name different key numbers", press, release)
			}
			if !strings.Contains(release, ":3") {
				t.Errorf("release %q carries no release event type", release)
			}
		})
	}
}
