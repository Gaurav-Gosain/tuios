package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The pane navigator (choose_tree, prefix /), driven through a real client:
// it lists the sessions and their panes, a search finds a pane in another
// session by a line on its screen, the preview shows that line, and Enter
// switches the client to that session and pane. The host test does the same
// for a pane on another machine, through the ssh stand-in.
//
// How these could pass wrongly, written down first:
//   - The search could match the pane by its name or by the command typed to
//     print the line. The pane's name holds no part of the marker, and the
//     command prints it in two halves (printf 'nee%s' dle-...), so only the
//     printed line holds it whole.
//   - The marker on screen could be the query echoed in the search line, not
//     a row. The test waits for the marker twice on top of the search line:
//     the row's snippet and the preview.
//   - The jump could leave the focus where it was in a session that only
//     looks right. Each jump reads the target session's focused pane from
//     list-windows, and the rail's current session, after Enter.
//   - The host pane could be listed from a cached name with no read of its
//     screen. Its search is by its screen text, which only a capture over
//     the link can supply.
//
// Negative controls, all confirmed red (see NEGATIVE_CONTROLS.md).

// navMarker is printed in the target pane, in two halves.
const (
	navMarker      = "needle-7781"
	navPrintMarker = "printf 'nee%s\\n' dle-7781"
)

// printMarker types a command into a pane that prints marker, and waits for
// the pane's screen to show it.
func printMarker(t *testing.T, base, sess, window, command, marker string) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", window, command); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-w", window, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, o)
	}
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", window)
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(l) == marker {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("the pane never printed %s", marker)
}

// openNavigator presses the leader and / and waits for the panel with every
// marker in it.
func openNavigator(t *testing.T, term *tuitest.Terminal, markers ...string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed", append([]string{"Panes", "Press / to search"}, markers...)...)
}

// searchNavigator moves to the search line, types query, and waits for the
// query to be found in a row and in the preview as well as in the search
// line.
func searchNavigator(t *testing.T, term *tuitest.Terminal, query, row string) {
	t.Helper()
	sendKeys(t, term, "/")
	if err := term.SendKeys(query); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Count(text, query) >= 3 && strings.Contains(text, row)
	}, uiTimeout); err != nil {
		t.Fatalf("the search for %q never found it on a row and in the preview: %v\n%s", query, err, term.Snapshot())
	}
}

// focusedIn is the focused pane of a session, from list-windows.
func focusedIn(t *testing.T, base, sess string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", sess)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Focused string `json:"focused_window_id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode list-windows: %v\n%s", err, out)
	}
	return res.Focused
}

// windowIDByName is the id of the pane called name in a session.
func windowIDByName(t *testing.T, base, sess, name string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", sess)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []struct {
			ID      string `json:"window_id"`
			Display string `json:"display_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode list-windows: %v\n%s", err, out)
	}
	for _, w := range res.Windows {
		if w.Display == name {
			return w.ID
		}
	}
	t.Fatalf("no pane called %s in %s:\n%s", name, sess, out)
	return ""
}

// navigatorSessions makes two sessions, home and work, with a pane called
// logs in work that prints the marker, and the work pane that is not logs
// focused. It returns the isolation root and the logs pane's id.
func navigatorSessions(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	for _, args := range [][]string{
		{"new", "home", "--detach"},
		{"new", "work", "--detach"},
		{"new-window", "logs", "-s", "work", "--no-focus"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	printMarker(t, base, "work", "logs", navPrintMarker, navMarker)
	logs := windowIDByName(t, base, "work", "logs")
	if focusedIn(t, base, "work") == logs {
		t.Fatalf("the logs pane has the focus before the jump, so the jump would prove nothing")
	}
	return base, logs
}

// TestNavigatorFindsAPaneByScreenText: the tree lists both sessions, a
// search finds the logs pane in the other session by its screen text, the
// preview shows the text, and Enter switches the client to work and focuses
// logs. Esc on a fresh tree closes it with no switch.
func TestNavigatorFindsAPaneByScreenText(t *testing.T) {
	base, logs := navigatorSessions(t)
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)
	waitRailCurrent(t, term, "home")

	openNavigator(t, term, "home", "work", "current")
	saveFrame(t, term, "navigator-tree")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Press / to search") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the navigator: %v\n%s", err, term.Snapshot())
	}
	waitRailCurrent(t, term, "home")

	openNavigator(t, term, "work")
	searchNavigator(t, term, navMarker, "logs")
	saveFrame(t, term, "navigator-search")
	sendKeys(t, term, tuitest.Enter)
	waitRailCurrent(t, term, "work")
	deadline := time.Now().Add(uiTimeout)
	for focusedIn(t, base, "work") != logs {
		if time.Now().After(deadline) {
			t.Fatalf("enter switched to work but left the focus on %s, not logs\n%s", focusedIn(t, base, "work"), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestNavigatorListsAHostPane: a pane on another machine is listed under its
// session, found by its screen text through the link, and Enter takes the
// client to it.
func TestNavigatorListsAHostPane(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	for _, args := range [][]string{
		{"new", "far-shell", "--detach"},
		{"new-window", "farlog", "-s", "far-shell", "--no-focus"},
	} {
		if o, err := tuiosCLI(t, remote, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	printMarker(t, remote, "far-shell", "farlog", "printf 'far%s\\n' mark-5521", "farmark-5521")
	farlog := windowIDByName(t, remote, "far-shell", "farlog")

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env, cols: 140, rows: 40})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "far-shell")
	waitRailCurrent(t, term, "home")

	openNavigator(t, term, "far-shell @ build")
	searchNavigator(t, term, "farmark-5521", "farlog")
	saveFrame(t, term, "navigator-host")
	sendKeys(t, term, tuitest.Enter)
	waitRailCurrent(t, term, "far-shell")
	deadline := time.Now().Add(uiTimeout)
	for focusedIn(t, remote, "far-shell") != farlog {
		if time.Now().After(deadline) {
			t.Fatalf("enter went to far-shell but did not focus farlog\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestNavigatorLooks saves the navigator, on its tree and on a search, as
// text, styled text and a PNG on the dark and the light look at every colour
// depth, for a person to look at.
func TestNavigatorLooks(t *testing.T) {
	for _, look := range []chromeLook{lookDark, lookLatte} {
		for _, depth := range chromeDepths {
			t.Run(look.name+"-"+depth.name, func(t *testing.T) {
				base, _ := navigatorSessions(t)
				if look.theme != "" {
					writeConfig(t, base, "[appearance]\ntheme = \""+look.theme+"\"\n")
				}
				term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40, shippedLooks: true, env: depth.env})
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
					t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
				}
				dir := artifactDir(t)
				_ = os.WriteFile(filepath.Join(dir, "README"), []byte("choose_tree on two sessions: the tree, then a search by screen text\n"), 0o644)
				openNavigator(t, term, "home", "work")
				sendKeys(t, term, "G", "l")
				waitScreen(t, term, "work did not open", "logs")
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("the screen never settled: %v", err)
				}
				saveArtifact(t, term, dir, "tree")
				savePNG(t, term.Screen(), hostPalette(t, look.theme), dir, "tree")
				searchNavigator(t, term, navMarker, "logs")
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("the screen never settled: %v", err)
				}
				saveArtifact(t, term, dir, "search")
				savePNG(t, term.Screen(), hostPalette(t, look.theme), dir, "search")
			})
		}
	}
}

// TestListWindowsAllHoldsToTheReadGrant: list-windows --all --text reads
// screen text with capture-pane, so it is held to capture-pane's grants. Run
// from outside every pane it lists the other session's pane with its text.
// Run from a pane that holds the read grant alone, it lists its own session,
// says the listing across sessions was refused, and holds no text of the
// other session.
func TestListWindowsAllHoldsToTheReadGrant(t *testing.T) {
	base, _ := navigatorSessions(t)

	// The positive half: the person's own shell, outside every pane.
	out, err := tuiosCLI(t, base, "list-windows", "--all", "--text", "5", "--json")
	if err != nil {
		t.Fatalf("list-windows --all from outside every pane: %v\n%s", err, out)
	}
	if !strings.Contains(out, navMarker) || !strings.Contains(out, `"session": "home"`) {
		t.Fatalf("list-windows --all --text from outside every pane does not hold work's text:\n%s", out)
	}

	home := focusedIn(t, base, "home")
	if o, err := tuiosCLI(t, base, "set-pane-grants", "-s", "home", "-w", home, "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, o)
	}
	file := filepath.Join(base, "listing.json")
	line := tuiosBin + " list-windows --all --text 5 --json > " + file + "; echo LIST_EXIT=$? > " + file + ".done\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", "home", "-w", home, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(file + ".done"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never ran the listing")
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the listing: %v", err)
	}
	got := string(data)
	t.Logf("the pane's listing:\n%s", got)
	if strings.Contains(got, navMarker) || strings.Contains(got, `"session": "work"`) {
		t.Fatalf("a pane holding read alone listed the other session:\n%s", got)
	}
	if !strings.Contains(got, `"session": "home"`) || !strings.Contains(got, "list-sessions") {
		t.Fatalf("the pane's listing does not hold its own session and the refusal:\n%s", got)
	}
}
