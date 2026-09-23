package tuie2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// buildFakeACP builds the scripted ACP agent in testdata/fakeacp.
func buildFakeACP(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeacp")
	build := exec.Command("go", "build", "-o", bin, "./testdata/fakeacp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeacp: %v\n%s", err, out)
	}
	return bin
}

// waitCapture polls capture-pane until every marker is in the pane.
func waitCapture(t *testing.T, base, session, window string, markers ...string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var out string
	for {
		out, _ = tuiosCLI(t, base, "capture-pane", "-s", session, "-w", window)
		if containsAll(out, markers...) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %v:\n%s", markers, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestProtocolPaneTurnAndInboxApproval runs an ACP agent headless with
// start-agent --protocol acp, against a real daemon, the real pane program and
// a real client, with no [agents.approvals] in the config:
//
//   - start-agent returns once the pane reports idle, and types the first
//     prompt, which the agent answers in the transcript;
//   - list-agents names the pane's protocol, and it reads done after the turn;
//   - the OSC sequence in the agent's reply never reaches the pane;
//   - a permission to run a command shows in the Inbox with answer keys, 1
//     allows it, and the agent gets allow and says so in the pane.
func TestProtocolPaneTurnAndInboxApproval(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fake := buildFakeACP(t)
	if out, err := tuiosCLI(t, base, "new", "e2e-home", "--detach"); err != nil {
		t.Fatalf("create the attached session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	out, err := tuiosCLI(t, base, "start-agent", fake, "--protocol", "acp", "-s", "e2e-agent", "--name", "helper", "--prompt", "hello there")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is ready: it reads idle") || !strings.Contains(out, "The prompt was typed and taken.") {
		t.Fatalf("start-agent printed:\n%s", out)
	}
	// The reply's OSC title sequence arrives as inert text: its ESC and BEL
	// are gone, so the pane shows what is left of it.
	pane := waitCapture(t, base, "e2e-agent", "helper", "connected to fakeacp 1.0", "you  hello there", "ECHO: hello there]0;pwned", "turn finished")
	// The agent tried to write to its terminal; it has none.
	if strings.Contains(pane, "TTYLEAK") {
		t.Errorf("the agent wrote to the pane past the transcript:\n%s", pane)
	}

	raw, err := tuiosCLI(t, base, "list-agents", "-s", "e2e-agent", "--json")
	if err != nil {
		t.Fatalf("list-agents: %v\n%s", err, raw)
	}
	var listing struct {
		Agents []struct {
			Name     string `json:"name"`
			State    string `json:"state"`
			Protocol string `json:"protocol"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &listing); err != nil {
		t.Fatalf("list-agents json: %v\n%s", err, raw)
	}
	if len(listing.Agents) != 1 || listing.Agents[0].Protocol != "acp" || listing.Agents[0].State != "done" {
		t.Fatalf("list-agents = %+v, want the helper done with protocol acp", listing.Agents)
	}
	// The window title is the name the pane was given: the agent's OSC
	// sequence did not set it.
	raw, _ = tuiosCLI(t, base, "list-windows", "-s", "e2e-agent", "--json")
	var windows struct {
		Windows []struct {
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(raw), &windows); err != nil || len(windows.Windows) != 1 || windows.Windows[0].Title != "helper" {
		t.Errorf("the window title is not the pane's name (%v):\n%s", err, raw)
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "please run the tests\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-agent", "helper", "permission execute: go test ./...", "1 Allow")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Approvals 1", "approve execute: go test", "allow", "deny")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never offered to answer the protocol pane's permission: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "protocol-pane-approval-held")
	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitCapture(t, base, "e2e-agent", "helper", "answered: Allow (from the Inbox", "PERMISSION: allow", "[done] execute: go test ./...")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Approvals 1")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered approval stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after answering a protocol pane's permission from the Inbox")
}
