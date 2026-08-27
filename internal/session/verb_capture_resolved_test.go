package session

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestVerbCapturePaneResolved exercises the resolved capture path end to end:
// real PTY output carrying an SGR index is captured twice — once plain, once
// resolved against a palette — and the resolved copy must carry the palette's
// RGB instead of the index. This is issue #135's example: a mocha-theme red
// (index 1) must come out as 38;2;243;139;168, not 31.
func TestVerbCapturePaneResolved(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	pty, err := d.resolvePTYForTarget(sess, "")
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	// printf so the escape survives whatever login shell the PTY runs.
	if _, err := pty.Write([]byte("printf '\\033[31mRED\\033[0m\\n'\r")); err != nil {
		t.Fatalf("pty write: %v", err)
	}

	// Wait for the painted output, not the echoed command: the shell echoes
	// "printf '\033[31mRED...'" (backslash literal), while the painted line
	// carries the real ESC byte, so matching "\x1b[31mRED" is deterministic.
	waitFor(t, "capture sees painted RED", func() bool {
		res := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","source":"visible","styled":true}}`))
		content, _ := res["content"].(string)
		return strings.Contains(content, "\x1b[31mRED")
	})

	// Unresolved capture keeps the index: that is the documented contract.
	plain := result(t, c.call(t, `{"id":2,"verb":"capture-pane","params":{"session":"work","source":"visible","styled":true}}`))
	plainContent, _ := plain["content"].(string)
	if !strings.Contains(plainContent, "\x1b[31m") {
		t.Fatalf("unresolved capture lost the SGR index: %q", plainContent)
	}

	// Resolved capture maps 31 → #f38ba8 (index 1 of the mocha palette).
	res := result(t, c.call(t, `{"id":3,"verb":"capture-pane","params":{"session":"work","source":"visible","styled":true,"resolved":true,"palette":["#45475a","#f38ba8","#a6e3a1","#f9e2af","#89b4fa","#f5c2e7","#94e2d5","#bac2de","#585b70","#f38ba8","#a6e3a1","#f9e2af","#89b4fa","#f5c2e7","#94e2d5","#a6adc8"]}}`))
	if res["resolved"] != true {
		t.Fatalf("resolved flag not echoed: %v", res)
	}
	content, _ := res["content"].(string)
	if !strings.Contains(content, "\x1b[38;2;243;139;168mRED") {
		t.Fatalf("resolved capture did not map 31 to #f38ba8: %q", content)
	}
	if strings.Contains(content, "\x1b[31m") {
		t.Fatalf("resolved capture still contains index 31: %q", content)
	}
}

// TestVerbCapturePaneResolvedBadPalette checks the palette parameter is
// validated: a short palette is refused with the invalid-params code rather
// than silently resolving against xterm defaults.
func TestVerbCapturePaneResolvedBadPalette(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","source":"visible","resolved":true,"palette":["#000000"]}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("bad palette code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestVerbCapturePaneResolvedDefaultPalette verifies resolved without a
// palette still works: the xterm defaults produce true-colour output for a
// basic index, so the capture is renderable by a consumer with no palette.
func TestVerbCapturePaneResolvedDefaultPalette(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	pty, err := d.resolvePTYForTarget(sess, "")
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	if _, err := pty.Write([]byte("printf '\\033[32mGREEN\\033[0m\\n'\r")); err != nil {
		t.Fatalf("pty write: %v", err)
	}

	// The capture is resolved from the start, so the painted GREEN arrives as
	// true colour already; match the resolved form (real ESC byte + 38;2;)
	// followed by GREEN. The echoed command carries "\033" literally, so it
	// can never match.
	resolvedGREEN := regexp.MustCompile("\x1b\\[38;2;\\d+;\\d+;\\d+mGREEN")
	deadline := time.Now().Add(8 * time.Second)
	for {
		res := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"work","source":"visible","resolved":true}}`))
		content, _ := res["content"].(string)
		if resolvedGREEN.MatchString(content) {
			if strings.Contains(content, "\x1b[32m") {
				t.Fatalf("resolved default palette kept index 32: %q", content)
			}
			// Resolved implies styled: the content carries rewritten escapes,
			// so reporting styled=false here would leave the caller guessing.
			if res["styled"] != true {
				t.Fatalf("resolved capture reported styled=%v, want true", res["styled"])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("resolved capture never produced true-colour GREEN: %q", content)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
