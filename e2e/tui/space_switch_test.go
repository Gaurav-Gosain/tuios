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

// Switching space: ctrl+b } and ctrl+b { (next_space and prev_space), and a
// click or Enter on a row of the rail's spaces section. The client chooses the
// space, the daemon chooses the session with pick-space, and the client
// switches itself to it. Every landing is read from the
// daemon's own list of clients, by the client process's pid, and the screens
// are saved under artifactDir.

// clientOnPid waits until the client process pid shows want, and fails if it
// settles anywhere else.
func clientOnPid(t *testing.T, base string, pid int, want string) {
	t.Helper()
	got := ""
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		got = ""
		for _, r := range clientRows(t, base) {
			if r.PID == pid {
				got = r.Session
			}
		}
		if got == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: client pid %d shows %q, want %s\n%+v", pid, got, want, clientRows(t, base))
}

// stillOnPid checks, after the moment a wrong move would have landed, that the
// client process pid is still on want.
func stillOnPid(t *testing.T, base string, pid int, want, why string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	for _, r := range clientRows(t, base) {
		if r.PID == pid && r.Session != want {
			t.Fatalf("ASSERTION: %s: client pid %d moved to %s, want it still on %s\n%+v", why, pid, r.Session, want, clientRows(t, base))
		}
	}
	clientOnPid(t, base, pid, want)
}

// pressSpaceSwitch presses the prefix and key. The caller waits for the
// landing, and then settleAfterSwitch, before the next key.
func pressSpaceSwitch(t *testing.T, term *tuitest.Terminal, key string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), key)
}

// settleAfterSwitch waits for the frame to settle and out the input guard a
// switch re-arms.
func settleAfterSwitch(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	_ = term.WaitStable(2 * time.Second)
	time.Sleep(insertGuard + 150*time.Millisecond)
}

// TestSpaceKeysMoveOnlyThisClient: two clients on e2e-a1, in alpha. } in the
// first moves the first alone to the next space in the rail's order, which is
// none (e2e-loose); { in the second moves the second alone to the previous
// space, beta (e2e-b1). } again in the first wraps from none to beta.
//
// The fixture's spaces come in their first member's order, beta then alpha,
// and the sessions in no space last, so alpha's neighbours are none and beta.
//
// How it could pass wrongly, and what stops it:
//   - a switch that moves whichever client the daemon finds on the session,
//     or both: each move is checked on both clients, by pid, and the one that
//     did not press must still be where it was well after the move landed.
//   - the key walking sessions rather than spaces: } from e2e-a1 by session
//     would go to e2e-a2, in the same space.
//   - the order ignoring the none row, or not wrapping: } from alpha must reach
//     none, and } from none must come back round to beta.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSpaceKeysMoveOnlyThisClient(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	spaceUIFixture(t, base, true)

	b := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	a := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	if rows := clientRows(t, base); len(rows) != 2 {
		t.Fatalf("want two clients attached to e2e-a1, got %+v", rows)
	}
	windowManagementMode(t, a)
	windowManagementMode(t, b)

	pressSpaceSwitch(t, a, "}")
	clientOnPid(t, base, a.Pid(), "e2e-loose")
	stillOnPid(t, base, b.Pid(), "e2e-a1", "} in the other client")
	settleAfterSwitch(t, a)
	saveArtifact(t, a, dir, "a-next-to-none")
	saveArtifact(t, b, dir, "b-stayed-on-a1")

	pressSpaceSwitch(t, b, "{")
	clientOnPid(t, base, b.Pid(), "e2e-b1")
	stillOnPid(t, base, a.Pid(), "e2e-loose", "{ in the other client")
	settleAfterSwitch(t, b)
	saveArtifact(t, b, dir, "b-prev-to-beta")
	saveArtifact(t, a, dir, "a-stayed-on-loose")

	pressSpaceSwitch(t, a, "}")
	clientOnPid(t, base, a.Pid(), "e2e-b1")
	settleAfterSwitch(t, a)
	saveArtifact(t, a, dir, "a-next-wraps-to-beta")
	alive(t, a, "after switching spaces")
	alive(t, b, "after the other client switched spaces")
}

// TestSpaceKeyChordInTerminalMode: next_space bound to a direct chord (alt+y)
// works from terminal mode, twice in a row: from e2e-a1 in alpha to none
// (e2e-loose), then to beta (e2e-b1). The chord reaches the action through the
// terminal-mode router (handleTerminalModeBinds), the path a direct chord
// takes while typing into a shell.
//
// How it could pass wrongly, and what stops it:
//   - the router dropping the command the action returns: the pick is that
//     command, so the first press would move nothing. Two landings are
//     required, so a fix that worked once and then wedged (a flag set by the
//     first press and never cleared) fails too.
//   - the chord reaching the shell instead: the client would stay on e2e-a1.
//   - the presses made in window-management mode: each press is made after
//     entering terminal mode, and the screen must say so.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSpaceKeyChordInTerminalMode(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail+"\n[keybindings.window_management]\nnext_space = [\"alt+y\"]\n")
	spaceUIFixture(t, base, true)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	for i, want := range []string{"e2e-loose", "e2e-b1"} {
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
		if err := term.SendKeys(tuitest.Alt('y')); err != nil {
			t.Fatalf("press alt+y: %v", err)
		}
		clientOnPid(t, base, term.Pid(), want)
		settleAfterSwitch(t, term)
		saveArtifact(t, term, dir, "chord-"+string(rune('1'+i))+"-"+want)
	}
	alive(t, term, "after the space chord in terminal mode")
}

// TestSpaceKeysWithOneSpace: with one space and no session outside it, } has
// nowhere to go, says so, and leaves the client where it is.
func TestSpaceKeysWithOneSpace(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail)
	killDaemon(t, base)
	for _, name := range []string{"e2e-a1", "e2e-a2"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach", "--space", "alpha"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)
	pressSpaceSwitch(t, term, "}")
	if err := term.WaitForText("No other spaces", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: } with one space never said so: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "one-space")
	stillOnPid(t, base, term.Pid(), "e2e-a1", "} with one space")
}

// spaceLastUsed is each member's last_used, in the whole seconds list-spaces
// reports.
func spaceLastUsed(t *testing.T, base string) map[string]int64 {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "list-spaces")
	if err != nil {
		t.Fatalf("list-spaces: %v: %s", err, out)
	}
	var res struct {
		Spaces []struct {
			Sessions []struct {
				Name     string `json:"name"`
				LastUsed int64  `json:"last_used"`
			} `json:"sessions"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode list-spaces: %v: %s", err, out)
	}
	used := map[string]int64{}
	for _, sp := range res.Spaces {
		for _, m := range sp.Sessions {
			used[m.Name] = m.LastUsed
		}
	}
	return used
}

// sessionLastActive is each session's last_active, in whole seconds.
func sessionLastActive(t *testing.T, base string) map[string]int64 {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "list-sessions")
	if err != nil {
		t.Fatalf("list-sessions: %v: %s", err, out)
	}
	var res struct {
		Sessions []struct {
			Name       string `json:"name"`
			LastActive int64  `json:"last_active"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode list-sessions: %v: %s", err, out)
	}
	active := map[string]int64{}
	for _, s := range res.Sessions {
		active[s.Name] = s.LastActive
	}
	return active
}

// useBothInOneSecond types one key into first's pane and then into second's,
// both inside one wall-clock second, so the two uses and the two activities
// tie in the whole seconds every listing carries while second's is the later
// at full precision. It retries on a run that straddled a second, and says
// what it got.
func useBothInOneSecond(t *testing.T, base string, first, second *tuitest.Terminal, firstName, secondName string) {
	t.Helper()
	// A client reports a use at most once a second per session, and the keys
	// that put these clients in terminal mode may have just sent one.
	time.Sleep(1100 * time.Millisecond)
	for attempt := range 5 {
		now := time.Now()
		time.Sleep(now.Truncate(time.Second).Add(time.Second + 100*time.Millisecond).Sub(now))
		if err := first.SendKeys("x"); err != nil {
			t.Fatalf("type into %s: %v", firstName, err)
		}
		time.Sleep(150 * time.Millisecond)
		if err := second.SendKeys("x"); err != nil {
			t.Fatalf("type into %s: %v", secondName, err)
		}
		time.Sleep(600 * time.Millisecond)
		used, active := spaceLastUsed(t, base), sessionLastActive(t, base)
		t.Logf("attempt %d: last_used %v, last_active %v", attempt, used, active)
		if used[firstName] != 0 && used[firstName] == used[secondName] && active[firstName] == active[secondName] {
			return
		}
		// A key that missed its pane, or a run across a second: the reports go
		// at most once a second per session, so wait one out.
		time.Sleep(1100 * time.Millisecond)
	}
	t.Fatalf("could not make two uses inside one second: last_used %v, last_active %v", spaceLastUsed(t, base), sessionLastActive(t, base))
}

// spaceRowOf is the screen row of the spaces section's row whose text starts
// with label, -1 when not drawn.
func spaceRowOf(s tuitest.Screen, label string) int {
	top := railHeaderRow(s, "spaces")
	if top < 0 {
		return -1
	}
	for i, text := range spaceRowsListed(s) {
		if strings.Contains(text, label) {
			return top + 1 + i
		}
	}
	return -1
}

// TestSpaceRowLandsOnTheDaemonsPick: a click on a space's row in the split rail
// lands on the session the daemon picks, in a case the listing the client
// holds cannot tell. alpha holds e2e-a1, e2e-a2 and e2e-a3, made a second
// apart in that order; e2e-a1 is never used, and e2e-a3 and then e2e-a2 are
// used inside one second. The listing says those two were used, and active,
// in the same second, and ranks e2e-a3 first because it was made later; the
// daemon compares the uses at full precision and picks e2e-a2. Then a click on
// none lands on e2e-loose.
//
// How it could pass wrongly, and what stops it:
//   - the pick made by the client from its listing (the rail's own rank): it
//     goes to e2e-a3, the session made later, since the listing ties on use
//     and on activity. The test checks that tie itself before it clicks, from
//     the same listings, and retries the uses until it holds.
//   - the pick made by the client from the rail's order, its first member: it
//     goes to e2e-a1.
//   - the click moving one of the other clients: two more are attached, on
//     e2e-a2 and e2e-a3, and each must stay where it is.
//   - a none row that targets nothing: its click must land on e2e-loose.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSpaceRowLandsOnTheDaemonsPick(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail("split"))
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	// Each made a whole second after the last, so a listing that ties on use
	// and activity ranks the later one first by its creation.
	for i, name := range []string{"e2e-a1", "e2e-a2", "e2e-a3"} {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond)
		}
		if out, err := tuiosCLI(t, base, "new", name, "--detach", "--space", "alpha"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	a2 := attachSpaceUI(t, base, "e2e-a2", 100, 30)
	a3 := attachSpaceUI(t, base, "e2e-a3", 100, 30)
	for _, term := range []*tuitest.Terminal{a2, a3} {
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
	}
	useBothInOneSecond(t, base, a3, a2, "e2e-a3", "e2e-a2")

	term := attachSpaceUI(t, base, "e2e-loose", 120, 40)
	waitSpaceRail(t, term, "the split rail", "spaces", "alpha", "none", "e2e-loose")
	// Long enough for the rail's listing poll to have taken both uses, so the
	// client holds the tie the old pick read.
	time.Sleep(3500 * time.Millisecond)
	s := term.Screen()
	saveArtifact(t, term, dir, "before-click")
	alpha := spaceRowOf(s, "alpha")
	if alpha < 0 {
		t.Fatalf("no alpha row in the spaces section\n%s", term.Snapshot())
	}

	mouseClick(t, term, 5, alpha, tuitest.MouseLeft, 0)
	clientOnPid(t, base, term.Pid(), "e2e-a2")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "alpha-clicked")
	stillOnPid(t, base, a2.Pid(), "e2e-a2", "the click on alpha")
	stillOnPid(t, base, a3.Pid(), "e2e-a3", "the click on alpha")

	var none int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		none = spaceRowOf(s, "none")
		return none >= 0 && railFocusMarked(s, spaceRowOf(s, "alpha"))
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never marked alpha after the switch: %v\n%s", err, term.Snapshot())
	}
	mouseClick(t, term, 5, none, tuitest.MouseLeft, 0)
	clientOnPid(t, base, term.Pid(), "e2e-loose")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "none-clicked")
	alive(t, term, "after clicking space rows")
}

// pickSpace calls pick-space with params and returns the session it names.
func pickSpace(t *testing.T, base, params string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "call", "pick-space", params)
	var res struct {
		Type    string `json:"type"`
		Session string `json:"session"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Type != "space_pick" {
		t.Fatalf("ASSERTION: pick-space %s failed: %v: %s", params, err, out)
	}
	return res.Session
}

// switchSpaceCLI runs tuios switch-space and returns its output.
func switchSpaceCLI(t *testing.T, base string, args ...string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"switch-space"}, args...)...)
	if err != nil {
		t.Fatalf("ASSERTION: switch-space %v failed: %v: %s", args, err, out)
	}
	return out
}

// TestSwitchSpaceNone: switch-space --none, and none: true, switch among the
// sessions in no space, counting a global session as in none, as the rail
// draws it. A named space never counts a global session. Both forms of the verb are refused together, and the
// command line refuses a name with --none. pick-space names the same sessions
// first, takes the same refusals, and moves nobody.
//
// Uses, oldest first: e2e-loose, e2e-loose2, e2e-g (global, which
// set-session-space refuses to file under alpha), e2e-a1 (alpha).
//
// How it could pass wrongly, and what stops it:
//   - none not filtering at all: it would pick e2e-a1, used last of all.
//   - none ignoring that a global session is drawn in no space: it would pick
//     e2e-loose2.
//   - the global session filed under alpha after all: after e2e-g is used
//     again, alpha would pick e2e-g rather than e2e-a1.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSwitchSpaceNone(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	killDaemon(t, base)
	for _, name := range []string{"e2e-loose", "e2e-loose2", "e2e-home"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-a1", "--detach", "--space", "alpha"); err != nil {
		t.Fatalf("create e2e-a1: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-g", "--global"); err != nil {
		t.Fatalf("create the global session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-g"); err != nil {
		t.Fatalf("open a window in the global session: %v: %s", err, out)
	}
	// A global session is in no space, so filing it under one is refused
	// (TestAGlobalSessionIsInNoSpace). The ranking below still has to count
	// it among the sessions in no space and never in alpha.
	if out, err := tuiosCLI(t, base, "call", "set-session-space", `{"session":"e2e-g","space":"alpha"}`); err == nil {
		t.Fatalf("ASSERTION: set-session-space filed the global session under alpha: %s", out)
	}

	for i, name := range []string{"e2e-loose", "e2e-loose2", "e2e-g", "e2e-a1"} {
		typeInSession(t, base, name, i)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	clientShows(t, base, "e2e-home")

	// pick-space names the same sessions switch-space lands on below, and
	// moves nobody.
	if got := pickSpace(t, base, `{"none":true}`); got != "e2e-g" {
		t.Fatalf("ASSERTION: pick-space none picked %q, want e2e-g", got)
	}
	if got := pickSpace(t, base, `{"space":"alpha"}`); got != "e2e-a1" {
		t.Fatalf("ASSERTION: pick-space alpha picked %q, want e2e-a1", got)
	}
	if out, err := tuiosCLI(t, base, "call", "pick-space", `{"space":"alpha","none":true}`); err == nil || !strings.Contains(out, "none") {
		t.Fatalf("ASSERTION: pick-space took space and none together: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "call", "pick-space", `{}`); err == nil {
		t.Fatalf("ASSERTION: pick-space took neither space nor none: %s", out)
	}
	stillOnPid(t, base, term.Pid(), "e2e-home", "pick-space")

	out := switchSpaceCLI(t, base, "--none")
	if !strings.Contains(out, "session e2e-g in no space") {
		t.Fatalf("ASSERTION: switch-space --none did not go to e2e-g, the session in no space (the global one counts) used last: %s", out)
	}
	clientShows(t, base, "e2e-g")
	saveFrame(t, term, "switch-space-none")

	// The verb's own form, and the refusal of both at once.
	if out, err := tuiosCLI(t, base, "call", "switch-space", `{"space":"alpha","none":true}`); err == nil || !strings.Contains(out, "none") {
		t.Fatalf("ASSERTION: space and none together were not refused: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "switch-space", "--none", "alpha"); err == nil {
		t.Fatalf("ASSERTION: the command line took a space name with --none: %s", out)
	}
	if out, err := tuiosCLI(t, base, "switch-space"); err == nil {
		t.Fatalf("ASSERTION: the command line took neither a space nor --none: %s", out)
	}

	// e2e-g is now the session used last of all, and in no space.
	typeInSession(t, base, "e2e-g", 9)
	clientShows(t, base, "e2e-g")
	out = switchSpaceCLI(t, base, "-s", "e2e-g", "alpha")
	if !strings.Contains(out, "session e2e-a1 of space alpha") {
		t.Fatalf("ASSERTION: switch-space alpha did not go to e2e-a1: a global session is never in a space for it: %s", out)
	}
	clientShows(t, base, "e2e-a1")

	out, err := tuiosCLI(t, base, "call", "switch-space", `{"none":true,"session":"e2e-a1"}`)
	var res struct {
		Type    string `json:"type"`
		Session string `json:"session"`
		None    bool   `json:"none"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Type != "space_switched" || res.Session != "e2e-g" || !res.None {
		t.Fatalf("ASSERTION: none: true did not switch to e2e-g: %v: %s", err, out)
	}
	clientShows(t, base, "e2e-g")
	if err := os.WriteFile(filepath.Join(dir, "none-verb.json"), []byte(out), 0o644); err != nil {
		t.Errorf("save the verb's answer: %v", err)
	}
}

// TestSpaceKeysOnAHost: a client attached to a session on another machine
// switches space there. The keys walk that machine's spaces, pick-space goes
// to that machine's daemon through the link, and the client switches itself
// over its connection to that daemon. Nothing is routed from the far daemon to
// the client, so the fence a client keeps against another machine's routed
// commands is not involved (internal/session/tuiclient_host.go is unchanged).
//
// How it could pass wrongly, and what stops it:
//   - the pick asked of this machine's daemon: it holds no session in beta or
//     alpha, refuses, and the client stays on far-a1.
//   - this machine's spaces walked: this machine has a session in zeta, and
//     } then } must go beta, then alpha, never off the far machine.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSpaceKeysOnAHost(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	for _, s := range []struct{ name, space string }{{"far-a1", "alpha"}, {"far-b1", "beta"}} {
		if out, err := tuiosCLI(t, remote, "new", s.name, "--detach", "--space", s.space); err != nil {
			t.Fatalf("create %s on the far machine: %v: %s", s.name, err, out)
		}
	}

	// The client starts this machine's daemon, with the ssh stand-in it needs
	// for the link, so the session here is made once it runs.
	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-a1"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	soleClientOn(t, remote, "far-a1")
	if out, err := tuiosCLI(t, base, "new", "home", "--detach", "--space", "zeta"); err != nil {
		t.Fatalf("create home here: %v: %s", err, out)
	}
	windowManagementMode(t, term)

	pressSpaceSwitch(t, term, "}")
	soleClientOn(t, remote, "far-b1")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "far-next-beta")

	pressSpaceSwitch(t, term, "}")
	soleClientOn(t, remote, "far-a1")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "far-next-wraps-alpha")

	pressSpaceSwitch(t, term, "{")
	soleClientOn(t, remote, "far-b1")
	settleAfterSwitch(t, term)

	// Nothing here moved, and nothing here was attached.
	for _, r := range clientRows(t, base) {
		if r.Session == "home" {
			t.Fatalf("ASSERTION: a client here was attached to home: %+v", clientRows(t, base))
		}
	}
	alive(t, term, "after switching spaces on a host")
}

// TestSpaceSwitchWithAnOlderDaemon: a client on a daemon from before
// pick-space does not switch space, and says why. Two clients are attached,
// and neither may move.
//
// It needs that older build: name it with TUIOS_E2E_BASELINE_BIN, a build
// that has spaces (the fixture files sessions with new --space) and no
// pick-space, such as the commit that adds the split space layout. Without
// it the test is skipped and says so.
//
// How it could pass wrongly, and what stops it:
//   - the keys unbound: an unbound key also leaves the client put. The dock
//     must say why, which only the space switch does.
//   - the client guessing: on the unknown verb it could pick a session from
//     its own listing and switch, which moves it. Both clients must stay, and
//     the notice is checked word for word.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space switching").
func TestSpaceSwitchWithAnOlderDaemon(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from a commit with spaces and no pick-space, such as the commit that adds the split space layout, and name it there")
	}
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail("split"))

	// The fixture's CLI starts the daemon, so the daemon is the older build.
	prev := tuiosBin
	tuiosBin = baseline
	restore := func() { tuiosBin = prev }
	// A fatal step inside must not leave every later test on the baseline.
	defer restore()
	spaceUIFixture(t, base, true)
	restore()

	other := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)

	const notice = "Switching spaces needs a newer tuios daemon"
	pressSpaceSwitch(t, term, "}")
	if err := term.WaitForText(notice, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: } on an older daemon never said why it did nothing: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "key-refused")
	stillOnPid(t, base, term.Pid(), "e2e-a1", "} on an older daemon")
	stillOnPid(t, base, other.Pid(), "e2e-a1", "} on an older daemon")

	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), notice) }, 3*uiTimeout); err != nil {
		t.Fatalf("the notice never cleared: %v\n%s", err, term.Snapshot())
	}
	beta := spaceRowOf(term.Screen(), "beta")
	if beta < 0 {
		t.Fatalf("no beta row in the spaces section\n%s", term.Snapshot())
	}
	mouseClick(t, term, 5, beta, tuitest.MouseLeft, 0)
	if err := term.WaitForText(notice, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on beta on an older daemon never said why it did nothing: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "row-refused")
	stillOnPid(t, base, term.Pid(), "e2e-a1", "a click on beta on an older daemon")
	stillOnPid(t, base, other.Pid(), "e2e-a1", "a click on beta on an older daemon")
	alive(t, term, "after space switches an older daemon cannot take")
}
