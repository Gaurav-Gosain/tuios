package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The first-run approval of a native plugin, and the typing rule of a
// plugin with write.
//
// A linked native plugin does not run until the person allows it on this
// machine. An attached client asks in a dialog, which is drawn at truecolor
// and at 16 colours. A pane cannot answer for the person, also with its pane
// variables cut. Allowed, the plugin's startup tries what write gives and
// what it does not:
//
//   - send-text into an ordinary shell pane, which holds admin under the
//     default open mode: it types (the plugin's own typing rule).
//   - send-text into a pane that waits on a prompt: refused, the prompt
//     rule holds a plugin.
//   - respond, set-pane-grants, list-sessions and plugin.approve: refused,
//     each needs what the plugin does not hold.
//
// A changed grant in the manifest asks again.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Plugin approval".

const writerScript = `T='%TUIOS%'
D="$HERDR_PLUGIN_STATE_DIR"
. ./targets.sh
run() { n=$1; shift; "$@" >"$D/$n.out" 2>&1; echo $? >"$D/$n.code"; }
run ordinary "$T" send-text -s work -w "$SHELLW" "echo typed-by-plugin
"
run prompt "$T" send-text -s work -w "$PROMPTW" "1"
run respond "$T" respond approve -s work -w "$PROMPTW"
run regrant "$T" set-pane-grants -w "$SHELLW" --grants admin
run read "$T" list-sessions
run approve "$T" plugins approve e2e.writer --deny --yes
`

func writerManifest(grants string) string {
	return `schema = 1
id = "e2e.writer"
name = "Writer probe"
version = "1.0.0"
platforms = ["linux", "macos"]

[permissions]
grants = [` + grants + `]

[[process]]
id = "main"
run = "startup"
command = ["sh", "probe.sh"]
`
}

// TestNativePluginApproval checks the first-run approval and a plugin's
// typing rule.
func TestNativePluginApproval(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "work", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	ids := windowIDs(t, base, "work")
	if len(ids) != 2 {
		t.Fatalf("session work has %d windows, want 2", len(ids))
	}
	shellW, promptW := ids[0], ids[1]
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "work", "-w", promptW); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}

	dir := filepath.Join(t.TempDir(), "writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("probe.sh", strings.ReplaceAll(writerScript, "%TUIOS%", tuiosBin))
	write("targets.sh", "SHELLW='"+shellW+"'\nPROMPTW='"+promptW+"'\n")
	write("tuios-plugin.toml", writerManifest(`"write"`))
	if out, err := tuiosCLI(t, base, "plugins", "link", dir, "--enable"); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	art := artifactDir(t)

	// It waits, and runs nothing.
	listed, _ := tuiosCLI(t, base, "plugins", "list")
	_ = os.WriteFile(filepath.Join(art, "list-waiting.txt"), []byte(listed), 0o644)
	if !strings.Contains(listed, "waiting for approval") {
		t.Fatalf("plugins list does not say the plugin waits:\n%s", listed)
	}
	time.Sleep(time.Second)
	if got := readPluginFile(base, "e2e.writer", "ordinary.code"); got != "" {
		t.Fatalf("the plugin ran before anyone allowed it: ordinary.code is %q", got)
	}

	// A pane cannot answer for the person, also with its pane variables
	// cut: the daemon places the caller by the kernel.
	paneOut := filepath.Join(t.TempDir(), "pane-approve")
	line := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID '" + tuiosBin + "' plugins approve e2e.writer --yes >'" + paneOut + ".out' 2>&1; echo $? >'" + paneOut + ".code'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", shellW, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	code := waitFileText(t, paneOut+".code", "\n")
	paneText, _ := os.ReadFile(paneOut + ".out")
	_ = os.WriteFile(filepath.Join(art, "pane-approve.txt"), paneText, 0o644)
	if strings.TrimSpace(code) == "0" || !strings.Contains(string(paneText), "plugin.approve is for the person") {
		t.Fatalf("a pane answered for the person (exit %s):\n%s", strings.TrimSpace(code), paneText)
	}
	if listed, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(listed, "waiting for approval") {
		t.Fatalf("the plugin left the waiting state after the pane tried:\n%s", listed)
	}

	// The dialog, at truecolor. Not now leaves the plugin waiting.
	dialog := func(name string, env []string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{args: []string{"attach", "work"}, env: env})
		if err := term.WaitForText("Allow the plugin Writer probe", bootTimeout); err != nil {
			t.Fatalf("no approval dialog (%s): %v\n%s", name, err, term.Snapshot())
		}
		if err := term.WaitForText("Types into any pane that does not wait on a prompt.", uiTimeout); err != nil {
			t.Fatalf("the dialog does not say what write does (%s): %v\n%s", name, err, term.Snapshot())
		}
		saveArtifact(t, term, art, "dialog-"+name)
		savePNG(t, term.Screen(), hostPalette(t, ""), art, "dialog-"+name)
		return term
	}
	term := dialog("truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor"})
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	term.Close()
	time.Sleep(500 * time.Millisecond)
	if got := readPluginFile(base, "e2e.writer", "ordinary.code"); got != "" {
		t.Fatalf("not now let the plugin run: ordinary.code is %q", got)
	}

	// At 16 colours, and Allow: up twice from Not now, then enter.
	term = dialog("16", []string{"TERM=xterm", "COLORTERM="})
	for _, k := range []string{"\x1b[A", "\x1b[A", "\r"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("allow did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, art, "allowed-16")

	// The plugin runs now. "0" is a success, "x" any failure.
	want := map[string]string{"ordinary": "0", "prompt": "x", "respond": "x", "regrant": "x", "read": "x", "approve": "x"}
	for name, code := range want {
		got := strings.TrimSpace(waitPluginFile(t, base, "e2e.writer", name+".code", "\n"))
		if (got == "0") != (code == "0") {
			t.Errorf("%s exited %s, want %s (x is any failure). Output:\n%s", name, got, code, readPluginFile(base, "e2e.writer", name+".out"))
		}
		savePluginArtifact(t, base, "e2e.writer", name+".out")
	}
	for name, text := range map[string]string{
		"prompt":  "A plugin never answers a prompt",
		"respond": "a plugin cannot answer a prompt",
		"regrant": "set-pane-grants is refused for plugin e2e.writer",
		"read":    "list-sessions needs the read grant",
		"approve": "plugin.approve is for the person",
	} {
		if got := readPluginFile(base, "e2e.writer", name+".out"); !strings.Contains(got, text) {
			t.Errorf("%s.out lacks %q:\n%s", name, text, got)
		}
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		screen, _ := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", shellW)
		if strings.Contains(screen, "typed-by-plugin") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell pane does not show what the plugin typed:\n%s", screen)
		}
		time.Sleep(100 * time.Millisecond)
	}
	prompt, _ := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", promptW)
	_ = os.WriteFile(filepath.Join(art, "prompt-pane.txt"), []byte(prompt), 0o644)
	info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.writer")
	_ = os.WriteFile(filepath.Join(art, "info-allowed.txt"), []byte(info), 0o644)
	if !strings.Contains(info, "grants    asks write, holds write") {
		t.Errorf("plugins info after allow:\n%s", info)
	}

	// A changed grant in the manifest asks again.
	write("tuios-plugin.toml", writerManifest(`"read", "write"`))
	listed, _ = tuiosCLI(t, base, "plugins", "list")
	_ = os.WriteFile(filepath.Join(art, "list-changed.txt"), []byte(listed), 0o644)
	if !strings.Contains(listed, "waiting for approval") {
		t.Errorf("a changed grant did not ask again:\n%s", listed)
	}
	term.Close()
	again := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := again.WaitForText("Reads every session and pane on this machine.", bootTimeout); err != nil {
		t.Fatalf("no dialog for the changed plugin: %v\n%s", err, again.Snapshot())
	}
	saveArtifact(t, again, art, "dialog-changed")
}

// waitFileText waits until the file at path holds want.
func waitFileText(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * uiTimeout)
	for {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %q. It holds:\n%s", path, want, b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
