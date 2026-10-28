package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
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
		for l := range strings.SplitSeq(out, "\n") {
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

// TestNavigatorKeepsADonePaneSeen: a finished agent in another session is
// drawn unread (■) until the person looks at it, and seen (○) after, also
// when the navigator is opened from another session (issue #902).
//
// How it could pass wrongly, written down first:
//   - The seen mark could come from the cached row before the load lands,
//     and the load could put the unread mark back. Each check waits for the
//     marker in the preview, which only the load reads, before it reads the
//     mark.
//   - No mark at all would read as "not unread". The first check needs ■ on
//     the logs row, and the second needs ○ there.
func TestNavigatorKeepsADonePaneSeen(t *testing.T) {
	base, logs := navigatorSessions(t)
	for _, state := range []string{"working", "done"} {
		if o, err := tuiosCLI(t, base, "set-agent-state", "-s", "work", "-w", logs, state, "--harness", "claude-code"); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, o)
		}
	}
	term := navClient(t, base, nil)
	logsMark := func(mark, what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			if !strings.Contains(text, navMarker) {
				return false
			}
			for l := range strings.SplitSeq(text, "\n") {
				if strings.Contains(navListRow(l), mark+" logs") {
					return true
				}
			}
			return false
		}, uiTimeout); err != nil {
			t.Fatalf("the logs row never showed %s (%s) after the load: %v\n%s", mark, what, err, term.Snapshot())
		}
	}
	attached := func(name string) {
		t.Helper()
		isAttached := func(rows []lsRow) bool {
			for _, r := range rows {
				if r.Name == name && r.Attached {
					return true
				}
			}
			return false
		}
		if !isAttached(waitForRows(t, base, isAttached)) {
			t.Fatalf("the client never showed %s\n%s", name, term.Snapshot())
		}
	}

	openWorkLogs(t, term)
	logsMark("■", "unread")
	sendKeys(t, term, tuitest.Enter)
	attached("work")
	deadline := time.Now().Add(uiTimeout)
	for focusedIn(t, base, "work") != logs {
		if time.Now().After(deadline) {
			t.Fatalf("enter did not focus logs\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	if o, err := tuiosCLI(t, base, "switch-session", "-s", "work", "home"); err != nil {
		t.Fatalf("switch-session home: %v\n%s", err, o)
	}
	attached("home")
	openWorkLogs(t, term)
	logsMark("○", "seen")
	saveFrame(t, term, "navigator-done-seen")
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
		for l := range strings.SplitSeq(text, "\n") {
			if row := navListRow(l); strings.Contains(row, "logs │") {
				return strings.HasPrefix(row, "▎")
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("the search for %s did not put the cursor and the preview on logs above %q: %v\n%s", navMarker, navDecoy, err, term.Snapshot())
	}
	saveFrame(t, term, "navigator-ranking")
}

// navListRow is a screen line from the list box's left border on, without
// the border: a row of the list starts with the cursor bar or a blank. It is
// empty for a line with no border.
func navListRow(l string) string {
	_, row, ok := strings.Cut(strings.TrimLeft(l, " "), "│")
	if !ok {
		return ""
	}
	return row
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
			if strings.HasPrefix(strings.TrimLeft(navListRow(l), " ▎"), prefix) {
				return i
			}
		}
		return -1
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		lines := strings.Split(s.Text(), "\n")
		logs, late, decoy := rowOf(lines, "logs │"), rowOf(lines, "late │"), rowOf(lines, navDecoy+" │")
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
	waitScreen(t, term, "the preview is not on logs", "work › 1")
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
		for line := range strings.SplitSeq(out, "\n") {
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

// TestNavigatorOpensReadyToType: with panes.navigator_search on, prefix /
// opens the navigator on its search line, so the marker typed at once finds
// the logs pane in the other session, with no / first, and enter goes there.
// esc with nothing typed closes it in one press.
//
// How it could pass wrongly, written down first:
//   - The typed text could reach the list as keys instead of the query: the
//     list binds no key to the marker's letters, but n, e and d are not all
//     free there, and a list key would move or fold, not search. The wait
//     needs the marker on a row, in the preview and in the search line.
//   - The jump could leave the focus where it was: list-windows reads the
//     focused pane of work after enter.
//
// Negative control: with OpenNavigator not reading navigator_search, the
// navigator opens on the list, the typed marker never reaches the search
// line, and the wait for it fails. With the esc rule cut, the first esc
// only leaves the search line, and the wait for the navigator to close
// fails. The runs are in NEGATIVE_CONTROLS.md.
func TestNavigatorOpensReadyToType(t *testing.T) {
	base, logs := navigatorSessions(t)
	writeConfig(t, base, "[panes]\nnavigator_search = true\n")
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	toggleSidebarViaPalette(t, term)
	waitRailCurrent(t, term, "home")

	// esc with nothing typed closes it at once.
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed", "Panes", "home", "work")
	saveFrame(t, term, "navigator-ready-to-type")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Panes") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc with nothing typed did not close the navigator: %v\n%s", err, term.Snapshot())
	}

	// Typed at once, the marker is the search.
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed", "Panes", "work")
	if err := term.SendKeys(navMarker); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Count(text, navMarker) >= 3 && strings.Contains(text, "logs")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the marker typed right after prefix / was not a search: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
	waitRailCurrent(t, term, "work")
	deadline := time.Now().Add(uiTimeout)
	for focusedIn(t, base, "work") != logs {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: enter switched to work but left the focus on %s, not logs\n%s", focusedIn(t, base, "work"), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestNavigatorOpensOverTheFocusedRail: with the rail holding the keyboard,
// prefix / opens the navigator, the navigator takes the keys at once, and the
// rail has them again when it closes. The go-to picker is checked the same
// way, since the fix is one rule for every panel. The navigator sits in the
// pane area beside the rail with even margins, clear of the dock.
//
// How it could pass wrongly, written down first:
//   - The rail could swallow / and the marker, and the marker could still
//     show on screen from the rail or a pane. The wait needs it on a row, in
//     the preview and in the search line, which only the navigator draws.
//   - The rail could lose its focus when the panel opens, and the panel get
//     the keys for that reason. The mode pill must still say SIDEBAR after
//     the panel closes.
//   - The margins could be read off the text, which has no edge where the
//     panel's ground ends. They are read from the cells of the panel's ground.
//
// Negative control: with the rail's check for an open panel cut from
// routeKey, the typed marker never reaches the navigator's search line and
// the wait for it fails. With panelRoomHeight back to the rows under the top
// dock alone, the panel runs over the dock row and the margin check fails.
// The runs are in NEGATIVE_CONTROLS.md.
func TestNavigatorOpensOverTheFocusedRail(t *testing.T) {
	base, logs := navigatorSessions(t)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	term := attachIn(t, base, "home", startOpts{cols: 200, rows: 50})
	waitRailCurrent(t, term, "home")
	sendKeys(t, term, tuitest.Ctrl('b'), "e")
	waitText(t, term, "the mode pill after ctrl+b e", "SIDEBAR")

	openNavigator(t, term, "home", "work")
	time.Sleep(insertGuard)
	saveFrame(t, term, "navigator-over-rail")
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "navigator-over-rail")
	navCheckMargins(t, term.Screen())

	sendKeys(t, term, "/")
	if err := term.SendKeys(navMarker); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Count(text, navMarker) >= 3 && strings.Contains(text, "logs")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the navigator opened over the focused rail did not take the keys: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "navigator-over-rail-search")
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), "navigator-over-rail-search")
	// esc leaves the search line, esc clears the query, and q closes.
	sendKeys(t, term, tuitest.Esc)
	sendKeys(t, term, tuitest.Esc)
	sendKeys(t, term, "q")
	railHasKeysAgain(t, term, "Panes")

	// The go-to picker, opened over the rail, takes its query too.
	openGotoPicker(t, term)
	if err := term.SendKeys("zq7x"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	waitText(t, term, "the go-to picker's query typed over the focused rail", "zq7x")
	sendKeys(t, term, tuitest.Esc)
	railHasKeysAgain(t, term, "@ # ! filter")

	if f := focusedIn(t, base, "work"); f == logs {
		t.Fatalf("a key typed into a panel reached the work session and focused logs")
	}
}

// railHasKeysAgain waits for the panel that shows marker to close, and for the
// mode pill to still say SIDEBAR: the rail has the keyboard back.
func railHasKeysAgain(t *testing.T, term *tuitest.Terminal, marker string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !strings.Contains(text, marker) && strings.Contains(text, "SIDEBAR")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the panel did not close back onto the focused rail: %v\n%s", err, term.Snapshot())
	}
}

// navCheckMargins reads the navigator's box from the cells of its ground, and
// fails unless it sits in the pane area with a margin on every side: the same
// margin, to a cell, above and below it in the rows over the dock, and each
// side in the columns beside the rail. The ground is read in the pane area
// only, since panels drawn there never cover the dock or the rail, and a
// panel over the dock leaves no margin under it.
func navCheckMargins(t *testing.T, s tuitest.Screen) {
	t.Helper()
	cols, rows := s.Size()
	// The panel's ground is under the corner of its Search box.
	gx, gy := -1, -1
	for y := 0; y < rows && gx < 0; y++ {
		if strings.Contains(s.Line(y), "╭─ Search") {
			for x := range cols {
				if s.Cell(x, y).Content == "╭" && s.Cell(x+3, y).Content == "S" {
					gx, gy = x, y
					break
				}
			}
		}
	}
	if gx < 0 {
		t.Fatalf("no Search box on screen\n%s", s.Text())
	}
	ground := s.Cell(gx, gy).Bg
	// The rail's edge is its border column on the first row, left of the
	// panel. The dock is the rows from its top rule down.
	rail := -1
	for x := range gx {
		if s.Cell(x, 0).Content == "│" {
			rail = x
		}
	}
	dock := rows - 1
	for y := rows - 1; y > rows-5; y-- {
		if s.Cell(0, y).Content == "─" {
			dock = y
		}
	}
	x0, y0, x1, y1 := cols, rows, -1, -1
	for y := range dock {
		for x := rail + 1; x < cols; x++ {
			if s.Cell(x, y).Bg == ground {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	top, bottom := y0, dock-1-y1
	left, right := x0-(rail+1), cols-1-x1
	t.Logf("navigator box x %d-%d, y %d-%d; rail edge %d; margins top %d bottom %d left %d right %d", x0, x1, y0, y1, rail, top, bottom, left, right)
	if rail < 0 || abs(top-bottom) > 1 || abs(left-right) > 1 || top < 1 || bottom < 1 || left < 1 || right < 1 {
		t.Fatalf("ASSERTION: the navigator is not centred in the pane area: margins top %d bottom %d left %d right %d (dock row %d, panel to row %d)\n%s",
			top, bottom, left, right, dock, y1, s.Text())
	}
}
