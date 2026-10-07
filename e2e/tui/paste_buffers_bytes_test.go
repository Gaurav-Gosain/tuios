package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Paste buffers that hold bytes, not only text (#514). A tmux buffer can hold
// any bytes, and the tmux shim's files kept every one. With the daemon holding
// the buffers, every byte has to survive the trip over the JSON protocol, and
// a buffer sent in parts has to come back whole.
//
// How these could pass wrongly: a check on text alone passes when a byte that
// is not UTF-8 is lost, so the files are compared byte for byte. A file under
// one part never takes the parts path, so the large file is over 1 MiB, and
// its characters are three bytes wide, so a cut on a byte count lands inside
// one.

// shimRoundTrip loads in into a buffer through the tmux shim, saves the
// buffer to out, and waits for the shell to say it is done.
func shimRoundTrip(t *testing.T, term *tuitest.Terminal, base, in, out, buffer string) {
	t.Helper()
	// The marker names the buffer, so the end of an earlier round trip on
	// the screen cannot end the wait for this one.
	marker := strings.ToUpper(buffer) + "_EXIT="
	runInShell(t, term, "clear; "+tuiosBin+" tmux-shim -- sh -c 'tmux load-buffer -b "+buffer+" "+in+" && tmux save-buffer -b "+buffer+" "+out+"'; echo "+splitMarker(marker)+"$?", marker, shellTimeout)
	if !strings.Contains(term.Screen().Text(), marker+"0") {
		t.Fatalf("the shim's load-buffer and save-buffer failed\n%s", term.Snapshot())
	}
}

// sameBytes fails when the files at a and b differ, naming the first byte
// that does.
func sameBytes(t *testing.T, what, a, b string) {
	t.Helper()
	want, err := os.ReadFile(a) //nolint:gosec // a file of this test's own
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(b) //nolint:gosec // a file of this test's own
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	t.Fatalf("%s: the copy differs from the original at byte %d: %d bytes, want %d", what, at, len(got), len(want))
}

// TestPasteBufferKeepsEveryByte sends a binary file through the shim, and a
// file of 1.8 MB of three-byte characters, which goes in parts, and gets both
// back byte for byte. The CLI does the same with the binary file.
//
// Negative controls: with the shim sending a single part as text in data
// rather than data_b64, the binary file comes back with U+FFFD in it. With the
// parts of an upload sent as text, the large file differs where the first cut
// splits a character.
func TestPasteBufferKeepsEveryByte(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")

	dir := filepath.Join(base, "bytes")
	mustMkdir(dir)
	bin := filepath.Join(dir, "all.bin")
	var all []byte
	for i := range 256 {
		all = append(all, byte(i))
	}
	all = append(all, []byte("a\xff\xfeb")...)
	if err := os.WriteFile(bin, all, 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "euro.txt")
	// One ASCII byte first, so the three-byte characters straddle every cut
	// that falls on a round number of bytes.
	if err := os.WriteFile(big, []byte("x"+strings.Repeat("€", 600000)), 0o600); err != nil {
		t.Fatal(err)
	}

	binOut := filepath.Join(dir, "all.out")
	shimRoundTrip(t, term, base, bin, binOut, "binary")
	sameBytes(t, "the binary file through the shim", bin, binOut)

	bigOut := filepath.Join(dir, "euro.out")
	shimRoundTrip(t, term, base, big, bigOut, "large")
	sameBytes(t, "the 1.8 MB file through the shim", big, bigOut)

	// The CLI keeps every byte too.
	cliOut := filepath.Join(dir, "cli.out")
	runInShell(t, term, "clear; "+tuiosBin+" set-buffer -b clibin < "+bin+" && "+tuiosBin+" show-buffer -b clibin > "+cliOut+"; echo CLI_\"\"EXIT=$?", "CLI_EXIT=", shellTimeout)
	if !strings.Contains(term.Screen().Text(), "CLI_EXIT=0") {
		t.Fatalf("the CLI's set-buffer and show-buffer failed\n%s", term.Snapshot())
	}
	sameBytes(t, "the binary file through the CLI", bin, cliOut)

	// The listing shows a sample of every buffer, escaped, and never cuts a
	// character.
	var listing struct {
		Buffers []struct {
			Name   string `json:"name"`
			Sample string `json:"sample"`
		} `json:"buffers"`
	}
	out, err := tuiosCLI(t, base, "list-buffers", "--json")
	if err != nil || json.Unmarshal([]byte(out), &listing) != nil {
		t.Fatalf("list-buffers: %v\n%s", err, out)
	}
	for _, b := range listing.Buffers {
		if strings.ContainsRune(b.Sample, '�') {
			t.Fatalf("the sample of %s holds U+FFFD, a cut or a lost byte: %q", b.Name, b.Sample)
		}
	}
	alive(t, term, "after the byte checks")
}

// TestPasteBufferSendsNoEscapeSequences pastes a buffer that tries to end the
// bracketed paste early and set the title, into cat -v with bracketed paste
// on. The paste carries the marks at its ends, and the escape bytes inside are
// removed.
//
// Negative control: with SanitizePaste cut from verbPasteBuffer, ^[[201~evil
// shows inside the paste.
func TestPasteBufferSendsNoEscapeSequences(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	evil := filepath.Join(base, "evil.txt")
	if err := os.WriteFile(evil, []byte("safe\x1b[201~evil\x1b]0;owned\x07end"), 0o600); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, "clear; "+tuiosBin+" set-buffer -b evil < "+evil+"; printf '\\033[?2004h'; echo CAT\"\"ON; cat -v", "CATON", shellTimeout)
	if out, err := tuiosCLI(t, base, "paste-buffer", "-s", pbSession, "-b", "evil"); err != nil {
		t.Fatalf("paste-buffer: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "end^[[201~")
	}, shellTimeout); err != nil {
		t.Fatalf("the paste never arrived whole: %v\n%s", err, term.Snapshot())
	}
	text := term.Screen().Text()
	if !strings.Contains(text, "^[[200~safe") {
		t.Fatalf("the paste does not open with the bracketed mark\n%s", term.Snapshot())
	}
	if strings.Contains(text, "^[[201~evil") || strings.Contains(text, "^[]0;") {
		t.Fatalf("an escape sequence inside the buffer reached the pane\n%s", term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-escapes")
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatal(err)
	}
	alive(t, term, "after the escape check")
}

// TestPasteBufferChooserPastesWhereItOpened opens the chooser on one pane,
// moves the focus to another from the CLI, and pastes. The paste goes to the
// pane the chooser opened on.
//
// Negative control: with BufferChooserActivate pasting into the focused pane
// rather than its target, the text lands in the second pane.
func TestPasteBufferChooserPastesWhereItOpened(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	first := onlyPaneOf(t, base, pbSession)
	out, err := tuiosCLI(t, base, "new-window", "-s", pbSession, "second", "--print-id")
	if err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	second := strings.TrimSpace(out)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", pbSession, first); err != nil {
		t.Fatalf("focus-window first: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-buffer", "pinned"+"-text"); err != nil {
		t.Fatalf("set-buffer: %v\n%s", err, out)
	}
	time.Sleep(300 * time.Millisecond)

	if err := term.SendKeys(tuitest.Ctrl('b'), "#"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Paste buffers", uiTimeout); err != nil {
		t.Fatalf("the chooser did not open: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", pbSession, second); err != nil {
		t.Fatalf("focus-window second: %v\n%s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Pasted", uiTimeout); err != nil {
		t.Fatalf("the chooser pasted nothing: %v\n%s", err, term.Snapshot())
	}
	capture := func(window string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, "capture-pane", "-s", pbSession, "-w", window)
		if err != nil {
			t.Fatalf("capture-pane %s: %v\n%s", window, err, out)
		}
		return out
	}
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(capture(first), "pinned-text") {
		if time.Now().After(deadline) {
			t.Fatalf("the paste did not reach the pane the chooser opened on\nfirst:\n%s\nsecond:\n%s", capture(first), capture(second))
		}
		time.Sleep(150 * time.Millisecond)
	}
	if strings.Contains(capture(second), "pinned-text") {
		t.Fatalf("the paste reached the pane that took the focus later:\n%s", capture(second))
	}
	alive(t, term, "after the pinned paste")
}

// TestPasteBuffersWithAnOldDaemon runs the client against a daemon that
// answers the buffer verbs with unknown_verb, as a daemon from before them
// does. The client keeps the yank itself and prefix ] still pastes it.
//
// Negative control: with the local save cut from handlePasteBufferSaveFailed,
// prefix ] says there are no paste buffers.
func TestPasteBuffersWithAnOldDaemon(t *testing.T) {
	t.Setenv("TUIOS_E2E_NO_BUFFER_VERBS", "1")
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	if out, err := tuiosCLI(t, base, "list-buffers"); err == nil || !strings.Contains(out, "does not know list-buffers") {
		t.Fatalf("the daemon answers list-buffers, so it is not old: %v\n%q", err, out)
	}
	runInShell(t, term, `printf 'pb''old42\n'`, "pbold42", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatal(err)
	}
	waitCopyCursor(t, term, "prefix+[")
	yankLine(t, term, "?pbold42")
	if err := term.SendKeys("q"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys("echo OLD-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Ctrl('b'), "]"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Pasted", uiTimeout); err != nil {
		t.Fatalf("prefix ] said nothing with an old daemon: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHasLine(s, "OLD-pbold42") }, shellTimeout); err != nil {
		t.Fatalf("prefix ] did not paste the yank with an old daemon: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after a paste with an old daemon")
}
