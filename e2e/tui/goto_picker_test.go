package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The go-to picker (goto_picker, prefix G), driven through a real client over
// a real daemon: a fuzzy query finds a pane on another workspace of another
// session and Enter lands there; selector terms narrow the rows; and with
// nothing typed, Enter goes back to where the client was before.
//
// How these could pass wrongly, written down first:
//   - The jump could look right on screen and leave the daemon elsewhere. Each
//     landing is read from the daemon: the client's session from
//     list-clients, the workspace from list-workspaces, and the focused pane
//     from list-windows.
//   - The target could be on the workspace or in the session the client is
//     already on. It is a pane on workspace 2 of work, the client starts on
//     home, and work shows workspace 1 until the jump.
//   - A filter could pass by showing everything. Every structured query names
//     rows that must be absent as well as rows that must be present, and the
//     absent ones exist and are listed by another query in the same run.
//   - The query text echoed in the search line could be read as a row. Rows
//     are matched with their kind tag in front ("session  home"), which the
//     search line never holds.
//   - "Back to where I was" could be the first row by luck. The flip back is
//     done twice: once to another session, and once to a pane of the same
//     session that was focused with the prefix key rather than the picker,
//     so the place is the one the client was on, not the one the picker
//     last left.
//
// Negative controls, all confirmed red (see NEGATIVE_CONTROLS.md).

// gotoRowText is a row as the picker draws it: the kind tag in its column,
// then the place.
func gotoRowText(kind, label string) string {
	return kind + strings.Repeat(" ", len("session")+2-len(kind)) + label
}

// gotoKindRow matches a row of a kind with any label: the tag sits right
// after the row's marker column, which is the cursor or two spaces, and the
// label follows the tag's padding. A pane count ("1 pane   ") is not one.
func gotoKindRow(kind string) *regexp.Regexp {
	return regexp.MustCompile(`(› |  )` + regexp.QuoteMeta(gotoRowText(kind, "")) + `\S`)
}

// gotoWaitRows waits until every present row is drawn and no absent row is.
// An absent entry is a full row, or a bare kind for every row of that kind.
func gotoWaitRows(t *testing.T, term *tuitest.Terminal, what string, present, absent []string) {
	t.Helper()
	var wrong string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, p := range present {
			if !strings.Contains(text, p) {
				wrong = "missing " + strconv.Quote(p)
				return false
			}
		}
		for _, a := range absent {
			shown := strings.Contains(text, a)
			if gotoKindNames[a] {
				shown = gotoKindRow(a).MatchString(text)
			}
			if shown {
				wrong = "showing " + strconv.Quote(a)
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %s: %v\n%s", what, wrong, err, term.Snapshot())
	}
}

// gotoKindNames are the kind tags, which gotoWaitRows takes as "any row of
// this kind".
var gotoKindNames = map[string]bool{"agent": true, "session": true, "ws": true, "pane": true}

// openGotoPicker presses the leader and G and waits for the panel.
func openGotoPicker(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "G")
	waitScreen(t, term, "the go-to picker never showed", "Go to", "agent session ws")
}

// gotoQuery clears the picker's query and types q.
func gotoQuery(t *testing.T, term *tuitest.Terminal, q string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('u'))
	if err := term.SendKeys(q); err != nil {
		t.Fatalf("type %q: %v", q, err)
	}
}

// waitLanded waits for the daemon to say the client is on sess, showing
// workspace ws, with pane focused.
func waitLanded(t *testing.T, term *tuitest.Terminal, base, what, sess string, ws int, pane string) {
	t.Helper()
	clientShows(t, base, sess)
	deadline := time.Now().Add(uiTimeout)
	for {
		gotWs, gotPane := currentWorkspace(t, base, sess), focusedIn(t, base, sess)
		if gotWs == ws && gotPane == pane {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s shows workspace %d with %s focused, want workspace %d with %s\n%s", what, sess, gotWs, gotPane, ws, pane, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestGotoPickerGoesAnywhere(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, args := range [][]string{
		{"new", "home", "--detach", "--space", "surge"},
		{"new", "work", "--detach"},
		{"new", "ops", "--detach", "--space", "surge"},
		{"new-window", "homeb", "-s", "home", "--no-focus"},
		{"new-window", "zebra-ledger", "-s", "work", "--workspace", "2", "--no-focus"},
		{"new-window", "agentpane", "-s", "work", "--no-focus"},
		{"set-agent-state", "working", "-s", "work", "-w", "agentpane", "--harness", "claude-code", "-m", "editing files"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	homePane := focusedIn(t, base, "home")
	homeB := windowIDByName(t, base, "home", "homeb")
	zebra := windowIDByName(t, base, "work", "zebra-ledger")
	if homePane == homeB {
		t.Fatalf("homeb has the focus before the test moves it, so the last step would prove nothing")
	}
	if currentWorkspace(t, base, "work") == 2 {
		t.Fatalf("work already shows workspace 2, so the jump would prove nothing")
	}

	term := attachIn(t, base, "home", startOpts{cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "home")
	dir := artifactDir(t)

	// A fuzzy query finds the pane on workspace 2 of work, and Enter goes there.
	openGotoPicker(t, term)
	gotoQuery(t, term, "zebra")
	gotoWaitRows(t, term, "the fuzzy query did not find the pane", []string{gotoRowText("pane", "work › 2 › zebra-ledger")}, nil)
	saveArtifact(t, term, dir, "fuzzy")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on the pane row", "work", 2, zebra)

	// Selector terms narrow the rows, and combine with the picker's own.
	openGotoPicker(t, term)
	gotoQuery(t, term, "kind:session space:surge")
	gotoWaitRows(t, term, "kind:session space:surge",
		[]string{gotoRowText("session", "home [space: surge]"), gotoRowText("session", "ops [space: surge]")},
		[]string{gotoRowText("session", "work"), "ws"})
	saveArtifact(t, term, dir, "kind-session-space")
	gotoQuery(t, term, "kind:ws session:work")
	gotoWaitRows(t, term, "kind:ws session:work",
		[]string{gotoRowText("ws", "work › 1"), gotoRowText("ws", "work › 2")},
		[]string{gotoRowText("ws", "home › 1"), "pane", "session"})
	saveArtifact(t, term, dir, "kind-ws-session")
	gotoQuery(t, term, "@harness:claude")
	gotoWaitRows(t, term, "@harness:claude",
		[]string{gotoRowText("agent", "claude · work › 1 › agentpane"), "editing files"},
		[]string{"session", "pane", "ws"})
	saveArtifact(t, term, dir, "agent-sigil")
	sendKeys(t, term, tuitest.Esc)
	waitLanded(t, term, base, "esc closed the picker", "work", 2, zebra)

	// With nothing typed, Enter goes back to the place the client left.
	openGotoPicker(t, term)
	saveArtifact(t, term, dir, "empty-query")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on an empty query, back to home", "home", 1, homePane)

	// A move made without the picker is a place left as well.
	sendKeys(t, term, tuitest.Ctrl('b'), "n")
	waitLanded(t, term, base, "the prefix n to homeb", "home", 1, homeB)
	openGotoPicker(t, term)
	saveArtifact(t, term, dir, "empty-query-after-key")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on an empty query, back to the first home pane", "home", 1, homePane)
}
