package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTMLInputKeepsTypingOverALatePush types into a TML field while the
// daemon's pushes of that field's state are slow to arrive. Each key is a
// tml-input, answered on its own connection, and the daemon pushes the new
// state to every client on the attach connection. Under load the push can
// arrive after the client has had the answers to later keys, and the client
// applied it: the field went back to an older text, and the next input sent
// that older text over the daemon's. That is how the CI run of
// TestTMLInputTyping lost "y: " after End and a paste. Here the daemon holds
// each push back one second after it is built (TUIOS_E2E_TML_PUSH_DELAY), so
// the push for the first key lands after the answers to all the others.
//
// How this could pass wrongly, written down first:
//   - The field could go back and come forward again between two looks. The
//     screen is read every 20 ms for three seconds, past the time the late
//     pushes land, and one frame without the text fails.
//   - No push could be late at all. The control without the fix shows the
//     field taken back, so the late push is made and lands.
//   - The screen could hold the text while the daemon has the older one. The
//     form is submitted after one more key, and the agent's --wait must print
//     all of it.
func TestTMLInputKeepsTypingOverALatePush(t *testing.T) {
	t.Setenv("TUIOS_E2E_TML_PUSH_DELAY", "1s")
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	const sess = "e2e-late-push"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "writer", "--grants", "write"); err != nil {
		t.Fatalf("open writer: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", sess}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	waitWindowCount(t, term, 2, "the two panes")

	doc := writeTMLDoc(t, base, "note.tml", tmlTypingDoc)
	answer, done := filepath.Join(base, "answer.json"), filepath.Join(base, "answer.done")
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "writer",
		fmt.Sprintf("%s tml show %s --wait --width 70 --height 12 > %s; echo $? > %s\n", tuiosBin, doc, answer, done)); err != nil {
		t.Fatalf("send the show line: %v\n%s", err, out)
	}
	waitText(t, term, "the form", "Write the release note.", "Say why")
	// The pushes for the form itself are late too.
	time.Sleep(2500 * time.Millisecond)
	cockpitClick(t, term, "Say why")
	cockpitClick(t, term, "Say why")

	const typed = "draft"
	if err := term.SendKeys(typed); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the typed text", typed)
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !strings.Contains(term.Screen().Text(), typed) {
			saveTMLFrame(t, term, "taken-back")
			t.Fatalf("ASSERTION: a late push took the typed text back: the field no longer shows %q\n%s", typed, term.Snapshot())
		}
	}
	saveTMLFrame(t, term, "after-the-late-pushes")

	if err := term.SendKeys("!"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "one more key", typed+"!")
	time.Sleep(2500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: enter in the field never submitted the form\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, _ := os.ReadFile(answer)
	if strings.TrimSpace(string(got)) != `"`+typed+`!"` {
		t.Fatalf("ASSERTION: the agent's --wait printed %q, want %q", got, typed+"!")
	}
	alive(t, term, "after typing over late pushes")
}
