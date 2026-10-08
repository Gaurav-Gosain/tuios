package tuie2e

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The GUI bridge's Inbox (plan 2.13): the verb proxy with the person's nonce,
// the "attention", "notify" and "detached" events. Each test runs a real
// bridge against a real daemon, as tuios-gpui does, and an agent made of a
// shell script in a real pane.
//
// The nonce is a security boundary. Only the person may answer an agent's
// prompt, and the daemon proves the person by the attach nonce of a client
// outside every pane. The bridge adds its own nonce to the renderer's verbs,
// so the GUI never holds it. These tests check that the renderer's pipe can
// answer, and that a process in a pane cannot: neither with tuios respond,
// nor by starting a bridge of its own.

type wireVerbResult struct {
	Req    int64           `json:"req"`
	Verb   string          `json:"verb"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Code   string          `json:"code"`
	Error  string          `json:"error"`
}

type wireAttentionItem struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Session   string   `json:"session"`
	Window    string   `json:"window"`
	Summary   string   `json:"summary"`
	Options   []string `json:"options"`
	RequestID string   `json:"request_id"`
	Risk      []string `json:"risk"`
}

type wireInboxEvent struct {
	Type      string          `json:"type"`
	VerbRes   *wireVerbResult `json:"verb_result"`
	Attention *struct {
		Live  bool                `json:"live"`
		Items []wireAttentionItem `json:"items"`
	} `json:"attention"`
	Notify *struct {
		ID         string `json:"id"`
		Message    string `json:"message"`
		Level      string `json:"level"`
		AgentState string `json:"agent_state"`
		Session    string `json:"session"`
		Window     string `json:"window"`
		Desktop    bool   `json:"desktop"`
	} `json:"notify"`
	Detached *struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"detached"`
}

// waitEvent waits for an event the reader kept that satisfies ok, and returns
// it. The newest event is looked at first.
func (b *guiBridge) waitEvent(ok func(*wireInboxEvent) bool, what string, d time.Duration) *wireInboxEvent {
	b.t.Helper()
	deadline := time.Now().Add(d)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		for i := len(b.events) - 1; i >= 0; i-- {
			var ev wireInboxEvent
			if json.Unmarshal(b.events[i], &ev) == nil && ok(&ev) {
				return &ev
			}
		}
		if b.closed {
			b.t.Fatalf("bridge closed while waiting for %s", what)
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("bridge never sent %s", what)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// eventsOf returns the kept events of one type, in order.
func (b *guiBridge) eventsOf(typ string) []wireInboxEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []wireInboxEvent
	for _, raw := range b.events {
		var ev wireInboxEvent
		if json.Unmarshal(raw, &ev) == nil && ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// verb proxies one daemon verb through the bridge and returns its result.
func (b *guiBridge) verb(verb string, params map[string]any) wireVerbResult {
	b.t.Helper()
	return b.verbOn("", verb, params)
}

// verbOn is verb on a host's daemon. An empty host is this machine.
func (b *guiBridge) verbOn(host, verb string, params map[string]any) wireVerbResult {
	b.t.Helper()
	b.mu.Lock()
	b.req++
	req := b.req
	b.mu.Unlock()
	cmd := map[string]any{"cmd": "verb", "req": req, "verb": verb, "params": params}
	if host != "" {
		cmd["host"] = host
	}
	if sent, err := json.Marshal(map[string]any{"sent": cmd}); err == nil {
		b.mu.Lock()
		b.log = append(b.log, string(sent))
		b.mu.Unlock()
	}
	b.send(cmd)
	ev := b.waitEvent(func(e *wireInboxEvent) bool {
		return e.Type == "verb_result" && e.VerbRes != nil && e.VerbRes.Req == req
	}, "the result of "+verb, 40*time.Second)
	if got, err := json.Marshal(map[string]any{"verb_result": ev.VerbRes}); err == nil {
		b.mu.Lock()
		b.log = append(b.log, string(got))
		b.mu.Unlock()
	}
	return *ev.VerbRes
}

// writeAgentScript writes an executable script into the root's work folder.
func writeAgentScript(t *testing.T, base, name, body string) string {
	t.Helper()
	path := filepath.Join(workDirIn(t, base), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// newPaneRunning opens a pane in session that runs argv, and returns its id.
func newPaneRunning(t *testing.T, base, session, name string, argv ...string) string {
	t.Helper()
	args := append([]string{"new-window", "-s", session, "--no-focus", "--print-id", name, "--"}, argv...)
	out, err := tuiosCLI(t, base, args...)
	if err != nil {
		t.Fatalf("new-window %s: %v\n%s", name, err, out)
	}
	id := strings.TrimSpace(out)
	if i := strings.LastIndexByte(id, '\n'); i >= 0 {
		id = strings.TrimSpace(id[i+1:])
	}
	return id
}

// waitFile waits for a file to hold text that satisfies ok, and returns it.
func waitFile(t *testing.T, path string, ok func(string) bool, what string) string {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		data, _ := os.ReadFile(path)
		if ok(string(data)) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s holds %q", what, path, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// questionAgent is a Claude Code question as the pane shows it: the
// claude-code manifest's screen rules read the prompt and its numbered
// options from it. The first key typed is written to the answer file.
func questionAgent(answer string) string {
	return `#!/bin/bash
printf '\e[2J\e[H'
cat <<'T'
● Read src/client.ts
● Search "status === 429" · 3 matches

The client throws on any non-2xx response today.

Should the client retry on 429, or surface the error?

❯ 1. Retry with backoff
  2. Surface the error
  3. Type something else

Enter to select · ↑↓ to navigate · Esc to cancel
T
stty -echo -icanon 2>/dev/null
IFS= read -rsn1 key
printf 'key=%s\n' "$key" > ` + answer + `
sleep 600
`
}

// TestGUIBridgeVerbAnswersOnlyFromTheRenderer: the renderer answers an
// agent's question through the verb proxy, and the bridge supplies the
// nonce, dropping one the renderer forged. A process in a pane cannot answer
// the same prompt, with tuios respond or with a bridge it starts itself.
func TestGUIBridgeVerbAnswersOnlyFromTheRenderer(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "inbox", 160, 48)
	work := workDirIn(t, base)
	answer := filepath.Join(work, "answer.txt")
	agent := newPaneRunning(t, base, "inbox", "question", writeAgentScript(t, base, "question.sh", questionAgent(answer)))
	time.Sleep(500 * time.Millisecond)
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "inbox", "-w", agent,
		"--harness", "claude-code", "--kind", "question", "-m", "Retry on 429, or surface the error?"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}

	// The Inbox reaches the renderer as an attention event.
	b.waitEvent(func(e *wireInboxEvent) bool {
		if e.Type != "attention" || e.Attention == nil || !e.Attention.Live {
			return false
		}
		for _, it := range e.Attention.Items {
			if it.Window == agent && it.Kind == "question" {
				return true
			}
		}
		return false
	}, "an attention event with the question", shellTimeout)

	// A process in a pane: tuios respond is refused.
	denied := filepath.Join(work, "denied.txt")
	newPaneRunning(t, base, "inbox", "intruder", "/bin/sh", "-c",
		fmt.Sprintf("%q respond -s inbox -w %s choose 1 >%q 2>&1; echo rc=$? >>%q; sleep 600", tuiosBin, agent, denied, denied))
	got := waitFile(t, denied, func(s string) bool { return strings.Contains(s, "rc=") }, "tuios respond from a pane")
	if strings.Contains(got, "rc=0") || !strings.Contains(got, "for the person") {
		t.Fatalf("tuios respond from a pane was not refused as the person's:\n%s", got)
	}

	// A process in a pane: a bridge it starts is refused too. It attaches to
	// a session of its own and asks through its own renderer pipe.
	if out, err := tuiosCLI(t, base, "new", "-d", "side"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}
	fifo := filepath.Join(work, "in.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	outBin := filepath.Join(work, "inner.bin")
	newPaneRunning(t, base, "inbox", "inner-bridge", "/bin/sh", "-c",
		fmt.Sprintf("exec %q gui-bridge --session side --cols 80 --rows 24 <%q >%q 2>%q", tuiosBin, fifo, outBin, outBin+".err"))
	inner := openFifo(t, fifo)
	defer inner.Close()
	waitFrames(t, outBin, func(ev map[string]any) bool { return ev["type"] == "state" }, "the in-pane bridge's first state")
	writeFrame(t, inner, map[string]any{"cmd": "verb", "req": 1, "verb": "respond", "params": map[string]any{
		"session": "inbox", "window": agent, "action": "choose", "value": "1", "timeout": 1000,
	}})
	res := waitFrames(t, outBin, func(ev map[string]any) bool { return ev["type"] == "verb_result" }, "the in-pane bridge's verb result")
	if vr, _ := res["verb_result"].(map[string]any); vr == nil || vr["ok"] == true || vr["code"] != "not_human" {
		t.Fatalf("a bridge in a pane was not refused as not_human: %v", res)
	}
	t.Logf("the in-pane bridge got: %v", res["verb_result"])
	if data, _ := os.ReadFile(answer); len(data) > 0 {
		t.Fatalf("the agent got a key from inside a pane: %q", data)
	}

	// The renderer's own pipe answers. Its forged nonce is dropped.
	peek := b.verb("peek-prompt", map[string]any{"session": "inbox", "window": agent})
	if !peek.OK {
		t.Fatalf("peek-prompt: %s %s", peek.Code, peek.Error)
	}
	var pk struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal(peek.Result, &pk); err != nil || pk.PromptID == "" {
		t.Fatalf("peek-prompt gave no prompt id: %s", peek.Result)
	}
	r := b.verb("respond", map[string]any{
		"session": "inbox", "window": agent, "action": "choose", "value": "1",
		"prompt_id": pk.PromptID, "human_nonce": "forged-by-the-renderer", "timeout": 1000,
	})
	if !r.OK {
		t.Fatalf("respond through the renderer pipe failed: %s %s", r.Code, r.Error)
	}
	waitFile(t, answer, func(s string) bool { return strings.Contains(s, "key=1") }, "the agent's answer")
}

// openFifo opens a FIFO for writing, once its reader has it open.
func openFifo(t *testing.T, path string) *os.File {
	t.Helper()
	done := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- nil
			return
		}
		done <- f
	}()
	select {
	case f := <-done:
		if f == nil {
			t.Fatalf("open %s", path)
		}
		return f
	case <-time.After(shellTimeout):
		t.Fatalf("nothing read %s", path)
	}
	return nil
}

// writeFrame writes one JSON command frame.
func writeFrame(t *testing.T, w io.Writer, cmd map[string]any) {
	t.Helper()
	body, _ := json.Marshal(cmd)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(body)+1))
	buf.WriteByte(1)
	buf.Write(body)
	if _, err := w.Write(buf.Bytes()); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// waitFrames reads a file of bridge frames until a JSON event satisfies ok.
func waitFrames(t *testing.T, path string, ok func(map[string]any) bool, what string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for {
		data, _ := os.ReadFile(path)
		br := bufio.NewReader(bytes.NewReader(data))
		var hdr [5]byte
		for {
			if _, err := io.ReadFull(br, hdr[:]); err != nil {
				break
			}
			n := binary.BigEndian.Uint32(hdr[:4])
			body := make([]byte, n-1)
			if _, err := io.ReadFull(br, body); err != nil {
				break
			}
			if hdr[4] != 1 {
				continue
			}
			var ev map[string]any
			if json.Unmarshal(body, &ev) == nil && ok(ev) {
				return ev
			}
		}
		if time.Now().After(deadline) {
			errText, _ := os.ReadFile(path + ".err")
			t.Fatalf("%s never came; stderr: %s", what, errText)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// approvalAgent holds a risky Bash call for the Inbox through the Claude
// Code hook, as the installed integration does, and writes the hook's answer.
func approvalAgent(out string) string {
	return `#!/bin/bash
echo "asking to remove build"
printf '%s' '{"hook_event_name":"PermissionRequest","session_id":"s-1","tool_name":"Bash","tool_input":{"command":"rm -rf build/"}}' \
  | TUIOS_AGENT=claude-code "` + tuiosBin + `" agent-hook claude-code > ` + out + `.tmp 2>&1
mv ` + out + `.tmp ` + out + `
sleep 600
`
}

// TestGUIBridgeAttentionCarriesARiskyApproval: a held approval reaches the
// renderer with its options and risk. An allow without the risk named is
// refused; with it, the hook gets allow, and the Inbox empties. The bridge's
// own model does not announce the answer as one from another client.
func TestGUIBridgeAttentionCarriesARiskyApproval(t *testing.T) {
	base := t.TempDir()
	pinPreV080Looks(t, base)
	cfg := configPathIn(base)
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	data = append(data, []byte("\n[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 120\n")...)
	writeConfigAtomically(t, cfg, data)

	b := startBridge(t, base, "approve", 160, 48)
	hook := filepath.Join(workDirIn(t, base), "hook.json")
	agent := newPaneRunning(t, base, "approve", "cleaner", writeAgentScript(t, base, "approve.sh", approvalAgent(hook)))

	var item wireAttentionItem
	b.waitEvent(func(e *wireInboxEvent) bool {
		if e.Type != "attention" || e.Attention == nil {
			return false
		}
		for _, it := range e.Attention.Items {
			if it.Window == agent && it.Kind == "approval" && it.RequestID != "" {
				item = it
				return true
			}
		}
		return false
	}, "an attention event with the held approval", shellTimeout)
	if len(item.Risk) == 0 || len(item.Options) == 0 {
		t.Fatalf("the approval came without its risk or options: %+v", item)
	}

	// The second press is the GUI's: one allow that names no risk is
	// refused by the daemon.
	r := b.verb("reply-approval", map[string]any{"request_id": item.RequestID, "decision": "once", "summary": item.Summary})
	if r.OK || r.Code != "risk_unacknowledged" {
		t.Fatalf("an allow without risk_ack: ok=%v code=%q", r.OK, r.Code)
	}
	r = b.verb("reply-approval", map[string]any{"request_id": item.RequestID, "decision": "once", "summary": item.Summary, "risk_ack": item.Risk})
	if !r.OK {
		t.Fatalf("reply-approval: %s %s", r.Code, r.Error)
	}
	got := waitFile(t, hook, func(s string) bool { return s != "" }, "the hook's answer")
	if !strings.Contains(got, `"allow"`) {
		t.Fatalf("the hook did not get allow: %s", got)
	}
	b.waitEvent(func(e *wireInboxEvent) bool {
		if e.Type != "attention" || e.Attention == nil {
			return false
		}
		for _, it := range e.Attention.Items {
			if it.Window == agent && it.Kind == "approval" {
				return false
			}
		}
		return true
	}, "the approval leaving the Inbox", shellTimeout)
	time.Sleep(500 * time.Millisecond)
	for _, n := range b.eventsOf("notify") {
		if strings.Contains(n.Notify.Message, "another client") {
			t.Fatalf("the bridge announced its own answer as another client's: %q", n.Notify.Message)
		}
	}
}

// TestGUIBridgeNotifyAndDetached: an agent's error reaches the renderer as one
// notify event naming the pane, and killing the session sends a detached
// event with the reason before the bridge exits.
func TestGUIBridgeNotifyAndDetached(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "alerts", 160, 48)
	agent := newPaneRunning(t, base, "alerts", "migrations", "sleep", "600")
	time.Sleep(500 * time.Millisecond)
	if out, err := tuiosCLI(t, base, "set-agent-state", "errored", "-s", "alerts", "-w", agent,
		"--harness", "codex", "-m", "Migration failed"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	n := b.waitEvent(func(e *wireInboxEvent) bool {
		return e.Type == "notify" && e.Notify != nil && e.Notify.AgentState == "errored" && e.Notify.Window == agent
	}, "a notify event for the error", shellTimeout)
	if n.Notify.Session != "alerts" || !strings.Contains(n.Notify.Message, "Migration failed") || n.Notify.Level != "error" {
		t.Fatalf("the notify event: %+v", *n.Notify)
	}
	time.Sleep(time.Second)
	seen := map[string]int{}
	for _, e := range b.eventsOf("notify") {
		seen[e.Notify.ID]++
		if seen[e.Notify.ID] > 1 {
			t.Fatalf("alert %s sent twice", e.Notify.ID)
		}
	}

	if out, err := tuiosCLI(t, base, "kill-session", "alerts"); err != nil {
		t.Fatalf("kill-session: %v\n%s", err, out)
	}
	d := b.waitEvent(func(e *wireInboxEvent) bool { return e.Type == "detached" }, "the detached event", shellTimeout)
	if d.Detached.Reason != "session_ended" || !strings.Contains(d.Detached.Message, "alerts") {
		t.Fatalf("the detached event: %+v", *d.Detached)
	}
}
