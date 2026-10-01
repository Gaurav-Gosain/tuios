package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// openCrushPeek opens the Inbox, waits for the one approval, and peeks it:
// the dialog's tool, what it acts on and the three answers show. what is the
// command, or the description a narrow dialog shows in its place.
func openCrushPeek(t *testing.T, term *tuitest.Terminal, base, what, frame string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-attention", "--json")
	if err != nil || !strings.Contains(out, `"summary": "approve bash: `+what+`"`) {
		t.Fatalf("ASSERTION: the Inbox item does not carry the request (%v):\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	// The row shows the summary beside the pane's name, however long the
	// name is. The whole summary is in list-attention.
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
		return strings.Contains(text, "Tool bash") && strings.Contains(text, what) &&
			strings.Contains(text, "Allow for Session")
	}, uiTimeout); err != nil {
		t.Fatalf("the peek never showed Crush's dialog: %v\n%s", err, term.Snapshot())
	}
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

	typeIn(t, base, win, crush)
	waitCapture(t, base, crushSession, win, "FAKE-CRUSH-READY")
	typeIn(t, base, win, "dialog")
	what := "touch hello.txt"
	if !strings.Contains(waitJoined(t, base, win, "Permission Required", "Allow"), what) {
		what = "Create hello.txt" // the narrow dialog
	}

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

	openCrushPeek(t, term, base, what, "crush-inbox-list")
	saveFrame(t, term, "crush-inbox-peek")
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("approve: %v", err)
	}
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
	// description in a narrow one.
	what := "touch tuios-e2e-marker"
	if !strings.Contains(waitJoined(t, base, win, "Permission Required", "Allow"), what) {
		what = "Create a marker file"
	}
	waitAgentMeta(t, base, win, `"message": "approve bash: `+what+`"`)
	waitForAll(t, term, uiTimeout, "the rail row", "crush")
	saveFrame(t, term, "real-crush-needs-input")

	refusedFromAPane(t, base, win)

	openCrushPeek(t, term, base, what, "real-crush-inbox-list")
	saveFrame(t, term, "real-crush-inbox-peek")
	if err := term.SendKeys("a"); err != nil {
		t.Fatalf("approve: %v", err)
	}
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
