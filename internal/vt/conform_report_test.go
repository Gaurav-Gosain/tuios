package vt_test

// Conformance for the sequences that answer back: DSR, CPR, DECXCPR, DA and
// DECRQM.
//
// These are the sequences where being wrong is invisible on screen and obvious
// to the guest. A program that asks where the cursor is and gets the answer
// transposed will draw its prompt in the wrong place forever after, and nothing
// in a screen-dump test would ever notice.
//
// Cases are drawn from esctest (iTerm2's conformance suite,
// tests/esctest/esctest/tests/{cpr,decrqm,dsr}.py), from the VT510 reference
// manual entries for CPR, DECXCPR, DECRQM and DA, and from what xterm's
// charproc.c actually sends for CASE_DSR.

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// reply runs input into a fresh emulator and returns whatever the emulator
// wrote back to the guest. It gives up after a moment rather than blocking,
// because "no reply at all" is an answer a case wants to assert.
func reply(t *testing.T, cols, rows int, in string) string {
	t.Helper()
	emu := vt.NewEmulator(cols, rows)
	if _, err := emu.WriteString(in); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := emu.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(2 * time.Second):
		return ""
	}
}

// TestConform_CursorPositionReport pins the shape of a CPR answer.
//
// CPR is CSI Pl ; Pc R, line first and column second (VT510 reference manual,
// CPR). esctest's tests/cpr.py builds its expectation the same way round, and
// every case here is one of its shapes.
func TestConform_CursorPositionReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		// Home is line 1, column 1, which is where esctest's tests/cpr.py
		// starts too.
		{"home", "\x1b[H\x1b[6n", "\x1b[1;1R"},

		// The row and the column have to be told apart, so every case below
		// puts the cursor somewhere the two numbers differ. A report that
		// swapped them would pass a case addressing 3;3.
		{"row 3 column 4", "\x1b[3;4H\x1b[6n", "\x1b[3;4R"},
		{"row 5 column 2", "\x1b[5;2H\x1b[6n", "\x1b[5;2R"},
		{"row 1 column 10", "\x1b[1;10H\x1b[6n", "\x1b[1;10R"},

		// After printing, the column is one past the text.
		{"after five characters", "hello\x1b[6n", "\x1b[1;6R"},

		// With DECOM set the report is
		// relative to the scroll region, so the guest reads back the same
		// numbers it would use to address the cursor.
		{"origin mode reports region-relative", "\x1b[10;20r\x1b[?6h\x1b[1;1H\x1b[6n", "\x1b[1;1R"},
		{"origin mode, third row of the region", "\x1b[10;20r\x1b[?6h\x1b[3;5H\x1b[6n", "\x1b[3;5R"},

		// Without DECOM the same cursor reports its absolute position.
		{"absolute when origin mode is off", "\x1b[10;20r\x1b[3;5H\x1b[6n", "\x1b[3;5R"},

		// DECXCPR is the same numbers behind a private marker. The page
		// number is omitted because this emulator has one page.
		{"DECXCPR", "\x1b[5;2H\x1b[?6n", "\x1b[?5;2R"},
		{"DECXCPR in origin mode", "\x1b[10;20r\x1b[?6h\x1b[3;5H\x1b[?6n", "\x1b[?3;5R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_CursorPositionReportUnderLeftMargin checks the horizontal half of
// origin mode. DECOM makes the left margin column 1, so a report has to
// subtract it the same way the row subtracts the top margin (xterm charproc.c,
// CASE_DSR: `if (xw->flags & ORIGIN) { row -= top_marg; col -= lft_marg; }`).
func TestConform_CursorPositionReportUnderLeftMargin(t *testing.T) {
	const in = "\x1b[?69h\x1b[10;40s\x1b[5;15r\x1b[?6h\x1b[1;1H\x1b[6n"
	if got, want := reply(t, 80, 24, in), "\x1b[1;1R"; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
}

// TestConform_DeviceStatusReport covers the non-cursor half of DSR.
func TestConform_DeviceStatusReport(t *testing.T) {
	// DSR 5 asks whether the terminal is in working order. CSI 0 n means yes
	// (VT510 reference manual, DSR-OS). xterm answers with the private form
	// CSI ? 0 n, which is what this emulator sends and what every guest
	// accepts, so the case pins that rather than the bare ANSI form.
	if got, want := reply(t, 80, 24, "\x1b[5n"), "\x1b[?0n"; got != want {
		t.Errorf("DSR 5 replied %q, want %q", got, want)
	}
}

// TestConform_DeviceAttributes pins the identity this emulator claims.
//
// DA1 decides what a guest is willing to send. A program reading the answer
// picks its feature set from it, so the reply has to be a well-formed DA
// response and it has to keep claiming the features that are actually here.
func TestConform_DeviceAttributes(t *testing.T) {
	da1 := reply(t, 80, 24, "\x1b[c")
	if !strings.HasPrefix(da1, "\x1b[?6") || !strings.HasSuffix(da1, "c") {
		t.Errorf("DA1 replied %q, which is not a VT220-or-later device attributes report", da1)
	}
	// 4 is sixel and 22 is colour. Both are implemented here, and a guest that
	// reads the list is entitled to use them.
	for _, want := range []string{";4;", ";22c"} {
		if !strings.Contains(da1, want) {
			t.Errorf("DA1 replied %q, which does not contain %q", da1, want)
		}
	}

	da2 := reply(t, 80, 24, "\x1b[>c")
	if !strings.HasPrefix(da2, "\x1b[>") || !strings.HasSuffix(da2, "c") {
		t.Errorf("DA2 replied %q, which is not a secondary device attributes report", da2)
	}
}

// silence reports whether the emulator wrote nothing back. Replies are written
// during the write that provokes them, so anything at all is already in the
// pipe by the time WriteString returns and a short wait is conclusive.
func silence(t *testing.T, in string) string {
	t.Helper()
	emu := vt.NewEmulator(80, 24)
	if _, err := emu.WriteString(in); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := emu.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(200 * time.Millisecond):
		return ""
	}
}

// TestConform_QueriesThisEmulatorLeavesUnanswered pins the requests that get no
// reply, so that staying silent is a decision on the record rather than a gap.
//
// Silence is safe for all three: each is a capability probe a guest sends with
// a timeout and falls back from. It is not free, though, because the guest pays
// that timeout. These are listed in the order they would be worth answering.
func TestConform_QueriesThisEmulatorLeavesUnanswered(t *testing.T) {
	for _, tc := range []struct{ name, in, why string }{
		{
			"XTVERSION", "\x1b[>0q",
			"a guest reads the terminal's name and version from it to decide which " +
				"extensions to use; answering would let one pick this emulator's " +
				"kitty graphics and keyboard support without probing for them",
		}, {
			"DA3, the terminal unit identifier", "\x1b[=c",
			"the answer is a made-up unit id, so there is nothing to be right about",
		}, {
			"DECRQSS for SGR", "\x1bP$qm\x1b\\",
			"answered with a refusal rather than silence, see TestConform_DECRQSS",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := silence(t, tc.in)
			if tc.name == "DECRQSS for SGR" {
				if got != "\x1bP0$r\x1b\\" {
					t.Errorf("reply = %q, want a refusal (%s)", got, tc.why)
				}
				return
			}
			if got != "" {
				t.Errorf("reply = %q, want none; if this now answers, delete the case (%s)", got, tc.why)
			}
		})
	}
}

// TestConform_DECRQSS covers requests for the current value of a setting.
//
// DECRQSS is DCS $ q <setting> ST, answered with DCS Ps $ r <value> ST where Ps
// is 1 for a setting the terminal reports and 0 for one it does not. The half
// that matters most is that there is always an answer. A guest that asks and
// hears nothing waits out a timeout before deciding, and some do that on every
// start, so silence costs a visible pause where a refusal costs nothing.
func TestConform_DECRQSS(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"the scroll region with none set",
			"\x1bP$qr\x1b\\",
			"\x1bP1$r1;24r\x1b\\",
		}, {
			"the scroll region as the guest set it",
			"\x1b[2;5r\x1bP$qr\x1b\\",
			"\x1bP1$r2;5r\x1b\\",
		}, {
			"the horizontal margins with none set",
			"\x1bP$qs\x1b\\",
			"\x1bP1$r1;80s\x1b\\",
		}, {
			"the horizontal margins as the guest set them",
			"\x1b[?69h\x1b[10;40s\x1bP$qs\x1b\\",
			"\x1bP1$r10;40s\x1b\\",
		}, {
			// A setting this emulator does not report still gets an answer.
			// Zero says so, and the guest stops waiting.
			"a setting this emulator does not report is refused, not ignored",
			"\x1bP$qm\x1b\\",
			"\x1bP0$r\x1b\\",
		}, {
			"a setting nobody defines is refused too",
			"\x1bP$qZZ\x1b\\",
			"\x1bP0$r\x1b\\",
		}, {
			"an empty request is refused",
			"\x1bP$q\x1b\\",
			"\x1bP0$r\x1b\\",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_DECRQM covers the mode-report matrix.
//
// DECRQM answers CSI ? Pm ; Ps $ y where Ps is 0 for a mode the terminal does
// not recognise, 1 set, 2 reset, 3 permanently set and 4 permanently reset
// (VT510 reference manual, DECRPM). esctest exercises the same values in
// tests/decrqm.py. Reporting 1 for a mode nothing acts on is worse than
// reporting 0: the guest changes what it sends on the strength of the answer.
func TestConform_DECRQM(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"DECAWM is set by default", "\x1b[?7$p", "\x1b[?7;1$y"},
		{"DECAWM reports reset after ?7l", "\x1b[?7l\x1b[?7$p", "\x1b[?7;2$y"},
		{"DECAWM reports set again after ?7h", "\x1b[?7l\x1b[?7h\x1b[?7$p", "\x1b[?7;1$y"},
		{"DECOM is reset by default", "\x1b[?6$p", "\x1b[?6;2$y"},
		{"DECOM reports set after ?6h", "\x1b[?6h\x1b[?6$p", "\x1b[?6;1$y"},
		{"DECTCEM reports reset after ?25l", "\x1b[?25l\x1b[?25$p", "\x1b[?25;2$y"},
		{"the alternate screen reports set while in it", "\x1b[?1049h\x1b[?1049$p", "\x1b[?1049;1$y"},
		{"synchronised output reports set", "\x1b[?2026h\x1b[?2026$p", "\x1b[?2026;1$y"},

		// A mode nobody defines has to report 0, not 2. Reporting reset says
		// the terminal knows the mode and has it off, which is a different
		// claim and one a guest acts on.
		{"an unknown private mode reports not recognised", "\x1b[?9999$p", "\x1b[?9999;0$y"},

		// The ANSI form has no private marker and is a separate table.
		// Reset, not "not recognised": this emulator implements IRM, and
		// telling a guest otherwise would send it down a fallback path it
		// does not need.
		{"ANSI IRM reports reset by default", "\x1b[4$p", "\x1b[4;2$y"},
		{"ANSI IRM reports set after CSI 4 h", "\x1b[4h\x1b[4$p", "\x1b[4;1$y"},
		{"ANSI LNM reports set after CSI 20 h", "\x1b[20h\x1b[20$p", "\x1b[20;1$y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}
