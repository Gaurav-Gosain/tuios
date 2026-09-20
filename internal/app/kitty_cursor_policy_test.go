package app

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every placement tuios sends the host has to carry C=1.
//
// The protocol's default is C=0, which moves the cursor to after the image's
// bottom right cell. tuios places images anywhere in a pane, including its last
// rows, so that move runs past the bottom of the screen and the host terminal
// scrolls to make room. The save and restore around a placement put the cursor
// back; they cannot put back a scroll. Every frame after it is then drawn one
// row out, which is the whole screen sliding up and the chrome smearing across
// it, and nothing recovers until the screen is fully redrawn.

// kittyPlacement matches an APC placement command and captures its parameters.
var kittyPlacement = regexp.MustCompile(`\x1b_G([^;\x1b]*)`)

// TestEveryHostPlacementPinsTheCursor walks the emitted bytes of each placement
// builder and checks the parameter is there.
func TestEveryHostPlacementPinsTheCursor(t *testing.T) {
	for _, c := range []struct {
		name  string
		bytes []byte
	}{
		{"video replace", buildVideoReplace(7, &remoteVideoState{hostX: 4, hostY: 9})},
	} {
		assertPinsCursor(t, c.name, string(c.bytes))
	}
}

// assertPinsCursor fails when a real placement in out does not carry C=1.
func assertPinsCursor(t *testing.T, name, out string) {
	t.Helper()
	found := 0
	for _, m := range kittyPlacement.FindAllStringSubmatch(out, -1) {
		params := m[1]
		if !strings.HasPrefix(params, "a=p") {
			continue
		}
		if strings.Contains(params, "U=1") {
			// A virtual placement puts the image in cells the guest wrote, so
			// the host never moves a cursor for it.
			continue
		}
		found++
		if !strings.Contains(params, "C=1") {
			t.Errorf("%s: placement %q does not pin the cursor, so an image at the bottom of a pane scrolls the screen",
				name, params)
		}
	}
	if found == 0 {
		t.Errorf("%s: no real placement found in %q, so this checks nothing", name, out)
	}
}

// TestThePlacementBuildersAllPinTheCursor reads the source of the placement
// builders rather than their output, because two of them need a great deal of
// state stood up before they emit anything and the parameter is a property of
// the command they write, not of the state that reached them.
//
// Negative control: dropping C=1 from any of the three fails this.
func TestThePlacementBuildersAllPinTheCursor(t *testing.T) {
	for _, path := range []string{
		"kitty_passthrough_placement.go",
		"kitty_passthrough_forward.go",
	} {
		src := readSourceFile(t, path)
		for _, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, `"a=p`) && !strings.Contains(line, `\x1b_Ga=p`) {
				continue
			}
			if strings.Contains(line, "U=1") {
				continue
			}
			if !strings.Contains(line, "C=1") {
				t.Errorf("%s: a placement is written without C=1: %s", path, strings.TrimSpace(line))
			}
		}
	}
}

// readSourceFile reads one file of this package.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
