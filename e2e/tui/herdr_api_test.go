package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// herdrSocket is the herdr socket of the daemon under base.
func herdrSocket(base string) string {
	return filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock.herdr")
}

// herdrCall sends one request to the herdr socket from the test process,
// which runs in no pane: the person.
func herdrCall(t *testing.T, base, method string, params any) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatalf("dial the herdr socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	line, _ := json.Marshal(map[string]any{"id": "e2e", "method": method, "params": params})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("%s: %q: %v", method, reply, err)
	}
	if e, ok := out["error"]; ok {
		t.Fatalf("%s: %v", method, e)
	}
	return out["result"].(map[string]any)
}

// herdrPaneByLabel finds a pane in session.snapshot by its label.
func herdrPaneByLabel(t *testing.T, base, label string) map[string]any {
	t.Helper()
	snap := herdrCall(t, base, "session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	for _, p := range snap["panes"].([]any) {
		if m := p.(map[string]any); m["label"] == label {
			return m
		}
	}
	t.Fatalf("no pane labelled %s in the snapshot", label)
	return nil
}

func buildHerdrClient(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "herdrclient")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/herdrclient").CombinedOutput(); err != nil {
		t.Fatalf("build herdrclient: %v\n%s", err, out)
	}
	return bin
}

// TestHerdrSocketAPI drives a real daemon, with a client attached, through
// herdr's socket API, the way Collie's herdr adapter and herdr plugins do:
//
//   - From outside every pane (the person): session.snapshot lists the
//     panes with their labels; pane.rename shows on the screen; an agent on
//     needs_input reads as herdr's blocked; text and keys typed with
//     pane.send_text and pane.send_keys reach the pane; tab.create makes a
//     workspace with the label; the event stream reports a new pane.
//   - From inside a pane, with herdr's environment only: pane.current is the
//     caller's own pane, by the id in HERDR_PANE_ID; typing into a pane that
//     waits on a prompt is refused, since the pane has no respond grant.
//
// Negative control: with the typingRefusal check taken out of
// holdTypingTarget, the in-pane send_text answers ok and the forbidden wait
// fails.
func TestHerdrSocketAPI(t *testing.T) {
	term, base := crushClient(t)
	client := buildHerdrClient(t)
	crushPanes(t, base, "caller", "target")

	targetWin := windowID(t, base, crushSession, "target")
	target := herdrPaneByLabel(t, base, "target")
	targetID := target["pane_id"].(string)
	if !strings.HasPrefix(targetID, "w") || !strings.Contains(targetID, ":p") || target["agent_status"] != "unknown" {
		t.Fatalf("target pane record %v", target)
	}

	// A rename through herdr is the window's name on screen.
	renamed := herdrCall(t, base, "pane.rename", map[string]any{"pane_id": targetID, "label": "herdr-named"})
	if renamed["pane"].(map[string]any)["label"] != "herdr-named" {
		t.Fatalf("pane.rename %v", renamed)
	}
	waitForAll(t, term, uiTimeout, "the renamed pane", "herdr-named")

	// The person types into the pane through herdr.
	herdrCall(t, base, "pane.send_text", map[string]any{"pane_id": targetID, "text": "echo typed-by-herdr"})
	herdrCall(t, base, "pane.send_keys", map[string]any{"pane_id": targetID, "keys": []string{"Enter"}})
	waitJoined(t, base, "herdr-named", "typed-by-herdr")
	read := herdrCall(t, base, "pane.read", map[string]any{"pane_id": targetID, "source": "recent", "lines": 50, "format": "text"})["read"].(map[string]any)
	if !strings.Contains(read["text"].(string), "typed-by-herdr") {
		t.Fatalf("pane.read did not see the typed text: %q", read["text"])
	}

	// An agent waiting on a prompt reads as herdr's blocked.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", crushSession, "-w", targetWin, "--harness", "claude-code", "needs_input"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	target = herdrPaneByLabel(t, base, "herdr-named")
	if target["agent"] != "claude" || target["agent_status"] != "blocked" {
		t.Fatalf("an agent on needs_input reads %v / %v", target["agent"], target["agent_status"])
	}

	// A pane holds its grants: it finds itself, and may not type into a
	// prompt without respond.
	typeIn(t, base, "caller", client+` pane.current '{}'`)
	out := waitJoined(t, base, "caller", "REPLY ")
	self := ""
	if m := regexp.MustCompile(`SELF (w[0-9a-f]+:p[0-9a-f]{12})`).FindStringSubmatch(out); m != nil {
		self = m[1]
	}
	if self == "" || !strings.Contains(out, `"pane_id":"`+self+`"`) {
		t.Fatalf("pane.current from the pane did not name HERDR_PANE_ID %q:\n%s", self, out)
	}
	typeIn(t, base, "caller", "clear")
	typeIn(t, base, "caller", client+` pane.send_text '{"pane_id":"`+targetID+`","text":"1"}'`)
	out = waitJoined(t, base, "caller", `"code":"forbidden"`)
	if !strings.Contains(out, "respond") {
		t.Fatalf("the refusal does not name the respond grant:\n%s", out)
	}

	// Events: a pane made now arrives on the stream.
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	_, _ = conn.Write([]byte(`{"id":"es","method":"events.subscribe","params":{"subscriptions":[{"type":"pane.created"},{"type":"tab.created"}]}}` + "\n"))
	stream := bufio.NewReader(conn)
	if ack, _ := stream.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}
	tab := herdrCall(t, base, "tab.create", map[string]any{"workspace_id": target["workspace_id"], "focus": false, "label": "herdr-tab"})
	if tab["tab"].(map[string]any)["label"] != "herdr-tab" {
		t.Fatalf("tab.create %v", tab)
	}
	seen := ""
	for !strings.Contains(seen, "pane_created") || !strings.Contains(seen, "tab_created") {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before pane_created and tab_created: %v\n%s", err, seen)
		}
		seen += line
	}
	if !strings.Contains(seen, `"label":"herdr-tab"`) {
		t.Errorf("tab_created does not carry the label:\n%s", seen)
	}
	saveFrame(t, term, "herdr-socket-api")
	alive(t, term, "after the herdr socket API")
}
