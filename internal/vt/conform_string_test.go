package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The sequences that carry a string payload: OSC, DCS and APC. They are the
// ones a guest can make arbitrarily long, and the ones where the terminator is
// a second thing to get wrong.

// oscCapture collects what an OSC made the emulator do, so a case can assert on
// the effect rather than on the absence of a crash.
type oscCapture struct {
	title       string
	iconName    string
	cwd         string
	clipboard   [][2]string
	notify      [][2]string
	progress    []progressReport
	clipQueried []string
}

type progressReport struct {
	state   vt.ProgressState
	percent int
}

func newOSCEmulator(t *testing.T, cols, rows int) (*vt.Emulator, *oscCapture) {
	t.Helper()
	c := &oscCapture{}
	emu := vt.NewEmulator(cols, rows)
	emu.SetCallbacks(vt.Callbacks{
		Title:            func(s string) { c.title = s },
		IconName:         func(s string) { c.iconName = s },
		WorkingDirectory: func(s string) { c.cwd = s },
		ClipboardSet:     func(sel, content string) { c.clipboard = append(c.clipboard, [2]string{sel, content}) },
		ClipboardQuery: func(sel string) string {
			c.clipQueried = append(c.clipQueried, sel)
			return "Y2xpcA=="
		},
		Notify: func(title, body string) { c.notify = append(c.notify, [2]string{title, body}) },
		Progress: func(state vt.ProgressState, percent int) {
			c.progress = append(c.progress, progressReport{state, percent})
		},
	})
	return emu, c
}

func TestConform_OSCTitleAndDirectory(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		title    string
		iconName string
		cwd      string
	}{
		{
			name:     "OSC 0 sets both the title and the icon name",
			in:       "\x1b]0;hello\x07",
			title:    "hello",
			iconName: "hello",
		},
		{
			name:  "OSC 2 sets only the title",
			in:    "\x1b]2;hello\x07",
			title: "hello",
		},
		{
			name:     "OSC 1 sets only the icon name",
			in:       "\x1b]1;hello\x07",
			iconName: "hello",
		},
		{
			// ST is as valid a terminator as BEL, and the one a program that
			// cares about correctness uses.
			name:  "a string terminator ends an OSC",
			in:    "\x1b]2;hello\x1b\\",
			title: "hello",
		},
		{
			name:  "an empty payload clears the title",
			in:    "\x1b]2;set\x07\x1b]2;\x07",
			title: "",
		},
		{
			name:  "a title may hold wide characters",
			in:    "\x1b]2;世界\x07",
			title: "世界",
		},
		{
			name: "OSC 7 reports the working directory",
			in:   "\x1b]7;file://host/tmp/x\x07",
			cwd:  "file://host/tmp/x",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emu, c := newOSCEmulator(t, 20, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if c.title != tc.title {
				t.Errorf("title = %q, want %q", c.title, tc.title)
			}
			if tc.iconName != "" && c.iconName != tc.iconName {
				t.Errorf("icon name = %q, want %q", c.iconName, tc.iconName)
			}
			if tc.cwd != "" && c.cwd != tc.cwd {
				t.Errorf("working directory = %q, want %q", c.cwd, tc.cwd)
			}
		})
	}
}

// TestConform_OSC8Hyperlinks checks that a link attaches to the cells written
// while it is open and to nothing else. A link that leaks past its close turns
// the rest of the line into part of somebody's URL.
func TestConform_OSC8Hyperlinks(t *testing.T) {
	const url = "https://example.invalid/x"

	runConform(t, []conformCase{
		{
			name: "a link covers the cells written inside it",
			cols: 10,
			in:   "a\x1b]8;;" + url + "\x07bc\x1b]8;;\x07d",
			want: "abcd",
			cells: []cellWant{
				{x: 0, y: 0, content: "a", link: ptr("")},
				{x: 1, y: 0, content: "b", link: ptr(url)},
				{x: 2, y: 0, content: "c", link: ptr(url)},
				{x: 3, y: 0, content: "d", link: ptr("")},
			},
		},
		{
			name: "a link ends at a string terminator too",
			cols: 10,
			in:   "\x1b]8;;" + url + "\x1b\\b\x1b]8;;\x1b\\c",
			want: "bc",
			cells: []cellWant{
				{x: 0, y: 0, content: "b", link: ptr(url)},
				{x: 1, y: 0, content: "c", link: ptr("")},
			},
		},
		{
			name: "a link carries its id parameter",
			cols: 10,
			in:   "\x1b]8;id=n1;" + url + "\x07b\x1b]8;;\x07",
			want: "b",
			cells: []cellWant{
				{x: 0, y: 0, content: "b", link: ptr(url)},
			},
		},
		{
			name: "a link survives a wrap onto the next row",
			cols: 4,
			in:   "\x1b]8;;" + url + "\x07abcdef\x1b]8;;\x07",
			want: "abcd\nef",
			cells: []cellWant{
				{x: 3, y: 0, content: "d", link: ptr(url)},
				{x: 0, y: 1, content: "e", link: ptr(url)},
			},
		},
	})
}

func TestConform_OSCClipboardAndNotifications(t *testing.T) {
	t.Run("OSC 52 writes the clipboard", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]52;c;aGVsbG8=\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.clipboard) != 1 {
			t.Fatalf("clipboard writes = %d, want 1", len(c.clipboard))
		}
		if got := c.clipboard[0]; got[0] != "c" {
			t.Errorf("selection = %q, want %q", got[0], "c")
		}
	})

	t.Run("OSC 52 with a question mark reads the clipboard", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]52;c;?\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.clipQueried) != 1 {
			t.Fatalf("clipboard queries = %d, want 1", len(c.clipQueried))
		}
		// The answer goes back to the guest, which is the whole point of a
		// query: a reply that never reaches the program is a hang.
		buf := make([]byte, 256)
		n, _ := emu.Read(buf)
		if !strings.Contains(string(buf[:n]), "52;c;") {
			t.Errorf("the reply to a clipboard query was %q, which does not answer it", buf[:n])
		}
	})

	t.Run("OSC 9 raises a notification", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]9;the build finished\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.notify) != 1 {
			t.Fatalf("notifications = %d, want 1", len(c.notify))
		}
	})

	t.Run("OSC 777 raises a notification with a title and a body", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]777;notify;build;finished\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.notify) != 1 {
			t.Fatalf("notifications = %d, want 1", len(c.notify))
		}
		if got := c.notify[0]; got[0] != "build" || got[1] != "finished" {
			t.Errorf("notification = %q / %q, want %q / %q", got[0], got[1], "build", "finished")
		}
	})

	t.Run("OSC 9;4 reports progress", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]9;4;1;40\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.progress) != 1 {
			t.Fatalf("progress reports = %d, want 1", len(c.progress))
		}
		if c.progress[0].percent != 40 {
			t.Errorf("progress = %d%%, want 40%%", c.progress[0].percent)
		}
	})

	t.Run("an OSC 9;4 percentage past a hundred is clamped", func(t *testing.T) {
		emu, c := newOSCEmulator(t, 20, 3)
		if _, err := emu.WriteString("\x1b]9;4;1;400\x07"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(c.progress) != 1 {
			t.Fatalf("progress reports = %d, want 1", len(c.progress))
		}
		if p := c.progress[0].percent; p > 100 {
			t.Errorf("progress = %d%%, which is not a percentage", p)
		}
	})
}

// TestConform_StringSequencesDoNotReachTheScreen is the property that matters
// for every payload-carrying sequence: whatever is inside one, none of it is
// text. A terminator the emulator fails to recognise turns the rest of the
// stream into visible garbage, which is what an unterminated OSC from a
// crashing program looks like.
func TestConform_StringSequencesDoNotReachTheScreen(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "an OSC payload is not printed",
			cols: 12,
			in:   "A\x1b]0;not this\x07B",
			want: "AB",
		},
		{
			// An ESC inside an OSC ends the string. What follows is a fresh
			// sequence, not text, and the emulator says it does not know it.
			name:      "an OSC payload holding an escape is not printed",
			cols:      12,
			in:        "A\x1b]0;a\x1bb\x07B",
			want:      "AB",
			unhandled: true,
		},
		{
			name:      "a DCS payload is not printed",
			cols:      12,
			in:        "A\x1bP0;1|payload\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			name: "an APC payload is not printed",
			cols: 12,
			in:   "A\x1b_Ga=d\x1b\\B",
			want: "AB",
		},
		{
			name:      "a PM payload is not printed",
			cols:      12,
			in:        "A\x1b^private\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			name:      "an SOS payload is not printed",
			cols:      12,
			in:        "A\x1bXstart\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			// GNU screen's passthrough wraps the inner sequence as-is, so the
			// inner ESC is a single byte and the DCS ends at the first ST.
			// Nothing here unwraps it; what matters for now is that none of it
			// is drawn. See TestConform_Passthrough.
			name:      "screen passthrough hides the inner sequence",
			cols:      12,
			in:        "A\x1bP\x1b[31m\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			// tmux requires every inner ESC doubled, so an inner sequence
			// arrives as ESC ESC and must not terminate the DCS early.
			name:      "tmux passthrough hides the inner sequence",
			cols:      12,
			in:        "A\x1bPtmux;\x1b\x1b[31m\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			name:      "a tmux passthrough carrying a graphics payload is hidden",
			cols:      12,
			in:        "A\x1bPtmux;\x1b\x1b_Gf=24,s=1,v=1;AAAA\x1b\x1b\\\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			// An oversized payload has to be bounded somewhere, and wherever
			// that is, none of it may spill onto the screen.
			name: "an oversized OSC payload is not printed",
			cols: 12,
			in:   "A\x1b]0;" + strings.Repeat("x", 1<<16) + "\x07B",
			want: "AB",
		},
		{
			name:      "an oversized DCS payload is not printed",
			cols:      12,
			in:        "A\x1bP0;1|" + strings.Repeat("x", 1<<16) + "\x1b\\B",
			want:      "AB",
			unhandled: true,
		},
		{
			// A payload that never ends swallows what follows, which is
			// correct: the terminal cannot know the program crashed. What it
			// must not do is print the payload.
			name: "an unterminated OSC swallows what follows",
			cols: 12,
			in:   "A\x1b]0;never ends and then B",
			want: "A",
		},
	})
}

// TestConform_Passthrough records what the emulator does with the two
// multiplexer passthrough wrappings. Neither is implemented, and the two come
// out differently anyway, which is the point of pinning it.
//
// GNU screen's wrapping puts the inner sequence in a DCS as it stands. The
// parser keeps it all inside the string and discards it, so the inner sequence
// never happens.
//
// tmux's wrapping requires every inner ESC doubled. The parser abandons the DCS
// at the first ESC of the pair and the second one starts a fresh sequence, so
// the inner sequence executes. That is the right outcome by accident rather
// than by rule: nothing here halves the doubled escapes, it is just that
// aborting on one of the two leaves exactly one behind. The malformed form,
// with the inner ESC left single, executes too, where tmux's rule says it
// should not.
//
// What holds across all three, and is the property that matters for a terminal
// that does not implement passthrough, is that no part of a payload is ever
// drawn as text.
func TestConform_Passthrough(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// executes says whether the wrapped sequence takes effect.
		executes bool
	}{
		{
			name: "screen wrapping is swallowed whole",
			in:   "\x1bP\x1b[31m\x1b\\",
		},
		{
			name:     "tmux wrapping lets the inner sequence through",
			in:       "\x1bPtmux;\x1b\x1b[31m\x1b\\",
			executes: true,
		},
		{
			name:     "tmux wrapping with a single inner escape also lets it through",
			in:       "\x1bPtmux;\x1b[31m\x1b\\",
			executes: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(12, 2)
			if _, err := emu.WriteString("A" + tc.in + "B"); err != nil {
				t.Fatalf("write: %v", err)
			}

			// Nothing may be drawn from inside a payload, whatever else happens.
			if got := dumpScreen(emu); got != "AB" {
				t.Fatalf("screen = %q, want %q: part of the payload reached the screen", got, "AB")
			}

			// The inner sequence set a foreground colour, so B carries one if
			// and only if it ran.
			c := emu.CellAt(1, 0)
			if c == nil {
				t.Fatal("no cell where B should be")
			}
			ran := c.Style.Fg != nil
			if ran != tc.executes {
				if tc.executes {
					t.Errorf("the wrapped sequence no longer takes effect; passthrough " +
						"handling has changed and this test should describe the new rules")
				} else {
					t.Errorf("the wrapped sequence now takes effect; passthrough handling " +
						"has changed and this test should describe the new rules")
				}
			}
		})
	}
}
