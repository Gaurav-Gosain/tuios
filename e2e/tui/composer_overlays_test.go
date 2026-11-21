package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestReplyEditorShowsOneOverlayAtATime drives the reply editor in the agents
// view through the two overlays it can open over the draft: the @ list and
// the ctrl+r history search. One reply is sent first, so the history holds
// "hi". With the @ list open, ctrl+r shows the search alone, and enter loads
// "hi" with no list left over the shorter draft and no crash. esc in the
// search gives the list back while the cursor is still in the @ word, and a
// list closed with esc stays closed after the search opens and closes. Last,
// ctrl+g with the list open runs an external editor that writes "hi", and the
// draft takes it with no list drawn over it. Frames of the search, the loaded
// draft and the editor's text are saved with saveFrame.
//
// How this could pass wrongly, written down first:
//   - ctrl+r could do nothing, and "the list is not drawn with the search"
//     would hold on a screen with no search. Every check that the list is
//     gone is made on a frame that shows the step's own mark: "history:"
//     for the search, the draft line for the loaded or edited text.
//   - A key could be sent before the list was drawn, so the search would
//     open over a draft whose list never opened. Each step waits for the
//     list's row and its "tab insert" hint before ctrl+r or ctrl+g.
//   - The crash screen could be drawn after the last check. The test looks
//     for it after every key and at every look of every wait, so a crash
//     fails at once with its frame, and it holds the end state for a second.
//   - The history could be read before the sent reply reached it, and the
//     search open with nothing to load. The test opens the editor again
//     until ctrl+r shows a row "1 hi" before the rows that need it.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: main, with the list
// and the search drawn together and the crash screen after enter, fails;
// so do the search guard in syncMention cut, the re-sync in change cut, and
// the re-sync when the search closes cut.
func TestReplyEditorShowsOneOverlayAtATime(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	editor := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s' hi > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0", "EDITOR=" + editor, "VISUAL="}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	screen := func() string { return term.Screen().Text() }
	listOpen := func() bool {
		s := screen()
		return strings.Contains(s, "pane  home/") && strings.Contains(s, "tab insert")
	}
	listRow := func() bool { return strings.Contains(screen(), "pane  home/") }
	crashed := func() bool { return strings.Contains(screen(), "tuios hit a bug") }
	alive := func(what string, probe func() (bool, string)) func() (bool, string) {
		return func() (bool, string) {
			t.Helper()
			if crashed() {
				t.Fatalf("ASSERTION: the client crashed while waiting for %s\n%s", what, term.Snapshot())
			}
			return probe()
		}
	}
	waitScreen := func(what string, want ...string) {
		t.Helper()
		waitUntil(t, "waiting for "+what, uiTimeout, alive(what, func() (bool, string) {
			s := term.Snapshot()
			for _, w := range want {
				if !strings.Contains(s, w) {
					return false, s
				}
			}
			return true, s
		}))
	}
	send := func(what string, keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if crashed() {
			t.Fatalf("ASSERTION: the client crashed after %s\n%s", what, term.Snapshot())
		}
	}
	draftShown := func(text string) bool { return strings.Contains(screen(), "› "+text) }
	waitDraft := func(text string) {
		t.Helper()
		waitUntil(t, "the draft line reads "+text, uiTimeout, alive("the draft "+text, func() (bool, string) {
			return draftShown(text), term.Snapshot()
		}))
		if crashed() {
			t.Fatalf("ASSERTION: the client crashed with the draft %q\n%s", text, term.Snapshot())
		}
	}
	waitList := func(what string) {
		t.Helper()
		waitUntil(t, "the @ list opens "+what, uiTimeout, alive("the @ list "+what, func() (bool, string) {
			return listOpen(), term.Snapshot()
		}))
	}
	waitListGone := func(what string) {
		t.Helper()
		waitUntil(t, "the @ list closes "+what, uiTimeout, alive("the @ list to close "+what, func() (bool, string) {
			return draftShown("hello @") && !listRow(), term.Snapshot()
		}))
	}
	waitSearch := func(what string) {
		t.Helper()
		waitScreen("the history search "+what, "history:")
	}
	waitSearchGone := func(what string) {
		t.Helper()
		waitUntil(t, "the history search closes "+what, uiTimeout, alive("the history search to close "+what, func() (bool, string) {
			return !strings.Contains(screen(), "history:"), term.Snapshot()
		}))
	}
	openEditor := func() {
		t.Helper()
		waitScreen("the closed reply box", "Write a reply.")
		row, col := findText(t, term, "Write a reply.")
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		waitScreen("the open reply editor", "Type your reply.")
	}

	send("open the agents view", tuitest.Ctrl('b'), "i")
	waitScreen("the closed reply area", "Reply to docs-cli", "Write a reply.")
	openEditor()

	send("ctrl+r with no history", tuitest.Ctrl('r'))
	waitScreen("the empty history's line", "Nothing was sent to this agent yet.")
	if strings.Contains(screen(), "history:") {
		t.Fatalf("ASSERTION: ctrl+r opened a search over an empty history\n%s", term.Snapshot())
	}
	send("type hi", "hi")
	waitDraft("hi")
	send("send hi", tuitest.Enter)
	waitScreen("the closed box after the send", "Write a reply.")
	waitUntil(t, "an editor whose history holds the sent reply", uiTimeout, alive("the sent reply in the history", func() (bool, string) {
		openEditor()
		send("ctrl+r", tuitest.Ctrl('r'))
		waitUntil(t, "the search or the empty history's line", uiTimeout, alive("the search or the empty history's line", func() (bool, string) {
			s := screen()
			return strings.Contains(s, "history:") || strings.Contains(s, "Nothing was sent to this agent yet."), term.Snapshot()
		}))
		s := screen()
		if strings.Contains(s, "history:") && strings.Contains(s, "1 hi") {
			send("esc in the search", tuitest.Esc)
			waitSearchGone("after esc")
			return true, s
		}
		snap := term.Snapshot()
		send("esc on the empty history's line", tuitest.Esc)
		waitScreen("the closed box after esc", "Write a reply.")
		return false, snap
	}))
	waitScreen("the empty editor", "Type your reply.")

	send("type hello @", "hello @")
	waitList("on hello @")
	send("ctrl+r with the list open", tuitest.Ctrl('r'))
	var frame string
	waitUntil(t, "the history search opens over the @ list", uiTimeout, alive("the history search over the @ list", func() (bool, string) {
		frame = term.Snapshot()
		return strings.Contains(frame, "history:"), frame
	}))
	if strings.Contains(frame, "pane  home/") {
		t.Fatalf("ASSERTION: the @ list is drawn with the history search\n%s", frame)
	}
	saveFrame(t, term, "overlay-search")
	stayTrue(t, "the search shows without the @ list", time.Second, alive("the search without the @ list", func() (bool, string) {
		s := screen()
		return strings.Contains(s, "history:") && !strings.Contains(s, "pane  home/"), term.Snapshot()
	}))
	send("enter in the search", tuitest.Enter)
	waitDraft("hi")
	if frame = term.Snapshot(); !strings.Contains(frame, "› hi") || strings.Contains(frame, "pane  home/") {
		t.Fatalf("ASSERTION: the @ list is drawn over the loaded draft\n%s", frame)
	}
	saveFrame(t, term, "overlay-loaded")

	send("ctrl+c to clear the loaded draft", tuitest.Ctrl('c'))
	waitScreen("the cleared draft", "Type your reply.")
	send("type hello @", "hello @")
	waitList("on hello @ again")
	send("ctrl+r with the list open", tuitest.Ctrl('r'))
	waitSearch("over the @ list")
	send("esc in the search", tuitest.Esc)
	waitUntil(t, "the search closes and the @ list is open again", uiTimeout, alive("the @ list after the search", func() (bool, string) {
		return !strings.Contains(screen(), "history:") && listOpen(), term.Snapshot()
	}))
	send("esc on the list", tuitest.Esc)
	waitListGone("after esc")
	send("ctrl+c to clear the draft", tuitest.Ctrl('c'))
	waitScreen("the cleared draft", "Type your reply.")

	send("type hello @", "hello @")
	waitList("on a third hello @")
	send("esc on the list", tuitest.Esc)
	waitListGone("after esc")
	send("ctrl+r with the list dismissed", tuitest.Ctrl('r'))
	waitSearch("with the list dismissed")
	send("esc in the search", tuitest.Esc)
	waitSearchGone("with the list dismissed")
	stayTrue(t, "the dismissed list stays closed after the search", time.Second, alive("the dismissed list to stay closed", func() (bool, string) {
		return draftShown("hello @") && !listRow(), term.Snapshot()
	}))
	send("ctrl+c to clear the draft", tuitest.Ctrl('c'))
	waitScreen("the cleared draft", "Type your reply.")

	send("type hello @", "hello @")
	waitList("before the external editor")
	send("ctrl+g with the list open", tuitest.Ctrl('g'))
	waitDraft("hi")
	saveFrame(t, term, "overlay-editor")
	stayTrue(t, "no @ list over the editor's text", time.Second, alive("the editor's text with no @ list", func() (bool, string) {
		return draftShown("hi") && !listRow() && !crashed(), term.Snapshot()
	}))
}
