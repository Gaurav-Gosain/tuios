package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func gotoRowText(kind, label string) string {
	return kind + strings.Repeat(" ", len("workspace")+2-len(kind)) + label
}

func gotoKindRow(kind string) *regexp.Regexp {
	return regexp.MustCompile(`(› |  )` + regexp.QuoteMeta(gotoRowText(kind, "")) + `\S`)
}

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

var gotoKindNames = map[string]bool{"agent": true, "session": true, "workspace": true, "pane": true}

func openGotoPicker(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "G")
	waitScreen(t, term, "the go-to picker never showed", "Go to", "@ # ! filter")
}

func gotoQuery(t *testing.T, term *tuitest.Terminal, q string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('u'))
	if err := term.SendKeys(q); err != nil {
		t.Fatalf("type %q: %v", q, err)
	}
}

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
		{"new-window", "needy", "-s", "ops", "--no-focus"},
		{"set-agent-state", "needs_input", "-s", "ops", "-w", "needy", "-m", "approve the deploy"},
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

	openGotoPicker(t, term)
	gotoQuery(t, term, "zebra")
	gotoWaitRows(t, term, "the fuzzy query did not find the pane", []string{gotoRowText("pane", "work › 2 › zebra-ledger")}, nil)
	saveArtifact(t, term, dir, "fuzzy")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on the pane row", "work", 2, zebra)

	openGotoPicker(t, term)
	gotoQuery(t, term, "kind:session space:surge")
	gotoWaitRows(t, term, "kind:session space:surge",
		[]string{gotoRowText("session", "home [space: surge]"), gotoRowText("session", "ops [space: surge]")},
		[]string{gotoRowText("session", "work"), "workspace"})
	saveArtifact(t, term, dir, "kind-session-space")
	gotoQuery(t, term, "kind:ws session:work")
	gotoWaitRows(t, term, "kind:ws session:work",
		[]string{gotoRowText("workspace", "work › 1"), gotoRowText("workspace", "work › 2")},
		[]string{gotoRowText("workspace", "home › 1"), "pane", "session"})
	saveArtifact(t, term, dir, "kind-ws-session")
	gotoQuery(t, term, "@harness:claude")
	gotoWaitRows(t, term, "@harness:claude",
		[]string{gotoRowText("agent", "claude · work › 1 › agentpane"), "editing files"},
		[]string{"session", "pane", "workspace"})
	saveArtifact(t, term, dir, "agent-sigil")
	sendKeys(t, term, tuitest.Esc)
	waitLanded(t, term, base, "esc closed the picker", "work", 2, zebra)

	needy := gotoRowText("agent", "ops › 1 › needy")
	openGotoPicker(t, term)
	gotoWaitRows(t, term, "the empty query does not list the agent that needs you", []string{needy}, nil)
	saveArtifact(t, term, dir, "empty-query")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on an empty query, back to home", "home", 1, homePane)

	sendKeys(t, term, tuitest.Ctrl('b'), "n")
	waitLanded(t, term, base, "the prefix n to homeb", "home", 1, homeB)
	openGotoPicker(t, term)
	gotoWaitRows(t, term, "the empty query after the key does not list the agent that needs you", []string{needy}, nil)
	saveArtifact(t, term, dir, "empty-query-after-key")
	sendKeys(t, term, tuitest.Enter)
	waitLanded(t, term, base, "enter on an empty query, back to the first home pane", "home", 1, homePane)
}
