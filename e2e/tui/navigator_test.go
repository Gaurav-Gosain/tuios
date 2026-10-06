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

// navDecoy is a pane name that holds every character of navMarker in order,
// spread out, and never the marker whole. A fuzzy search for the marker
// matches it.
const navDecoy = "need a ledge-77 81"

// TestNavigatorRanksScreenTextOverAScatteredName: a pane whose screen shows
// the query whole ranks above a pane whose name only holds the query's
// characters spread out, so the cursor and the preview are on the pane that
// shows the text. The decoy is listed too, so the search did match it and the
// order is what decides.
//
// The failure this covers came from the random parts of a pane's fields: a
// t.TempDir path and a pane id spelled needle-7781 out of order once in CI,
// and that pane took the cursor from logs.
func TestNavigatorRanksScreenTextOverAScatteredName(t *testing.T) {
	base, _ := navigatorSessions(t)
	if o, err := tuiosCLI(t, base, "new-window", navDecoy, "-s", "work", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	openNavigator(t, term, "home", "work")
	sendKeys(t, term, "/")
	if err := term.SendKeys(navMarker); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	// The load fills in the logs pane's text after the decoy is already
	// listed by name, so the wait is for the order the finished load gives.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if !strings.Contains(text, navDecoy) || strings.Count(text, navMarker) < 3 {
			return false
		}
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, "logs ·") {
				return strings.Contains(l, "›")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("the search for %s did not put the cursor and the preview on logs above %q: %v\n%s", navMarker, navDecoy, err, term.Snapshot())
	}
	saveFrame(t, term, "navigator-ranking")
}

// TestNavigatorScoresTheWholeOccurrence: a pane whose screen line holds the
// query's characters spread out and then the query whole ranks by the whole
// occurrence. The pane late prints navDecoy and then the marker on one line,
// so the spread match in that line scores exactly what the decoy's name
// scores, and only the whole occurrence puts late above the decoy. logs is
// listed as well, so the load has read every screen when the order is read.
func TestNavigatorScoresTheWholeOccurrence(t *testing.T) {
	base, _ := navigatorSessions(t)
	for _, args := range [][]string{
		{"new-window", navDecoy, "-s", "work", "--no-focus"},
		{"new-window", "late", "-s", "work", "--no-focus"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	printMarker(t, base, "work", "late", "printf '"+navDecoy+" nee%s\\n' dle-7781", navDecoy+" "+navMarker)
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	openNavigator(t, term, "home", "work")
	sendKeys(t, term, "/")
	if err := term.SendKeys(navMarker); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	rowOf := func(lines []string, prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimLeft(l, " ›"), prefix) {
				return i
			}
		}
		return -1
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		lines := strings.Split(s.Text(), "\n")
		logs, late, decoy := rowOf(lines, "logs · work"), rowOf(lines, "late · work"), rowOf(lines, navDecoy+" · work")
		return logs >= 0 && decoy >= 0 && late >= 0 && late < decoy
	}, uiTimeout); err != nil {
		t.Fatalf("the search for %s did not rank late, which shows it whole, above %q: %v\n%s", navMarker, navDecoy, err, term.Snapshot())
	}
	saveFrame(t, term, "navigator-whole-occurrence")
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

// navClient attaches a client to home with env, in terminal mode.
func navClient(t *testing.T, base string, env []string) *tuitest.Terminal {
	t.Helper()
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	return term
}

// openWorkLogs opens the navigator and puts the cursor on work's logs pane:
// G goes to the last row, work, l opens it, and G again goes to its last
// pane, logs.
func openWorkLogs(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openNavigator(t, term, "work")
	sendKeys(t, term, "G", "l")
	waitScreen(t, term, "work did not open", "logs")
	sendKeys(t, term, "G")
	waitScreen(t, term, "the preview is not on logs", "work · workspace 1")
}

// TestNavigatorTakesPastes: a paste never reaches the shell under the
// navigator. In the list it is dropped. In the search line it is search
// text. The positive half, first, shows that the same paste reaches the
// shell with the navigator closed, and leaves the line the check looks for.
func TestNavigatorTakesPastes(t *testing.T) {
	base, _ := navigatorSessions(t)
	home := focusedIn(t, base, "home")
	if o, err := tuiosCLI(t, base, "send-text", "-s", "home", "-w", home, "PS1='NAV> '"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "send-keys", "-s", "home", "-w", home, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, o)
	}
	last := func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "home", "-w", home)
		l := ""
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				l = strings.TrimRight(line, " ")
			}
		}
		return l
	}
	waitLast := func(want, what string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for last() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the shell's last line is %q, want %q", what, last(), want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitLast("NAV>", "the prompt was never set")
	term := navClient(t, base, nil)
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	if err := term.Paste("pz1"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	waitLast("NAV> pz1", "a paste with the navigator closed")
	sendKeys(t, term, tuitest.Ctrl('u'))
	waitLast("NAV>", "ctrl+u")

	openNavigator(t, term, "work")
	if err := term.Paste("pz2"); err != nil {
		t.Fatalf("paste: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := last(); got != "NAV>" {
		t.Fatalf("a paste in the navigator's list reached the shell: %q", got)
	}
	sendKeys(t, term, "/")
	if err := term.Paste(navMarker); err != nil {
		t.Fatalf("paste: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), navMarker) >= 3 && strings.Contains(s.Text(), "logs")
	}, uiTimeout); err != nil {
		t.Fatalf("a paste in the search line did not search: %v\n%s", err, term.Snapshot())
	}
	if got := last(); got != "NAV>" {
		t.Fatalf("a paste in the search line reached the shell: %q", got)
	}
}

// TestNavigatorDropsAStaleLoad: a load that the person closed the navigator
// on, and that answers after a newer navigator's load, changes nothing. The
// first load is held by the test seam, cancelled by esc, and let go after
// the second load has filled the tree. A cancelled load answers that every
// session did not answer in time, so a stale answer that got in would put
// that note on work's row.
func TestNavigatorDropsAStaleLoad(t *testing.T) {
	base, _ := navigatorSessions(t)
	hold := filepath.Join(base, "hold-nav")
	if err := os.WriteFile(hold, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	term := navClient(t, base, []string{"TUIOS_E2E_HOLD_NAV=" + hold})
	openNavigator(t, term, "work")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Press / to search") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the navigator: %v\n%s", err, term.Snapshot())
	}
	openWorkLogs(t, term)
	waitScreen(t, term, "the second load never filled the preview", navMarker)
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if text := term.Screen().Text(); strings.Contains(text, "Did not answer in time") || !strings.Contains(text, navMarker) {
		t.Fatalf("the first, cancelled load changed the tree after the second:\n%s", term.Snapshot())
	}
}

// TestNavigatorSaysWhatItDidNotRead: past the cap on screens one load reads
// (set to 1 for the test), a pane's preview says it was not read, and a
// session that does not answer by the load's deadline says so on its row.
func TestNavigatorSaysWhatItDidNotRead(t *testing.T) {
	t.Run("cap", func(t *testing.T) {
		base, _ := navigatorSessions(t)
		term := navClient(t, base, []string{"TUIOS_E2E_NAV_CAPTURES=1"})
		openWorkLogs(t, term)
		waitScreen(t, term, "the pane past the cap does not say it was not read", "Not read. The navigator reads the screens of the first 1 panes.")
		if strings.Contains(term.Screen().Text(), navMarker) {
			t.Fatalf("the pane past the cap was read:\n%s", term.Snapshot())
		}
	})
	t.Run("deadline", func(t *testing.T) {
		base, _ := navigatorSessions(t)
		hold := filepath.Join(base, "hold-nav")
		if err := os.WriteFile(hold, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		term := navClient(t, base, []string{"TUIOS_E2E_HOLD_NAV=" + hold})
		openNavigator(t, term, "work")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "Did not answer in time")
		}, 15*time.Second); err != nil {
			t.Fatalf("a session held past the deadline does not say so: %v\n%s", err, term.Snapshot())
		}
	})
}
