package tuie2e

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const crushSession = "e2e-home"

func crushClient(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", crushSession, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", crushSession}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	return term, base
}

func crushPanes(t *testing.T, base string, names ...string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", crushSession, "--no-focus"); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		ids[name] = windowID(t, base, crushSession, name)
	}
	return ids
}

func typeIn(t *testing.T, base, window, text string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", crushSession, "-w", window, text+"\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
}

// TestHerdrCrushPanesInParallel runs three stand-in Crushes started from shell
// prompts, side by side, each reporting over herdr's protocol the way Crush
// does with charmbracelet/crush#3541: a permission block, a question block
// and a plain turn, all at once. Each pane follows only its own reports, and
// the rail shows the three together. Then one quits and releases its pane,
// one crashes with no release and its pane clears once the shell is back, and
// a reporter that uses herdr's CLI through HERDR_BIN_PATH is answered too.
//
// Negative control: with the always default put back to agents in
// config.NormalizeHerdrProtocol, the stand-ins print NO-HERDR and the first
// wait fails; with the herdr claim lapse taken out of detectionPass, the
// crashed pane stays working and the crash wait fails.
func TestHerdrCrushPanesInParallel(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	term, base := crushClient(t)
	crush := buildFakeCrush(t)
	log := &stateLog{name: "herdr-crush-parallel"}
	ids := crushPanes(t, base, "crush-a", "crush-b", "crush-c")
	for name := range ids {
		typeIn(t, base, name, crush)
	}
	for name, id := range ids {
		out := waitJoined(t, base, name, "FAKE-CRUSH-READY")
		if !strings.Contains(out, `HERDR_ENV="1" PANE_MATCHES=true SOCKET_SET=true`) {
			t.Fatalf("%s was not told about the herdr socket:\n%s", name, out)
		}
		waitAgentState(t, base, crushSession, id, "idle", "crush", log, name+" first report, idle")
	}

	for name, id := range ids {
		typeIn(t, base, name, "working")
		waitAgentState(t, base, crushSession, id, "working", "crush", log, name+" working")
	}
	typeIn(t, base, "crush-a", "permission")
	typeIn(t, base, "crush-b", "question")
	a := waitAgentState(t, base, crushSession, ids["crush-a"], "needs_input", "crush", log, "crush-a permission")
	b := waitAgentState(t, base, crushSession, ids["crush-b"], "needs_input", "crush", log, "crush-b question")
	if a.Message != "Permission: bash - go test ./..." || b.Message != "Pick a database" {
		t.Fatalf("block messages %q and %q", a.Message, b.Message)
	}
	if st := readAgentState(t, base, crushSession, ids["crush-c"]); st.State != "working" {
		t.Fatalf("crush-c moved to %s on the others' reports", st.State)
	}
	typeIn(t, base, "crush-c", "meta")
	typeIn(t, base, "crush-c", "notify")
	waitAgentMeta(t, base, ids["crush-c"], `"title": "Fix the flaky test"`, `"model": "fake-model"`)
	log.add("%-44s title and model tokens set", "crush-c pane.report_metadata")
	waitJoined(t, base, "crush-c", `REPLY {"id":"crush:notification.show:`)
	time.Sleep(time.Second)
	saveFrame(t, term, "herdr-crush-parallel-blocked")
	waitForAll(t, term, uiTimeout, "the rail rows", "Pick a data", "Permission")

	typeIn(t, base, "crush-a", "working")
	waitAgentState(t, base, crushSession, ids["crush-a"], "working", "crush", log, "crush-a answered")
	typeIn(t, base, "crush-a", "idle")
	waitAgentState(t, base, crushSession, ids["crush-a"], "done", "crush", log, "crush-a turn ended, done")
	typeIn(t, base, "crush-a", "release")
	typeIn(t, base, "crush-a", "quit")
	waitAgentState(t, base, crushSession, ids["crush-a"], "none", "", log, "crush-a quit and released")

	typeIn(t, base, "crush-b", "crash")
	waitAgentState(t, base, crushSession, ids["crush-b"], "none", "", log, "crush-b crashed, cleared at the shell")
	if st := readAgentState(t, base, crushSession, ids["crush-c"]); st.State != "working" {
		t.Fatalf("crush-c is %s after the others left", st.State)
	}
	saveFrame(t, term, "herdr-crush-parallel-after")

	typeIn(t, base, "crush-a", `"$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" --source custom:e2e --agent prime-agent --state working --seq 1 && sleep 60`)
	waitAgentState(t, base, crushSession, ids["crush-a"], "working", "prime-agent", log, "herdr CLI report through HERDR_BIN_PATH")
	if out, err := tuiosCLI(t, base, "send-keys", "-s", crushSession, "-w", ids["crush-a"], "ctrl+c"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	waitAgentState(t, base, crushSession, ids["crush-a"], "none", "", log, "the CLI reporter gone, cleared at the shell")
	log.save(t)
	alive(t, term, "after three Crush panes")
}

// TestRealCrushInParallelPanes runs a real Crush binary, three of them side by
// side in shell panes of an isolated daemon, against a local stand-in model
// provider (testdata/fakeopenai), so a whole turn costs nothing. The provider
// answers with a call to Crush's bash tool, Crush asks for permission, and the
// test approves it. It runs only when TUIOS_E2E_CRUSH names a Crush binary.
//
// It checks the states Crush reports: idle at start, working on the prompt,
// needs_input on the permission dialog, and done when the allowed turn ends.
// Then one Crush quits, which releases its pane, and one is killed, which
// sends nothing, and both panes clear.
//
// A Crush without charmbracelet/crush#3541 can report working after its own
// permission request, a race in its herdr bridge. The pane still goes to
// needs_input, since tuios reads the dialog when Crush reports working (see
// crushScreenWins), so any Crush passes.
func TestRealCrushInParallelPanes(t *testing.T) {
	requireReal(t, "perf or external program, env-gated")
	crushBin := os.Getenv("TUIOS_E2E_CRUSH")
	if crushBin == "" {
		t.Skip("set TUIOS_E2E_CRUSH to a crush binary to run a real Crush")
	}
	term, base := crushClient(t)
	log := &stateLog{name: "real-crush-parallel"}

	writeRealCrushConfig(t, base, startFakeProvider(t))

	names := []string{"crush-a", "crush-b", "crush-c"}
	ids := crushPanes(t, base, names...)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		saveFrame(t, term, "real-crush-parallel-failed")
		for _, name := range names {
			out, err := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", name)
			t.Logf("%s at the failure (%v):\n%s", name, err, out)
		}
	})
	bins := map[string]string{}
	for _, name := range names {
		dir := filepath.Join(t.TempDir(), name)
		mustMkdir(dir)
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		bins[name] = filepath.Join(dir, "crush")
		if err := os.Symlink(crushBin, bins[name]); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# e2e\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		typeIn(t, base, name, realCrushCommand(dir, bins[name]))
	}
	for _, name := range names {
		waitAgentState(t, base, crushSession, ids[name], "idle", "crush", log, name+" started, idle")
		waitCapture(t, base, crushSession, name, "Ready")
	}
	send := func(name string, keys string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-keys", "-s", crushSession, "-w", ids[name], keys); err != nil {
			t.Fatalf("send-keys: %v\n%s", err, out)
		}
	}
	time.Sleep(2 * time.Second)
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "send-text", "-s", crushSession, "-w", ids[name], "RUN-TOOL "+name); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		waitJoined(t, base, name, "RUN-TOOL "+name)
		send(name, "Enter")
	}
	for _, name := range names {
		st := waitAgentState(t, base, crushSession, ids[name], "needs_input", "crush", log, name+" permission dialog")
		if st.BlockedBy != "approval" {
			t.Fatalf("%s blocked by %q, want approval", name, st.BlockedBy)
		}
	}
	waitForAll(t, term, uiTimeout, "the rail rows", "crush")
	saveFrame(t, term, "real-crush-parallel-blocked")
	for _, name := range names {
		waitJoined(t, base, name, "Permission Required", "Allow")
		time.Sleep(500 * time.Millisecond)
		send(name, "a")
		waitAgentState(t, base, crushSession, ids[name], "done", "crush", log, name+" allowed, turn ended, done")
	}
	saveFrame(t, term, "real-crush-parallel-done")

	send("crush-a", "ctrl+c")
	time.Sleep(500 * time.Millisecond)
	send("crush-a", "ctrl+c")
	waitAgentState(t, base, crushSession, ids["crush-a"], "none", "", log, "crush-a quit, released")

	if out, err := exec.Command("pkill", "-9", "-f", "^"+bins["crush-b"]).CombinedOutput(); err != nil {
		t.Fatalf("kill crush-b: %v\n%s", err, out)
	}
	waitAgentState(t, base, crushSession, ids["crush-b"], "none", "", log, "crush-b killed, cleared at the shell")
	if st := readAgentState(t, base, crushSession, ids["crush-c"]); st.State != "done" {
		t.Fatalf("crush-c is %s after the others left", st.State)
	}
	log.save(t)
	alive(t, term, "after three real Crush panes")
}

func startFakeProvider(t *testing.T, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeopenai")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakeopenai").CombinedOutput(); err != nil {
		t.Fatalf("build fakeopenai: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("fakeopenai printed no address: %v", err)
	}
	return strings.TrimSpace(line)
}

func waitAgentMeta(t *testing.T, base, window string, markers ...string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "get-agent-state", "--json", "-s", crushSession, "-w", window)
		if containsAll(out, markers...) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane's agent state never held %v:\n%s", markers, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitJoined(t *testing.T, base, window string, markers ...string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", window)
		var b strings.Builder
		for line := range strings.SplitSeq(out, "\n") {
			b.WriteString(strings.TrimRight(line, " "))
		}
		if joined := b.String(); containsAll(joined, markers...) {
			return joined
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %v:\n%s", markers, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
