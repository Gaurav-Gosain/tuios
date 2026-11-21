package app

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

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

func assertPinsCursor(t *testing.T, name, out string) {
	t.Helper()
	found := 0
	for _, m := range kittyPlacement.FindAllStringSubmatch(out, -1) {
		params := m[1]
		if !strings.HasPrefix(params, "a=p") {
			continue
		}
		if strings.Contains(params, "U=1") {
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
		for line := range strings.SplitSeq(src, "\n") {
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

func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
