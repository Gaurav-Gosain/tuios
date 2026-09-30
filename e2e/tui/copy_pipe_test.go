package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Copy-pipe keys against the real binary (discussion #281): three lines
// selected in copy mode, piped through tr by a [[keybindings.copy_pipe]] key,
// and the clipboard write read off the wire as OSC 52.
//
// How this could pass wrongly, written down first:
//   - The clipboard could hold the plain selection. The want has no new line,
//     and only tr takes the new lines out.
//   - A stale write could match. Each check counts the writes before the key
//     and reads only the new one.
//   - Copy mode could look kept when it was left and entered again. The copy
//     cursor must stay on the row where the selection ended.

const copyPipeConfig = copyCursorConfig + `
[[keybindings.copy_pipe]]
key = "p"
command = "tr '\n' ' '"
description = "flatten"

[[keybindings.copy_pipe]]
key = "P"
command = "tr '\n' ','"
cancel = true
`

// pipeWrite waits for the one clipboard write after from, and returns it.
func pipeWrite(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, from int, what string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for len(clipboardWrites(out)) <= from {
		if time.Now().After(deadline) {
			t.Fatalf("%s: nothing reached the clipboard\n%s", what, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(clipboardSettle)
	writes := clipboardSince(out, from)
	if len(writes) != 1 {
		t.Fatalf("%s: %d clipboard writes, want 1: %q", what, len(writes), writes)
	}
	return writes[0]
}

func TestCopyPipeKeys(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyPipeConfig)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: copyColorOpts.env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'pi''pe-one\npi''pe-two\npi''pe-three\n'`, "pipe-three", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	if err := term.SendKeys("?pipe-one", tuitest.Enter); err != nil {
		t.Fatalf("search: %v", err)
	}
	one := lastRowWith(term.Screen(), "pipe-one")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r == one
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not land on pipe-one (row %d)\n%s", one, term.Snapshot())
	}

	// copy-pipe: the flat line on the clipboard, and copy mode stays where
	// the selection ended.
	from := len(clipboardWrites(out))
	for _, k := range []string{"V", "j", "j"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.SendKeys("p"); err != nil {
		t.Fatalf("send p: %v", err)
	}
	if got, want := pipeWrite(t, term, out, from, "p"), "pipe-one pipe-two pipe-three"; got != want {
		t.Fatalf("p copied %q, want %q", got, want)
	}
	if err := term.WaitForText("from flatten", uiTimeout); err != nil {
		t.Fatalf("no dock message for the pipe: %v\n%s", err, term.Snapshot())
	}
	three := lastRowWith(term.Screen(), "pipe-three")
	if r, _ := waitCopyCursor(t, term, "after p"); r != three {
		t.Fatalf("after p the copy cursor is on row %d, want row %d of pipe-three\n%s", r, three, term.Snapshot())
	}
	t.Logf("after p:\n%s", term.Snapshot())

	// copy-pipe-and-cancel: the result on the clipboard, and copy mode ends.
	from = len(clipboardWrites(out))
	for _, k := range []string{"V", "k"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.SendKeys("P"); err != nil {
		t.Fatalf("send P: %v", err)
	}
	if got, want := pipeWrite(t, term, out, from, "P"), "pipe-two,pipe-three"; got != want {
		t.Fatalf("P copied %q, want %q", got, want)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := copyCursorCell(s)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("P did not leave copy mode\n%s", term.Snapshot())
	}
	alive(t, term, "after the copy pipes")
}
