package tuie2e

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
)

// The opt-in explorers: tuios help -i, tuios config browse and tuios keybinds
// browse, each run in a real terminal and driven by keys and the mouse.
//
// How these could pass wrongly, written down first:
//   - An explorer that draws its first frame and ignores input passes "it
//     opens". So each test searches and checks that rows go away, and moves
//     the cursor and checks the detail pane follows it.
//   - A search that filters nothing passes "the row is there". So the test
//     also checks that a row the search excludes is gone.
//   - An explorer that ignores q passes when the harness kills it. So each
//     test waits for the process to exit by itself with status 0.
//   - "Not reachable without the flag" passes if the plain command fails.
//     So the plain command runs on a terminal, must exit 0 by itself, never
//     switch to the alternate screen, and print what it prints with no
//     terminal at all.
//   - A set from the explorer that only changes the explorer's own row passes
//     a check of the screen. So get-config reads the value back from the
//     daemon.

// explorerShot saves the frame as text and PNG in the test's artifact
// directory.
func explorerShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, name)
	t.Logf("frame %s saved under %s", name, dir)
}

// ansiCodes matches a CSI sequence.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// startExplorer runs tuios with args in a terminal of its own, against the
// isolation root base.
func startExplorer(t *testing.T, base string, args ...string) *tuitest.Terminal {
	t.Helper()
	// animations true keeps the harness from adding --no-animations, a flag
	// of the root command that a subcommand does not take.
	return startIn(t, base, startOpts{cols: 110, rows: 34, animations: true, args: args})
}

// explorerExits waits for the explorer to leave by itself with status 0.
func explorerExits(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	code, err := term.WaitExit(10 * time.Second)
	if err != nil {
		t.Fatalf("ASSERTION: %s did not exit by itself: %v\n%s", what, err, term.Snapshot())
	}
	if code != 0 {
		t.Fatalf("ASSERTION: %s exited with status %d\n%s", what, code, term.Snapshot())
	}
}

// explorerShows reports whether the screen shows every one of want.
func explorerShows(want ...string) func(tuitest.Screen) bool {
	return func(s tuitest.Screen) bool {
		text := s.Text()
		for _, w := range want {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true
	}
}

// assertPlainOnTTY runs a command in a terminal and checks that it is the
// plain command: it exits 0 by itself, never shows the alternate screen,
// and ends with the lines it prints with no terminal at all.
func assertPlainOnTTY(t *testing.T, base string, args ...string) {
	t.Helper()
	plain, err := tuiosCLI(t, base, args...)
	if err != nil {
		t.Fatalf("tuios %s with no terminal: %v\n%s", strings.Join(args, " "), err, plain)
	}
	term := startIn(t, base, startOpts{cols: 200, rows: 60, animations: true, args: args})
	alt := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if term.TermState().Mode(1049) {
			alt = true
		}
		if _, done := term.ExitCode(); done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	explorerExits(t, term, "tuios "+strings.Join(args, " ")+" on a terminal")
	if alt {
		t.Errorf("ASSERTION: tuios %s switched to the alternate screen on a terminal", strings.Join(args, " "))
	}
	// The screen keeps the end of what the command printed, so the end is
	// what is compared.
	screen := term.Snapshot()
	lines := strings.Split(strings.TrimSpace(plain), "\n")
	lines = lines[max(len(lines)-8, 0):]
	for _, line := range lines {
		// The colour codes a listing writes are not text on the screen.
		line = strings.TrimSpace(ansiCodes.ReplaceAllString(line, ""))
		if line == "" || len(line) > 150 {
			continue
		}
		if !strings.Contains(screen, line) {
			t.Errorf("ASSERTION: tuios %s on a terminal lacks the line %q that it prints with no terminal\n%s", strings.Join(args, " "), line, screen)
			break
		}
	}
}

// TestHelpExplorer opens tuios help -i, searches for ship, reads its detail,
// moves with the mouse wheel and a click, and leaves with q. Then tuios help
// on a terminal is checked to be the plain help.
//
// Negative control (NEGATIVE_CONTROLS.md): with the interactive branch cut
// from the help command's Run, help -i prints the plain help and exits, and
// the wait for the explorer's title fails.
func TestHelpExplorer(t *testing.T) {
	base := t.TempDir()
	term := startExplorer(t, base, "help", "-i")
	if err := term.WaitFor(explorerShows("tuios commands", "checkpoint", "Press / to search"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: help -i did not open the explorer: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "help-open")

	if err := term.Type("/ship"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Search: ship") && strings.Contains(text, "Commit, merge, push") && !strings.Contains(text, "xpanes")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the search for ship did not narrow the list: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Usage: tuios ship"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the detail pane does not show ship's usage: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "help-search-ship")

	// A click on the second row moves the cursor there, and the detail
	// follows it.
	if err := term.Type("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("checkpoint"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Usage: tuios checkpoint"), uiTimeout); err != nil {
		t.Fatalf("the search for checkpoint: %v\n%s", err, term.Snapshot())
	}
	col, row, ok := tuitest.Find(term.Screen(), " checkpoint list ")
	if !ok {
		t.Fatalf("no list row under checkpoint:\n%s", term.Snapshot())
	}
	if err := term.SendMouse(tuitest.MouseEvent{Col: col + 2, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MousePress}); err != nil {
		t.Fatal(err)
	}
	_ = term.SendMouse(tuitest.MouseEvent{Col: col + 2, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseRelease})
	if err := term.WaitFor(explorerShows("Usage: tuios checkpoint list"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on the list row did not select it: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "help-click")

	if err := term.Type("q"); err != nil {
		t.Fatal(err)
	}
	explorerExits(t, term, "help -i after q")

	// The JSON the explorer reads is there for a script.
	out, err := tuiosCLI(t, base, "help", "--json")
	if err != nil {
		t.Fatalf("help --json: %v\n%s", err, out)
	}
	var tree struct {
		Commands []struct {
			Path  string `json:"path"`
			Short string `json:"short"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatalf("help --json is not JSON: %v", err)
	}
	found := false
	for _, c := range tree.Commands {
		if c.Path == "tuios ship" && strings.Contains(c.Short, "Commit, merge, push") {
			found = true
		}
	}
	if !found {
		t.Errorf("ASSERTION: help --json does not list tuios ship with the short help the explorer showed")
	}

	assertPlainOnTTY(t, base, "help")
	assertPlainOnTTY(t, base, "help", "checkpoint")
}

// TestExplorerEscLeaves: esc leaves an explorer when no search is open, and
// first closes the search when one is.
func TestExplorerEscLeaves(t *testing.T) {
	base := t.TempDir()
	term := startExplorer(t, base, "help", "-i")
	if err := term.WaitFor(explorerShows("tuios commands"), uiTimeout); err != nil {
		t.Fatalf("help -i did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.Type("/zzzznothing"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("No match"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a search with no match does not say so: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "no-match")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Press / to search", "checkpoint"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc in the search did not clear it: %v\n%s", err, term.Snapshot())
	}
	if _, done := term.ExitCode(); done {
		t.Fatal("ASSERTION: esc in the search left the explorer")
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	explorerExits(t, term, "help -i after esc")
}

// TestConfigExplorer opens tuios config browse against a daemon, filters to
// a section with a click on its tab, searches for an option, sets it from
// the explorer, and reads the value back with get-config. A value the
// option does not accept is refused in the explorer with the error
// set-config prints, and the value stays. Then tuios config on a terminal
// is checked to open nothing.
//
// Negative control (NEGATIVE_CONTROLS.md): with the setConfigOption call
// cut from the explorer's Apply, the explorer says it set the value, and
// get-config still reads the old one.
func TestConfigExplorer(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "cfg", "--detach"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}
	term := startExplorer(t, base, "config", "browse", "-s", "cfg")
	if err := term.WaitFor(explorerShows("tuios options", "All", "appearance"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: config browse did not open: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "config-open")

	if err := term.Type("/dockbar_position"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Type: string", "Default: ", "Source: ", "Accepted: "), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the detail pane lacks the type, default, source or accepted values: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "config-search")

	// A value the option refuses.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Set appearance.dockbar_position to:"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: enter did not open the input: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("sideways"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("sideways"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the refusal does not name the value: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "config-refused")
	cliOut, cliErr := tuiosCLI(t, base, "set-config", "-s", "cfg", "appearance.dockbar_position", "sideways")
	if cliErr == nil {
		t.Fatalf("set-config accepted sideways:\n%s", cliOut)
	}
	// The explorer shows set-config's own error: its first sentence is on
	// the screen.
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(cliOut), "\n", 2)[0])
	first = strings.TrimPrefix(first, "Error: ")
	if cut := strings.Index(first, ". "); cut > 0 {
		first = first[:cut]
	}
	if !strings.Contains(strings.Join(strings.Fields(term.Snapshot()), " "), first) {
		t.Errorf("ASSERTION: the explorer's refusal differs from set-config's:\nset-config: %s\nexplorer:\n%s", cliOut, term.Snapshot())
	}

	// A value it accepts.
	if err := term.SendKeys(tuitest.Enter, tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.Type("bottom"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(explorerShows("Set appearance.dockbar_position = bottom", "Source: session"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the set did not report and show its source: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "config-set")
	out, err := tuiosCLI(t, base, "get-config", "-s", "cfg", "appearance.dockbar_position", "--json")
	if err != nil || !strings.Contains(out, `"value": "bottom"`) || !strings.Contains(out, `"source": "session"`) {
		t.Errorf("ASSERTION: get-config does not read the value the explorer set: %v\n%s", err, out)
	}

	// A click on a section tab filters the list to it.
	// Open the search and clear it, which leaves the explorer open.
	// A bare esc right before the next key reads as alt and that key, so
	// the search is cleared with ctrl+u and closed with enter.
	if err := term.Type("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('u'), tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	col, row, ok := tuitest.Find(term.Screen(), " daemon ")
	if !ok {
		t.Fatalf("no daemon tab:\n%s", term.Snapshot())
	}
	_ = term.SendMouse(tuitest.MouseEvent{Col: col + 2, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MousePress})
	_ = term.SendMouse(tuitest.MouseEvent{Col: col + 2, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseRelease})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "daemon.log_level") && !strings.Contains(text, "appearance.dockbar_position")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on the daemon tab did not filter the list: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "config-tab")

	if err := term.Type("q"); err != nil {
		t.Fatal(err)
	}
	explorerExits(t, term, "config browse after q")

	assertPlainOnTTY(t, base, "config")
	assertPlainOnTTY(t, base, "list-options", "--section", "daemon")
}

// TestKeybindsExplorer opens tuios keybinds browse, searches for the
// spotlight, reads a row's keys and description in the detail pane, moves
// to the Copy mode tab with tab presses, and leaves with q. The rows are
// the rows of keybinds list --json. Then keybinds list and keybinds on a
// terminal are checked to be the plain commands.
//
// Negative control (NEGATIVE_CONTROLS.md): with the AddCommand of browse
// cut from addExplorers, keybinds browse prints the keybinds help and
// exits, and the wait for the explorer's title fails.
func TestKeybindsExplorer(t *testing.T) {
	base := t.TempDir()
	out, err := tuiosCLI(t, base, "keybinds", "list", "--json")
	if err != nil {
		t.Fatalf("keybinds list --json: %v\n%s", err, out)
	}
	var rows []struct {
		Action      string   `json:"action"`
		Keys        []string `json:"keys"`
		Description string   `json:"description"`
		ScopeName   string   `json:"scope_name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("keybinds list --json is not JSON: %v", err)
	}
	var spot string
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Description), "spotlight") && len(r.Keys) > 0 {
			spot = r.Description
			break
		}
	}
	if spot == "" {
		t.Fatalf("keybinds list --json has no spotlight row:\n%s", out)
	}

	term := startExplorer(t, base, "keybinds", "browse")
	if err := term.WaitFor(explorerShows("tuios keybindings", "All", "Window mode"), uiTimeout); err != nil {
		t.Fatalf("ASSERTION: keybinds browse did not open: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "keybinds-open")

	if err := term.Type("/spotlight"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Keys: ") && strings.Contains(text, "Scope: ") &&
			strings.Contains(strings.ToLower(text), "spotlight") && !strings.Contains(text, "Copy the selection")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the search for spotlight did not narrow the list and show a detail: %v\n%s", err, term.Snapshot())
	}
	explorerShot(t, term, "keybinds-search")

	// Clear the search, then step through the tabs to Copy mode.
	// A bare esc right before the next key reads as alt and that key, so
	// the search is cleared with ctrl+u and closed with enter.
	if err := term.Type("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('u'), tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	reached := false
	for range 40 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatal(err)
		}
		if term.WaitFor(explorerShows("Scope: Copy mode"), 300*time.Millisecond) == nil {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatalf("ASSERTION: tab never reached the Copy mode scope\n%s", term.Snapshot())
	}
	if strings.Contains(term.Snapshot(), "Scope: Window mode") {
		t.Errorf("ASSERTION: the Copy mode tab still shows Window mode rows\n%s", term.Snapshot())
	}
	explorerShot(t, term, "keybinds-copy-mode")

	if err := term.Type("q"); err != nil {
		t.Fatal(err)
	}
	explorerExits(t, term, "keybinds browse after q")

	assertPlainOnTTY(t, base, "keybinds", "list")
	assertPlainOnTTY(t, base, "keybinds")
}
