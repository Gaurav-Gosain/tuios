package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const respondPolicy = "[hosts.\"*\"]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\", \"files\", \"transcripts\"]\n"

func farErrored(t *testing.T, f *globalFleet, message string) {
	t.Helper()
	for _, args := range [][]string{
		{"set-window", "-s", "far", "-w", "0", "--name", "far-fix"},
		{"set-agent-state", "errored", "-s", "far", "-w", "far-fix", "--harness", "claude-code", "-m", message},
	} {
		if out, err := cliWithin(t, f.alpha, f.env, args...); err != nil {
			t.Fatalf("%v on alpha: %v\n%s", args, err, out)
		}
	}
}

func alphaInboxHas(t *testing.T, f *globalFleet, text string) (bool, string) {
	t.Helper()
	out, err := cliWithin(t, f.alpha, f.env, "list-attention", "--json")
	if err != nil {
		return false, out
	}
	var v struct {
		Items []struct {
			Summary string `json:"summary"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return false, out
	}
	for _, it := range v.Items {
		if strings.Contains(it.Summary, text) {
			return true, out
		}
	}
	return false, out
}

func attachAgentsViewOnAlpha(t *testing.T, f *globalFleet) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 160, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	return term
}

// TestInboxActsOnTheAttachedHost dismisses an item and answers an ask-human
// question from the agents view of a client attached through a host, and
// checks that both land on that host's daemon.
//
// The ways it can go wrong, each an assertion: the view lists this machine's
// Inbox, which holds neither item (the watch followed the local daemon); the
// title does not say whose Inbox it is; d is refused or goes to this
// machine's daemon, and the item stays on alpha; the digit is refused, and
// ask-human on alpha never prints the answer.
//
// Negative control: on main the view lists nothing of alpha's, and d says
// "Dismissing needs a client attached to this machine's daemon".
func TestInboxActsOnTheAttachedHost(t *testing.T) {
	f := startGlobalFleetWith(t, map[string]string{"beta": "someone@betabox"}, respondPolicy)
	farErrored(t, f, "build failed on alpha")
	term := attachAgentsViewOnAlpha(t, f)
	waitText(t, term, "alpha's item in the agents view, under a title that names alpha", "Agents on alpha", "build failed on alpha")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "attached-inbox-listed")

	if err := term.SendKeys("d"); err != nil {
		t.Fatalf("press d: %v", err)
	}
	waitUntil(t, "the dismissed item is gone from alpha's own Inbox", uiTimeout, func() (bool, string) {
		has, out := alphaInboxHas(t, f, "build failed on alpha")
		return !has, out + "\n" + term.Snapshot()
	})
	waitText(t, term, "the dismissed item leaves the agents view", "0 needs you")
	saveCockpitFrames(t, term, "attached-inbox-dismissed")

	asked := startCLIBackground(t, f.alpha, "ask-human", "-s", "far", "-w", "far-fix", "--timeout", "60000",
		"Ship it to staging?", "-o", "yes", "-o", "no")
	waitText(t, term, "alpha's question in the agents view", "Ship it to staging?")
	saveCockpitFrames(t, term, "attached-inbox-asked")
	var run askRun
	waitUntil(t, "ask-human on alpha returns the answer given in the agents view", uiTimeout, func() (bool, string) {
		select {
		case run = <-asked:
			return true, ""
		default:
		}
		_ = term.SendKeys("1")
		return false, term.Snapshot()
	})
	if run.err != nil || strings.TrimSpace(run.out) != "yes" {
		t.Fatalf("ASSERTION: ask-human on alpha printed %q and ended with %v, want yes and exit 0\n%s", run.out, run.err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "attached-inbox-answered")
	alive(t, term, "after acting on alpha's Inbox")
}

// TestInboxActOnTheAttachedHostSaysWhichGrant is the refusal: alpha's
// default link policy leaves respond out, so a dismiss from a client
// attached through alpha is refused there. The client names the grant and
// the machine that must give it, and the item stays.
//
// Negative control: on main the refusal is the local "Dismissing needs a
// client attached to this machine's daemon", which names no grant.
func TestInboxActOnTheAttachedHostSaysWhichGrant(t *testing.T) {
	f := startGlobalFleet(t)
	farErrored(t, f, "lint failed on alpha")
	term := attachAgentsViewOnAlpha(t, f)
	waitText(t, term, "alpha's item in the agents view", "Agents on alpha", "lint failed on alpha")
	_ = term.WaitStable(uiTimeout)

	if err := term.SendKeys("d"); err != nil {
		t.Fatalf("press d: %v", err)
	}
	waitText(t, term, "the refusal naming the respond grant", "The dismiss did not go through: it needs \"respond\"")
	saveCockpitFrames(t, term, "attached-inbox-refused")
	if has, out := alphaInboxHas(t, f, "lint failed on alpha"); !has {
		t.Fatalf("ASSERTION: a refused dismiss closed the item on alpha:\n%s", out)
	}
	alive(t, term, "after the refused dismiss")
}
