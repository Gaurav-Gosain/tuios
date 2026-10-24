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

// tmlTypingDoc is a form with one text field bound to the state, and a
// submit button.
const tmlTypingDoc = `<tml version="1" title="Release note">
  <state>{"note": ""}</state>
  <col pad="1" gap="1">
    <text>Write the release note.</text>
    <form on-submit="close(form.note)">
      <input name="note" bind="state.note" label="Note" placeholder="Say why"/>
      <button key="y" kind="primary" submit="true">Send</button>
    </form>
  </col>
</tml>
`

// TestTMLInputTyping types into a TML text field the way a person does. An
// agent's pane shows a form as a popup. A click puts the caret in the field.
// The person types, deletes the line with ctrl+u, types again, deletes a
// character with backspace and a word with ctrl+w, moves the caret with the
// arrows and home, and pastes. The y key of the Send button types a y while
// the field has the focus. Then the agents view takes the same form, the
// person types one more character there, and enter submits it. The agent's
// --wait prints the field's text.
//
// Negative control, in NEGATIVE_CONTROLS.md: on upstream main the field
// takes no typed text, so it never shows what was typed.
func TestTMLInputTyping(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	const sess = "e2e-typing"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("new session: %v: %s", err, out)
	}
	for _, name := range []string{"writer", "docs", "tests"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name, "--grants", "write"); err != nil {
			t.Fatalf("open %s: %v\n%s", name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", sess, "-w", name, "--harness", "claude-code", "-m", "Bash go test ./..."); err != nil {
			t.Fatalf("report %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", sess}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	waitWindowCount(t, term, 4, "the four panes")

	doc := writeTMLDoc(t, base, "note.tml", tmlTypingDoc)
	answer, done := filepath.Join(base, "answer.json"), filepath.Join(base, "answer.done")
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "writer",
		fmt.Sprintf("%s tml show %s --wait --width 70 --height 12 > %s; echo $? > %s\n", tuiosBin, doc, answer, done)); err != nil {
		t.Fatalf("send the show line: %v\n%s", err, out)
	}
	waitText(t, term, "the form", "Write the release note.", "Say why")

	// A click on the field: the popup takes the focus, and the caret goes
	// into the field.
	cockpitClick(t, term, "Say why")
	cockpitClick(t, term, "Say why")
	steps := []struct {
		keys []any
		want string
	}{
		{[]any{"draft"}, "draft"},
		{[]any{tuitest.Ctrl('u'), "ship it now"}, "ship it now"},
		{[]any{tuitest.Backspace, tuitest.Backspace, tuitest.Backspace, "today"}, "ship it today"},
		{[]any{tuitest.Ctrl('w'), "soon"}, "ship it soon"},
		{[]any{tuitest.Left, tuitest.Left, tuitest.Left, tuitest.Left, "very "}, "ship it very soon"},
		{[]any{tuitest.Home, "y: "}, "y: ship it very soon"},
	}
	for i, st := range steps {
		if err := term.SendKeys(st.keys...); err != nil {
			t.Fatal(err)
		}
		waitText(t, term, fmt.Sprintf("typing step %d", i+1), st.want)
	}
	if err := term.SendKeys(tuitest.End); err != nil {
		t.Fatal(err)
	}
	if err := term.Paste(" (signed)\n"); err != nil {
		t.Fatal(err)
	}
	const typed = "y: ship it very soon (signed)"
	waitText(t, term, "the paste in the field", typed)
	saveTMLFrame(t, term, "1-typed-in-popup")
	if _, err := os.Stat(done); err == nil {
		t.Fatalf("ASSERTION: a key closed the form while the field had the focus\n%s", term.Snapshot())
	}

	// The agents view takes the same form, with its text and caret.
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the form in the agents view", "You are in this surface.", typed)
	cockpitClick(t, term, typed)
	if err := term.SendKeys("!"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "typing in the agents view", typed+"!")
	saveTMLFrame(t, term, "2-typed-in-cockpit")
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
	code, _ := os.ReadFile(done)
	if strings.TrimSpace(string(code)) != "0" || strings.TrimSpace(string(got)) != `"`+typed+`!"` {
		t.Fatalf("ASSERTION: the agent's --wait printed %q with status %q, want %q and 0", got, code, typed+"!")
	}
	alive(t, term, "after typing into the form")
}
