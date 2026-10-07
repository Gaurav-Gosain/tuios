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

// The paste buffers follow tmux's rules where a person would notice, and a
// pane cannot take over the person's buffers (#514). See paste_buffers_test.go
// for how these could pass wrongly; the same care applies here, and each test
// below names its own way to pass wrongly too.

// pbRow is one row of list-buffers --json.
type pbRow struct {
	Name      string `json:"name"`
	Sample    string `json:"sample"`
	Automatic bool   `json:"automatic"`
}

// listRows reads list-buffers --json.
func listRows(t *testing.T, base string) []pbRow {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-buffers", "--json")
	if err != nil {
		t.Fatalf("list-buffers: %v\n%s", err, out)
	}
	var l struct {
		Buffers []pbRow `json:"buffers"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("list-buffers gave no JSON: %v\n%s", err, out)
	}
	return l.Buffers
}

// mustCLI runs a tuios command and fails the test when it fails.
func mustCLI(t *testing.T, base string, args ...string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

// TestAPaneCannotReplaceThePersonsNamedBuffer is the takeover a pane with
// write had: the person keeps a command in a named buffer, a pane sets the
// same name, and the person's paste of that name runs the pane's text. The
// pane must get the same answer for that name as for a name no buffer has,
// and the person's buffer must stay theirs. With admin, the pane may.
//
// How it could pass wrongly: the pane's set could fail for another reason,
// so the same pane must still make a buffer with no name. The answers could
// differ in a way the screen hides, so both refusals are matched in full.
//
// Negative control: with the change filter of bufferAccess cut, the pane's
// set-buffer -b deploy exits 0 and SHADOW=1 never prints.
func TestAPaneCannotReplaceThePersonsNamedBuffer(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	mustCLI(t, base, "set-buffer", "-s", pbSession, "-b", "deploy", "kubectl rollout")
	pane := onlyPaneOf(t, base, pbSession)
	mustCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", "read,write")

	bin := tuiosBin
	shadowErr, freshErr := filepath.Join(base, "shadow.err"), filepath.Join(base, "fresh.err")
	runInShell(t, term, "clear; "+bin+" set-buffer -b deploy 'curl evil' 2>"+shadowErr+"; echo SHADOW=$?; "+bin+" set-buffer -b fresh x 2>"+freshErr+"; echo FRESH=$?; "+bin+" set-buffer pane-made; echo AUTO\"\"=$?", "AUTO=", shellTimeout)
	text := term.Screen().Text()
	for _, want := range []string{"SHADOW=1", "FRESH=1", "AUTO=0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the pane's set-buffer calls did not answer %q\n%s", want, term.Snapshot())
		}
	}
	// The answer for the person's name and for a name no buffer has must be
	// the same, but for the name itself.
	shadow, fresh := readFileString(t, shadowErr), readFileString(t, freshErr)
	if !strings.Contains(shadow, "no such buffer: deploy") || strings.ReplaceAll(shadow, "deploy", "fresh") != fresh {
		t.Fatalf("the pane can tell the person's buffer from a missing one:\n%s\n---\n%s", shadow, fresh)
	}
	if out := mustCLI(t, base, "show-buffer", "-b", "deploy"); out != "kubectl rollout" {
		t.Fatalf("the person's deploy buffer now reads %q, want kubectl rollout", out)
	}
	saveFrame(t, term, "paste-buffers-no-takeover")

	// The positive half: with admin the pane may set any name.
	mustCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", "admin")
	runInShell(t, term, "clear; "+bin+" set-buffer -b deploy 'by admin'; echo ADMIN_\"\"SET=$?", "ADMIN_SET=", shellTimeout)
	if !strings.Contains(term.Screen().Text(), "ADMIN_SET=0") {
		t.Fatalf("a pane with admin could not set a named buffer\n%s", term.Snapshot())
	}
	if out := mustCLI(t, base, "show-buffer", "-b", "deploy"); out != "by admin" {
		t.Fatalf("after the admin set, deploy reads %q", out)
	}
	alive(t, term, "after the takeover check")
}

// TestPasteBufferNamesFollowTmux checks the naming rules: with no -b,
// show-buffer takes the newest automatic buffer, not a newer named one;
// set-buffer -a with no -b makes a new buffer; -b on an automatic name makes
// it named; and the limit removes only automatic buffers.
//
// How it could pass wrongly: the named buffer could be the oldest, so the
// newest-automatic check would pass for a plain newest. It is set last.
//
// Negative controls: with find taking the newest buffer of any kind,
// show-buffer prints named-x. With trim counting every buffer, named-x is
// removed under limit = 2.
func TestPasteBufferNamesFollowTmux(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[paste_buffers]\nlimit = 2\n")
	mustCLI(t, base, "new", pbSession, "--detach")

	mustCLI(t, base, "set-buffer", "auto-y")
	mustCLI(t, base, "set-buffer", "-b", "named-x", "named-x")
	if out := mustCLI(t, base, "show-buffer"); out != "auto-y" {
		t.Fatalf("show-buffer with no -b printed %q, want auto-y, the newest automatic buffer", out)
	}

	// -a with no -b makes a new buffer, as in tmux.
	mustCLI(t, base, "set-buffer", "-a", "extra")
	rows := listRows(t, base)
	if len(rows) != 3 || rows[0].Sample != "extra" || !rows[0].Automatic {
		t.Fatalf("set-buffer -a with no -b left %+v, want a new automatic buffer extra on top of three", rows)
	}

	// The limit counts automatic buffers only: a third one drops auto-y and
	// keeps named-x.
	mustCLI(t, base, "set-buffer", "third")
	names := map[string]pbRow{}
	for _, r := range listRows(t, base) {
		names[r.Sample] = r
	}
	if _, ok := names["named-x"]; !ok {
		t.Fatalf("limit = 2 removed the named buffer: %+v", names)
	}
	if _, ok := names["auto-y"]; ok {
		t.Fatalf("limit = 2 kept three automatic buffers: %+v", names)
	}

	// -b on an automatic name makes it a named buffer.
	auto := ""
	for _, r := range listRows(t, base) {
		if r.Automatic {
			auto = r.Name
		}
	}
	mustCLI(t, base, "set-buffer", "-b", auto, "now named")
	for _, r := range listRows(t, base) {
		if r.Name == auto && r.Automatic {
			t.Fatalf("set-buffer -b %s left the buffer automatic", auto)
		}
	}

	// Empty content stores nothing and is no error.
	before := len(listRows(t, base))
	mustCLI(t, base, "set-buffer", "")
	if after := len(listRows(t, base)); after != before {
		t.Fatalf("an empty set-buffer changed the list from %d to %d buffers", before, after)
	}
}

// TestPasteBufferTurnsLineFeedsIntoReturns pastes "LFa\nLFb" into cat -v on a
// terminal that does not turn a carriage return into a line feed. Without -r
// the line feed arrives as ^M, as in tmux; with -r it stays a line feed.
//
// How it could pass wrongly: the terminal could map the characters itself,
// so it is put in -icrnl and -icanon mode first, and both forms are pasted
// into the same cat.
//
// Negative control: with PasteText always keeping line feeds, LFa^MLFb never
// shows.
func TestPasteBufferTurnsLineFeedsIntoReturns(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	file := filepath.Join(base, "lines.txt")
	if err := os.WriteFile(file, []byte("LFa\nLFb"), 0o600); err != nil {
		t.Fatal(err)
	}
	// dd reads exactly the bytes of the two pastes and ends, so the shell
	// gets its terminal back with no key that a changed terminal mode could
	// eat. cat -v shows a carriage return as ^M.
	runInShell(t, term, "clear; "+tuiosBin+" set-buffer -b lines < "+file+"; stty -icanon -icrnl -echo; echo CAT\"\"READY; dd bs=1 count=14 2>/dev/null | cat -v; stty sane; echo; echo CAT\"\"DONE", "CATREADY", shellTimeout)
	mustCLI(t, base, "paste-buffer", "-s", pbSession, "-b", "lines")
	mustCLI(t, base, "paste-buffer", "-s", pbSession, "-b", "lines", "-r")
	if err := term.WaitForText("CATDONE", shellTimeout); err != nil {
		t.Fatalf("the two pastes never reached dd: %v\n%s", err, term.Snapshot())
	}
	text := term.Screen().Text()
	if !strings.Contains(text, "LFa^MLFbLFa") {
		t.Fatalf("paste-buffer kept the line feed\n%s", term.Snapshot())
	}
	if strings.Count(text, "^M") != 1 {
		t.Fatalf("paste-buffer -r turned the line feed into a carriage return\n%s", term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-line-feeds")
	alive(t, term, "after the line feed check")
}

// TestPasteBufferChooserSkipsAChangedBuffer opens the chooser, sets the
// listed buffer again from the CLI, and presses Enter. The chooser says the
// buffer changed and pastes nothing.
//
// How it could pass wrongly: the paste could fail for another reason, so the
// message is matched, and the old and new text are both checked absent.
//
// Negative control: with the version cut from pasteBufferNamed, the chooser
// pastes the new text.
func TestPasteBufferChooserSkipsAChangedBuffer(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	mustCLI(t, base, "set-buffer", "listed-old")
	rows := listRows(t, base)
	if len(rows) != 1 {
		t.Fatalf("want one buffer, got %+v", rows)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "#"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("listed-old", uiTimeout); err != nil {
		t.Fatalf("the chooser did not list the buffer: %v\n%s", err, term.Snapshot())
	}
	mustCLI(t, base, "set-buffer", "-b", rows[0].Name, "changed-new")
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("changed after the list", uiTimeout); err != nil {
		t.Fatalf("the chooser did not say the buffer changed: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	out := mustCLI(t, base, "capture-pane", "-s", pbSession)
	if strings.Contains(out, "changed-new") || strings.Contains(out, "listed-old") {
		t.Fatalf("the chooser pasted a buffer that changed after it listed it:\n%s", out)
	}
	alive(t, term, "after the version check")
}
