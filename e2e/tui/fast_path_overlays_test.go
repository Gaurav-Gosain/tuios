package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

var fullscreenFastPathOverlays = []struct {
	name  string
	open  func(t *testing.T, term *tuitest.Terminal)
	shows string
}{
	{"copies", func(t *testing.T, term *tuitest.Terminal) {
		if err := term.SendKeys(tuitest.Ctrl('b'), "u"); err != nil {
			t.Fatal(err)
		}
	}, "No copies."},
	{"glyph picker", openOverSettings("glyph set"), "↑↓ preview"},
	{"dock editor", openOverSettings("components"), "Dock layout"},
	{"section editor", openOverSettings("sections"), "Rail sections"},
}

func openOverSettings(query string) func(t *testing.T, term *tuitest.Terminal) {
	return func(t *testing.T, term *tuitest.Terminal) {
		t.Helper()
		const settingsFooter = "tab section"
		if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
			t.Fatal(err)
		}
		waitPaletteOpen(t, term, "for "+query)
		if err := term.SendKeys(query, tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitForText(settingsFooter, uiTimeout); err != nil {
			t.Fatalf("the palette did not open Settings on %q: %v\n%s", query, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		col, row := screenCell(t, term, "Settings")
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		mouseClick(t, term, 1, 20, tuitest.MouseLeft, 0)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), settingsFooter)
		}, uiTimeout); err != nil {
			t.Fatalf("a click away did not dismiss Settings over the %q picker: %v\n%s", query, err, term.Snapshot())
		}
	}
}

func waitFillsScreen(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		cols, _ := s.Size()
		top := []rune(s.Line(0))
		return len(top) >= cols && top[0] == '╭' && top[cols-1] == '╮'
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the pane does not fill the screen: %v\n%s", when, err, term.Snapshot())
	}
}

func shellHasKeys(t *testing.T, term *tuitest.Terminal, marker string) {
	t.Helper()
	runInShell(t, term, "echo "+marker+"-$((6*7))", marker+"-42", shellTimeout)
}

// TestPanelsDrawOverALoneFullscreenPane opens each panel over one pane that
// fills the screen, checks it is drawn, closes it with Esc and checks the
// shell has the keyboard back.
func TestPanelsDrawOverALoneFullscreenPane(t *testing.T) {
	for _, tc := range fullscreenFastPathOverlays {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			term := startHome(t, base, "fp")
			waitFillsScreen(t, term, "before "+tc.name)

			tc.open(t, term)
			if err := term.WaitForText(tc.shows, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the %s is not drawn over a lone fullscreen pane: %v\n%s", tc.name, err, term.Snapshot())
			}
			saveFrame(t, term, "fast-path-"+strings.ReplaceAll(tc.name, " ", "-"))

			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), tc.shows)
			}, uiTimeout); err != nil {
				t.Fatalf("esc did not close the %s: %v\n%s", tc.name, err, term.Snapshot())
			}
			shellHasKeys(t, term, "BACK")
			waitFillsScreen(t, term, "after "+tc.name)
			alive(t, term, "after the "+tc.name)
		})
	}
}

// TestDropDialogDrawsOverALoneFullscreenPane pastes a file's path into the
// only pane, which runs on another machine and fills the screen once tiled.
// The paste is held for the question whether to send the file, which has to
// be drawn, and Esc gives the keys back to the pane.
func TestDropDialogDrawsOverALoneFullscreenPane(t *testing.T) {
	base, _, term := copiesMachines(t, 64<<20)
	src := filepath.Join(base, "fast path drop.txt")
	if err := os.WriteFile(src, []byte("dropped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "faraway", "-s", "home", "--host", "build"); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitForText("build:faraway", uiTimeout); err != nil {
		t.Fatalf("the far window never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * insertGuard)
	if err := term.SendKeys("t"); err != nil {
		t.Fatal(err)
	}
	waitFillsScreen(t, term, "after tiling the far pane")
	enterTerminalMode(t, term)

	if err := term.Paste("'" + src + "' "); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Send fast path drop.txt", "to build?", "Paste the text")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the drop question is not drawn over a lone fullscreen pane: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "fast-path-drop-dialog")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Paste the text")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the drop question: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatal(err)
	}
	shellHasKeys(t, term, "FARBACK")
	alive(t, term, "after the drop question")
}
