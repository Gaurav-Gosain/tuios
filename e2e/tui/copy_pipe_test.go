package tuie2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Copy-pipe keys against the real binary (discussion #281): three lines
// selected in copy mode, piped through tr by a [[keybindings.copy_pipe]] key,
// and the clipboard write read off the wire as OSC 52. Then a command that
// sets the paste buffer and its own clipboard (a file here) and prints
// nothing, as tmux-copy-it does: tuios must leave both alone.
//
// How this could pass wrongly, written down first:
//   - The clipboard could hold the plain selection. The want has no new line,
//     and only tr takes the new lines out.
//   - A stale write could match. Each check counts the writes before the key
//     and reads only the new one.
//   - Copy mode could look kept when it was left and entered again. The copy
//     cursor must stay on the row where the selection ended.
//   - The silent command's checks could pass because it never ran. Its file
//     and its buffer must hold its value before anything is read.
//   - A late clipboard write or buffer could come after the checks. Both are
//     read after a settle, and the buffers are counted, not only the newest.
//
//   - A failing command could look handled by the error message alone. The
//     plain selection must reach the clipboard and the newest paste buffer.
//
// Negative control: with handleCopyPipeDone copying the selection when the
// output is empty, as before, the silent key puts "pipe-three" on the
// clipboard. With the selection saved to a buffer before the command runs,
// as before, the silent key adds two buffers. See NEGATIVE_CONTROLS.md.

const copyPipeConfig = copyCursorConfig + `
[[keybindings.copy_pipe]]
key = "p"
command = "tr '\n' ' '"
description = "flatten"

[[keybindings.copy_pipe]]
key = "P"
command = "tr '\n' ','"
cancel = true

[[keybindings.copy_pipe]]
key = "x"
command = "echo refused >&2; exit 3"
description = "broken"
`

// silentPipeValue is what the silent copy pipe writes to its file and its
// paste buffer.
const silentPipeValue = "set-by-the-command"

// silentPipeConfig is a [[keybindings.copy_pipe]] entry on X whose command
// writes its own clipboard (a file) and paste buffer, and prints nothing.
func silentPipeConfig(file string) string {
	return fmt.Sprintf(`
[[keybindings.copy_pipe]]
key = "X"
command = "printf %%s %[1]s > '%[2]s' && '%[3]s' set-buffer %[1]s"
description = "silent"
`, silentPipeValue, file, tuiosBin)
}

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
	silentFile := filepath.Join(base, "silent-clipboard")
	writeConfig(t, base, copyPipeConfig+silentPipeConfig(silentFile))
	out := &lockedBuffer{}
	// A daemon session, so the paste buffers are the daemon's and the
	// command's set-buffer reaches the same ones.
	term := startIn(t, base, startOpts{out: out, env: copyColorOpts.env, args: []string{"new", "pipe"}})
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

	// A command that sets the clipboard and the buffer itself and prints
	// nothing: tuios writes neither, so the command's values stay.
	from = len(clipboardWrites(out))
	buffersBefore := len(listBuffers(t, base).Buffers)
	for _, k := range []string{"V", "X"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("silent wrote no output. The clipboard is unchanged.", uiTimeout); err != nil {
		t.Fatalf("no dock message for the silent pipe: %v\n%s", err, term.Snapshot())
	}
	waitForFileText(t, silentFile, func(s string) bool { return s == silentPipeValue }, "the silent pipe never wrote its file")
	if got := mustCLI(t, base, "show-buffer"); got != silentPipeValue {
		t.Fatalf("the newest buffer is %q, want the command's %q", got, silentPipeValue)
	}
	time.Sleep(clipboardSettle + 500*time.Millisecond)
	if writes := clipboardSince(out, from); len(writes) != 0 {
		t.Fatalf("the silent pipe wrote the clipboard over the command's value: %q", writes)
	}
	if got := mustCLI(t, base, "show-buffer"); got != silentPipeValue {
		t.Fatalf("after a settle the newest buffer is %q, want the command's %q", got, silentPipeValue)
	}
	if n := len(listBuffers(t, base).Buffers); n != buffersBefore+1 {
		t.Fatalf("the silent pipe left %d new buffers, want 1, the command's own", n-buffersBefore)
	}

	// A command that fails: the error on the dock, and the plain selection
	// on the clipboard and in the newest buffer.
	from = len(clipboardWrites(out))
	for _, k := range []string{"V", "x"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if got, want := strings.TrimRight(pipeWrite(t, term, out, from, "x"), "\n"), "pipe-three"; got != want {
		t.Fatalf("the failing pipe copied %q, want the selection %q", got, want)
	}
	if err := term.WaitForText("broken failed with exit code 3: refused.", uiTimeout); err != nil {
		t.Fatalf("no dock message for the failing pipe: %v\n%s", err, term.Snapshot())
	}
	if got, want := strings.TrimRight(mustCLI(t, base, "show-buffer"), "\n"), "pipe-three"; got != want {
		t.Fatalf("after the failing pipe the newest buffer is %q, want the selection %q", got, want)
	}

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
