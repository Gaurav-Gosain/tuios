package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// decodeKey decodes a single key event from raw terminal bytes exactly as
// bubbletea does, so these cases exercise the real decoder rather than a
// hand-built KeyPressMsg.
func decodeKey(t *testing.T, raw []byte) tea.KeyPressMsg {
	t.Helper()
	var dec uv.EventDecoder
	_, ev := dec.Decode(raw)
	kp, ok := ev.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("decode %q: got %T, want KeyPressEvent", raw, ev)
	}
	return tea.KeyPressMsg(kp)
}

// TestIsCtrlPAcrossEncodings pins that Ctrl+P is recognised no matter how the
// terminal encodes it. The associated-text and alternate-key Kitty variants
// stringify to "p" and "ctrl+P" respectively, which the old msg.String() ==
// "ctrl+p" comparison missed; isCtrlP matches the decoded event and catches all.
func TestIsCtrlPAcrossEncodings(t *testing.T) {
	cases := map[string][]byte{
		"legacy control byte":       {0x10},
		"kitty disambiguate":        []byte("\x1b[112;5u"),
		"kitty associated text":     []byte("\x1b[112;5;112u"),
		"kitty alternate/base keys": []byte("\x1b[112::80;5u"),
		"modifyOtherKeys level 2":   []byte("\x1b[27;5;112~"),
	}
	for name, raw := range cases {
		msg := decodeKey(t, raw)
		if !isCtrlP(msg) {
			t.Errorf("%s: isCtrlP(%q) = false (String()=%q, Code=%q, Mod=%v), want true",
				name, raw, msg.String(), string(msg.Code), msg.Mod)
		}
	}
}

// TestIsCtrlPRejectsBareP guards that a plain 'p' never matches, so ordinary
// typing into the shell is not swallowed by the palette intercept.
func TestIsCtrlPRejectsBareP(t *testing.T) {
	for name, raw := range map[string][]byte{
		"bare p":  []byte("p"),
		"alt+p":   []byte("\x1bp"),
		"bare P":  []byte("P"),
		"ctrl+o":  {0x0f},
	} {
		msg := decodeKey(t, raw)
		if isCtrlP(msg) {
			t.Errorf("%s: isCtrlP(%q) = true (String()=%q), want false", name, raw, msg.String())
		}
	}
}
