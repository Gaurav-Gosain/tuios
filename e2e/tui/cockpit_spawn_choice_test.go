package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// fakeSheetClaude is a program called claude for the sheet test. Its auth
// check answers signed in when the marker file exists and signed out when it
// does not. Started as an agent, it writes its argv to the argv file, reports
// itself idle, and writes the first line typed into it to the prompt file.
const fakeSheetClaude = `#!/bin/sh
if [ "$1" = auth ]; then
  if [ -f "__MARK__" ]; then echo '{"loggedIn": true}'; exit 0; fi
  echo '{"loggedIn": false}'; exit 1
fi
printf '%s\n' "$@" > "__ARGV__"
"__TUIOS__" set-agent-state idle --harness claude-code -m ready
IFS= read -r line
printf '%s' "$line" > "__PROMPT__"
exec sleep 600
`

// TestNewAgentSheetChoicesAndReadiness opens the New agent sheet with a fake
// Claude Code on the PATH and holds:
//
//   - the machine row says what the daemon found: "claude ready" when the
//     agent is signed in, "claude not signed in" when it is not, and the
//     picked row's warning says so under the machines;
//   - the sheet offers Claude Code's effort and permission pills from its
//     manifest, and a mode that turns approvals off says "approvals off";
//   - Start passes the model, the effort and the permission mode as
//     claude's own flags;
//   - an image pasted into the task goes to the session's file store, and
//     the agent's first prompt names the stored file, which holds the image.
//
// How this could pass wrongly, written down first:
//   - "claude ready" was drawn from a PATH lookup in the client before.
//     The signed-out run has claude on the PATH too, so only the daemon's
//     check can say "not signed in".
//   - The argv or the prompt could be read before the agent wrote it. The
//     test waits for each file.
//   - The prompt could name a path that holds something else. The stored
//     file's bytes are compared with the image's.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on the build before
// the sheet change the rows say "claude ready" when signed out, the sheet
// has no pills, and the argv has --model only.
func TestNewAgentSheetChoicesAndReadiness(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	work := t.TempDir()
	for _, s := range []string{"home"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach", "--cwd", work); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	dir := t.TempDir()
	bin := func(name string, signed bool) (path, argv, prompt string) {
		t.Helper()
		folder := filepath.Join(dir, name)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		mark := filepath.Join(folder, "signed-in")
		argv, prompt = filepath.Join(folder, "argv"), filepath.Join(folder, "prompt")
		script := strings.NewReplacer("__MARK__", mark, "__ARGV__", argv, "__PROMPT__", prompt, "__TUIOS__", tuiosBin).Replace(fakeSheetClaude)
		if err := os.WriteFile(filepath.Join(folder, "claude"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if signed {
			if err := os.WriteFile(mark, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return folder, argv, prompt
	}
	open := func(path string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true,
			env: []string{"PATH=" + path + ":/usr/bin:/bin", "TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
		}
		windowManagementMode(t, term)
		sendKeys(t, term, tuitest.Ctrl('b'), "i")
		waitText(t, term, "the agents view", "Agents", "New agent")
		sendKeys(t, term, "n")
		waitText(t, term, "the sheet", "Task", "Where it works", "Machine", "Start agent")
		return term
	}

	// Signed out: claude is on the PATH, and the daemon says it is not
	// signed in.
	outPath, _, _ := bin("out", false)
	term := open(outPath)
	waitText(t, term, "the signed-out row", "claude not signed in", ": claude not signed in.")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "sheet-signed-out-200x55")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Where it works") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the sheet: %v\n%s", err, term.Snapshot())
	}
	if err := term.Close(); err != nil {
		t.Logf("close: %v", err)
	}

	// Signed in.
	inPath, argvFile, promptFile := bin("in", true)
	term = open(inPath)
	waitText(t, term, "the ready row", "claude ready", "Effort", "Permissions", "Most free cores first")
	sendKeys(t, term, "Look at the login error in this picture ")
	pic := filepath.Join(t.TempDir(), "login-error.png")
	img := gradientPNG(t, 64, 32)
	if err := os.WriteFile(pic, img, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := term.Paste(pic); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the image in the task", "[Image #1", "64×32 png]")

	// tab: Agent, Model. The model is typed; then the effort and the mode
	// are stepped with the arrows.
	sendKeys(t, term, tuitest.Tab, tuitest.Tab, "opus", tuitest.Tab)
	sendKeys(t, term, tuitest.Right, tuitest.Right, tuitest.Right)
	waitText(t, term, "the effort pill on high", " high ")
	sendKeys(t, term, tuitest.Tab, tuitest.Left)
	waitText(t, term, "the risky mode marked", "bypassPermissions", "approvals off")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "sheet-risky-200x55")
	// default, manual, acceptEdits, plan.
	sendKeys(t, term, tuitest.Right, tuitest.Right, tuitest.Right, tuitest.Right)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), " plan ") && !strings.Contains(s.Text(), "approvals off")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the permission pill did not step to plan: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "sheet-choices-200x55")
	sendKeys(t, term, tuitest.Enter)

	read := func(path, what string) []byte {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
				return b
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: the agent never wrote its %s\n%s", what, term.Snapshot())
		return nil
	}
	argv := string(read(argvFile, "argv"))
	if want := "--model\nopus\n--effort\nhigh\n--permission-mode\nplan\n"; argv != want {
		t.Errorf("ASSERTION: the agent started with argv %q, want %q", argv, want)
	}
	prompt := string(read(promptFile, "first prompt"))
	stored := ""
	for f := range strings.FieldsSeq(prompt) {
		if strings.HasSuffix(f, ".png") && filepath.IsAbs(f) {
			stored = f
		}
	}
	if !strings.Contains(prompt, "Look at the login error in this picture") || stored == "" {
		t.Fatalf("ASSERTION: the first prompt %q does not hold the task and a stored image path", prompt)
	}
	if got, err := os.ReadFile(stored); err != nil || !bytes.Equal(got, img) {
		t.Errorf("ASSERTION: the stored image %s does not hold the pasted image (err %v)", stored, err)
	}
	art := filepath.Join(artifactDir(t), "sheet-start.txt")
	_ = os.WriteFile(art, []byte("argv after the program:\n"+argv+"\nfirst prompt:\n"+prompt+"\n"), 0o644)
	t.Logf("artifact %s", art)
}
