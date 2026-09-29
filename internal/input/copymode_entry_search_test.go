package input

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Copy mode entry position and directional search (discussion #233). The
// window is a real emulator, so the cursor, the scrollback and the matches
// are the cells a person would see.

// entryOS is a client with one real pane holding raw output, focused.
func entryOS(t *testing.T, output string) (*app.OS, *terminal.Window) {
	t.Helper()
	w := multiPaneWindow(t, "cm-entry", "shell")
	w.WriteOutput([]byte(output))
	o := &app.OS{Settings: config.Global, Mode: app.TerminalMode, RemoteClient: true}
	o.Windows = []*terminal.Window{w}
	o.FocusedWindow = 0
	return o, w
}

// numberedOutput is n lines "row 00" to "row n-1", then a "$ " prompt with the
// cursor after it. With 80 lines and a 22-row pane every "row 4x" line is in the scrollback.
func numberedOutput(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "row %02d\r\n", i)
	}
	b.WriteString("$ ")
	return b.String()
}

// cursorLine is the text of the buffer line the copy cursor is on.
func cursorLine(t *testing.T, w *terminal.Window) string {
	t.Helper()
	w.RLockIO()
	defer w.RUnlockIO()
	absY := getAbsoluteY(w.CopyMode, w)
	sb := w.ScrollbackLen()
	if absY < sb {
		return strings.TrimRight(extractLineTextFromCells(w.ScrollbackLine(absY)), " ")
	}
	return strings.TrimRight(extractScreenLineText(w.Terminal, absY-sb), " ")
}

func typeCopyKeys(o *app.OS, w *terminal.Window, keys ...string) {
	for _, k := range keys {
		if k == "esc" || k == "enter" || k == "backspace" {
			HandleCopyModeKey(multiKey(k), o, w)
			continue
		}
		for _, r := range k {
			HandleCopyModeKey(multiKey(string(r)), o, w)
		}
	}
}

func lastNotification(o *app.OS) string {
	if len(o.Notifications) == 0 {
		return ""
	}
	return o.Notifications[len(o.Notifications)-1].Message
}

func TestCopyModeEntersAtTerminalCursor(t *testing.T) {
	tests := []struct {
		name   string
		output string
		x, y   int
	}{
		// A short session: the prompt is on row 3 (the fixture starts with an
		// empty line), well above the middle.
		{"prompt near the top", "one\r\ntwo\r\n$ ", 2, 3},
		// A full screen: the prompt is on the last row.
		{"prompt on the bottom row", numberedOutput(80), 2, 21},
		// A program moved the cursor to the middle of the screen.
		{"cursor mid-screen", "a\r\nb\r\n\x1b[8;15H", 14, 7},
		// A full-screen program on the alternate screen: its cursor, not the
		// shell's.
		{"alternate screen", "one\r\n$ \x1b[?1049h\x1b[18;30H", 29, 17},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, w := entryOS(t, tc.output)
			o.EnterCopyModeFocused()
			if !w.CopyModeVisible() {
				t.Fatal("copy mode did not start")
			}
			if got := [2]int{w.CopyMode.CursorX, w.CopyMode.CursorY}; got != [2]int{tc.x, tc.y} {
				t.Errorf("copy cursor starts at (x=%d, y=%d), want the terminal cursor (x=%d, y=%d)",
					got[0], got[1], tc.x, tc.y)
			}
		})
	}
}

func TestCopyModeEntryCenterSetting(t *testing.T) {
	o, w := entryOS(t, "one\r\ntwo\r\n$ ")
	o.Settings.CopyEntry = config.CopyEntryCenter
	o.EnterCopyModeFocused()
	if w.CopyMode.CursorX != 0 || w.CopyMode.CursorY != w.ContentHeight()/2 {
		t.Errorf("copy_entry = center starts at (x=%d, y=%d), want (0, %d)",
			w.CopyMode.CursorX, w.CopyMode.CursorY, w.ContentHeight()/2)
	}
}

func TestCopyModeEntryConfig(t *testing.T) {
	if config.DefaultSettings().CopyEntry != config.CopyEntryCursor {
		t.Error("the default copy_entry is not cursor")
	}
}

// In multi copy mode each pane starts on its own cursor, not on one row for
// all of them.
func TestMultiCopyEntersEachPaneAtItsCursor(t *testing.T) {
	o, ws := multiCopyOS(t)
	if !o.EnterMultiCopyMode() {
		t.Fatal("multi copy mode did not start")
	}
	// multiCopyOS writes 3, 4 and 3 lines, each ending in a newline.
	for i, want := range []int{3, 4, 3} {
		if got := ws[i].CopyMode.CursorY; got != want {
			t.Errorf("pane %s starts on row %d, want its cursor row %d", ws[i].ID, got, want)
		}
	}
}

// ? from the prompt finds the nearest match above it. Every key typed into
// the prompt searches again from where the prompt opened, so "row 4" lands on
// "row 49" and not on a line the earlier letters jumped past.
func TestCopyModeBackwardSearchFromPrompt(t *testing.T) {
	o, w := entryOS(t, numberedOutput(80))
	o.EnterCopyModeFocused()
	typeCopyKeys(o, w, "?", "row 4", "enter")
	if got := cursorLine(t, w); got != "row 49" {
		t.Fatalf("?row 4 from the prompt lands on %q, want %q", got, "row 49")
	}
	if !w.CopyMode.SearchBackward {
		t.Fatal("? did not record a backward search")
	}

	// n follows the search: up.
	typeCopyKeys(o, w, "n")
	if got := cursorLine(t, w); got != "row 48" {
		t.Errorf("n after ? moves to %q, want %q", got, "row 48")
	}
	// N goes the other way: down.
	typeCopyKeys(o, w, "N")
	if got := cursorLine(t, w); got != "row 49" {
		t.Errorf("N after ? moves to %q, want %q", got, "row 49")
	}
	// A count repeats n.
	typeCopyKeys(o, w, "3n")
	if got := cursorLine(t, w); got != "row 46" {
		t.Errorf("3n after ? moves to %q, want %q", got, "row 46")
	}
}

// / from the prompt has nothing below it, so it wraps to the oldest match in
// the scrollback. n then goes down and N up, and N past the top wraps to the
// bottom and says so.
func TestCopyModeForwardSearchWrapsIntoScrollback(t *testing.T) {
	o, w := entryOS(t, numberedOutput(80))
	o.EnterCopyModeFocused()
	typeCopyKeys(o, w, "/", "row 4", "enter")
	if got := cursorLine(t, w); got != "row 40" {
		t.Fatalf("/row 4 from the prompt lands on %q, want the wrapped %q", got, "row 40")
	}
	if w.CopyMode.ScrollOffset == 0 {
		t.Error("the match is in the scrollback but the view did not scroll")
	}
	typeCopyKeys(o, w, "n")
	if got := cursorLine(t, w); got != "row 41" {
		t.Errorf("n after / moves to %q, want %q", got, "row 41")
	}
	typeCopyKeys(o, w, "N", "N")
	if got := cursorLine(t, w); got != "row 49" {
		t.Errorf("N past the first match moves to %q, want the wrapped %q", got, "row 49")
	}
	if msg := lastNotification(o); !strings.Contains(msg, "top") {
		t.Errorf("wrapping past the top said %q", msg)
	}
}

// n and N run from the cursor, so a match is found from wherever the cursor
// was moved to after the search.
func TestCopyModeNextMatchRunsFromCursor(t *testing.T) {
	o, w := entryOS(t, numberedOutput(80))
	o.EnterCopyModeFocused()
	typeCopyKeys(o, w, "?", "row 5", "enter") // lands on row 59
	typeCopyKeys(o, w, "5k")                  // up to row 54
	typeCopyKeys(o, w, "n")                   // up from row 54
	if got := cursorLine(t, w); got != "row 53" {
		t.Errorf("n from row 54 after ? moves to %q, want %q", got, "row 53")
	}
}

// Each key typed into the prompt searches again from where the prompt opened.
// Searching from the last match instead walks past the line the whole query
// names: ?car would stop on "c" at car, then "ca" before it at cat, then "car"
// before that at cart.
func TestCopyModeIncrementalSearchKeepsOrigin(t *testing.T) {
	out := "cart\r\ncat\r\ncar\r\n$ "
	for _, tc := range []struct{ prompt, want string }{
		{"?", "car"},
		{"/", "cart"}, // nothing below the prompt: wraps to the top
	} {
		o, w := entryOS(t, out)
		o.EnterCopyModeFocused()
		typeCopyKeys(o, w, tc.prompt, "car")
		if got := cursorLine(t, w); got != tc.want {
			t.Errorf("%scar lands on %q, want %q", tc.prompt, got, tc.want)
		}
	}
}

// Esc in the prompt gives the search up and puts the cursor back.
func TestCopyModeSearchEscRestoresCursor(t *testing.T) {
	o, w := entryOS(t, numberedOutput(80))
	o.EnterCopyModeFocused()
	x, y := w.CopyMode.CursorX, w.CopyMode.CursorY
	typeCopyKeys(o, w, "?", "row 1", "esc")
	if w.CopyMode.CursorX != x || w.CopyMode.CursorY != y || w.CopyMode.ScrollOffset != 0 {
		t.Errorf("esc left the cursor at (x=%d, y=%d, scroll=%d), want (x=%d, y=%d, scroll=0)",
			w.CopyMode.CursorX, w.CopyMode.CursorY, w.CopyMode.ScrollOffset, x, y)
	}
}

// The search actions enter copy mode and open the prompt in one key, bound
// under the leader or in the global table.
func TestCopyModeSearchActions(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		backward bool
	}{
		{"backward", config.ActionCopyModeSearchBackward, true},
		{"forward", config.ActionCopyModeSearchForward, false},
	}
	for _, tc := range tests {
		t.Run(tc.name+" under the leader", func(t *testing.T) {
			o := osWithBindings(t, func(k *config.KeybindingsConfig) {
				k.PrefixMode[tc.action] = []string{"g"}
			})
			w := multiPaneWindow(t, "cm-act", "shell", "error one", "ok", "error two")
			w.Workspace = o.CurrentWorkspace
			o.Windows = []*terminal.Window{w}
			o.FocusedWindow = 0
			o.Mode = app.TerminalMode
			o.PrefixActive = true
			HandlePrefixCommand(press("g"), o)
			if !w.CopyModeVisible() || w.CopyMode.State != terminal.CopyModeSearch {
				t.Fatalf("%s did not open copy mode with the search prompt", tc.action)
			}
			if w.CopyMode.SearchBackward != tc.backward {
				t.Errorf("%s opened a search with backward=%v", tc.action, w.CopyMode.SearchBackward)
			}
			// The next keys are the query.
			typeCopyKeys(o, w, "error", "enter")
			want := "error two"
			if !tc.backward {
				want = "error one" // nothing below the prompt: wraps to the top
			}
			if got := cursorLine(t, w); got != want {
				t.Errorf("the search lands on %q, want %q", got, want)
			}
		})
	}

	t.Run("global binding in terminal mode", func(t *testing.T) {
		o := osWithBindings(t, func(k *config.KeybindingsConfig) {
			k.Global[config.ActionCopyModeSearchBackward] = []string{"alt+/"}
		})
		w := multiPaneWindow(t, "cm-glob", "shell", "x")
		w.Workspace = o.CurrentWorkspace
		o.Windows = []*terminal.Window{w}
		o.FocusedWindow = 0
		o.Mode = app.TerminalMode
		HandleKeyPress(tea.KeyPressMsg{Code: '/', Mod: tea.ModAlt}, o)
		if !w.CopyModeVisible() || w.CopyMode.State != terminal.CopyModeSearch || !w.CopyMode.SearchBackward {
			t.Fatal("alt+/ bound to copy_mode_search_backward did not open a backward search")
		}
	})

	t.Run("already in copy mode keeps the cursor", func(t *testing.T) {
		o, w := entryOS(t, numberedOutput(80))
		o.EnterCopyModeFocused()
		typeCopyKeys(o, w, "gg")
		o.EnterCopyModeSearch(true)
		if w.CopyMode.ScrollOffset == 0 {
			t.Error("the search action reset a copy mode session that was already open")
		}
	})

	t.Run("multi copy mode opens the prompt in every pane", func(t *testing.T) {
		o, ws := multiCopyOS(t)
		o.EnterCopyModeSearch(true)
		for _, w := range ws[:3] {
			if !w.InCopyMode() || w.CopyMode.State != terminal.CopyModeSearch || !w.CopyMode.SearchBackward {
				t.Errorf("pane %s is not at a backward search prompt", w.ID)
			}
		}
	})
}

// With more matches than a search keeps, the ones kept are the newest, so ?
// from the prompt still finds the nearest match above it.
func TestCopyModeSearchLimitKeepsNewestMatches(t *testing.T) {
	var b strings.Builder
	for i := range maxSearchMatches + 500 {
		fmt.Fprintf(&b, "hit %04d\r\n", i)
	}
	b.WriteString("$ ")
	o, w := entryOS(t, b.String())
	o.EnterCopyModeFocused()
	typeCopyKeys(o, w, "?", "hit", "enter")
	want := fmt.Sprintf("hit %04d", maxSearchMatches+499)
	if got := cursorLine(t, w); got != want {
		t.Fatalf("?hit from the prompt lands on %q, want the newest match %q", got, want)
	}
	if n := len(w.CopyMode.SearchMatches); n != maxSearchMatches {
		t.Errorf("the search kept %d matches, want %d", n, maxSearchMatches)
	}
	for i := 1; i < len(w.CopyMode.SearchMatches); i++ {
		if w.CopyMode.SearchMatches[i].Line <= w.CopyMode.SearchMatches[i-1].Line {
			t.Fatalf("the matches are not in buffer order at %d", i)
		}
	}
}
