package vt_test

// Conformance cases that run through vt.New, so the build checks whichever
// backend it compiles: the pure emulator by default, libghostty-vt under the
// ghostty tag. The ghostty-vt job runs them on the second.
//
// Each group matches a fix in libghostty-vt 1.0.2, so both backends are held
// to the same behaviour.

import (
	"fmt"
	"strings"
	"testing"

	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

// backendBugs maps a backend name (vt.Backend) to the reason a case fails
// there. A case with an entry for the backend under test must fail, and says
// so when it passes, so a fixed bug cannot stay on the list.
type backendBugs map[string]string

const (
	pure    = "pure-Go"
	ghostty = "ghostty"
)

// report fails the test with problems, or, when the case is a known bug on
// this backend, when there are none.
func report(t *testing.T, bugs backendBugs, problems []string) {
	t.Helper()
	bug := bugs[vt.Backend]
	if bug == "" {
		for _, p := range problems {
			t.Error(p)
		}
		return
	}
	if len(problems) == 0 {
		t.Errorf("this case is a known bug on the %s backend (%s) but passed; delete the entry", vt.Backend, bug)
	}
}

// Known bugs, by what causes them.
var (
	marksStay = backendBugs{
		pure:    "a prompt mark keeps the row it was printed on, so it does not move with the cells",
		ghostty: "a prompt mark keeps the row it was printed on, so it does not move with the cells",
	}
	reprintArmsWrap = backendBugs{ghostty: "the restore prints the cursor cell again to arm the wrap, which arms it only at the last column or the right margin"}
	pureSkinTone    = backendBugs{pure: "the pure emulator takes the width of x/ansi, which keeps a narrow base one cell wide"}
)

func newBackend(t *testing.T, cols, rows int, in string) vt.Terminal {
	t.Helper()
	term := vt.New(cols, rows)
	t.Cleanup(func() { _ = term.Close() })
	if _, err := term.Write([]byte(in)); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	return term
}

// softWraps returns one byte per screen row: 'w' when the row wraps into the
// next one, '.' when it does not.
func softWraps(t *testing.T, term vt.Terminal) string {
	t.Helper()
	var b strings.Builder
	for y := range term.Height() {
		wrapped, known := term.RowSoftWrapped(y)
		if !known {
			t.Fatalf("row %d: the backend does not know its wrap state", y)
		}
		if wrapped {
			b.WriteByte('w')
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

// screenText is the visible screen, one line per row, trailing blanks
// trimmed. A wide cluster prints once, in its lead column.
func screenText(term vt.Terminal) string {
	var b strings.Builder
	for y := range term.Height() {
		var line strings.Builder
		for x := 0; x < term.Width(); x++ {
			c := term.CellAt(x, y)
			switch {
			case c == nil || c.Content == "" && c.Width == 0:
			case c.Content == "":
				line.WriteByte(' ')
			default:
				line.WriteString(c.Content)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// TestBackendLineMovesKeepSoftWraps checks the wrap state of each row after
// IL, DL, SU, SD and RI. Rows that move together keep their wrap. A row stops
// wrapping when the row it wrapped into is taken away or pushed out of the
// region. The table is the one libghostty-vt 1.0.2 tests itself with, less
// the continuation flag, which the Terminal interface does not expose.
func TestBackendLineMovesKeepSoftWraps(t *testing.T) {
	// Four lines of 13 characters at 10 columns. Rows 1, 3, 5 and 7 wrap.
	const fill = "\x1b[H" +
		"aaaaaaaaaaAAA\r\n" +
		"bbbbbbbbbbBBB\r\n" +
		"ccccccccccCCC\r\n" +
		"ddddddddddDDD"
	for _, tc := range []struct {
		name, ops, want string
		bugs            backendBugs
	}{
		{"no operation", "", "w.w.w.w.", nil},
		{"IL above a pair", "\x1b[3;1H\x1b[L", "w..w.w..", nil},
		{"IL 3 above a pair", "\x1b[3;1H\x1b[3L", "w....w..", nil},
		{"IL past the bottom", "\x1b[3;1H\x1b[99L", "w.......", nil},
		{"DL 2 at a pair", "\x1b[3;1H\x1b[2M", "w.w.w...", nil},
		{"DL past the bottom", "\x1b[3;1H\x1b[99M", "w.......", nil},
		{"IL inside a pair", "\x1b[2;1H\x1b[L", "...w.w..", nil},
		{"IL inside a pair, region", "\x1b[3;6r\x1b[4;1H\x1b[L", "w.....w.", nil},
		{"DL the top of a pair", "\x1b[3;1H\x1b[M", "w..w.w..", nil},
		{"DL the bottom of a pair", "\x1b[2;1H\x1b[M", ".w.w.w..", nil},
		{"DL 2 across two pairs", "\x1b[4;1H\x1b[2M", "w...w...", nil},
		{"DL inside a pair, region", "\x1b[3;6r\x1b[4;1H\x1b[M", "w..w..w.", nil},
		{"IL, region holds whole pairs", "\x1b[3;6r\x1b[3;1H\x1b[L", "w..w..w.", nil},
		{"DL, region holds whole pairs", "\x1b[3;6r\x1b[3;1H\x1b[M", "w..w..w.", nil},
		{"IL, region edges split pairs", "\x1b[2;5r\x1b[2;1H\x1b[L", "...w..w.", nil},
		{"DL, region edges split pairs", "\x1b[2;5r\x1b[2;1H\x1b[M", ".w....w.", nil},
		{"SU, region edges split pairs", "\x1b[2;5r\x1b[S", ".w....w.", nil},
		{"SD, region edges split pairs", "\x1b[2;5r\x1b[T", "...w..w.", nil},
		{"RI, region edges split pairs", "\x1b[2;5r\x1b[2;1H\x1bM", "...w..w.", nil},
		// Left and right margins move cells, not rows, so every row keeps
		// its state. This is what xterm does.
		{"IL inside left and right margins", "\x1b[?69h\x1b[3;8s\x1b[3;3H\x1b[L", "w.w.w.w.", nil},
		{"DL inside left and right margins", "\x1b[?69h\x1b[3;8s\x1b[3;3H\x1b[M", "w.w.w.w.", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := newBackend(t, 10, 8, fill+tc.ops)
			var problems []string
			if got := softWraps(t, term); got != tc.want {
				problems = append(problems, fmt.Sprintf("wraps = %s, want %s\n%s", got, tc.want, screenText(term)))
			}
			report(t, tc.bugs, problems)
		})
	}
}

// TestBackendLineMovesClearSpacerHead takes away the row a wide character
// wrapped to. The row above no longer wraps, and the cell the wide character
// did not fit in is a plain blank again.
func TestBackendLineMovesClearSpacerHead(t *testing.T) {
	for _, op := range []string{"\x1b[L", "\x1b[M"} {
		t.Run(fmt.Sprintf("%q", op), func(t *testing.T) {
			// AAAA and a spacer in the last column, then the emoji and B.
			term := newBackend(t, 5, 4, "AAAA\U0001F600B\x1b[2;1H"+op)
			var problems []string
			if wrapped, _ := term.RowSoftWrapped(0); wrapped {
				problems = append(problems, "row 0 still wraps\n"+screenText(term))
			}
			if c := term.CellAt(4, 0); c == nil || c.Width != 1 || (c.Content != "" && c.Content != " ") {
				problems = append(problems, fmt.Sprintf("cell (4,0) = %+v, want a blank of width 1", c))
			}
			report(t, nil, problems)
		})
	}
}

// TestBackendLineMovesKeepPromptMark prints a prompt with OSC 133 A, then runs
// IL or DL inside left and right margins. Margins 3;10 leave the prompt where
// it is. Margins 1;8 hold it, so it moves with the cells. Either way the row
// the prompt is on afterwards must hold the prompt mark.
func TestBackendLineMovesKeepPromptMark(t *testing.T) {
	for _, tc := range []struct {
		seq, margins    string
		promptY, checkY int
		bugs            backendBugs
	}{
		{"\x1b[L", "\x1b[3;10s", 3, 3, nil},
		{"\x1b[L", "\x1b[1;8s", 2, 3, marksStay},
		{"\x1b[M", "\x1b[3;10s", 2, 2, nil},
		{"\x1b[M", "\x1b[1;8s", 3, 2, marksStay},
	} {
		t.Run(fmt.Sprintf("%q margins %q", tc.seq, tc.margins), func(t *testing.T) {
			in := fmt.Sprintf("\x1b[%d;1H\x1b]133;A\x07$ \x1b]133;C\x07", tc.promptY+1) +
				"\x1b[?69h" + tc.margins + "\x1b[3;3H" + tc.seq
			term := newBackend(t, 10, 5, in)
			if c := term.CellAt(0, tc.checkY); c == nil || c.Content != "$" {
				t.Fatalf("the prompt is not on row %d\n%s", tc.checkY, screenText(term))
			}
			var rows []int
			for _, m := range term.SemanticMarkers().Markers() {
				if m.Type == vt.MarkerPromptStart {
					rows = append(rows, m.AbsLine-term.ScrollbackLen())
				}
			}
			var problems []string
			if len(rows) != 1 || rows[0] != tc.checkY {
				problems = append(problems, fmt.Sprintf("prompt marks on rows %v, want [%d]", rows, tc.checkY))
			}
			report(t, tc.bugs, problems)
		})
	}
}

// TestBackendSnapshotKeepsPendingWrap takes a snapshot while a wrap is
// pending away from the last column, applies it to a new terminal, and then
// writes the same bytes to both. The two screens and cursors must match.
func TestBackendSnapshotKeepsPendingWrap(t *testing.T) {
	// Margins at columns 3 and 12, and ten characters from column 3. That
	// fills the margins and leaves a wrap pending at column 12.
	const margin = "\x1b[?69h\x1b[3;12s\x1b[1;3Hten chars!"
	for _, tc := range []struct {
		name, setup, after string
		// pending says a wrap is pending when the setup ends. A carriage
		// return clears it, and DECRC in the after bytes brings it back.
		pending bool
		bugs    backendBugs
	}{
		{"at the right margin", margin, "X", true, nil},
		{"saved with DECSC", margin + "\x1b7\r\n", "\x1b8X", false, nil},
		{"margins turned off", margin + "\x1b[?69l", "X", true, reprintArmsWrap},
		{"inside wider margins", margin + "\x1b7\x1b[1;20s\x1b8", "X", true, reprintArmsWrap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := newBackend(t, 24, 6, tc.setup)
			if src.CursorPendingWrap() != tc.pending {
				t.Fatalf("pending wrap after the setup = %v, want %v", !tc.pending, tc.pending)
			}
			st := snapshot.Capture(src, 24, 6, -1, 0)
			dst := vt.New(24, 6)
			t.Cleanup(func() { _ = dst.Close() })
			if err := snapshot.Apply(dst, st); err != nil {
				t.Fatalf("apply: %v", err)
			}
			var problems []string
			if dst.CursorPendingWrap() != tc.pending {
				problems = append(problems, fmt.Sprintf("pending wrap after the restore = %v, want %v", !tc.pending, tc.pending))
			}
			for _, term := range []vt.Terminal{src, dst} {
				if _, err := term.Write([]byte(tc.after)); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := screenText(dst), screenText(src); got != want {
				problems = append(problems, fmt.Sprintf("restored screen\n%s\nwant\n%s", got, want))
			}
			if got, want := dst.CursorPosition(), src.CursorPosition(); got != want {
				problems = append(problems, fmt.Sprintf("restored cursor %v, want %v", got, want))
			}
			report(t, tc.bugs, problems)
		})
	}
}

// TestBackendSkinToneWidensBase prints a base that is one cell wide on its
// own, then a skin tone modifier. The modifier asks for the emoji form, so
// the cluster is two cells wide, as VS16 makes it.
func TestBackendSkinToneWidensBase(t *testing.T) {
	bases := []rune{0x261D, 0x26F9, 0x270C, 0x270D, 0x1F3CB, 0x1F3CC, 0x1F574, 0x1F575, 0x1F590}
	seconds := []rune{0xFE0F, 0x1F3FB, 0x1F3FC, 0x1F3FD, 0x1F3FE, 0x1F3FF}
	for _, tc := range []struct {
		name       string
		x          int
		wraparound bool
		baseX      int
		baseY      int
		wide       bool
		cursor     string
	}{
		{"first column", 0, true, 0, 0, true, "2,0"},
		{"first column, no autowrap", 0, false, 0, 0, true, "2,0"},
		{"middle column", 1, true, 1, 0, true, "2,0"},
		{"middle column, no autowrap", 1, false, 1, 0, true, "2,0"},
		{"last column wraps", 2, true, 0, 1, true, "2,1"},
		{"last column, no autowrap", 2, false, 2, 0, false, "2,0"},
	} {
		for _, second := range seconds {
			// VS16 widens on both backends. A skin tone modifier does not
			// change a cluster that is narrow anyway.
			var bugs backendBugs
			if second != 0xFE0F && tc.wide {
				bugs = pureSkinTone
			}
			t.Run(fmt.Sprintf("%s %U", tc.name, second), func(t *testing.T) {
				var problems []string
				for _, base := range bases {
					mode := "h"
					if !tc.wraparound {
						mode = "l"
					}
					cluster := string(base) + string(second)
					in := "\x1b[?2027h\x1b[?7" + mode + fmt.Sprintf("\x1b[1;%dH", tc.x+1) + cluster
					term := newBackend(t, 3, 5, in)
					c := term.CellAt(tc.baseX, tc.baseY)
					want := 1
					if tc.wide {
						want = 2
					}
					if c == nil || c.Width != want {
						problems = append(problems, fmt.Sprintf("%U: cell (%d,%d) = %+v, want width %d", base, tc.baseX, tc.baseY, c, want))
						continue
					}
					if tc.wide && c.Content != cluster {
						problems = append(problems, fmt.Sprintf("%U: cell content %q, want %q", base, c.Content, cluster))
					}
					p := term.CursorPosition()
					if got := fmt.Sprintf("%d,%d", p.X, p.Y); got != tc.cursor {
						problems = append(problems, fmt.Sprintf("%U: cursor %s, want %s", base, got, tc.cursor))
					}
				}
				report(t, bugs, problems)
			})
		}
	}
}
