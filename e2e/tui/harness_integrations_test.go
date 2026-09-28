package tuie2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The harness integrations, end to end: the real tuios agent-hook, the real
// plugins run by node against a stand-in for the harness's plugin API, a
// stand-in Crush that speaks herdr's protocol the way Crush does, and a
// stand-in Codex that chooses OSC 9 or the bell the way Codex does, each
// against a real daemon, with a real client attached where the result is
// something a person sees. Every test writes what the pane went through to a
// log under TUIOS_E2E_FRAMES, beside the frames it saves.

// agentStateJSON is what get-agent-state --json says about a pane.
type agentStateJSON struct {
	State        string `json:"state"`
	Message      string `json:"message"`
	Source       string `json:"source"`
	Harness      string `json:"harness_id"`
	AgentSession string `json:"agent_session_id"`
}

// readAgentState reads a pane's agent state, the zero value when it cannot.
func readAgentState(t *testing.T, base, session, window string) agentStateJSON {
	t.Helper()
	out, err := tuiosCLI(t, base, "get-agent-state", "--json", "-s", session, "-w", window)
	var st agentStateJSON
	if err == nil {
		_ = json.Unmarshal([]byte(out), &st)
	}
	return st
}

// stateLog is the artifact a test leaves: every state it waited for, and what
// the pane said when it got there.
type stateLog struct {
	name  string
	lines []string
}

func (l *stateLog) add(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// save writes the log beside the frames, when frames are kept.
func (l *stateLog) save(t *testing.T) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, l.name+".log"), []byte(strings.Join(l.lines, "\n")+"\n"), 0o644); err != nil {
		t.Logf("could not save %s: %v", l.name, err)
	}
}

// waitAgentState polls until the pane is in state, with harness when it is
// not empty, and logs what it saw.
func waitAgentState(t *testing.T, base, session, window, state, harness string, log *stateLog, step string) agentStateJSON {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		st := readAgentState(t, base, session, window)
		if st.State == state && (harness == "" || st.Harness == harness) {
			log.add("%-44s state=%s source=%s harness=%s message=%q session=%s", step, st.State, st.Source, st.Harness, st.Message, st.AgentSession)
			return st
		}
		if time.Now().After(deadline) {
			log.save(t)
			t.Fatalf("%s: the pane is %+v, want state %s harness %q", step, st, state, harness)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// runHook runs the real hook for harness with payload, for a pane named on
// the command line, and returns what it printed.
func runHook(t *testing.T, base, harness, session, window, payload string) string {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", harness, "--session", session, "--window", window)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("agent-hook %s: %v", harness, err)
	}
	return out.String()
}

// windowID is the id of the window named name.
func windowID(t *testing.T, base, session, name string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Windows []struct {
			ID    string `json:"window_id"`
			Name  string `json:"name"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	for _, w := range payload.Windows {
		if w.Name == name || w.Title == name {
			return w.ID
		}
	}
	t.Fatalf("no window %q in %s", name, out)
	return ""
}

// agentSessions makes the detached session the agents run in, and a home
// session with a client attached, which is where a person would be looking.
// env is layered over the client's environment.
func agentSessions(t *testing.T, env ...string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"e2e-home", "e2e-agent"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	return term, base
}

// TestHookIntegrationsReportTheTurn runs a turn of Qwen Code, GitHub Copilot
// CLI and Cursor Agent through the real hook, payloads as each harness's hook
// reference documents them, and checks the pane follows: idle at the start,
// working on the prompt, needs_input on the harness's own blocked signal,
// working once it is answered, and done at the end. Until this change all
// three reported the session id alone, and the pane's state never moved.
//
// Negative control: with the Qwen, Copilot and Cursor cases taken out of
// Translate, the hook maps them as session-only harnesses again and the first
// working wait fails.
func TestHookIntegrationsReportTheTurn(t *testing.T) {
	term, base := agentSessions(t)
	type step struct {
		name, payload, state string
	}
	cases := []struct {
		harness string
		steps   []step
	}{
		{"qwen", []step{
			{"SessionStart", `{"hook_event_name":"SessionStart","session_id":"qw-e2e","source":"startup"}`, "idle"},
			{"UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"qw-e2e","prompt":"fix the build"}`, "working"},
			{"Notification permission_prompt", `{"hook_event_name":"Notification","session_id":"qw-e2e","message":"Qwen Code needs your permission to use run_shell_command","notification_type":"permission_prompt"}`, "needs_input"},
			{"PostToolUse", `{"hook_event_name":"PostToolUse","session_id":"qw-e2e","tool_name":"run_shell_command"}`, "working"},
			{"Stop", `{"hook_event_name":"Stop","session_id":"qw-e2e","stop_hook_active":false,"last_assistant_message":"The build passes."}`, "done"},
		}},
		{"copilot", []step{
			{"SessionStart", `{"hook_event_name":"SessionStart","session_id":"cp-e2e","source":"startup"}`, "idle"},
			{"UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"cp-e2e","prompt":"fix the build"}`, "working"},
			{"notification permission_prompt", `{"sessionId":"cp-e2e","hook_event_name":"Notification","message":"Copilot wants to run git push","notification_type":"permission_prompt"}`, "needs_input"},
			{"PostToolUse", `{"hook_event_name":"PostToolUse","session_id":"cp-e2e","tool_name":"Bash"}`, "working"},
			{"Stop", `{"hook_event_name":"Stop","session_id":"cp-e2e","stop_reason":"end_turn","stop_hook_active":false}`, "done"},
		}},
		{"cursor-agent", []step{
			{"sessionStart", `{"hook_event_name":"sessionStart","session_id":"cu-e2e","is_background_agent":false}`, "idle"},
			{"beforeSubmitPrompt", `{"hook_event_name":"beforeSubmitPrompt","conversation_id":"cu-e2e","prompt":"fix the build"}`, "working"},
			{"stop completed", `{"hook_event_name":"stop","conversation_id":"cu-e2e","status":"completed","loop_count":0}`, "done"},
		}},
	}
	for _, tc := range cases {
		log := &stateLog{name: "hook-turn-" + tc.harness}
		if out, err := tuiosCLI(t, base, "new-window", tc.harness, "-s", "e2e-agent", "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		win := windowID(t, base, "e2e-agent", tc.harness)
		for _, s := range tc.steps {
			if out := runHook(t, base, tc.harness, "e2e-agent", win, s.payload); out != "" {
				t.Fatalf("%s %s: the hook printed %q, want nothing", tc.harness, s.name, out)
			}
			waitAgentState(t, base, "e2e-agent", win, s.state, tc.harness, log, tc.harness+" "+s.name)
		}
		log.save(t)
	}
	saveFrame(t, term, "hook-turns-done")
	alive(t, term, "after three harnesses' turns")
}

// TestQwenApprovalFromTheInbox is the approval round trip for Qwen Code: with
// [agents.approvals] naming qwen, its PermissionRequest for a shell command is
// held, the Inbox offers allow and deny (and not always, which Qwen Code does
// not take from this hook), 1 allows it, and the hook prints Qwen Code's own
// decision shape.
//
// Negative control: with the Qwen case taken out of Approval.Answer, the hook
// has no answer format for Qwen Code, prints nothing after the answer, and
// the decision check fails.
func TestQwenApprovalFromTheInbox(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents.approvals]\nenabled = [\"qwen\"]\nhold_seconds = 60\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, name := range []string{"e2e-home", "e2e-agent"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
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

	hook := exec.Command(tuiosBin, "agent-hook", "qwen", "--session", "e2e-agent", "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"qw-approval","permission_mode":"default","tool_name":"run_shell_command","tool_input":{"command":"go test ./...","is_background":false}}`)
	var stdout bytes.Buffer
	hook.Stdout = &stdout
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	exited := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		exited <- hook.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		<-finished
	})

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Approvals 1", "approve run_shell_command: go test", "allow", "deny")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never offered to answer the held approval: %v\n%s", err, term.Snapshot())
	}
	if text := term.Snapshot(); strings.Contains(text, "2 always") {
		t.Fatalf("the Inbox offers always, which Qwen Code does not take:\n%s", text)
	}
	saveFrame(t, term, "qwen-approval-held")
	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the hook failed: %v", err)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the hook never returned after the answer\n%s", term.Snapshot())
	}
	var decision struct {
		Out struct {
			Event    string `json:"hookEventName"`
			Decision struct {
				Behavior string `json:"behavior"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil || decision.Out.Event != "PermissionRequest" || decision.Out.Decision.Behavior != "allow" {
		t.Fatalf("the hook printed %q, want Qwen Code's allow decision", stdout.String())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Approvals 1")
	}, uiTimeout); err != nil {
		t.Fatalf("the answered approval stayed in the Inbox: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "qwen-approval-answered")
	alive(t, term, "after answering a Qwen Code approval")
}

// pluginDriver loads a real plugin tuios installed, under node, with a
// stand-in for the harness's plugin API, and emits the events the test writes
// to it, one JSON line each. The plugin runs the real tuios agent-hook for
// every event, against the test's daemon, for the pane named in the
// environment.
type pluginDriver struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	ready chan struct{}
}

// pluginLoadDeadline is a failsafe, not a budget. The driver says when the
// plugin has loaded, and the wait ends there; this only bounds a driver that
// hangs without exiting.
const pluginLoadDeadline = 60 * time.Second

var (
	nodeOnce sync.Once
	nodePath string
	nodeErr  error
)

// realNode is the node binary itself, asked of node under the suite's own
// environment, once.
//
// The node on PATH is often a version manager's shim (vite-plus, volta, asdf,
// nvm's wrappers), and a shim keeps the runtime it hands out under HOME. Each
// plugin runs with a HOME of its own, empty, so the shim installed a whole
// Node into it before the plugin could load: about five seconds and 200 MB
// per plugin on an idle machine, more under load, and the fixed wait for the
// plugin ran out. process.execPath is the binary the shim ended up running,
// which needs nothing from HOME.
func realNode() (string, error) {
	nodeOnce.Do(func() {
		shim, err := exec.LookPath("node")
		if err != nil {
			nodeErr = err
			return
		}
		out, err := exec.Command(shim, "-e", "process.stdout.write(process.execPath)").Output()
		if err != nil {
			nodeErr = fmt.Errorf("%s -e process.execPath: %w", shim, err)
			return
		}
		nodePath = strings.TrimSpace(string(out))
		if nodePath == "" {
			nodePath = shim
		}
	})
	return nodePath, nodeErr
}

// startPlugin renders harness's plugin with tuios integration install and
// starts it under the driver script, which knows each harness's API shape.
func startPlugin(t *testing.T, base, harness, session, window string) *pluginDriver {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	node, err := realNode()
	if err != nil {
		t.Fatalf("find the node binary: %v", err)
	}
	home := filepath.Join(base, "plugin-home-"+harness)
	env := []string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent")}
	var file string
	switch harness {
	case "pi":
		mustMkdir(filepath.Join(home, ".pi", "agent"))
		file = filepath.Join(home, ".pi", "agent", "extensions", "tuios-agent-state.ts")
	case "amp":
		mustMkdir(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "amp"))
		file = filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "amp", "plugins", "tuios-agent-state.ts")
	case "opencode":
		mustMkdir(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "opencode"))
		file = filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "opencode", "plugins", "tuios-agent-state.js")
	}
	if out, err := tuiosCLIEnv(t, base, env, "integration", "install", harness, "--command", tuiosBin); err != nil {
		t.Fatalf("integration install %s: %v\n%s", harness, err, out)
	}
	driver := filepath.Join("testdata", "plugindriver.mjs")
	abs, _ := filepath.Abs(driver)
	cmd := exec.Command(node, abs, harness, file)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = os.Environ()
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	// After the isolation keys, so this plugin's own HOME wins over the
	// suite's.
	cmd.Env = append(cmd.Env, env...)
	cmd.Env = append(cmd.Env, "TUIOS_ENV=1", "TUIOS_SESSION="+session, "TUIOS_PANE_ID="+window)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start node: %v", err)
	}
	d := &pluginDriver{cmd: cmd, stdin: stdin, ready: make(chan struct{})}
	// closed is closed when node closes its stdout, which it does when it
	// exits: a driver that dies before DRIVER-READY fails the wait at once.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if sc.Text() == "DRIVER-READY" {
				close(d.ready)
			}
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { <-closed; _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("plugin driver stderr:\n%s", stderr.String())
		}
	})
	start := time.Now()
	select {
	case <-d.ready:
		t.Logf("the %s plugin loaded in %v", harness, time.Since(start).Round(time.Millisecond))
	case <-closed:
		select {
		case <-d.ready:
			t.Fatalf("the %s plugin driver exited right after the plugin loaded:\n%s", harness, stderr.String())
		default:
		}
		t.Fatalf("the %s plugin driver exited before the plugin loaded:\n%s", harness, stderr.String())
	case <-time.After(pluginLoadDeadline):
		t.Fatalf("the %s plugin did not load in %v:\n%s", harness, pluginLoadDeadline, stderr.String())
	}
	return d
}

// emit hands the plugin one event.
func (d *pluginDriver) emit(t *testing.T, event map[string]any) {
	t.Helper()
	line, _ := json.Marshal(event)
	if _, err := d.stdin.Write(append(line, '\n')); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

// TestPluginsReportBlockingPrompts loads the real Pi, Amp and opencode
// plugins under node, each against a stand-in for its harness's plugin API,
// and plays the events the harness emits around a prompt that waits on the
// person: Pi's ui_prompt_start and ui_prompt_end, Amp's ask_user_choice tool
// call and result, and the end of an opencode turn announced only by
// session.status idle. The pane goes to needs_input with the prompt's text,
// back to working when it is answered, and to done at the end of the turn.
//
// Negative controls: with the ui_prompt_start handler taken out of the Pi
// extension, or the tool.call handler out of the Amp plugin, the pane never
// leaves working and the needs_input wait fails. With the session.status idle
// case taken out of translateOpenCode, the opencode turn never reads done.
func TestPluginsReportBlockingPrompts(t *testing.T) {
	term, base := agentSessions(t)
	type step struct {
		name  string
		event map[string]any
		state string
	}
	cases := []struct {
		harness string
		steps   []step
	}{
		{"pi", []step{
			{"session_start", map[string]any{"type": "session_start", "idle": true}, "idle"},
			{"agent_start", map[string]any{"type": "agent_start", "idle": false}, "working"},
			{"ui_prompt_start confirm", map[string]any{"type": "ui_prompt_start", "idle": false, "event": map[string]any{"reason": "ui_prompt", "kind": "confirm", "title": "Allow rm -rf build?"}}, "needs_input"},
			{"ui_prompt_end", map[string]any{"type": "ui_prompt_end", "idle": false, "event": map[string]any{"reason": "ui_prompt", "kind": "confirm"}}, "working"},
			{"agent_settled", map[string]any{"type": "agent_settled", "idle": true}, "done"},
		}},
		{"amp", []step{
			{"session.start", map[string]any{"type": "session.start", "event": map[string]any{"thread": map[string]any{"id": "T-e2e"}}}, ""},
			{"agent.start", map[string]any{"type": "agent.start", "event": map[string]any{"thread": map[string]any{"id": "T-e2e"}}}, "working"},
			{"tool.call ask_user_choice", map[string]any{"type": "tool.call", "event": map[string]any{"thread": map[string]any{"id": "T-e2e"}, "toolUseID": "toolu_1", "tool": "ask_user_choice", "input": map[string]any{"question": "Which approach?", "options": []string{"a", "b"}}}}, "needs_input"},
			{"tool.result ask_user_choice", map[string]any{"type": "tool.result", "event": map[string]any{"thread": map[string]any{"id": "T-e2e"}, "toolUseID": "toolu_1", "tool": "ask_user_choice", "status": "done"}}, "working"},
			{"agent.end done", map[string]any{"type": "agent.end", "event": map[string]any{"thread": map[string]any{"id": "T-e2e"}, "status": "done"}}, "done"},
		}},
		{"opencode", []step{
			{"session.created", map[string]any{"type": "event", "event": map[string]any{"type": "session.created", "properties": map[string]any{"info": map[string]any{"id": "ses_e2e"}}}}, "idle"},
			{"session.status busy", map[string]any{"type": "event", "event": map[string]any{"type": "session.status", "properties": map[string]any{"sessionID": "ses_e2e", "status": map[string]any{"type": "busy"}}}}, "working"},
			{"session.status idle", map[string]any{"type": "event", "event": map[string]any{"type": "session.status", "properties": map[string]any{"sessionID": "ses_e2e", "status": map[string]any{"type": "idle"}}}}, "done"},
		}},
	}
	for _, tc := range cases {
		log := &stateLog{name: "plugin-prompts-" + tc.harness}
		if out, err := tuiosCLI(t, base, "new-window", tc.harness, "-s", "e2e-agent", "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		win := windowID(t, base, "e2e-agent", tc.harness)
		d := startPlugin(t, base, tc.harness, "e2e-agent", win)
		for _, s := range tc.steps {
			d.emit(t, s.event)
			if s.state == "" {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			st := waitAgentState(t, base, "e2e-agent", win, s.state, tc.harness, log, tc.harness+" "+s.name)
			if s.state == "needs_input" && st.Message == "" {
				t.Fatalf("%s: the pane blocked with no message", tc.harness)
			}
		}
		log.save(t)
	}
	saveFrame(t, term, "plugin-prompts-done")
	alive(t, term, "after the plugins' prompts")
}

// buildFakeCrush builds the stand-in Crush as a program named crush, so a
// pane that runs it runs "crush" the way a person would.
func buildFakeCrush(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "crush")
	build := exec.Command("go", "build", "-o", bin, "./testdata/fakecrush")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakecrush: %v\n%s", err, out)
	}
	return bin
}

// TestHerdrProtocolReportsACrushPane starts a stand-in Crush in a pane of a
// real daemon. It reports over herdr's protocol exactly as Crush's own client
// does, only because the pane told it where: tuios's own socket, never
// herdr's. Its reports move the pane through idle, working, needs_input and
// done, a stale seq is dropped, a report for another pane and a method tuios
// does not answer are refused, and release clears the pane. A shell pane in
// the same session is not told it is a herdr pane.
//
// Negative control: with the HerdrEnv call taken out of buildEnvFor, the
// stand-in finds no herdr environment, prints NO-HERDR, and the first wait
// fails.
func TestHerdrProtocolReportsACrushPane(t *testing.T) {
	term, base := agentSessions(t)
	crush := buildFakeCrush(t)
	log := &stateLog{name: "herdr-protocol-crush"}
	if out, err := tuiosCLI(t, base, "new-window", "crush", "-s", "e2e-agent", "--no-focus", "--", crush); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	win := windowID(t, base, "e2e-agent", "crush")
	out := waitCapture(t, base, "e2e-agent", "crush", "FAKE-CRUSH-READY")
	if !strings.Contains(out, `HERDR_ENV="1" PANE_MATCHES=true SOCKET_SET=true`) {
		t.Fatalf("the pane was not told about the herdr socket:\n%s", out)
	}
	log.add("pane environment: %s", firstLineWith(out, "HERDR_ENV="))
	waitAgentState(t, base, "e2e-agent", win, "idle", "crush", log, "first report, idle")

	send := func(word string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", win, word+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}
	send("working")
	waitAgentState(t, base, "e2e-agent", win, "working", "crush", log, "working")
	send("blocked")
	st := waitAgentState(t, base, "e2e-agent", win, "needs_input", "crush", log, "blocked")
	if st.AgentSession != "fake-session" {
		t.Errorf("the pane's agent session is %q, want the one Crush reported", st.AgentSession)
	}
	if st.Message != "waits for approval" {
		t.Errorf("the block says %q, want Crush's permission request", st.Message)
	}
	waitForAll(t, term, uiTimeout, "the rail row", "crush")
	saveFrame(t, term, "herdr-protocol-crush-blocked")
	send("working")
	waitAgentState(t, base, "e2e-agent", win, "working", "crush", log, "answered, working")
	send("idle")
	waitAgentState(t, base, "e2e-agent", win, "done", "crush", log, "idle after a turn, done")

	// A report older than the last one is dropped without an error, as
	// herdr drops it: the pane stays done.
	send("stale")
	time.Sleep(500 * time.Millisecond)
	if st := readAgentState(t, base, "e2e-agent", win); st.State != "done" {
		t.Fatalf("a stale report moved the pane to %s", st.State)
	}
	log.add("%-44s state=done (unchanged)", "stale seq")

	send("foreign")
	send("unsupported")
	out = waitCapture(t, base, "e2e-agent", "crush", `"code":"forbidden"`, `"code":"unsupported"`)
	log.add("foreign pane: %s", firstLineWith(out, `"forbidden"`))
	log.add("unsupported method: %s", firstLineWith(out, `"unsupported"`))

	send("release")
	waitAgentState(t, base, "e2e-agent", win, "none", "", log, "release, none")

	// A shell is not a herdr pane.
	if out, err := tuiosCLI(t, base, "new-window", "shell", "-s", "e2e-agent", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "shell", "echo HE=${HERDR_ENV:-unset} HS=${HERDR_SOCKET_PATH:-unset} HP=${HERDR_PANE_ID:-unset}\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	out = waitCapture(t, base, "e2e-agent", "shell", "HE=unset HS=unset HP=unset")
	log.add("shell pane environment: %s", firstLineWith(out, "HE=unset"))
	log.save(t)
	alive(t, term, "after the herdr protocol reports")
}

// TestHerdrProtocolAlwaysTellsShellPanes sets herdr_protocol = "always" and
// checks a shell pane is then told about the socket, so a Crush started from
// a shell prompt reports too, and that "off" tells even a Crush pane nothing.
//
// Negative control: with the always case taken out of Manager.HerdrEnv, the
// shell pane reads HE=unset and the first wait fails.
func TestHerdrProtocolAlwaysTellsShellPanes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(dir)
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[agents]\nherdr_protocol = \"always\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "shell", "-s", "e2e-agent"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	win := windowID(t, base, "e2e-agent", "shell")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "shell", "echo HE=${HERDR_ENV:-unset} HP=${HERDR_PANE_ID:-unset}\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-agent", "shell", "HE=1 HP="+win)

	crush := buildFakeCrush(t)
	if err := os.WriteFile(cfg, []byte("[agents]\nherdr_protocol = \"off\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// The daemon follows the file; wait for a pane made after the change to
	// see it.
	deadline := time.Now().Add(uiTimeout)
	for i := 0; ; i++ {
		name := "crush" + itoa(i)
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "e2e-agent", "--no-focus", "--", crush); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		out := waitCapture(t, base, "e2e-agent", name, "HERDR_ENV=")
		if strings.Contains(out, "NO-HERDR") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("herdr_protocol = off never reached a new Crush pane:\n%s", out)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// firstLineWith is the first line of s that holds sub.
func firstLineWith(s, sub string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// TestCodexPaneNotificationsArrive starts a stand-in Codex in a pane, a
// script named codex that picks OSC 9 or the bell from TERM_PROGRAM the way
// Codex's own notification code does, on a terminal with neither kitty
// graphics nor sixel. The pane is told a terminal Codex sends OSC 9 to, so
// the notification's text reaches the attached client. A shell pane beside it
// is still told TUIOS.
//
// The stand-in notifies when a turn ends, after it is sent a prompt, as Codex
// does, and the prompt is sent only once the client shows the pane. A
// notification written the instant the pane starts can land before the client
// has taken the pane's snapshot, and a snapshot carries the screen, not the
// notifications that passed through it. On Linux a shell script starts fast
// enough to do that, and the toast never appeared.
//
// Negative control: with TermProgramFor answering TermProgram's name for
// Codex too, the stand-in sees TUIOS and rings the bell instead, and the test
// fails on the name the pane was told.
func TestCodexPaneNotificationsArrive(t *testing.T) {
	// The terminal the suite drives answers kitty graphics, so the client is
	// told the host has neither protocol, which is the host this is about.
	term, base := agentSessions(t, "TUIOS_KITTY_GRAPHICS=0", "TUIOS_SIXEL_GRAPHICS=0")
	log := &stateLog{name: "codex-notification"}
	dir := t.TempDir()
	codex := filepath.Join(dir, "codex")
	// supports_osc9 in codex-rs/tui/src/notifications/mod.rs, by the names
	// codex-rs/terminal-detection maps TERM_PROGRAM to.
	script := `#!/bin/sh
echo "CODEX-SAW TERM_PROGRAM=$TERM_PROGRAM"
read -r prompt
case "$TERM_PROGRAM" in
  ghostty|iTerm.app|kitty|WarpTerminal|WezTerm) printf '\033]9;%s\007' "Codex finished: e2e notification" ;;
  *) printf '\007' ;;
esac
echo CODEX-NOTIFIED
exec cat
`
	if err := os.WriteFile(codex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "codex", "-s", "e2e-home", "--", codex); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	if err := term.WaitForText("CODEX-SAW", uiTimeout); err != nil {
		t.Fatalf("the client never showed the Codex pane: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-home", "-w", "codex", "finish the turn\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	out := waitCapture(t, base, "e2e-home", "codex", "CODEX-NOTIFIED")
	log.add("codex pane: %s", firstLineWith(out, "CODEX-SAW"))
	if !strings.Contains(out, "TERM_PROGRAM=WarpTerminal") {
		log.save(t)
		t.Fatalf("the Codex pane was not told a terminal Codex sends OSC 9 to:\n%s", out)
	}
	if err := term.WaitForText("e2e notification", uiTimeout); err != nil {
		log.save(t)
		t.Fatalf("the notification's text never reached the client: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "codex-notification")

	if out, err := tuiosCLI(t, base, "new-window", "shell", "-s", "e2e-home", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-home", "-w", "shell", "echo SHELL-SAW TERM_PROGRAM=$TERM_PROGRAM\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	out = waitCapture(t, base, "e2e-home", "shell", "SHELL-SAW TERM_PROGRAM=TUIOS")
	log.add("shell pane: %s", firstLineWith(out, "SHELL-SAW TERM_PROGRAM=TUIOS"))
	log.save(t)
	alive(t, term, "after the Codex notification")
}

// TestGooseIsRecognisedWhereItsInstallerPutsIt starts a stand-in for goose's
// CLI from ~/.local/bin, where goose's installer puts it, drawing goose's
// tool approval prompt. The detector names the pane goose and its screen
// rules read the prompt as needs_input, then the spinner as working once the
// prompt is answered. The same binary installed as a Go tool, where pressly's
// migration tool of the same name lives, is not taken for an agent at all.
//
// Negative controls: with the goose manifest taken out of the bundle, the
// pane is never named goose and the first wait fails. With goose back in the
// detector's list of bare agent names, the Go tool reads as an unnamed agent
// that is working, and the last check fails.
func TestGooseIsRecognisedWhereItsInstallerPutsIt(t *testing.T) {
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "1")
	term, base := agentSessions(t)
	log := &stateLog{name: "goose-manifest"}
	root := t.TempDir()
	installed := filepath.Join(root, "home", ".local", "bin", "goose")
	elsewhere := filepath.Join(root, "go", "bin", "goose")
	for _, bin := range []string{installed, elsewhere} {
		build := exec.Command("go", "build", "-o", bin, "./testdata/fakegoose")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build fakegoose: %v\n%s", err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "new-window", "goose", "-s", "e2e-agent", "--no-focus", "--", installed); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	win := windowID(t, base, "e2e-agent", "goose")
	waitCapture(t, base, "e2e-agent", "goose", "fake goose started")
	waitAgentState(t, base, "e2e-agent", win, "working", "goose", log, "recognised")
	// The prompt comes after goose has started, as a tool call does.
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", win, "ask\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	st := waitAgentState(t, base, "e2e-agent", win, "needs_input", "goose", log, "approval prompt on screen")
	if st.Source != "screen" {
		t.Errorf("the block came from %s, want the screen rules", st.Source)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", win, "y\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitAgentState(t, base, "e2e-agent", win, "working", "goose", log, "answered, spinner on screen")

	if out, err := tuiosCLI(t, base, "new-window", "migrate", "-s", "e2e-agent", "--no-focus", "--", elsewhere); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	other := windowID(t, base, "e2e-agent", "migrate")
	waitCapture(t, base, "e2e-agent", "migrate", "fake goose started")
	time.Sleep(2 * time.Second)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", other, "ask\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, "e2e-agent", "migrate", "Goose would like to call the above tool")
	// Several detector ticks, and long enough for the screen rules to have
	// read the same prompt had the pane been goose.
	time.Sleep(4 * time.Second)
	st = readAgentState(t, base, "e2e-agent", other)
	if st.Harness == "goose" || st.State != "none" {
		log.save(t)
		t.Fatalf("a goose outside goose's install locations was taken for the agent: %+v", st)
	}
	log.add("%-44s not goose (state=%s source=%s harness=%q)", "the same binary under go/bin", st.State, st.Source, st.Harness)
	log.save(t)
	saveFrame(t, term, "goose-manifest")
	alive(t, term, "after the goose panes")
}
