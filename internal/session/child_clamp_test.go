package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// A child never gets wider grants or a wider permission mode than the pane
// that starts it. This is a security boundary: a prompt-injected agent that
// could start a helper wider than itself would get, through the helper, what
// the person did not give it. See child_clamp.go.

// TestAChildNeverHoldsGrantsItsParentLacks starts panes from a pane the person
// gave admin alone, under a default that gives every pane on it respond. A
// child that names no grants used to hold the default, respond included, which
// the parent does not hold and could not give.
func TestAChildNeverHoldsGrantsItsParentLacks(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	setStrict(d, "read", "write", "fan", "respond")
	admin := GrantAdmin
	if !d.manager.grants.set(a1, &admin) {
		t.Fatal("pane a1 is not in the grant table")
	}
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		if cs.peerPID == os.Getpid() {
			return false, ""
		}
		return true, a1
	})

	// launchGrants is what start-agent, fan and worktree fan decide with.
	cs := &connState{}
	g, verr := d.launchGrants(cs, nil)
	if verr != nil {
		t.Fatalf("launchGrants: %v", verr)
	}
	if held := grantsOrDefault(d, g); held.Has(GrantRespond) {
		t.Errorf("a child of an admin pane holds %v, with respond its parent lacks", held)
	}

	// The verbs that open a pane through addWindowFor, called by a process
	// in the pane.
	out := callAsChildProcess(t, sp, "pane",
		map[string]any{"verb": "new-window", "params": map[string]any{"session": "a", "focus": false}},
		map[string]any{"verb": "new-session", "params": map[string]any{"name": "child-of-admin"}},
	)
	for i, what := range []string{"new-window", "new-session"} {
		res, ok := out[i]["result"].(map[string]any)
		if !ok {
			t.Fatalf("%s from the pane failed: %v", what, out[i])
		}
		id, _ := res["window_id"].(string)
		if held, _ := d.manager.grants.effective(id); held.Has(GrantRespond) {
			t.Errorf("%s from an admin pane gave the new pane %v, with respond its parent lacks", what, held)
		}
	}

	// The person, outside every pane, is not held: the new pane keeps the
	// default the person configured.
	c := dialVerb(t, sp)
	res := result(t, callP(c, t, "new-window", map[string]any{"session": "a", "focus": false}))
	if held, explicit := d.manager.grants.effective(res["window_id"].(string)); explicit || !held.Has(GrantRespond) {
		t.Errorf("a window the person opened holds %v (explicit %v), want the default with respond", held, explicit)
	}
}

// TestAChildAgentNeverRunsWiderThanItsParent runs a parent agent, this test
// binary under the name claude with a permission mode, in a pane. It starts
// child agents through the daemon. A child that asks for a wider mode is
// refused, and a child that names none while the harness default is wider
// than the parent is stepped down to the parent's mode.
func TestAChildAgentNeverRunsWiderThanItsParent(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no procfs: the parent agent is read from the process table")
	}
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		if cs.peerPID == os.Getpid() {
			return false, ""
		}
		return true, a1
	})
	bin := fakeAgentBin(t)
	env := map[string]any{"PATH": bin}
	repo := testutil.GitRepo(t)
	start := func(agent string, args ...string) map[string]any {
		p := map[string]any{"session": "a", "agent": agent, "env": env, "ready_timeout": 200}
		if len(args) > 0 {
			p["args"] = args
		}
		return map[string]any{"verb": "start-agent", "params": p}
	}

	out := callAsChildProcess(t, sp, "claude --permission-mode plan",
		start("claude", "--permission-mode", "bypassPermissions"),
		start("claude --dangerously-skip-permissions"),
		start("claude --permission-mode=acceptEdits"),
		map[string]any{"verb": "new-window", "params": map[string]any{
			"session": "a", "focus": false, "env": env,
			"command": []string{filepath.Join(bin, "claude"), "--permission-mode", "auto"},
		}},
		map[string]any{"verb": "fan", "params": map[string]any{
			"count": 1, "agent": "codex --yolo", "prompt": "x", "repo": repo, "env": env,
		}},
		start("claude"),
		start("codex"),
		start("claude", "--permission-mode", "plan"),
	)
	for i, what := range []string{
		"start-agent claude --permission-mode bypassPermissions",
		"start-agent claude --dangerously-skip-permissions",
		"start-agent claude --permission-mode=acceptEdits",
		"new-window claude --permission-mode auto",
		"fan codex --yolo",
	} {
		e, ok := out[i]["error"].(map[string]any)
		if !ok || e["code"] != ErrVerbForbidden {
			t.Errorf("%s from a parent in plan mode was not refused: %v", what, out[i])
			continue
		}
		if msg, _ := e["message"].(string); !strings.Contains(msg, "permission mode") {
			t.Errorf("%s was refused without naming the permission mode: %v", what, e)
		}
	}
	for i, want := range map[int]string{
		5: "--permission-mode plan",
		6: "--sandbox read-only",
		7: "--permission-mode plan",
	} {
		res, ok := out[i]["result"].(map[string]any)
		if !ok {
			t.Errorf("call %d from a parent in plan mode failed: %v", i, out[i])
			continue
		}
		if cmd, _ := res["command"].(string); !strings.Contains(cmd, want) {
			t.Errorf("call %d started %q, want it held to the parent's mode with %s", i, cmd, want)
		}
		if i == 7 && strings.Count(res["command"].(string), "--permission-mode") != 1 {
			t.Errorf("a child already in the parent's mode was changed: %q", res["command"])
		}
	}

	// A parent that runs with every check off may start a child the same.
	out = callAsChildProcess(t, sp, "claude --dangerously-skip-permissions",
		start("claude", "--permission-mode", "bypassPermissions"))
	if _, ok := out[0]["result"].(map[string]any); !ok {
		t.Errorf("a parent in bypass mode could not start a child in bypass mode: %v", out[0])
	}

	// The person, outside every pane, is never held.
	c := dialVerb(t, sp)
	res := result(t, callP(c, t, "start-agent", map[string]any{
		"session": "a", "agent": "claude --dangerously-skip-permissions", "env": env, "ready_timeout": 200,
	}))
	if cmd, _ := res["command"].(string); !strings.HasSuffix(cmd, "--dangerously-skip-permissions") {
		t.Errorf("the person's start-agent ran %q", cmd)
	}
}

// grantsOrDefault is what a pane given g holds: g, or the default for nil.
func grantsOrDefault(d *Daemon, g *Grants) Grants {
	if g != nil {
		return *g
	}
	return d.manager.grants.defaults()
}

// fakeAgentBin is a directory where claude and codex are this test binary,
// which TestMain turns into a wait (or a caller, see callAsChildProcess).
func fakeAgentBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		t.Fatal(err)
	}
	for _, name := range fakeParentNames {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// callAsChildProcess runs this test binary as another process, under the name
// and with the arguments of command, and has it send calls over the daemon
// socket. It returns one response per call. The process is the caller the
// daemon sees, so the daemon reads its name and arguments from the process
// table as it would read a real agent's.
func callAsChildProcess(t *testing.T, sp, command string, calls ...map[string]any) []map[string]any {
	t.Helper()
	words := strings.Fields(command)
	bin := fakeAgentBin(t)
	name := words[0]
	if name == "pane" {
		// A plain process in the pane, no agent: run under a name no
		// manifest knows.
		self, _ := os.Executable()
		name = filepath.Join(bin, fakeCallerName)
		if err := os.Symlink(self, name); err != nil {
			t.Fatal(err)
		}
	} else {
		name = filepath.Join(bin, name)
	}
	var lines []string
	for i, c := range calls {
		c["id"] = i + 1
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	cmd := exec.Command(name, words[1:]...)
	cmd.Env = append(os.Environ(), "TUIOS_TEST_CALL_SOCKET="+sp, "TUIOS_TEST_CALL_LINES="+strings.Join(lines, "\n"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("the caller process failed: %v\n%s", err, raw)
	}
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("the caller process printed %q: %v", line, err)
		}
		out = append(out, m)
	}
	if len(out) != len(calls) {
		t.Fatalf("%d calls gave %d responses:\n%s", len(calls), len(out), raw)
	}
	return out
}

// fakeParentNames are the names the test binary runs under as an agent. As
// such it waits, or with TUIOS_TEST_CALL_SOCKET set calls the daemon.
var fakeParentNames = []string{"claude", "codex"}

// fakeCallerName is the name it runs under as a plain process that calls the
// daemon.
const fakeCallerName = "plain-caller"

// runFakeCaller is the test binary as a caller: it sends each line of
// TUIOS_TEST_CALL_LINES to the daemon and prints each response line.
func runFakeCaller(socket string) int {
	conn, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	for line := range strings.SplitSeq(os.Getenv("TUIOS_TEST_CALL_LINES"), "\n") {
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		if _, err := conn.Write([]byte(line + "\n")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		resp, err := r.ReadBytes('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.Stdout.Write(resp)
	}
	return 0
}
