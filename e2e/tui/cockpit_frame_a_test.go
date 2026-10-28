package tuie2e

import (
	"bytes"
	"encoding/json"
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

// frameAJudge is the judge's part of the fake agents: ready at once, and on
// its task, the verdict of frame (g) through the real CLI.
const frameAJudge = `  "$TUIOS_E2E_TUIOS" set-agent-state idle -w "$TUIOS_PANE_ID"
  while IFS= read -r line; do
    case "$line" in *"You judge"*)
      "$TUIOS_E2E_TUIOS" set-agent-state working -w "$TUIOS_PANE_ID"
      "$TUIOS_E2E_TUIOS" fan verdict repo-cache --pick repo-cache-2 \
        --summary "Keep B. It is the smallest change, and the only attempt that passes the check." \
        --reason "A takes a read lock on a plain Mutex, so the check fails." \
        --reason "C adds a shard type the task did not ask for, and its cache keeps the Mutex." \
        --reason "Take TestEvictUnderLoad from A. B has no test for eviction while Get runs." \
        --note "repo-cache=Takes RLock on a Mutex." --note "repo-cache=Adds TestEvictUnderLoad." \
        --note "repo-cache-2=Smallest change. Public API unchanged." --note "repo-cache-2=No test for eviction during Get." \
        --note "repo-cache-3=Adds a shard type." --note "repo-cache-3=Leaves the cache on a Mutex." >> "$TUIOS_E2E_JUDGE_LOG" 2>&1
      "$TUIOS_E2E_TUIOS" set-agent-state done -w "$TUIOS_PANE_ID"
      ;;
    esac
  done`

// frameALRU is the cache the fan's attempts change.
const frameALRU = `package cache

import "sync"

// Cache is a least recently used cache.
type Cache struct {
	mu    sync.Mutex
	items map[string]string
}

// Get returns the value of key.
func (c *Cache) Get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.items[key]
}
`

// claudeAuthPane is what api-auth's pane prints: Claude Code at its
// permission prompt, as the live box should show it.
const claudeAuthPane = "\033[32m●\033[0m \033[1mUpdate(internal/auth/token.go)\033[0m\n  \033[2m└  Updated with 12 additions and 3\n     removals\033[0m\n\n\033[33m●\033[0m \033[1mBash(go test ./internal/auth/...\n       -run Token -count=1)\033[0m\n\n \033[33;1mBash command\033[0m\n Do you want to proceed?\n \033[36m❯ 1. Yes\033[0m\n   2. Yes, and do not ask again for\n      go test commands\n   3. No, and tell Claude what to do\n"

// frameARig is the scenario's machine here: its isolation root and its
// repository.
type frameARig struct{ base, repo string }

// startFrameA builds the scenario and opens the cockpit at cols x rows. It
// returns the client.
func startFrameA(t *testing.T, cols, rows int) (*tuitest.Terminal, frameARig) {
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
	// One started in the repository's main checkout is the judge
	// judge-fan starts: it says it is ready, and on its task it reports
	// the verdict of DESIGN.md frame (g).
	// Each turns bracketed paste on first, as the real ones do when their
	// input box is live: a first prompt waits for it.
	for _, a := range []string{"claude", "codex", "gemini", "opencode", "goose"} {
		files[a] = "#!/bin/sh\nprintf '\\033[?2004h'\nif [ \"$PWD\" = '" + repo + "' ]; then\n" + frameAJudge + "\nfi\n" +
			"if [ -n \"$1\" ]; then cat \"$1\"; else echo " + a + " ready; fi\nwhile :; do sleep 900; done\n"
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(fakes, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUIOS_E2E_TUIOS", tuiosBin)
	t.Setenv("TUIOS_E2E_JUDGE_LOG", filepath.Join(fakes, "judge.log"))
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

	// The fan of three, on a cache the base already has.
	if err := os.MkdirAll(filepath.Join(repo, "internal", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "cache", "lru.go"), []byte(frameALRU), 0o644); err != nil {
		t.Fatal(err)
	}
	// Git ignores .env, which a keep deletes with a worktree.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "cache")
	// Each attempt reports idle, which is what fan waits
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
	return term, frameARig{base: base, repo: repo}
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
	term, _ := startFrameA(t, 200, 55)
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

// frameGDiffs are what each attempt of the fan wrote: A changes the most,
// B the least, C adds a file of its own.
var frameGDiffs = map[string]map[string]string{
	"repo-cache": {
		"internal/cache/lru.go":      strings.Replace(frameALRU, "\tc.mu.Lock()\n\tdefer c.mu.Unlock()", "\tc.mu.RLock()\n\tdefer c.mu.RUnlock()\n\tc.hits.Add(1)", 1),
		"internal/cache/lru_test.go": "package cache\n\nimport \"testing\"\n\nfunc TestEvictUnderLoad(t *testing.T) {}\n",
		"internal/cache/stats.go":    "package cache\n\n// Stats reads the hits.\nfunc (c *Cache) Stats() int { return 0 }\n",
	},
	"repo-cache-2": {
		"internal/cache/lru.go": strings.Replace(frameALRU, "\tmu    sync.Mutex", "\tmu    sync.RWMutex", 1),
	},
	"repo-cache-3": {
		".env":                    "SECRET=1\n",
		"internal/cache/shard.go": "package cache\n\nimport \"sync\"\n\ntype shard struct {\n\tmu sync.RWMutex\n\titems map[string]string\n}\n",
		"internal/cache/lru.go":   strings.Replace(frameALRU, "// Get returns the value of key.", "// Get returns the value of key, from its shard.", 1),
	},
}

// TestCockpitFanCompare is frame (g): c on an attempt compares the fan's
// three attempts side by side with their diffs, j starts the judge, whose
// verdict picks B, and k twice keeps B and removes A and C, their branches
// kept.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the judge's
// reach to the attempts cut, the verdict never shows; with keep-fan sent
// without stash, A's and C's changes keep their worktrees and the removal
// check fails.
func TestCockpitFanCompare(t *testing.T) {
	term, rig := startFrameA(t, 200, 55)
	rows := worktreeRows(t, rig.base, "--group", "cache")
	for _, r := range rows {
		session, _ := r["session"].(string)
		path, _ := r["path"].(string)
		for name, body := range frameGDiffs[session] {
			full := filepath.Join(path, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, a := range frameAFan {
		if out, err := tuiosCLI(t, rig.base, "set-agent-state", "done", "-s", a.session, "-w", a.name, "--harness", a.harness); err != nil {
			t.Fatalf("report %s done: %v\n%s", a.name, err, out)
		}
	}
	// One check, the person's: does the cache take a RWMutex? A and C fail,
	// B passes, and the verdict says so.
	out, _ := tuiosCLI(t, rig.base, "fan", "verify", "repo-cache", "--", "grep -q sync.RWMutex internal/cache/lru.go")
	if !strings.Contains(out, "1 of 3 passed") {
		t.Fatalf("fan verify did not pass B alone:\n%s", out)
	}
	// The done reports move the fan from Working to Ready for review. The
	// row is clicked where it ends up: on a slow runner the click once
	// landed on the row the fan had left.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ready, fan, working := fleetRowWith(s, "Ready for review"), fleetRowWith(s, "fan of 3"), fleetRowWith(s, "Working")
		return ready >= 0 && fan > ready && (working < 0 || fan < working) && fleetRowWith(s, "cache-2") == fan+3
	}, uiTimeout); err != nil {
		t.Fatalf("the fan never moved to Ready for review: %v\n%s", err, term.Snapshot())
	}
	row, col := findText(t, term, "cache-2")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "c compare") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: an attempt's row does not offer c to compare: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("c"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return containsAll(text, "fan of 3", "Ask a judge", "cache-2", "cache-3", "in 3 files", "First lines", "sync.RWMutex",
			"internal/cache/lru.go", "failed", "passed")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the comparison does not show the three attempts with their diffs: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "frame-g-before-judge")

	// enter goes to the focused attempt's pane, B's. B reported done, so its
	// row is its finished item, whose row key is the item's and not the
	// agent's: enter once found no row and said the pane was not in the list.
	if err := term.SendKeys("\r"); err != nil {
		t.Fatal(err)
	}
	waitClientOn(t, rig.base, term, "repo-cache-2")
	if text := term.Screen().Text(); strings.Contains(text, "is not in the list") {
		t.Fatalf("ASSERTION: enter in the comparison did not find B's row:\n%s", term.Snapshot())
	}
	// Back to the comparison, from B's row.
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Ready for review", "fan of 3") && fleetRowWith(s, "cache-2") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("the cockpit did not open again: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	row, col = findText(t, term, "cache-2")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "c compare") }, uiTimeout); err != nil {
		t.Fatalf("B's row does not offer c again: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("c"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "fan of 3", "Ask a judge", "cache-2", "cache-3", "First lines")
	}, uiTimeout); err != nil {
		t.Fatalf("the comparison did not open again: %v\n%s", err, term.Snapshot())
	}

	// j: the judge reads the attempts and picks B.
	if err := term.SendKeys("j"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Keep B.", "recommended", "Smallest change. Public API unchanged.", "judge says B") && !fleetHasRow(s, "judge")
	}, 3*uiTimeout); err != nil {
		judgeLog, _ := os.ReadFile(os.Getenv("TUIOS_E2E_JUDGE_LOG"))
		ls, _ := tuiosCLI(t, rig.base, "list-agents", "--all-sessions")
		t.Fatalf("ASSERTION: the judge's verdict never showed: %v\njudge log:\n%s\nagents:\n%s\n%s", err, judgeLog, ls, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "frame-g-200x55")

	// k once says what it does, k again keeps B.
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Press k again to keep B", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the first k did not say what a keep does: %v\n%s", err, term.Snapshot())
	}
	// C's .env is ignored by git: the warning names it before the keep.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "These files git ignores are deleted, not stashed:", "C: .env")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the first k did not name the ignored file a keep deletes: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "frame-g-keep")

	// Another ignored file in C after the list was read: the second k reads
	// again, says the list changed, and keeps nothing.
	var cPath string
	for _, r := range rows {
		if r["session"] == "repo-cache-3" {
			cPath, _ = r["path"].(string)
		}
	}
	if err := os.WriteFile(filepath.Join(cPath, "build.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "The attempts changed since the list was read", "C: .env, build.log")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a second k after a new ignored file did not warn again: %v\n%s", err, term.Snapshot())
	}
	if n := len(worktreeRows(t, rig.base, "--group", "cache")); n != 3 {
		t.Fatalf("ASSERTION: a keep went ahead over a list that grew: %d attempts left", n)
	}

	// The daemon holds a keep to the names shown, not their count: one
	// ignored file swapped for another, with the hash read before, is
	// refused and keeps nothing.
	shown := fanIgnoredHashes(t, rig.base, "repo-cache-2")
	if err := os.Rename(filepath.Join(cPath, "build.log"), filepath.Join(cPath, "other.log")); err != nil {
		t.Fatal(err)
	}
	keep, _ := json.Marshal(map[string]any{
		"session": "repo-cache-2", "stash": true,
		"siblings": []string{"repo-cache", "repo-cache-3"}, "ignored": shown,
	})
	if out, err := tuiosCLI(t, rig.base, "call", "keep-fan", string(keep)); err == nil || !strings.Contains(out, "are not the ones the caller showed") {
		t.Fatalf("ASSERTION: keep-fan went ahead over an ignored file swapped for another: %v: %s", err, out)
	}
	if n := len(worktreeRows(t, rig.base, "--group", "cache")); n != 3 {
		t.Fatalf("ASSERTION: a keep went ahead over a swapped ignored file: %d attempts left", n)
	}

	// A list git cannot read keeps nothing, and says why.
	gitFile := filepath.Join(cPath, ".git")
	if err := os.Rename(gitFile, gitFile+".off"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("tuios could not list the files git ignores, so nothing is kept.", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a list git could not read did not stop the keep: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if n := len(worktreeRows(t, rig.base, "--group", "cache")); n != 3 {
		t.Fatalf("ASSERTION: a keep went ahead with a list git could not read: %d attempts left", n)
	}
	if err := os.Rename(gitFile+".off", gitFile); err != nil {
		t.Fatal(err)
	}
	// k reads the list again; then k keeps.
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Press k again to keep B") && !strings.Contains(s.Text(), "could not list")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: k did not read the list again: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("k"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		left := worktreeRows(t, rig.base, "--group", "cache")
		if len(left) == 1 && left[0]["session"] == "repo-cache-2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: k k did not keep B alone: %v\n%s", left, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	repo := rig.repo
	branches := testutil.Git(t, repo, "branch", "--list", "cache*")
	if !containsAll(branches, "cache", "cache-2", "cache-3") {
		t.Fatalf("ASSERTION: the removed attempts lost their branches:\n%s", branches)
	}
	if stash := testutil.Git(t, repo, "stash", "list"); strings.Count(stash, "\n")+1 < 2 {
		t.Fatalf("ASSERTION: the changes of A and C were not stashed:\n%s", stash)
	}
	alive(t, term, "after the fan comparison")
}

// waitClientOn waits until the daemon lists term's client as attached to
// session.
func waitClientOn(t *testing.T, base string, term *tuitest.Terminal, session string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-clients", "--json")
		var rows []struct {
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}
		if json.Unmarshal([]byte(out), &rows) == nil {
			for _, r := range rows {
				if r.PID == term.Pid() && r.Session == session {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the client never went to %s: %s\n%s", session, out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fleetHasRow reports whether a row of the fleet column is named name: the
// name is the first word after the row's marks.
func fleetHasRow(s tuitest.Screen, name string) bool {
	_, rows := s.Size()
	for r := range rows {
		line := []rune(s.Line(r))
		fleet := strings.Fields(string(line[:min(len(line), 56)]))
		for i, f := range fleet {
			if i < 3 && f == name {
				return true
			}
		}
	}
	return false
}

// fanIgnoredHashes reads, through compare-fan, the ignored_hash of every
// attempt of session's fan but session itself.
func fanIgnoredHashes(t *testing.T, base, session string) map[string]string {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"session": session, "detail": true})
	out, err := tuiosCLI(t, base, "call", "compare-fan", string(params))
	if err != nil {
		t.Fatalf("compare-fan: %v: %s", err, out)
	}
	var res struct {
		Rows []struct {
			Session     string `json:"session"`
			IgnoredHash string `json:"ignored_hash"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("compare-fan output: %v: %s", err, out)
	}
	hashes := map[string]string{}
	for _, r := range res.Rows {
		if r.Session != session {
			if r.IgnoredHash == "" {
				t.Fatalf("compare-fan gave %s no ignored_hash: %s", r.Session, out)
			}
			hashes[r.Session] = r.IgnoredHash
		}
	}
	return hashes
}
