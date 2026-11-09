package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// "Any machine": tuios picks the machine a new agent starts on, this one or a
// linked one, and says why in one line. home is this machine and build the
// second daemon, reached over the real subprocess transport through the ssh
// stand-in, so every question placement asks build crosses the link.
//
// The two machines run on one box, so they have the same cores. Their load
// is set with TUIOS_LOAD1: home is loaded ten times over and build is idle,
// so build has the room. The agent is a stand-in for Claude Code that stays
// working once it has a prompt, so build's count of working agents holds
// still while the test reads it.
//
// What would pass a weaker test and fail these: a pick by link order or by
// name rather than by load; a machine without the agent that is still
// picked; a pick that did not count against the next one, so two quick
// placements both land on one full machine; a cap read from the wrong
// machine; a dead link that still wins; a card started on a machine that has
// no way to claim it; and a card placed on build that is never held there.

// poolAgentBin writes a Claude Code stand-in that reports working on its
// first prompt and stays working, and a codex stand-in. It returns the
// directory of each: claude's goes on every PATH, codex's only where a test
// puts it.
func poolAgentBin(t *testing.T) (claudeDir, codexDir string) {
	t.Helper()
	claudeDir, codexDir = filepath.Join(t.TempDir(), "bin"), filepath.Join(t.TempDir(), "codexbin")
	for _, d := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hook := func(event string) string {
		return "printf '%s' '{\"hook_event_name\":\"" + event + "\",\"session_id\":\"fake\"}' | '" + tuiosBin + "' agent-hook claude-code >/dev/null 2>&1; "
	}
	claude := "#!/bin/sh\nif [ \"$1\" = auth ]; then echo '{\"loggedIn\": true}'; exit 0; fi\n" +
		"printf '\\033]0;\\342\\234\\263 Claude Code\\007'\necho fake claude starting\n" +
		hook("SessionStart") + "\nsleep 1\nprintf '\\033[?2004h'\necho fake claude ready\n" +
		"IFS= read -r line; printf '\\033]0;\\342\\240\\220 Claude Code\\007'; " + hook("UserPromptSubmit") + "echo \"GOT: $line\"\n" +
		"while :; do sleep 60; done\n"
	if err := os.WriteFile(filepath.Join(claudeDir, "claude"), []byte(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "codex"), []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return claudeDir, codexDir
}

// poolPair is the two machines of a placement test.
type poolPair struct {
	base, remote string
	// here is home's checkout and farCheckout build's, of one repository
	// cloned under two spellings of its URL.
	here, farCheckout string
	// env is what every CLI call on home needs.
	env []string
}

// startPool starts build and home. build has [tasks] machine_cap = buildCap
// and no load; home is loaded ten times over. With back, build links to
// home too.
func startPool(t *testing.T, buildCap int, back bool) poolPair {
	t.Helper()
	return startPoolOn(t, buildCap, back, tuiosBin)
}

// startPoolOn is startPool with build running buildBin, its daemon and the
// far end of home's link to it, as a machine with another tuios installed.
func startPoolOn(t *testing.T, buildCap int, back bool, buildBin string) poolPair {
	t.Helper()
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	claudeDir, _ := poolAgentBin(t)
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	remote := remoteMachine(t)
	p := poolPair{base: base, remote: remote}

	reposRoot := filepath.Join(remote, "src")
	p.farCheckout = filepath.Join(reposRoot, "acme", "api")
	cloneWithOrigin(t, origin, p.farCheckout, "https://example.com/acme/api.git")
	p.here = filepath.Join(base, "work", "api")
	cloneWithOrigin(t, origin, p.here, "git@example.com:acme/api")

	writeCfg := func(root, body string) {
		dir := filepath.Join(xdgDir(root, "XDG_CONFIG_HOME"), "tuios")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	buildCfg := "[tasks]\nmachine_cap = " + strconv.Itoa(buildCap) + "\n"
	buildEnv := []string{"TUIOS_LOAD1=0"}
	if back {
		ssh := writeFakeSSHTo(t, remote, base)
		buildCfg = "[hosts.home]\naddr = \"someone@homebox\"\ncommand = \"" + tuiosBin + "\"\nconnect_timeout = 5\n\n" + buildCfg
		buildEnv = append(buildEnv, "TUIOS_SSH="+ssh)
	}
	writeCfg(remote, buildCfg)
	ssh := writeFakeSSHTo(t, base, remote)
	writeCfg(base, "[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+buildBin+"\"\nconnect_timeout = 5\nrepos_root = \""+reposRoot+"\"\n")
	p.env = []string{"TUIOS_SSH=" + ssh}

	killDaemon(t, base)
	homeLoad := strconv.Itoa(10 * runtime.NumCPU())
	if out, err := tuiosCLIEnv(t, base, append(p.env, "TUIOS_LOAD1="+homeLoad), "new", "home", "--detach", "--cwd", p.here); err != nil {
		t.Fatalf("start home's daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLIBinEnv(t, buildBin, remote, buildEnv, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	// "up" alone also matches a host with no daemon yet: wait until build
	// answers a placement question.
	deadline := time.Now().Add(45 * time.Second)
	for {
		out, _ := tuiosCLIEnv(t, base, p.env, "call", "place-agent", `{"agent":"claude","reserve":false}`)
		if strings.Contains(out, `"host":"build"`) && !strings.Contains(out, "link") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("build never answered a placement: %s\n%s", out, mustCLI(t, base, "hosts"))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if back {
		deadline := time.Now().Add(45 * time.Second)
		for !strings.Contains(mustCLI(t, remote, "hosts"), "up") {
			if time.Now().After(deadline) {
				t.Fatalf("build's link to home never came up: %s", mustCLI(t, remote, "hosts"))
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	return p
}

// placeJSON is place-agent's answer as the tests read it.
type placeJSON struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Why      string `json:"why"`
	TaskHost string `json:"task_host"`
	Machines []struct {
		Host string `json:"host"`
		OK   bool   `json:"ok"`
		Why  string `json:"why"`
	} `json:"machines"`
}

// why is the line placement gave machine host.
func (p placeJSON) why(host string) string {
	for _, m := range p.Machines {
		if m.Host == host {
			return m.Why
		}
	}
	return ""
}

// place calls place-agent on home through tuios call, from home's
// checkout, as start-agent -s any: asks it. params are added to the agent,
// the repository and the checkout here.
func (pp poolPair) place(t *testing.T, params map[string]any) placeJSON {
	t.Helper()
	all := map[string]any{"repo_url": "git@example.com:acme/api", "local_origin": "git@example.com:acme/api"}
	for k, v := range params {
		all[k] = v
	}
	raw, _ := json.Marshal(all)
	out, err := tuiosCLIEnv(t, pp.base, pp.env, "call", "place-agent", string(raw))
	if err != nil {
		t.Fatalf("place-agent %s: %v\n%s", raw, err, out)
	}
	var p placeJSON
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("place-agent printed no JSON: %v\n%s", err, out)
	}
	return p
}

func TestAnyMachinePlacesTheAgentWhereThereIsRoom(t *testing.T) {
	pp := startPool(t, 2, false)
	_, codexDir := poolAgentBin(t)
	me, _ := os.Hostname()
	if i := strings.IndexByte(me, '.'); i > 0 {
		me = me[:i]
	}
	cores := strconv.Itoa(runtime.NumCPU())
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "pool-place", report.String()) }()
	var text string
	var err error

	// 1. build is idle and has the checkout; home is loaded.
	p := pp.place(t, map[string]any{"agent": "claude", "reserve": false})
	note("place-agent claude: %s / %s / home: %s", p.Name, p.Why, p.why(""))
	if want := "build: " + cores + " cores, 0 working, the repo is there."; p.Host != "build" || p.Why != want {
		t.Errorf("ASSERTION: the pick is not build by its room, want %q: %+v", want, p)
	}
	if !strings.HasPrefix(p.why(""), me+": "+cores+" cores, 0 working, load ") {
		t.Errorf("ASSERTION: home's line does not give its load: %q", p.why(""))
	}

	// 2. codex is only on this machine: build is left out, and says why.
	p = pp.place(t, map[string]any{"agent": "codex", "reserve": false, "env": map[string]string{"PATH": codexDir + string(os.PathListSeparator) + os.Getenv("PATH")}})
	note("place-agent codex: %+v", p)
	if p.Host != "" {
		t.Errorf("ASSERTION: codex was placed on %q, where it is not installed", p.Host)
	}
	if w := p.why("build"); w != "build: codex is not installed there." {
		t.Errorf("ASSERTION: build's reason for codex = %q", w)
	}

	// 3. An agent with machine any starts on build, in its checkout.
	text, err = tuiosCLIInDir(t, pp.base, pp.here, pp.env, "start-agent", "-s", "any:work", "claude", "--prompt", "Add a retry.")
	note("$ tuios start-agent -s any:work claude --prompt 'Add a retry.'\n%s", text)
	if err != nil {
		t.Fatalf("ASSERTION: start-agent -s any:work failed: %v\n%s", err, text)
	}
	if !strings.Contains(text, "Placed on build. build: "+cores+" cores, 0 working, the repo is there.") || !strings.Contains(text, "on build") {
		t.Errorf("ASSERTION: start-agent did not say it placed the agent on build:\n%s", text)
	}
	if ls := remoteSessionsListed(t, pp.remote); !strings.Contains(ls, "work") {
		t.Fatalf("ASSERTION: build has no session work:\n%s", ls)
	}
	if ls, _ := tuiosCLI(t, pp.base, "ls"); strings.Contains(ls, "work") {
		t.Errorf("ASSERTION: the agent started on this machine:\n%s", ls)
	}
	// build counts the agent as working.
	deadline := time.Now().Add(30 * time.Second)
	for {
		p = pp.place(t, map[string]any{"agent": "claude", "reserve": false})
		if strings.Contains(p.why("build"), "1 working") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: build never counted its agent: %+v", p)
		}
		time.Sleep(500 * time.Millisecond)
	}
	note("after the start: %s", p.why("build"))

	// 4. A card on this machine: build has no link back here, so it cannot
	// claim it, and the card's agent stays here.
	mustCLI(t, pp.base, "task", "add", "-s", "home", "--project", "https://example.com/acme/api.git", "--machine", "any", "Fix the retry")
	p = pp.place(t, map[string]any{"agent": "claude", "task": "t1", "task_session": "home", "reserve": false})
	note("place-agent --task t1: %s / build: %s", p.Name, p.why("build"))
	if p.Host != "" || p.why("build") != "build: it has no link back to this machine, so its agent cannot take the card." {
		t.Errorf("ASSERTION: a card was placed on a machine that cannot claim it: host %q, build %q", p.Host, p.why("build"))
	}

	// 5. Two placements in a row: the first takes build's last place under
	// its cap of 2, and the second sees it taken.
	first := pp.place(t, map[string]any{"agent": "claude"})
	second := pp.place(t, map[string]any{"agent": "claude"})
	note("first: %s / %s\nsecond: %s / %s", first.Name, first.Why, second.Name, second.why("build"))
	if first.Host != "build" {
		t.Errorf("ASSERTION: the first placement went to %q, want build with one place left: %+v", first.Host, first)
	}
	if second.Host != "" || second.why("build") != "build: at its cap of 2 agents." {
		t.Errorf("ASSERTION: the second placement did not see the first one's pick: host %q, build %q", second.Host, second.why("build"))
	}

	// 6. build's daemon stops: its link goes down, and this machine wins.
	killDaemonNow(t, pp.remote)
	deadline = time.Now().Add(45 * time.Second)
	for {
		p = pp.place(t, map[string]any{"agent": "claude", "reserve": false})
		w := p.why("build")
		if p.Host == "" && (strings.Contains(w, "the link is not up") || strings.Contains(w, "the link went down")) {
			break
		}
		if p.Host == "build" || time.Now().After(deadline) {
			t.Fatalf("ASSERTION: a machine whose daemon stopped was not left out: %+v", p)
		}
		time.Sleep(500 * time.Millisecond)
	}
	note("build down: %s / build: %s", p.Name, p.why("build"))
}

func TestAnyMachineTakesACardAcrossTheLink(t *testing.T) {
	pp := startPool(t, 0, true)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "pool-place-card", report.String()) }()

	mustCLI(t, pp.base, "task", "add", "-s", "home", "--project", "https://example.com/acme/api.git", "--machine", "any", "Fix the retry")
	text, err := tuiosCLIInDir(t, pp.base, pp.here, pp.env, "start-agent", "-s", "any:home", "claude", "--task", "t1", "--prompt", "Take task t1.")
	note("$ tuios start-agent -s any:home claude --task t1\n%s", text)
	if err != nil {
		t.Fatalf("ASSERTION: start-agent -s any:home --task t1 failed: %v\n%s", err, text)
	}
	if !strings.Contains(text, "Placed on build.") || !strings.Contains(text, "holds task t1") {
		t.Errorf("ASSERTION: the card's agent was not placed on build holding the card:\n%s", text)
	}
	// The card is held here by a pane on build.
	out := mustCLI(t, pp.base, "task", "get", "t1", "-s", "home", "--json")
	note("home task get t1:\n%s", out)
	var row struct {
		Stage  string `json:"stage"`
		Holder *struct {
			Window string `json:"window"`
			Host   string `json:"host"`
		} `json:"holder"`
	}
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("task get --json: %v\n%s", err, out)
	}
	// The holder's host is the name build gives itself on the link, its
	// host name, which is this box's in the test.
	if row.Holder == nil || row.Holder.Host == "" || row.Stage != "doing" {
		t.Fatalf("ASSERTION: the card is not held by a pane on build: %s", out)
	}
	// That pane is build's, in its session home.
	agents := mustCLI(t, pp.remote, "list-agents", "-s", "home", "--json")
	note("build list-agents -s home:\n%s", agents)
	if !strings.Contains(agents, row.Holder.Window) {
		t.Errorf("ASSERTION: build has no pane %s for the card:\n%s", row.Holder.Window, agents)
	}
}

// TestAnyMachineInTheSheets drives both sheets the way a person does, in a
// client attached on home. The New agent sheet shows "Any machine" first,
// with the daemon's pick and why, and keeps this machine picked: a new agent
// moves only when asked. Picked by hand, Start sends the agent to build. The
// capture sheet's Machine pill opens on This machine; turned to Any machine,
// ctrl+enter files the card, starts its agent on build, and the card is held
// here by that pane.
func TestAnyMachineInTheSheets(t *testing.T) {
	pp := startPool(t, 0, true)
	useShippedLooks(pp.base)
	cores := strconv.Itoa(runtime.NumCPU())
	term := startIn(t, pp.base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true,
		env: append(pp.env, "TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0")})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	// The New agent sheet.
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "Agents", "New agent")
	sendKeys(t, term, "n")
	waitText(t, term, "the sheet with Any machine and its pick", "Any machine", "build: "+cores+" cores, 0 working, the repo is there.")
	sendKeys(t, term, "Add a retry to the client.")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "sheet-default-this-machine-160x45")
	// shift+tab from the task: Start, Cancel, Attempts, then Machine. The
	// rows are Any machine, build, then this machine, which is picked.
	sendKeys(t, term, tuitest.Key("\x1b[Z"), tuitest.Key("\x1b[Z"), tuitest.Key("\x1b[Z"), tuitest.Key("\x1b[Z"), tuitest.Up, tuitest.Up)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "sheet-any-machine-160x45")
	sendKeys(t, term, tuitest.Key("\x1b[13;5u"))
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(remoteSessionsListed(t, pp.remote), "add-retry-client") {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: Start with Any machine did not start the agent on build: %s\n%s", remoteSessionsListed(t, pp.remote), term.Snapshot())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if ls, _ := tuiosCLI(t, pp.base, "ls"); strings.Contains(ls, "add-retry-client") {
		t.Errorf("ASSERTION: the agent started on this machine too:\n%s", ls)
	}
	sendKeys(t, term, tuitest.Esc)

	// The capture sheet.
	sendKeys(t, term, tuitest.Ctrl('b'), "+")
	waitText(t, term, "the capture sheet on This machine", "New task", "Machine", "‹ This machine ›")
	sendKeys(t, term, "Fix the flaky retry test", tuitest.Tab, tuitest.Tab, tuitest.Tab, tuitest.Tab, tuitest.Right)
	waitText(t, term, "the Machine pill on Any machine", "‹ Any machine ›")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "capture-any-machine-160x45")
	sendKeys(t, term, tuitest.Key("\x1b[13;5u"))
	waitText(t, term, "the toast for the card's agent on build", "works on it on build.")
	saveCockpitFrames(t, term, "capture-started-on-build-160x45")
	out := mustCLI(t, pp.base, "task", "ls", "-s", "home", "--json")
	var rows []struct {
		Title   string `json:"title"`
		Machine string `json:"machine"`
		Holder  *struct {
			Host string `json:"host"`
		} `json:"holder"`
	}
	var wrapped struct {
		Tasks json.RawMessage `json:"tasks"`
	}
	raw := []byte(out)
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Tasks) > 0 {
		raw = wrapped.Tasks
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("task ls --json: %v\n%s", err, out)
	}
	found := false
	for _, r := range rows {
		if r.Title == "Fix the flaky retry test" {
			found = true
			if r.Machine != "any" || r.Holder == nil || r.Holder.Host == "" {
				t.Errorf("ASSERTION: the card is not for any machine and held by a pane on build: %s", out)
			}
		}
	}
	if !found {
		t.Fatalf("ASSERTION: the capture sheet filed no card:\n%s", out)
	}
}
