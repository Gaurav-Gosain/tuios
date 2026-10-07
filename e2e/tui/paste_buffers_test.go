package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Paste buffers (issue #514): each copy-mode yank is kept as a paste buffer in
// the daemon, prefix ] pastes the newest, prefix # lists them to pick one, and
// the CLI and the tmux shim read and write the same buffers.
//
// How these could pass wrongly, written down first:
//   - A pasted line could match text already on screen. The yanked words are
//     printed with a split in the typed command, and every paste is checked as
//     a whole output line that only running the pasted command prints.
//   - A buffer list could come from the client rather than the daemon. The
//     list is read with the CLI, from outside the client.
//   - The chooser could paste the newest buffer whatever the selection. It
//     pastes the older one, which ] does not.
//   - A bracketed paste check could pass on text the pane echoes anyway. The
//     same paste goes once with bracketed paste off, where no marks may show,
//     and once with it on, where they must.
//   - A grant check could pass because the call failed for another reason.
//     Each refusal is matched on the grant it names, and each refused call
//     has a positive half that is served once the grant is given.

// pbSession is the session the paste buffer tests attach to.
const pbSession = "e2e-pb"

// startPasteBufferClient starts a daemon session under base with the copy
// cursor config and attaches a client to it, in terminal mode.
func startPasteBufferClient(t *testing.T, base, extraConfig string) *tuitest.Terminal {
	t.Helper()
	killDaemon(t, base)
	writeConfig(t, base, copyCursorConfig+extraConfig)
	if out, err := tuiosCLI(t, base, "new", pbSession, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", pbSession}, env: copyColorOpts.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	return term
}

// pbListing is the JSON of tuios list-buffers.
type pbListing struct {
	Buffers []struct {
		Name   string `json:"name"`
		Bytes  int    `json:"bytes"`
		Sample string `json:"sample"`
	} `json:"buffers"`
	Limit int `json:"limit"`
}

// listBuffers reads the daemon's buffers with the CLI.
func listBuffers(t *testing.T, base string) pbListing {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-buffers", "--json")
	if err != nil {
		t.Fatalf("list-buffers: %v\n%s", err, out)
	}
	var l pbListing
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("list-buffers gave no JSON: %v\n%s", err, out)
	}
	return l
}

// yankLine finds text with a copy-mode search and yanks its line.
func yankLine(t *testing.T, term *tuitest.Terminal, search string) {
	t.Helper()
	if err := term.SendKeys(search, tuitest.Enter); err != nil {
		t.Fatalf("search %s: %v", search, err)
	}
	time.Sleep(300 * time.Millisecond)
	for _, k := range []string{"V", "y"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("Yanked", uiTimeout); err != nil {
		t.Fatalf("no yank after %s: %v\n%s", search, err, term.Snapshot())
	}
}

// screenHasLine reports whether a row of the screen shows want as output: on
// a row with no echo before it, so the typed command line does not count.
// The row also holds the pane's borders, so it is not compared whole.
func screenHasLine(s tuitest.Screen, want string) bool {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if i := strings.Index(line, want); i >= 0 && !strings.Contains(line[:i], "echo") {
			return true
		}
	}
	return false
}

// waitBufferCount waits for the daemon to hold n buffers, since a yank's
// buffer is saved off the client's update loop.
func waitBufferCount(t *testing.T, base string, n int, what string) pbListing {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		l := listBuffers(t, base)
		if len(l.Buffers) == n {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the daemon holds %d buffers, want %d: %+v", what, len(l.Buffers), n, l.Buffers)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPasteBuffersKeepYanksAndPasteThem yanks two lines in copy mode and
// pastes them back with the prefix keys, then drives the same buffers from
// the CLI and the tmux shim.
//
// Negative controls: with the SaveToPasteBuffers call cut from
// copyModeEffects.apply, the daemon holds no buffer after the yanks. With the
// paste_buffer Register line cut, prefix ] pastes nothing and PASTED-pbbravo42
// never prints. With the choose_buffer Register line cut, the chooser never
// opens. With the bracketed wrap cut from verbPasteBuffer, the marks never
// show with bracketed paste on. With daemonBuffers in the shim always false,
// the shim's set-buffer never reaches the daemon.
func TestPasteBuffersKeepYanksAndPasteThem(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")

	runInShell(t, term, `printf 'pb''alpha42\npb''bravo42\n'`, "pbbravo42", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	// Two yanks in copy mode.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	yankLine(t, term, "?pbalpha42")
	yankLine(t, term, "/pbbravo42")
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("leave copy mode: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := copyCursorCell(s)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("q did not leave copy mode\n%s", term.Snapshot())
	}

	// The daemon holds both, newest first: the CLI reads them from outside
	// the client.
	l := waitBufferCount(t, base, 2, "after two yanks")
	if l.Buffers[0].Sample != "pbbravo42" || l.Buffers[1].Sample != "pbalpha42" {
		t.Fatalf("the buffers are %+v, want pbbravo42 then pbalpha42", l.Buffers)
	}
	if l.Limit != 20 {
		t.Errorf("the default limit is %d, want 20", l.Limit)
	}
	if out, err := tuiosCLI(t, base, "show-buffer"); err != nil || out != "pbbravo42" {
		t.Fatalf("show-buffer printed %q (%v), want pbbravo42 with no line feed", out, err)
	}

	// prefix ] pastes the newest into the pane.
	if err := term.SendKeys("echo PASTED-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "]"); err != nil {
		t.Fatalf("send prefix+]: %v", err)
	}
	if err := term.WaitForText("Pasted", uiTimeout); err != nil {
		t.Fatalf("prefix ] said nothing: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, "PASTED-pbbravo42") }, shellTimeout); err != nil {
		t.Fatalf("prefix ] did not paste the newest buffer: %v\n%s", err, term.Snapshot())
	}

	// prefix # lists them, and the older one is pasted from the list.
	if err := term.SendKeys("echo CHOSEN-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "#"); err != nil {
		t.Fatalf("send prefix+#: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Paste buffers") && strings.Contains(text, "pbalpha42")
	}, uiTimeout); err != nil {
		t.Fatalf("prefix # did not list the buffers: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-chooser")
	if err := term.SendKeys("j"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Paste buffers") }, uiTimeout); err != nil {
		t.Fatalf("the chooser did not close after enter\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, "CHOSEN-pbalpha42") }, shellTimeout); err != nil {
		t.Fatalf("the chooser did not paste the older buffer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-pasted")

	// The tmux shim reads and writes the same buffers.
	if out, err := tuiosCLI(t, base, "set-buffer", "-b", "clibuf", "fromcli-xyz"); err != nil {
		t.Fatalf("set-buffer: %v\n%s", err, out)
	}
	runInShell(t, term, tuiosBin+` tmux-shim -- sh -c 'tmux show-buffer -b clibuf | tr a-z A-Z; echo; tmux set-buffer -b shimbuf "shim$((6*7))"'`, "FROMCLI-XYZ", shellTimeout)
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(t, base, "show-buffer", "-b", "shimbuf")
		if err == nil && out == "shim42" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shim's set-buffer did not reach the daemon: show-buffer printed %q (%v)", out, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// paste-buffer from the CLI: no marks while the pane's program has
	// bracketed paste off, the marks once it turns it on.
	runInShell(t, term, `printf '\033[?2004l'; echo CAT""OFF; cat -v`, "CATOFF", shellTimeout)
	if out, err := tuiosCLI(t, base, "paste-buffer", "-s", pbSession, "-b", "clibuf"); err != nil {
		t.Fatalf("paste-buffer: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("fromcli-xyz", shellTimeout); err != nil {
		t.Fatalf("paste-buffer typed nothing: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "[200~") {
		t.Fatalf("the paste carried the bracketed marks with bracketed paste off\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, `clear; printf '\033[?2004h'; echo CAT""ON; cat -v`, "CATON", shellTimeout)
	if out, err := tuiosCLI(t, base, "paste-buffer", "-s", pbSession, "-b", "clibuf", "-d"); err != nil {
		t.Fatalf("paste-buffer -d: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "[200~fromcli-xyz^[[201~")
	}, shellTimeout); err != nil {
		t.Fatalf("the paste did not carry the bracketed marks with bracketed paste on\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatal(err)
	}
	saveFrame(t, term, "paste-buffers-bracketed")

	// -d deleted it, and delete-buffer deletes the next.
	for _, b := range listBuffers(t, base).Buffers {
		if b.Name == "clibuf" {
			t.Fatalf("paste-buffer -d left clibuf in place")
		}
	}
	if out, err := tuiosCLI(t, base, "delete-buffer", "-b", "shimbuf"); err != nil {
		t.Fatalf("delete-buffer: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "show-buffer", "-b", "shimbuf"); err == nil || !strings.Contains(out, "no such buffer") {
		t.Fatalf("show-buffer of a deleted buffer printed %q (%v), want no such buffer", out, err)
	}
	alive(t, term, "after the paste buffers")
}

// TestPasteBufferVerbsFollowPaneGrants runs the buffer commands from inside a
// pane held to its grants. Reading needs read, changing needs write, and a
// paste needs both, since it types the text where the pane can read it back.
//
// Negative control: with the readsBuffers check cut from checkGrants, the
// pane without read reads the buffer and NONE_SHOW=1 never prints.
func TestPasteBufferVerbsFollowPaneGrants(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	if out, err := tuiosCLI(t, base, "set-buffer", "-b", "secret", "hunter"+"2"); err != nil {
		t.Fatalf("set-buffer from outside every pane: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", pbSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows gave no single window: %v\n%s", err, out)
	}
	pane := listing.Windows[0].WindowID
	grant := func(names string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", names); err != nil {
			t.Fatalf("set-pane-grants %s: %v\n%s", names, err, out)
		}
	}
	bin := tuiosBin

	// No grants: reading is refused, and names the read grant.
	grant("none")
	runInShell(t, term, "clear; "+bin+" show-buffer -b secret; echo NONE_SHOW=$?", "NONE_SHOW=1", shellTimeout)
	if !strings.Contains(term.Screen().Text(), "read grant") {
		t.Fatalf("the refusal does not name the read grant\n%s", term.Snapshot())
	}

	// read: reading is served, writing is refused.
	grant("read")
	runInShell(t, term, "clear; "+bin+" show-buffer -b secret | tr a-z A-Z; echo; "+bin+" set-buffer -b mine x; echo READ_SET=$?", "READ_SET=1", shellTimeout)
	text := term.Screen().Text()
	if !strings.Contains(text, "HUNTER2") {
		t.Fatalf("a pane with read could not read the buffer\n%s", term.Snapshot())
	}
	if !strings.Contains(text, "write grant") {
		t.Fatalf("the set-buffer refusal does not name the write grant\n%s", term.Snapshot())
	}

	// write alone: setting is served, reading and pasting are refused.
	grant("write")
	runInShell(t, term, "clear; "+bin+" set-buffer -b mine x; echo WRITE_SET=$?; "+bin+" paste-buffer -b secret; echo WRITE_PASTE=$?", "WRITE_PASTE=1", shellTimeout)
	text = term.Screen().Text()
	if !strings.Contains(text, "WRITE_SET=0") {
		t.Fatalf("a pane with write could not set a buffer\n%s", term.Snapshot())
	}
	if strings.Contains(text, "hunter2") {
		t.Fatalf("a pane without read pasted the buffer\n%s", term.Snapshot())
	}

	// read and write: the paste is served.
	grant("read,write")
	runInShell(t, term, "clear; "+bin+" set-buffer -b cmd 'echo PASTE'\"\"'_OK'; "+bin+" paste-buffer -b cmd; echo", "PASTE_OK", shellTimeout)
	saveFrame(t, term, "paste-buffers-grants")
	alive(t, term, "after the grant checks")
}

// TestPasteBufferLimitFollowsTheConfig keeps two buffers under limit = 2,
// drops the oldest on a third, and keeps one after the file says limit = 1,
// with no restart.
//
// Negative control: with the SetLimits call cut from applyUserConfig, two
// buffers stay after the file changes.
func TestPasteBufferLimitFollowsTheConfig(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[paste_buffers]\nlimit = 2\n")
	if out, err := tuiosCLI(t, base, "new", pbSession, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	for _, text := range []string{"one", "two", "three"} {
		if out, err := tuiosCLI(t, base, "set-buffer", text); err != nil {
			t.Fatalf("set-buffer %s: %v\n%s", text, err, out)
		}
	}
	l := listBuffers(t, base)
	if len(l.Buffers) != 2 || l.Buffers[0].Sample != "three" || l.Buffers[1].Sample != "two" {
		t.Fatalf("under limit = 2 the buffers are %+v, want three then two", l.Buffers)
	}
	if l.Limit != 2 {
		t.Fatalf("list-buffers reports limit %d, want 2", l.Limit)
	}

	saveConfigLikeAnEditor(t, base, "[paste_buffers]\nlimit = 1\n")
	deadline := time.Now().Add(configWatchTimeout)
	for {
		l = listBuffers(t, base)
		if len(l.Buffers) == 1 && l.Buffers[0].Sample == "three" && l.Limit == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after limit = 1 the buffers are %+v with limit %d, want three alone", l.Buffers, l.Limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestPasteBuffersWithoutADaemon is the same yank and paste in a client with
// no daemon, which keeps its own buffers.
//
// Negative control: with the local store branch of SaveToPasteBuffers cut,
// prefix ] says there are no paste buffers.
func TestPasteBuffersWithoutADaemon(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	term := startIn(t, base, startOpts{env: copyColorOpts.env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'pb''local42\n'`, "pblocal42", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	yankLine(t, term, "?pblocal42")
	if err := term.SendKeys("q"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys("echo LOCAL-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "]"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Pasted", uiTimeout); err != nil {
		t.Fatalf("prefix ] said nothing: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, "LOCAL-pblocal42") }, shellTimeout); err != nil {
		t.Fatalf("prefix ] did not paste with no daemon: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a paste with no daemon")
}
