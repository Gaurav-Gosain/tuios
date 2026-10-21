package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The cockpit of DESIGN.md frame (a), built for real: twelve agents on three
// machines, one approval held, three branches ready for review with their
// pull requests, a fan of three attempts, and an agent running two
// subagents. The local machine runs the worktrees and the fan, build and gpu
// are second daemons reached through the ssh stand-in.

// frameAPR is the pull request the fake gh answers for a branch: number,
// state and checks, as gh pr view --json prints them.
var frameAPR = map[string]string{
	"docs/cli-flags": `{"number":884,"url":"https://github.example/o/r/pull/884","state":"OPEN","statusCheckRollup":[` +
		`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
	"feat/sixel-scroll": `{"number":881,"url":"https://github.example/o/r/pull/881","state":"OPEN","statusCheckRollup":[` +
		`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"},` +
		`{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
}

// frameAWorktree is one local agent in a worktree session of its own.
type frameAWorktree struct {
	session, branch, name, harness, state, message string
	meta                                           []string
}

var frameAWorktrees = []frameAWorktree{
	{"auth", "fix/token-refresh", "api-auth", "claude-code", "", "", []string{"model=opus 4.5", "context=62%", "cost=$1.84", "diff=+214/-37"}},
	{"docs", "docs/cli-flags", "docs-cli", "codex", "done", "", []string{"diff=+88/-12"}},
	{"sixel", "feat/sixel-scroll", "vt-sixel", "claude-code", "done", "", []string{"diff=+412/-160"}},
	{"rail", "feat/rail-tokens", "rail-tokens", "opencode", "done", "", []string{"diff=+36/-4"}},
	{"migrate", "chore/config-v3", "migrate-cfg", "claude-code", "working", "", []string{"diff=+230/-198"}},
}

// frameARemote is one agent on another machine.
type frameARemote struct {
	host, name, harness, state, message string
	meta                                []string
}

var frameAFar = []frameARemote{
	{"build", "ws-reconnect", "codex", "working", "Edit internal/federation/link.go", []string{"diff=+54/-11"}},
	{"build", "release-notes", "goose", "idle", "", nil},
	{"gpu", "bench-idle", "claude-code", "working", "Bash go test -bench Idle -count 5", []string{"context=31%", "diff=+18/-2"}},
}

// frameAFan is the fan: its attempts, what each runs, and what each reports.
var frameAFan = []struct{ session, name, harness, message, diff string }{
	{"repo-cache", "cache-1", "gemini", "Bash go test ./internal/cache/...", "+142/-20"},
	{"repo-cache-2", "cache-2", "claude-code", "Edit internal/cache/lru.go", "+61/-9"},
	{"repo-cache-3", "cache-3", "codex", "Read internal/cache/evict.go", "+97/-31"},
}

// claudeAuthPane is what api-auth's pane prints: Claude Code at its
// permission prompt, as the live box should show it.
const claudeAuthPane = "\033[32m●\033[0m \033[1mUpdate(internal/auth/token.go)\033[0m\n  \033[2m└  Updated with 12 additions and 3\n     removals\033[0m\n\n\033[33m●\033[0m \033[1mBash(go test ./internal/auth/...\n       -run Token -count=1)\033[0m\n\n \033[33;1mBash command\033[0m\n Do you want to proceed?\n \033[36m❯ 1. Yes\033[0m\n   2. Yes, and do not ask again for\n      go test commands\n   3. No, and tell Claude what to do\n"

// startFrameA builds the scenario and opens the cockpit at cols x rows. It
// returns the client.
func startFrameA(t *testing.T, cols, rows int) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	build, gpu := remoteMachine(t), remoteMachine(t)
	repo := testutil.GitRepo(t)

	// The fakes: an ssh that routes by address, gh that answers per branch,
	// and agents that print a line and wait.
	fakes := filepath.Join(t.TempDir(), "fakes")
	if err := os.MkdirAll(fakes, 0o755); err != nil {
		t.Fatal(err)
	}
	var ssh strings.Builder
	ssh.WriteString("#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\naddr=$1\nshift\ncase \"$addr\" in\n")
	for addr, far := range map[string]string{"someone@buildbox": build, "someone@gpubox": gpu} {
		ssh.WriteString("  " + addr + ")")
		for _, key := range xdgKeys {
			ssh.WriteString(" export " + key + "=" + xdgDir(far, key) + ";")
		}
		ssh.WriteString(" ;;\n")
	}
	ssh.WriteString("esac\nexec /bin/sh -c \"$*\"\n")
	var gh strings.Builder
	gh.WriteString("#!/bin/sh\ncase \"$1 $2\" in\n'auth status') exit 0;;\n'pr view') case \"$3\" in\n")
	for branch, js := range frameAPR {
		os.WriteFile(filepath.Join(fakes, strings.ReplaceAll(branch, "/", "_")+".json"), []byte(js), 0o644)
		gh.WriteString("    " + branch + ") cat '" + filepath.Join(fakes, strings.ReplaceAll(branch, "/", "_")+".json") + "'; exit 0;;\n")
	}
	gh.WriteString("  esac; echo \"no pull requests found for branch \\\"$3\\\"\" >&2; exit 1;;\nesac\nexit 2\n")
	files := map[string]string{"ssh": ssh.String(), "gh": gh.String()}
	// Each fake keeps its own name as the pane's process, so the daemon
	// sees the harness still running: the script is not replaced by sleep.
	for _, a := range []string{"claude", "codex", "gemini", "opencode", "goose"} {
		files[a] = "#!/bin/sh\nif [ -n \"$1\" ]; then cat \"$1\"; else echo " + a + " ready; fi\nwhile :; do sleep 900; done\n"
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(fakes, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	writeConfig(t, base, "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n\n"+
		"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n\n"+
		"[hosts.gpu]\naddr = \"someone@gpubox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	env := []string{"TUIOS_SSH=" + filepath.Join(fakes, "ssh")}
	// bin is the fake of a harness, by its id.
	bin := func(harness string) string {
		return filepath.Join(fakes, strings.TrimSuffix(harness, "-code"))
	}

	// Each machine's daemon reports a known load, the design frame's: a
	// test machine's own load would read as an alarm or as nothing.
	loads := map[string]float64{base: 0.21, build: 0.38, gpu: 0.72}
	cli := func(b string, args ...string) {
		t.Helper()
		load := "TUIOS_LOAD1=" + strconv.FormatFloat(loads[b]*float64(runtime.NumCPU()), 'f', 2, 64)
		if out, err := tuiosCLIInDir(t, b, workDirIn(t, b), append(env, load), args...); err != nil {
			t.Fatalf("tuios %v: %v\n%s", args, err, out)
		}
	}
	report := func(b, session, name, harness, state, message string, meta []string) {
		t.Helper()
		if state != "" {
			args := []string{"set-agent-state", state, "-s", session, "-w", name, "--harness", harness}
			if message != "" {
				args = append(args, "-m", message)
			}
			cli(b, args...)
		}
		if len(meta) > 0 {
			cli(b, append([]string{"set-agent-meta", "-s", session, "-w", name}, meta...)...)
		}
	}

	// The machines over the links.
	for _, far := range []string{build, gpu} {
		cli(far, "new", "work", "--detach")
	}
	for _, a := range frameAFar {
		far := build
		if a.host == "gpu" {
			far = gpu
		}
		cli(far, "new-window", a.name, "-s", "work", "--", bin(a.harness))
		report(far, "work", a.name, a.harness, a.state, a.message, a.meta)
	}

	// This machine: the worktrees, each with its agent's pane.
	cli(base, "new", "misc", "--detach")
	for _, w := range frameAWorktrees {
		if out, err := tuiosCLIInDir(t, base, repo, env, "worktree", "new", w.branch, "--name", w.session, "--detach"); err != nil {
			t.Fatalf("worktree new %s: %v\n%s", w.branch, err, out)
		}
		args := []string{"new-window", w.name, "-s", w.session, "--", bin(w.harness)}
		if w.name == "api-auth" {
			screen := filepath.Join(fakes, "auth-screen.txt")
			if err := os.WriteFile(screen, []byte(claudeAuthPane), 0o644); err != nil {
				t.Fatal(err)
			}
			args = append(args, screen)
		}
		cli(base, args...)
		report(base, w.session, w.name, w.harness, w.state, w.message, w.meta)
	}
	for _, s := range []string{"docs", "sixel"} {
		cli(base, "ship", "status", "-s", s, "--refresh")
	}
	cli(base, "new-window", "scratch", "-s", "misc", "--", bin("claude-code"))
	report(base, "misc", "scratch", "claude-code", "idle", "", nil)

	// The fan of three. Each attempt reports idle, which is what fan waits
	// for before it types the task, then goes to work.
	if out, err := tuiosCLIInDir(t, base, repo, env, "fan", "3", "--agent", "gemini,claude,codex", "--repo", repo, "--name", "cache",
		"Make the LRU cache safe for concurrent reads"); err != nil {
		t.Fatalf("fan: %v\n%s", err, out)
	}
	for _, a := range frameAFan {
		cli(base, "set-window", "-s", a.session, "-w", "0", "--name", a.name)
		report(base, a.session, a.name, a.harness, "idle", "", nil)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		rows := worktreeRows(t, base, "--group", "cache")
		sent := 0
		for _, r := range rows {
			if r["prompt_status"] == "sent" || r["prompt_status"] == "stalled" {
				sent++
			}
		}
		if sent == len(frameAFan) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fan never typed its task: %v", rows)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, a := range frameAFan {
		report(base, a.session, a.name, a.harness, "working", a.message, []string{"diff=" + a.diff})
	}

	// migrate-cfg runs two subagents.
	hook := func(session, window string, payload string) {
		t.Helper()
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", window)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("agent-hook %s: %v\n%s", payload, err, out)
		}
	}
	for _, sa := range []struct{ id, call, typ, desc, tool, input string }{
		{"a1e0c9d2b7f4a861", "toolu_01Ex", "Explore", "Read the config docs", "Read", `{"file_path":"docs/CONFIGURATION.md"}`},
		{"a2e3f1b90c2d48a5", "toolu_01Pl", "Plan", "Map the options", "Grep", `{"pattern":"mapping 41 options"}`},
	} {
		hook("migrate", "migrate-cfg", cc("", `"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"`+sa.desc+`","prompt":"Do it.","subagent_type":"`+sa.typ+`","run_in_background":false},"tool_use_id":"`+sa.call+`"`))
		hook("migrate", "migrate-cfg", subagentStartOf(sa.id, sa.typ))
		hook("migrate", "migrate-cfg", subagentTool(sa.id, sa.call+"t", sa.tool, sa.input, false))
	}

	// api-auth: its transcript, then the approval it waits on.
	fixture, err := os.ReadFile(filepath.Join("testdata", "transcript", "cockpit-frame-a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(atProjects(base), "src-api", "9a1f0c2e.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	hook("auth", "api-auth", string(hookPayload(path, "9a1f0c2e")))
	ask := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "auth", "--window", "api-auth")
	ask.Dir = workDirIn(t, base)
	ask.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		ask.Env = append(ask.Env, key+"="+xdgDir(base, key))
	}
	ask.Stdin = strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"9a1f0c2e","tool_name":"Bash","tool_input":{"command":"go test ./internal/auth/... -run Token -count=1"},` +
		`"permission_suggestions":[{"type":"addRules","behavior":"allow","destination":"localSettings","rules":[{"toolName":"Bash","ruleContent":"go test:*"}]}]}`)
	var answer bytes.Buffer
	ask.Stdout = &answer
	if err := ask.Start(); err != nil {
		t.Fatalf("start the approval hook: %v", err)
	}
	t.Cleanup(func() { _ = ask.Process.Kill(); _ = ask.Wait() })
	waitForAttention(t, base, env, "go test ./internal/auth/...")
	report(base, "auth", "api-auth", "claude-code", "", "", frameAWorktrees[0].meta)

	term := startIn(t, base, startOpts{args: []string{"attach", "auth"}, cols: cols, rows: rows, shippedLooks: true,
		// HOME at the test's folder, so a frame names its folders from ~
		// and never the machine's own.
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "HOME=" + base}, env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Do you want to proceed?") }, bootTimeout); err != nil {
		t.Fatalf("client never showed api-auth's pane: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the cockpit: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Needs you", "Ready for review", "ws-reconnect", "bench-idle", "release-not")
	}, 3*uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the cockpit never showed the agents of all three machines: %v\n%s", err, term.Snapshot())
	}
	return term
}

// TestCockpitFrameA opens the cockpit on the scenario of frame (a). Every
// agent of the three machines has a row of its own, the fan's attempts sit
// under one row for the fan, a branch ready for review shows its pull
// request and its checks, and the machines are listed at the foot of the
// fleet. The machine scope narrows the fleet to one machine.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: on a build where a
// host's listing carries no agent panes the remote rows check fails; with
// the fan rows not gathered the fan check fails; with no load in the hello
// the load check fails.
func TestCockpitFrameA(t *testing.T) {
	term := startFrameA(t, 200, 55)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Running the auth tests.", "#884", "#881", "1 check", "fan of 3", "Machines")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the fleet lacks the pull requests, the fan or the machines: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	s := term.Screen()
	// The fan's attempts are under its row, before the next agent.
	fan := fleetRowWith(s, "fan of 3")
	for i, name := range []string{"cache-1", "cache-2", "cache-3"} {
		if r := fleetRowWith(s, name); r != fan+2+i {
			t.Fatalf("ASSERTION: %s is on row %d, not under the fan's row %d\n%s", name, r, fan, term.Snapshot())
		}
	}
	// The machines: this one and the two hosts, each with its agents.
	for _, want := range []string{"build     2 agents", "gpu       1 agent"} {
		if fleetRowWith(s, want) < 0 {
			t.Fatalf("ASSERTION: the machines block lacks %q\n%s", want, term.Snapshot())
		}
	}
	// Each host's load and round trip ride on its link's hello.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := fleetRowWith(s, "build     2 agents")
		if r < 0 {
			return false
		}
		line := []rune(s.Line(r))
		return len(line) > 56 && strings.Contains(string(line[:56]), "%") && strings.Contains(string(line[:56]), " ms")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the machines block does not show build's load and round trip: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "frame-a-200x55")

	// The scope narrows to build, from its row in the machines block, and
	// back to every machine from the header.
	row, col := findText(t, term, "build     2 agents")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "ws-reconnect") >= 0 && fleetRowWith(s, "docs-cli") < 0 && strings.Contains(s.Line(rowWith(s, "Agents")), "build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the build row did not narrow the fleet to build: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "frame-a-200x55-build")
	row, col = findText(t, term, "build "+"\uf078")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "docs-cli") < 0 && fleetRowWith(s, "ws-reconnect") < 0 && fleetRowWith(s, "release-not") < 0 && fleetRowWith(s, "bench-idle") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the header did not step the scope on to gpu: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after frame a")
}
