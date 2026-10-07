package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Paste buffers across sessions (#514): which buffers a pane may read, what
// the paste key may take, and the byte cap. See paste_buffers_test.go for the
// list of ways these could pass wrongly; the same rules apply here.

// pbOther is the second session of the two-session tests.
const pbOther = "e2e-pb-other"

// onlyPaneOf returns the id of the one window in session.
func onlyPaneOf(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows -s %s: %v\n%s", session, err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows -s %s gave no single window: %v\n%s", session, err, out)
	}
	return listing.Windows[0].WindowID
}

// TestPasteBuffersStayInTheirSession gives a pane the read grant and checks
// it sees the buffers of its own session only: not another session's, and
// not one the person set from outside every pane. With admin, the same pane
// sees them all.
//
// Negative control: with the filter in bufferAccess cut, the pane reads the
// other session's buffer and OTHER_SHOW=1 never prints.
func TestPasteBuffersStayInTheirSession(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	if out, err := tuiosCLI(t, base, "new", pbOther, "--detach"); err != nil {
		t.Fatalf("create the second session: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"set-buffer", "-s", pbSession, "-b", "mine", "own-text"},
		{"set-buffer", "-s", pbOther, "-b", "theirs", "their-text"},
		{"set-buffer", "-b", "personal", "personal-text"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	pane := onlyPaneOf(t, base, pbSession)
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants read: %v\n%s", err, out)
	}
	bin := tuiosBin
	runInShell(t, term, "clear; "+bin+" list-buffers | tr a-z A-Z; "+bin+" show-buffer -b theirs; echo OTHER_SHOW=$?", "OTHER_SHOW=1", shellTimeout)
	text := term.Screen().Text()
	if !strings.Contains(text, "OWN-TEXT") {
		t.Fatalf("the pane does not see its own session's buffer\n%s", term.Snapshot())
	}
	if strings.Contains(text, "THEIR-TEXT") || strings.Contains(text, "PERSONAL-TEXT") {
		t.Fatalf("the pane sees a buffer of another session, or the person's own\n%s", term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-own-session")

	// The positive half: with admin the same calls reach every buffer.
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", "admin"); err != nil {
		t.Fatalf("set-pane-grants admin: %v\n%s", err, out)
	}
	runInShell(t, term, "clear; "+bin+" show-buffer -b theirs | tr a-z A-Z; echo; echo ADMIN_SHOW=$?", "ADMIN_SHOW=0", shellTimeout)
	if !strings.Contains(term.Screen().Text(), "THEIR-TEXT") {
		t.Fatalf("a pane with admin cannot read another session's buffer\n%s", term.Snapshot())
	}
	alive(t, term, "after the session checks")
}

// TestPasteKeyIgnoresABufferPlantedFromAnotherSession has a process in a pane
// of another session set a buffer, newer than the person's. Prefix ] pastes
// the person's text, and the chooser marks the planted buffer.
//
// Negative control: with for_session cut from pasteBufferNamed, prefix ]
// pastes the planted text and GOT-pastedok never prints.
func TestPasteKeyIgnoresABufferPlantedFromAnotherSession(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	if out, err := tuiosCLI(t, base, "new", pbOther, "--detach"); err != nil {
		t.Fatalf("create the second session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-buffer", "pastedok"); err != nil {
		t.Fatalf("set-buffer: %v\n%s", err, out)
	}
	line := tuiosBin + " set-buffer planted\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", pbOther, line); err != nil {
		t.Fatalf("send-text to the other session: %v\n%s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		l := listBuffers(t, base)
		if len(l.Buffers) == 2 && l.Buffers[0].Sample == "planted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the other session's pane never set its buffer: %+v", l.Buffers)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := term.SendKeys("echo GOT-"); err != nil {
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
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHasLine(s, "GOT-pastedok") || screenHasLine(s, "GOT-planted")
	}, shellTimeout); err != nil {
		t.Fatalf("nothing was pasted: %v\n%s", err, term.Snapshot())
	}
	if !screenHasLine(term.Screen(), "GOT-pastedok") {
		t.Fatalf("prefix ] pasted the buffer another session's pane set\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "#"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Paste buffers") && strings.Contains(s.Text(), "from pane")
	}, uiTimeout); err != nil {
		t.Fatalf("the chooser does not mark the planted buffer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "paste-buffers-planted")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	alive(t, term, "after the planted buffer")
}

// TestPasteBufferByteCap holds the buffers to max_kb = 1: two buffers of 600
// bytes do not fit together, so the older goes, and one of 2000 bytes is
// refused with a hint that names max_kb.
//
// Negative control: with the byte test cut from the store's trim, both
// 600-byte buffers stay.
func TestPasteBufferByteCap(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[paste_buffers]\nmax_kb = 1\n")
	if out, err := tuiosCLI(t, base, "new", pbSession, "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	for _, c := range []string{"a", "b"} {
		if out, err := tuiosCLI(t, base, "set-buffer", strings.Repeat(c, 600)); err != nil {
			t.Fatalf("set-buffer %s: %v\n%s", c, err, out)
		}
	}
	l := listBuffers(t, base)
	if len(l.Buffers) != 1 || l.Buffers[0].Bytes != 600 || !strings.HasPrefix(l.Buffers[0].Sample, "b") {
		t.Fatalf("under max_kb = 1 the buffers are %+v, want the newer 600 bytes alone", l.Buffers)
	}
	out, err := tuiosCLI(t, base, "set-buffer", strings.Repeat("c", 2000))
	if err == nil || !strings.Contains(out, "max_kb") {
		t.Fatalf("a 2000-byte buffer under max_kb = 1 gave %q (%v), want a refusal that names max_kb", out, err)
	}
	if l := listBuffers(t, base); len(l.Buffers) != 1 || !strings.HasPrefix(l.Buffers[0].Sample, "b") {
		t.Fatalf("the refused buffer changed the list: %+v", l.Buffers)
	}
}
