package tuie2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Paste buffers across sessions (#514): which buffers a pane may read, what
// the paste key may take, and the byte cap. See paste_buffers_test.go for the
// list of ways these could pass wrongly; the same rules apply here.

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

// TestAPaneSeesOnlyItsOwnBuffers gives a pane read and write and checks it
// reaches only the buffers it set: not the person's, not another pane's in
// its own session. The totals of its listing count only its own. With admin,
// the same pane reaches every buffer.
//
// How it could pass wrongly: the pane could see nothing at all, so it must
// see its own buffer. The totals could hide a size in a field the test does
// not read, so bytes and total are both checked against its one buffer.
//
// Negative controls: with the owner filter in paneBuffers cut, the pane reads
// the person's buffer and OTHER_SHOW=1 never prints. With list-buffers
// summing every buffer, its bytes count the person's buffers too.
func TestAPaneSeesOnlyItsOwnBuffers(t *testing.T) {
	base := t.TempDir()
	term := startPasteBufferClient(t, base, "")
	sibling := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", pbSession, "sibling", "--no-focus", "--print-id"))
	pane := onlyPaneOfFirst(t, base, pbSession, sibling)
	mustCLI(t, base, "set-buffer", "-b", "persons", strings.Repeat("p", 5000))
	for _, id := range []string{pane, sibling} {
		mustCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", id, "--grants", "read,write")
	}
	// The sibling pane sets a buffer of its own.
	line := tuiosBin + " set-buffer sibling-text\n"
	mustCLI(t, base, "send-text", "-s", pbSession, "-w", sibling, line)
	deadline := time.Now().Add(shellTimeout)
	for len(listRows(t, base)) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the sibling pane never set its buffer: %+v", listRows(t, base))
		}
		time.Sleep(100 * time.Millisecond)
	}

	listing := filepath.Join(base, "pane-listing.json")
	bin := tuiosBin
	runInShell(t, term, "clear; "+bin+" set-buffer own-text; "+bin+" list-buffers --json > "+listing+"; "+bin+" show-buffer -b persons; echo OTHER_SHOW=$?", "OTHER_SHOW=1", shellTimeout)
	var l struct {
		Buffers []pbRow `json:"buffers"`
		Total   int     `json:"total"`
		Bytes   int     `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(readFileString(t, listing)), &l); err != nil {
		t.Fatalf("the pane's list-buffers gave no JSON: %v", err)
	}
	if l.Total != 1 || len(l.Buffers) != 1 || l.Buffers[0].Sample != "own-text" || l.Bytes != len("own-text") {
		t.Fatalf("the pane's listing is %+v with total %d and bytes %d, want its own buffer alone, 8 bytes", l.Buffers, l.Total, l.Bytes)
	}
	saveFrame(t, term, "paste-buffers-own-only")

	// The positive half: with admin the same pane reaches the others.
	mustCLI(t, base, "set-pane-grants", "-s", pbSession, "-w", pane, "--grants", "admin")
	runInShell(t, term, "clear; "+bin+" show-buffer -b persons | head -c 3; echo; echo ADMIN_SHOW=$?", "ADMIN_SHOW=0", shellTimeout)
	if !strings.Contains(term.Screen().Text(), "ppp") {
		t.Fatalf("a pane with admin cannot read the person's buffer\n%s", term.Snapshot())
	}
	alive(t, term, "after the ownership checks")
}

// onlyPaneOfFirst is the window of session that is not other.
func onlyPaneOfFirst(t *testing.T, base, session, other string) string {
	t.Helper()
	out := mustCLI(t, base, "list-windows", "-s", session, "--json")
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows gave no JSON: %v\n%s", err, out)
	}
	for _, w := range listing.Windows {
		if w.WindowID != other {
			return w.WindowID
		}
	}
	t.Fatalf("session %s has no window but %s", session, other)
	return ""
}

// TestThePersonsNewestIsNeverAPanes is repro B: an agent pane without admin
// in session work sets a buffer, newer than the person's. The person's bare
// show-buffer and paste-buffer, and prefix ] in session home, take the
// person's text. A buffer an admin pane sets with no name is the person's,
// and does count. The chooser marks the agent's buffer.
//
// How it could pass wrongly: the agent's set could have failed, so the test
// waits for it in the person's full listing first.
//
// Negative control: with bare in bufferCaller returning every buffer for the
// person, show-buffer prints the agent's text.
func TestThePersonsNewestIsNeverAPanes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, copyCursorConfig)
	mustCLI(t, base, "new", "home", "--detach")
	mustCLI(t, base, "new", "work", "--detach")
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, env: copyColorOpts.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	mustCLI(t, base, "set-buffer", "persons-text")
	agent := onlyPaneOf(t, base, "work")
	mustCLI(t, base, "set-pane-grants", "-s", "work", "-w", agent, "--grants", "read,write")
	mustCLI(t, base, "send-text", "-s", "work", "-w", agent, tuiosBin+" set-buffer planted\n")
	deadline := time.Now().Add(shellTimeout)
	for {
		rows := listRows(t, base)
		if len(rows) == 2 && rows[0].Sample == "planted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent pane never set its buffer: %+v", rows)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if out := mustCLI(t, base, "show-buffer"); out != "persons-text" {
		t.Fatalf("the person's bare show-buffer printed %q, want persons-text, not the agent's buffer", out)
	}
	// The person's bare paste-buffer, into the home pane.
	if err := term.SendKeys("echo GOT-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	mustCLI(t, base, "paste-buffer", "-s", "home")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHasLine(s, "GOT-persons-text") || screenHasLine(s, "GOT-planted")
	}, shellTimeout); err != nil {
		t.Fatalf("paste-buffer pasted nothing: %v\n%s", err, term.Snapshot())
	}
	if !screenHasLine(term.Screen(), "GOT-persons-text") {
		t.Fatalf("the person's bare paste-buffer pasted the agent's buffer\n%s", term.Snapshot())
	}
	// prefix ] takes the same.
	if err := term.SendKeys("echo KEY-"); err != nil {
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
		return screenHasLine(s, "KEY-persons-text") || screenHasLine(s, "KEY-planted")
	}, shellTimeout); err != nil {
		t.Fatalf("prefix ] pasted nothing: %v\n%s", err, term.Snapshot())
	}
	if !screenHasLine(term.Screen(), "KEY-persons-text") {
		t.Fatalf("prefix ] pasted the agent's buffer\n%s", term.Snapshot())
	}

	// The chooser lists the agent's buffer and says whose it is. The agent's
	// pane has a long name with tabs in it, and the row still shows the
	// buffer's own text and size, with the tag cut short.
	mustCLI(t, base, "set-window", "-s", "work", "-w", agent, "--name", strings.Repeat("x", 60)+"\t\t\tend")
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('b'), "#"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Paste buffers") && strings.Contains(s.Text(), "from pane")
	}, uiTimeout); err != nil {
		t.Fatalf("the chooser does not mark the agent's buffer: %v\n%s", err, term.Snapshot())
	}
	_, rows := term.Screen().Size()
	row := ""
	for r := range rows {
		if line := term.Screen().Line(r); strings.Contains(line, "from pane") {
			row = line
		}
	}
	if !strings.Contains(row, "planted") || !strings.Contains(row, "7 bytes") || strings.Contains(row, strings.Repeat("x", 20)) {
		t.Fatalf("a long pane name pushed the buffer's text or size off its row, or was not cut:\n%q", row)
	}
	saveFrame(t, term, "paste-buffers-planted")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	// A buffer an admin pane sets with no name is the person's.
	mustCLI(t, base, "set-pane-grants", "-s", "work", "-w", agent, "--grants", "admin")
	mustCLI(t, base, "send-text", "-s", "work", "-w", agent, tuiosBin+" set-buffer by-admin-pane\n")
	deadline = time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosCLI(t, base, "show-buffer")
		if out == "by-admin-pane" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the person's bare show-buffer never took the admin pane's buffer; it printed %q", out)
		}
		time.Sleep(100 * time.Millisecond)
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
