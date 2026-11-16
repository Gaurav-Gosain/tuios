package vt_test

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"

	"tuios.dev/vt"
)

// The command text an OSC 133 C mark captures is bounded.
//
// The failure modes this is written against:
//   - every C after one B reads every line from B to the cursor and keeps
//     the text, so a run of C marks after a long output costs lines times
//     marks in time and memory;
//   - one C after a very long span keeps all of it;
//   - the bound cuts what a shell sends in normal use: one B and one C per
//     command, a command of a few lines, a long command that soft-wraps.
//
// Both tests use NewWithScrollback, so they run against whichever backend the
// build selected.

// captureLimit is the most bytes of command text one C mark may keep. It is
// vt's maxCommandBytes.
const captureLimit = 16 << 10

func writeTerm(t *testing.T, term vt.Terminal, s string) {
	t.Helper()
	if _, err := term.Write([]byte(s)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// keptBytes is the bytes of command text the C marks in marks keep. Two marks
// that share one string count it once.
func keptBytes(marks []vt.SemanticMarker) (n, cMarks int) {
	seen := map[*byte]bool{}
	for _, m := range marks {
		if m.Type != vt.MarkerCommandExecuted {
			continue
		}
		cMarks++
		if p := unsafe.StringData(m.CapturedText); p != nil && !seen[p] {
			seen[p] = true
			n += len(m.CapturedText)
		}
	}
	return n, cMarks
}

// short is s cut for a failure message.
func short(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// TestCommandCaptureIsBounded writes one B, then lines of output, then 300 C
// marks. The C marks must keep no more than one capture's worth of text
// between them, and writing them must allocate no more than a small, fixed
// amount. 9000 lines is past the line bound. 60 lines is under it, so each C
// could read and keep all 60.
func TestCommandCaptureIsBounded(t *testing.T) {
	for _, lines := range []int{9000, 60} {
		t.Run(strconv.Itoa(lines)+" lines", func(t *testing.T) {
			term := vt.NewWithScrollback(80, 24, 10000)
			var marks []vt.SemanticMarker
			term.SetCallbacks(vt.Callbacks{
				SemanticMark: func(m vt.SemanticMarker) { marks = append(marks, m) },
			})
			writeTerm(t, term, "\x1b]133;A\x07$ \x1b]133;B\x07")
			writeTerm(t, term, strings.Repeat(strings.Repeat("x", 78)+"\r\n", lines))

			const cMarks = 300
			tail := strings.Repeat("\x1b]133;C\x07", cMarks)
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			writeTerm(t, term, tail)
			runtime.ReadMemStats(&after)

			kept, seen := keptBytes(marks)
			if seen != cMarks {
				t.Fatalf("saw %d C marks, want %d", seen, cMarks)
			}
			if kept > captureLimit {
				t.Errorf("%d C marks keep %d bytes of command text, want at most %d", cMarks, kept, captureLimit)
			}
			// Read once, the lines cost well under a megabyte. Read for
			// every mark they cost hundreds. The bound leaves the marks and
			// the callback room.
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 4<<20 {
				t.Errorf("writing %d C marks allocated %d bytes, want at most %d", cMarks, alloc, 4<<20)
			}
			if lines > 64 {
				if got := term.SemanticMarkers().Last(vt.MarkerCommandExecuted); got == nil || got.CapturedText != "" {
					t.Errorf("a C mark %d lines after B captured %d bytes (%q), want nothing", lines, len(got.CapturedText), short(got.CapturedText))
				}
			}
		})
	}
}

// TestCommandCaptureBytesAreBounded: a wide pane with a span under the line
// bound still keeps at most the byte bound, cut at a rune boundary.
func TestCommandCaptureBytesAreBounded(t *testing.T) {
	term := vt.NewWithScrollback(400, 24, 1000)
	var got string
	term.SetCallbacks(vt.Callbacks{
		SemanticMark: func(m vt.SemanticMarker) {
			if m.Type == vt.MarkerCommandExecuted {
				got = m.CapturedText
			}
		},
	})
	writeTerm(t, term, "\x1b]133;A\x07$ \x1b]133;B\x07")
	writeTerm(t, term, strings.Repeat(strings.Repeat("é", 398)+"\r\n", 60))
	writeTerm(t, term, "\x1b]133;C\x07")
	if len(got) > captureLimit {
		t.Errorf("a C mark on a 400 column pane kept %d bytes, want at most %d", len(got), captureLimit)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the captured text is cut inside a rune: %q", got[len(got)-4:])
	}
	if len(got) < captureLimit/2 {
		t.Errorf("a C mark kept %d bytes, want the text up to the bound", len(got))
	}
}

// TestCommandCaptureKeepsShortCommands holds normal shell use to what it was:
// one B and one C per command, captured whole.
func TestCommandCaptureKeepsShortCommands(t *testing.T) {
	captured := func(t *testing.T, width int, input string) []string {
		t.Helper()
		term := vt.NewWithScrollback(width, 10, 1000)
		var got []string
		term.SetCallbacks(vt.Callbacks{
			SemanticMark: func(m vt.SemanticMarker) {
				if m.Type == vt.MarkerCommandExecuted {
					got = append(got, m.CapturedText)
				}
			},
		})
		writeTerm(t, term, input)
		return got
	}
	prompt := "\x1b]133;A\x07$ \x1b]133;B\x07"
	run := "\r\n\x1b]133;C\x07"
	done := "\x1b]133;D;0\x07"

	t.Run("one command per prompt", func(t *testing.T) {
		got := captured(t, 40, prompt+"make test"+run+"ok\r\n"+done+prompt+"git status"+run+done)
		if strings.Join(got, "|") != "make test|git status" {
			t.Errorf("captured %q, want make test then git status", got)
		}
	})
	t.Run("a command of three lines", func(t *testing.T) {
		got := captured(t, 40, prompt+"for i in 1 2\r\n> do echo $i\r\n> done"+run+done)
		want := "for i in 1 2\n> do echo $i\n> done"
		if len(got) != 1 || got[0] != want {
			t.Errorf("captured %q, want %q", got, want)
		}
	})
	t.Run("a long command that soft-wraps", func(t *testing.T) {
		cmd := "echo " + strings.Repeat("abcdefghij", 12)
		got := captured(t, 20, prompt+cmd+run+done)
		if len(got) != 1 || got[0] != cmd {
			t.Errorf("captured %q, want %q", got, cmd)
		}
	})
	t.Run("a second C after one B keeps the first text", func(t *testing.T) {
		got := captured(t, 40, prompt+"make"+run+"\x1b]133;C\x07"+done)
		if strings.Join(got, "|") != "make|make" {
			t.Errorf("captured %q, want make twice", got)
		}
	})
}
