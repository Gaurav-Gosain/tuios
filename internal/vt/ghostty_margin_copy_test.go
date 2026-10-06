//go:build ghostty

package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestGhosttyMarginCopyFollowsTheLibrary covers the sequences that change the
// library's margins without a plain DECSTBM or DECSLRM. The ghostty backend
// keeps its own copy of the margins, because the library does not expose
// them, and the reattach snapshot carries that copy. Each case states the
// region on both backends.
//
// The copy is checked against what the library does, not against the pure
// emulator. Where the two backends disagree, the case says so, and
// TestGhosttyReattachKeepsTheGuestsMargins in internal/session checks the
// library's behaviour after a reattach with a probe.
func TestGhosttyMarginCopyFollowsTheLibrary(t *testing.T) {
	const w, h = 40, 12
	full := uv.Rect(0, 0, w, h)
	lr := uv.Rect(4, 0, 16, h)   // DECSLRM 5;20
	tb := uv.Rect(0, 2, w, 8)    // DECSTBM 3;10
	both := uv.Rect(4, 2, 16, 8) // both
	cases := []struct {
		name, in         string
		wantPure, wantGh uv.Rectangle
	}{
		{
			// DECALN resets every margin (Terminal.zig decaln).
			name:     "DECALN resets the margins",
			in:       "\x1b[3;10r\x1b[?69h\x1b[5;20s\x1b#8",
			wantPure: full, wantGh: full,
		},
		{
			// XTRESTORE of DECLRMM saved off runs the library's ?69l path.
			// The pure emulator does not implement XTSAVE or XTRESTORE, so
			// on that side every XTRESTORE case leaves the margins alone.
			name:     "XTRESTORE turns DECLRMM off and gives the columns back",
			in:       "\x1b[?69s\x1b[?69h\x1b[5;20s\x1b[?69r",
			wantPure: lr, wantGh: full,
		},
		{
			name:     "XTRESTORE of DECLRMM saved on keeps the columns",
			in:       "\x1b[?69h\x1b[?69s\x1b[5;20s\x1b[?69r",
			wantPure: lr, wantGh: lr,
		},
		{
			name:     "XTRESTORE without 69 leaves the columns",
			in:       "\x1b[?69h\x1b[5;20s\x1b[?7r",
			wantPure: lr, wantGh: lr,
		},
		{
			name:     "a full reset clears the saved DECLRMM",
			in:       "\x1b[?69h\x1b[?69s\x1bc\x1b[?69h\x1b[5;20s\x1b[?69r",
			wantPure: lr, wantGh: full,
		},
		{
			// The library ignores DECSTBM with more than two parameters
			// (stream.zig). The pure emulator obeys the first two, as it does
			// every CSI with surplus parameters.
			name:     "DECSTBM with three parameters",
			in:       "\x1b[3;10;5r",
			wantPure: tb, wantGh: full,
		},
		{
			name:     "DECSLRM with three parameters",
			in:       "\x1b[?69h\x1b[5;20;7s",
			wantPure: lr, wantGh: full,
		},
		{
			name:     "two parameters still set both",
			in:       "\x1b[3;10r\x1b[?69h\x1b[5;20s",
			wantPure: both, wantGh: both,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newDiffPair(t, w, h)
			p.write(t, []byte(tc.in))
			if got := p.pure.ScrollRegion(); got != tc.wantPure {
				t.Errorf("pure scroll region = %v, want %v", got, tc.wantPure)
			}
			if got := p.gh.ScrollRegion(); got != tc.wantGh {
				t.Errorf("ghostty copy of the region = %v, want %v", got, tc.wantGh)
			}
		})
	}
}
