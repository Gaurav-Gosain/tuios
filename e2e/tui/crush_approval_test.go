package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// crushHomeWindow is the window crushClient's session opens with. A Crush in
// it has the whole pane, so its dialog is drawn as wide as Crush draws it.
func crushHomeWindow(t *testing.T, base string) string {
	t.Helper()
	ids := grantWindowIDs(t, base, crushSession)
	if len(ids) != 1 {
		t.Fatalf("want one window in %s, got %v", crushSession, ids)
	}
	return ids[0]
}

// openCrushPeek opens the Inbox, waits for the one approval, and peeks it.
// summary is the whole message list-attention must carry, and shown is what
// the dialog shows of the call: the command, or the description a narrow
// dialog shows in its place.
func openCrushPeek(t *testing.T, term *tuitest.Terminal, base, summary, shown, frame string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-attention", "--json")
	if err != nil || !strings.Contains(out, `"summary": "`+summary+`"`) {
		t.Fatalf("ASSERTION: the Inbox item does not carry the request %q (%v):\n%s", summary, err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	// The row shows the summary beside the pane's name, however long the
	// name is.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Approvals 1") && strings.Contains(text, "approve bash:")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the Crush approval with its summary: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, frame)
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Tool bash") && strings.Contains(text, shown) &&
			strings.Contains(text, "Allow for Session")
	}, uiTimeout); err != nil {
		t.Fatalf("the peek never showed Crush's dialog: %v\n%s", err, term.Snapshot())
	}
}

// allowFromPeek presses a in the open peek. A risky call takes a second
// press: the first one must press nothing into the pane, which still shows
// the dialog a second later, and say what the second press does.
func allowFromPeek(t *testing.T, term *tuitest.Terminal, base, win string, risky bool) {
	t.Helper()
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !risky {
		return
	}
	if err := term.WaitForText("Press a again", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the first press on a call that is not all shown did not ask for a second: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if out, _ := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", win); !strings.Contains(out, "Permission Required") {
		t.Fatalf("ASSERTION: the first press reached Crush:\n%s", out)
	}
	saveFrame(t, term, "crush-risky-armed")
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("second press: %v", err)
	}
}

// tileCrushPane turns tiling on, so the one pane takes the screen and a
// Crush in it draws its wide dialog.
func tileCrushPane(t *testing.T, base string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "set-layout", "-s", crushSession, "--tiling", "true"); err != nil {
		t.Fatalf("set-layout: %v\n%s", err, out)
	}
}

// startFakeCrushIn starts a stand-in Crush in the window and returns its pid.
func startFakeCrushIn(t *testing.T, base, win, bin string) int {
	t.Helper()
	typeIn(t, base, win, bin)
	out := waitCapture(t, base, crushSession, win, "FAKE-CRUSH-READY")
	m := regexp.MustCompile(`PID=(\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the stand-in printed no pid:\n%s", out)
	}
	pid, _ := strconv.Atoi(m[1])
	return pid
}

// refusedFromAPane runs tuios respond from a pane with no respond grant, in a
// session of its own, against the Crush pane, and checks it is refused.
func refusedFromAPane(t *testing.T, base, window string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "new", "e2e-other", "--detach"); err != nil {
		t.Fatalf("create the other session: %v\n%s", err, out)
	}
	line := tuiosBin + " respond -s " + crushSession + " -w " + window + " approve; echo RESPOND_EXIT=$?\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-other", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "e2e-other")
		joined := strings.Join(strings.Fields(out), " ")
		if strings.Contains(joined, "RESPOND_EXIT=1") && strings.Contains(joined, "respond is for the person") {
			return
		}
		if strings.Contains(joined, "RESPOND_EXIT=0") {
			t.Fatalf("ASSERTION: a pane with no respond grant answered Crush's prompt:\n%s", out)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane's respond was never refused:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestCrushPermissionAnsweredFromTheInbox runs a stand-in Crush that shows its
// permission dialog the way Crush v0.97.1 and earlier does when its herdr bridge
// loses a race: the dialog is drawn, blocked and then working are reported,
// and the pane writes nothing more. The text and the order were recorded from
// crush v0.96.1 (see testdata/fakecrush). The pane still goes to needs_input,
// with the tool and the command as its message. The Inbox lists it, the peek
// shows the dialog alone, a pane with no respond grant is refused, and a in
// the peek reaches Crush as its own key for allow. The turn then ends.
//
// Negative control: with the screenWins branch taken out of
// blockerOverridesClaim, the pane stays on working and the needs_input wait
// fails. With the [screen.rule.answers] block taken out of crush.toml, the
// peek offers no answers and a sends nothing, so the ALLOWED wait fails.
// With the cap on the name's width taken out of inboxItemRow, the long name
// fills the row and the summary wait fails.
func TestCrushPermissionAnsweredFromTheInbox(t *testing.T) {
	term, base := crushClient(t)
	crush := buildFakeCrush(t)
	log := &stateLog{name: "crush-inbox-approval"}
	win := crushHomeWindow(t, base)

	tileCrushPane(t, base)
	startFakeCrushIn(t, base, win, crush)
	typeIn(t, base, win, "dialog")
	what := "touch hello.txt"
	waitJoined(t, base, win, "Permission Required", "Allow", what)

	st := waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "dialog up, blocked then working reported")
	if st.BlockedBy != "approval" {
		t.Fatalf("ASSERTION: blocked by %q, want approval", st.BlockedBy)
	}
	waitAgentMeta(t, base, win, `"message": "approve bash: `+what+`"`)
	waitForAll(t, term, uiTimeout, "the rail row", "crush")
	saveFrame(t, term, "crush-needs-input")

	refusedFromAPane(t, base, win)
	// Crush's pane is named for its folder, often a long path.
	if out, err := tuiosCLI(t, base, "set-window", "-s", crushSession, "-w", win, "--name", "crush /home/someone/src/github.com/example/a-project-with-a-long-name"); err != nil {
		t.Fatalf("set-window: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", win); !strings.Contains(out, "Permission Required") {
		t.Fatalf("ASSERTION: the refused respond reached Crush:\n%s", out)
	}

	openCrushPeek(t, term, base, "approve bash: "+what, what, "crush-inbox-list")
	saveFrame(t, term, "crush-inbox-peek")
	allowFromPeek(t, term, base, win, false)
	waitCapture(t, base, crushSession, win, "ALLOWED")
	waitAgentState(t, base, crushSession, win, "done", "crush", log, "allowed from the Inbox, turn ended")
	saveFrame(t, term, "crush-inbox-answered")
	log.save(t)
	alive(t, term, "after answering Crush from the Inbox")
}

// writeRealCrushConfig points a real Crush at the stand-in provider, and at
// nothing else, through the daemon's XDG_CONFIG_HOME.
func writeRealCrushConfig(t *testing.T, base, provider string) {
	t.Helper()
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "crush")
	mustMkdir(cfgDir)
	cfg, _ := json.Marshal(map[string]any{
		"providers": map[string]any{"fake": map[string]any{
			"type": "openai-compat", "base_url": provider, "api_key": "not-a-key",
			"models": []any{map[string]any{"id": "fake-model", "name": "Fake", "context_window": 128000, "default_max_tokens": 4096}},
		}},
		"models": map[string]any{
			"large": map[string]any{"model": "fake-model", "provider": "fake"},
			"small": map[string]any{"model": "fake-model", "provider": "fake"},
		},
		"options": map[string]any{"disable_provider_auto_update": true, "disable_metrics": true},
	})
	if err := os.WriteFile(filepath.Join(cfgDir, "crush.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

// realCrushCommand is the line that starts a real Crush in dir, with no key
// from the machine: the only provider it can call is the stand-in.
func realCrushCommand(dir, bin string) string {
	return "cd " + dir + " && env -u ANTHROPIC_API_KEY -u OPENAI_API_KEY -u GEMINI_API_KEY -u OPENROUTER_API_KEY -u GROQ_API_KEY -u XAI_API_KEY CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1 CRUSH_DISABLE_METRICS=1 " + bin
}

// TestRealCrushAnsweredFromTheInbox is TestCrushPermissionAnsweredFromTheInbox
// with a real Crush and the stand-in provider, which asks for one bash call:
// touch tuios-e2e-marker. It runs only with TUIOS_E2E_CRUSH set to a crush
// binary. The race in Crush's herdr bridge decides whether its last report
// is blocked or working, and the test passes either way. Allowing from the
// Inbox makes Crush run the command, so the marker file is the proof that
// the answer reached Crush.
func TestRealCrushAnsweredFromTheInbox(t *testing.T) {
	crushBin := os.Getenv("TUIOS_E2E_CRUSH")
	if crushBin == "" {
		t.Skip("set TUIOS_E2E_CRUSH to a crush binary to run a real Crush")
	}
	term, base := crushClient(t)
	log := &stateLog{name: "real-crush-inbox-approval"}
	writeRealCrushConfig(t, base, startFakeProvider(t))
	win := crushHomeWindow(t, base)
	t.Cleanup(func() {
		if t.Failed() {
			saveFrame(t, term, "real-crush-inbox-failed")
			out, err := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", win)
			t.Logf("the Crush pane at the failure (%v):\n%s", err, out)
		}
	})

	dir := filepath.Join(t.TempDir(), "proj")
	mustMkdir(dir)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	typeIn(t, base, win, realCrushCommand(dir, crushBin))
	waitAgentState(t, base, crushSession, win, "idle", "crush", log, "started, idle")
	waitCapture(t, base, crushSession, win, "Ready")
	// Crush draws its prompt a moment before it reads keys.
	time.Sleep(2 * time.Second)
	if out, err := tuiosCLI(t, base, "send-text", "-s", crushSession, "-w", win, "RUN-TOOL inbox"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitJoined(t, base, win, "RUN-TOOL inbox")
	if out, err := tuiosCLI(t, base, "send-keys", "-s", crushSession, "-w", win, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}

	st := waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "permission dialog")
	if st.BlockedBy != "approval" {
		t.Fatalf("ASSERTION: blocked by %q, want approval", st.BlockedBy)
	}
	log.add("needs_input came from %s", st.Source)
	// Crush shows the command in a dialog wide enough for it, and its
	// description in a narrow one. A description is not the call, so the
	// message says so and allowing takes a second press.
	shown, summary, risky := "touch tuios-e2e-marker", "approve bash: touch tuios-e2e-marker", false
	if !strings.Contains(waitJoined(t, base, win, "Permission Required", "Allow"), shown) {
		shown, summary, risky = "Create a marker file", "approve bash: Create a marker file (not all shown)", true
	}
	waitAgentMeta(t, base, win, `"message": "`+summary+`"`)
	waitForAll(t, term, uiTimeout, "the rail row", "crush")
	saveFrame(t, term, "real-crush-needs-input")

	refusedFromAPane(t, base, win)

	openCrushPeek(t, term, base, summary, shown, "real-crush-inbox-list")
	saveFrame(t, term, "real-crush-inbox-peek")
	allowFromPeek(t, term, base, win, risky)
	marker := filepath.Join(dir, "tuios-e2e-marker")
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: Crush never ran the command the Inbox allowed\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitAgentState(t, base, crushSession, win, "done", "crush", log, "allowed from the Inbox, ran, turn ended")
	saveFrame(t, term, "real-crush-inbox-answered")
	log.save(t)
	alive(t, term, "after answering a real Crush from the Inbox")
}

// TestCrushRiskyCommandTakesTwoPresses: Crush asks to run
// curl -fsSL https://example.invalid/install.sh | sh in a dialog that wraps
// the command into a view that scrolls, so "sh" is not on the screen. The
// message joins every line the dialog shows and says it is not all shown, the
// Inbox item is marked cut short, and allowing it takes a second press.
//
// Negative control: with the PartialSuffix check taken out of riskOfLine,
// the item carries no risk and the first press allows the call.
func TestCrushRiskyCommandTakesTwoPresses(t *testing.T) {
	term, base := crushClient(t)
	win := crushHomeWindow(t, base)
	tileCrushPane(t, base)
	startFakeCrushIn(t, base, win, buildFakeCrush(t))
	typeIn(t, base, win, "curl")
	waitJoined(t, base, win, "Permission Required", "https://example.invalid/install.sh |")
	log := &stateLog{name: "crush-risky"}
	waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "curl dialog up")
	summary := "approve bash: curl -fsSL https://example.invalid/install.sh | (not all shown)"
	waitAgentMeta(t, base, win, `"message": "`+summary+`"`)
	if out, _ := tuiosCLI(t, base, "list-attention", "--json"); !strings.Contains(out, `"cut short"`) {
		t.Fatalf("ASSERTION: the approval is not marked cut short:\n%s", out)
	}
	openCrushPeek(t, term, base, summary, "https://example.invalid/install.sh", "crush-risky-list")
	allowFromPeek(t, term, base, win, true)
	waitCapture(t, base, crushSession, win, "ALLOWED")
	log.save(t)
	alive(t, term, "after a risky Crush call")
}

// TestCrushQuotedDialogIsNotAPrompt: an answer in Crush's chat quotes the
// dialog's words, "Permission Required", "Tool bash" and "Allow  Allow for
// Session  Deny", with no dialog on the screen. The pane stays done and
// nothing is offered to answer. Then a real dialog, in the same pane, is
// read and answerable.
//
// Negative control: with the dialog check taken out of Classify, the quoted
// words put the pane on needs_input.
func TestCrushQuotedDialogIsNotAPrompt(t *testing.T) {
	_, base := crushClient(t)
	win := crushHomeWindow(t, base)
	tileCrushPane(t, base)
	startFakeCrushIn(t, base, win, buildFakeCrush(t))
	log := &stateLog{name: "crush-quoted"}
	typeIn(t, base, win, "quote")
	waitCapture(t, base, crushSession, win, "Here is what you will see")
	waitAgentState(t, base, crushSession, win, "done", "crush", log, "quoted the dialog, turn ended")
	// Past the screen tier's settle look, which reads the quote.
	time.Sleep(1500 * time.Millisecond)
	if st := readAgentState(t, base, crushSession, win); st.State != "done" {
		t.Fatalf("ASSERTION: the quoted dialog made the pane %s (%q)", st.State, st.Message)
	}
	if out, _ := tuiosCLI(t, base, "list-attention", "--json"); strings.Contains(out, `"kind": "approval"`) {
		t.Fatalf("ASSERTION: the quoted dialog opened an approval:\n%s", out)
	}

	typeIn(t, base, win, "dialog")
	waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "real dialog")
	if out, _ := tuiosCLI(t, base, "peek-prompt", "--json", "-s", crushSession, "-w", win); !strings.Contains(out, `"answerable": true`) {
		t.Fatalf("the real dialog is not answerable:\n%s", out)
	}
	log.save(t)
}

// TestCrushCrashAtItsDialogClearsThePane: Crush reports blocked and then
// working at its dialog, the screen takes the claim back to needs_input, and
// Crush is killed with no release. The pane is back at its shell, so the
// pane clears, and nothing is left to answer into the shell.
//
// Negative control: with the herdr fields left off the blocker claim in
// applyAgentReport, the pane stays on needs_input at the shell.
func TestCrushCrashAtItsDialogClearsThePane(t *testing.T) {
	_, base := crushClient(t)
	win := crushHomeWindow(t, base)
	// A tall pane, so what the shell prints after the crash leaves the
	// dialog whole on the screen.
	tileCrushPane(t, base)
	pid := startFakeCrushIn(t, base, win, buildFakeCrush(t))
	log := &stateLog{name: "crush-crash"}
	typeIn(t, base, win, "dialog")
	st := waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "dialog up")
	if st.Source != "screen" {
		t.Fatalf("the dialog's claim is from %s, want the screen's override", st.Source)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitAgentState(t, base, crushSession, win, "none", "", log, "killed at its dialog, cleared at the shell")
	if out, _ := tuiosCLI(t, base, "capture-pane", "-s", crushSession, "-w", win); !strings.Contains(out, "Permission Required") {
		t.Fatalf("the dialog left the screen, so the pane did not clear for the reason tested:\n%s", out)
	}
	if out, _ := tuiosCLI(t, base, "peek-prompt", "--json", "-s", crushSession, "-w", win); strings.Contains(out, `"answerable": true`) {
		t.Fatalf("ASSERTION: the shell is offered answers:\n%s", out)
	}
	log.save(t)
}

// TestOnlyCrushGetsCrushAnswers: a program named notcrush reports to the herdr
// socket as Crush and draws Crush's dialog. It gets needs_input, since it
// reported blocked, but no answers: tuios does not see Crush running in the
// pane. The same program named crush, in the same pane, gets them.
//
// Negative control: with the paneRunsHarness check taken out of
// lookAtPrompt, notcrush's dialog is answerable.
func TestOnlyCrushGetsCrushAnswers(t *testing.T) {
	_, base := crushClient(t)
	win := crushHomeWindow(t, base)
	tileCrushPane(t, base)
	crush := buildFakeCrush(t)
	impostor := filepath.Join(t.TempDir(), "notcrush")
	if err := os.Link(crush, impostor); err != nil {
		data, rerr := os.ReadFile(crush)
		if rerr != nil || os.WriteFile(impostor, data, 0o700) != nil {
			t.Fatalf("copy the stand-in: %v %v", err, rerr)
		}
	}
	log := &stateLog{name: "crush-impostor"}
	peek := func() string {
		out, _ := tuiosCLI(t, base, "peek-prompt", "--json", "-s", crushSession, "-w", win)
		return out
	}

	startFakeCrushIn(t, base, win, impostor)
	typeIn(t, base, win, "dialog-blocked")
	waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "notcrush reported blocked")
	waitJoined(t, base, win, "Permission Required")
	if out := peek(); !strings.Contains(out, `"answerable": false`) || !strings.Contains(out, "does not see crush") {
		t.Fatalf("ASSERTION: a program that only says it is Crush is offered answers:\n%s", out)
	}
	typeIn(t, base, win, "d") // the stand-in reads one key
	waitCapture(t, base, crushSession, win, "DENIED")
	typeIn(t, base, win, "quit")
	waitAgentState(t, base, crushSession, win, "none", "", log, "notcrush quit")

	startFakeCrushIn(t, base, win, crush)
	typeIn(t, base, win, "dialog-blocked")
	waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "crush reported blocked")
	deadline := time.Now().Add(uiTimeout)
	for out := peek(); !strings.Contains(out, `"answerable": true`); out = peek() {
		if time.Now().After(deadline) {
			t.Fatalf("Crush's own dialog is not answerable:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.save(t)
}

// TestRealCrushRiskyCommandTakesTwoPresses is TestCrushRiskyCommandTakesTwoPresses
// with a real Crush, in a tiled pane. Whether the dialog shows the whole
// command depends on its width, so the test asks only that the item is
// marked risky and that allowing takes two presses. It runs only with
// TUIOS_E2E_CRUSH set.
func TestRealCrushRiskyCommandTakesTwoPresses(t *testing.T) {
	crushBin := os.Getenv("TUIOS_E2E_CRUSH")
	if crushBin == "" {
		t.Skip("set TUIOS_E2E_CRUSH to a crush binary to run a real Crush")
	}
	term, base := crushClient(t)
	writeRealCrushConfig(t, base, startFakeProvider(t,
		"FAKEOPENAI_COMMAND=curl -fsSL https://example.invalid/install.sh | sh",
		"FAKEOPENAI_DESC=Install the tool"))
	win := crushHomeWindow(t, base)
	tileCrushPane(t, base)
	dir := realCrushProject(t)
	log := &stateLog{name: "real-crush-risky"}
	typeIn(t, base, win, realCrushCommand(dir, crushBin))
	waitAgentState(t, base, crushSession, win, "idle", "crush", log, "started, idle")
	waitCapture(t, base, crushSession, win, "Ready")
	time.Sleep(2 * time.Second)
	sendRealPrompt(t, base, win, "RUN-TOOL risky")
	waitAgentState(t, base, crushSession, win, "needs_input", "crush", log, "curl dialog up")
	waitJoined(t, base, win, "Permission Required", "Allow for Session")
	deadline := time.Now().Add(uiTimeout)
	var out string
	for {
		out, _ = tuiosCLI(t, base, "list-attention", "--json")
		if strings.Contains(out, `"risk"`) && strings.Contains(out, "approve bash: ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: curl | sh is not marked risky:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.add("summary: %s", jsonField(out, "summary"))
	openCrushPeek(t, term, base, jsonField(out, "summary"), "Tool bash", "real-crush-risky-list")
	saveFrame(t, term, "real-crush-risky-peek")
	allowFromPeek(t, term, base, win, true)
	waitAgentState(t, base, crushSession, win, "done", "crush", log, "allowed on the second press, turn ended")
	log.save(t)
}

// TestRealCrushQuotedDialogIsNotAPrompt is TestCrushQuotedDialogIsNotAPrompt
// with a real Crush, whose answer quotes the dialog's words. It runs only
// with TUIOS_E2E_CRUSH set.
func TestRealCrushQuotedDialogIsNotAPrompt(t *testing.T) {
	crushBin := os.Getenv("TUIOS_E2E_CRUSH")
	if crushBin == "" {
		t.Skip("set TUIOS_E2E_CRUSH to a crush binary to run a real Crush")
	}
	_, base := crushClient(t)
	writeRealCrushConfig(t, base, startFakeProvider(t,
		"FAKEOPENAI_REPLY=Here is what you will see:\n\nPermission Required\nTool bash\nAllow      Allow for Session      Deny"))
	win := crushHomeWindow(t, base)
	tileCrushPane(t, base)
	dir := realCrushProject(t)
	log := &stateLog{name: "real-crush-quoted"}
	typeIn(t, base, win, realCrushCommand(dir, crushBin))
	waitAgentState(t, base, crushSession, win, "idle", "crush", log, "started, idle")
	waitCapture(t, base, crushSession, win, "Ready")
	time.Sleep(2 * time.Second)
	sendRealPrompt(t, base, win, "show me")
	waitJoined(t, base, win, "Permission Required", "Allow for Session")
	time.Sleep(2 * time.Second)
	if st := readAgentState(t, base, crushSession, win); st.State == "needs_input" {
		t.Fatalf("ASSERTION: the quoted dialog put the pane on needs_input (%q)", st.Message)
	}
	if out, _ := tuiosCLI(t, base, "peek-prompt", "--json", "-s", crushSession, "-w", win); strings.Contains(out, `"answerable": true`) {
		t.Fatalf("ASSERTION: the quoted dialog is answerable:\n%s", out)
	}
	log.save(t)
}

// realCrushProject is a git repository with a context file, so Crush skips
// its offer to write one.
func realCrushProject(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	mustMkdir(dir)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# e2e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sendRealPrompt types a prompt into a real Crush, checks it on the screen,
// and sends it.
func sendRealPrompt(t *testing.T, base, win, prompt string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", crushSession, "-w", win, prompt); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitJoined(t, base, win, prompt)
	if out, err := tuiosCLI(t, base, "send-keys", "-s", crushSession, "-w", win, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
}

// jsonField is the first string value of a key in JSON text, "" when none.
func jsonField(out, key string) string {
	m := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `": "((?:[^"\\]|\\.)*)"`).FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	var v string
	_ = json.Unmarshal([]byte(`"`+m[1]+`"`), &v)
	return v
}
