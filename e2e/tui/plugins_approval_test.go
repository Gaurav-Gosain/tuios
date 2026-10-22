package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The first-run approval of a native plugin, the plugin token, and the
// typing rule of a plugin with write.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "Plugin approval".

// approvalPlugin links a native plugin with one startup script and enables
// it. %TUIOS% in the script is the tuios binary.
func approvalPlugin(t *testing.T, base, id, name, grants, script string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), strings.ReplaceAll(id, ".", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeApprovalManifest(t, dir, id, name, grants)
	if err := os.WriteFile(filepath.Join(dir, "probe.sh"), []byte(strings.ReplaceAll(script, "%TUIOS%", tuiosBin)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "link", dir, "--enable"); err != nil {
		t.Fatalf("link %s: %v\n%s", id, err, out)
	}
	return dir
}

func writeApprovalManifest(t *testing.T, dir, id, name, grants string) {
	t.Helper()
	m := `schema = 1
id = "` + id + `"
name = "` + name + `"
version = "1.0.0"
platforms = ["linux", "macos"]

[permissions]
grants = [` + grants + `]

[[process]]
id = "main"
run = "startup"
command = ["sh", "probe.sh"]
`
	if err := os.WriteFile(filepath.Join(dir, "tuios-plugin.toml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
}

// approvalSession makes the session work with n windows and returns their
// ids.
func approvalSession(t *testing.T, base string, n int) []string {
	t.Helper()
	writeConfig(t, base, "")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	for range n - 1 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "work", "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
	}
	ids := windowIDs(t, base, "work")
	if len(ids) != n {
		t.Fatalf("session work has %d windows, want %d", len(ids), n)
	}
	return ids
}

// herdrCall sends one method to the daemon's herdr socket from this test
// process, which is the person.
func approvalHerdrCall(t *testing.T, base, method string, params map[string]any) map[string]any {
	t.Helper()
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock.herdr")
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		t.Fatalf("dial the herdr socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, _ := json.Marshal(map[string]any{"id": "e2e", "method": method, "params": params})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: no answer: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("%s: %v: %s", method, err, reply)
	}
	return out
}

func herdrErrorCode(resp map[string]any) string {
	if e, ok := resp["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		return code
	}
	return ""
}

// pluginTrust reads a plugin's trust hash from plugins info --json.
func pluginTrust(t *testing.T, base, id string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "plugins", "info", id, "--json")
	if err != nil {
		t.Fatalf("plugins info %s: %v\n%s", id, err, out)
	}
	var e struct {
		Plugin struct {
			Trust string `json:"trust_hash"`
		} `json:"plugin"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.Plugin.Trust == "" {
		t.Fatalf("no trust hash for %s: %v\n%s", id, err, out)
	}
	return e.Plugin.Trust
}

const writerScript = `T='%TUIOS%'
D="$HERDR_PLUGIN_STATE_DIR"
. ./targets.sh
run() { n=$1; shift; "$@" >"$D/$n.out" 2>&1; echo $? >"$D/$n.code"; }
run ordinary "$T" send-text -s work -w "$SHELLW" "echo typed-by-plugin
"
run prompt "$T" send-text -s work -w "$PROMPTW" "1"
run keys "$T" call send-keys '{"session":"work","window":"'"$PROMPTW"'","keys":["Enter"]}'
run runp "$T" call run '{"session":"work","window":"'"$PROMPTW"'","command":"echo ran-by-plugin"}'
run submit "$T" call send-text '{"session":"work","window":"'"$RACEW"'","text":"x","submit":true}'
run askrace "$T" call ask-agent '{"session":"work","window":"'"$RACEASKW"'","text":"x","ready_timeout":3000}'
run runrace "$T" call run '{"session":"work","window":"'"$RACERUNW"'","command":"x","timeout":3000}'
run queue "$T" call queue-prompt '{"session":"work","window":"'"$SHELLW"'","text":"hello"}'
run respond "$T" respond approve -s work -w "$PROMPTW"
run regrant "$T" set-pane-grants -w "$SHELLW" --grants admin
run read "$T" list-sessions
run approve "$T" plugins approve e2e.writer --deny --yes
`

// TestNativePluginApproval: a linked native plugin waits and runs nothing.
// A pane cannot answer for the person. The dialog at truecolor and at 16
// colours takes no key alone as Allow: Up, Up, Enter, Tab, Enter from the
// default row allows nothing. Typing allow and Enter allows it. Then the
// plugin's write: it types into an ordinary shell, and is refused on a pane
// that waits on a prompt by send-text, send-keys and run, and at the Enter
// of send-text with submit when the pane comes to a prompt while the paste
// is taken in. It cannot queue, respond, change grants, read without read,
// or approve. An answer for an old trust hash, and an answer that does not
// type admin for a plugin that asks for it, are refused. A changed grant in
// the manifest asks again.
func TestNativePluginApproval(t *testing.T) {
	base := t.TempDir()
	ids := approvalSession(t, base, 5)
	shellW, promptW, raceW, raceAskW, raceRunW := ids[0], ids[1], ids[2], ids[3], ids[4]
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "work", "-w", promptW); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	// The race pane reads one byte, which the plugin's paste is, then comes
	// to a prompt, as an agent that shows a permission dialog does. An
	// Enter that reached it would print approved.
	// One race pane each for send-text with submit, ask-agent and run. The
	// run pane first prints the marks of a finished command and a new
	// prompt, so run takes it as a shell at its prompt.
	race := "stty raw -echo; dd bs=1 count=1 >/dev/null 2>&1; '" + tuiosBin + "' set-agent-state needs_input; stty sane; read -p 'Approve? ' y; echo \"approved:[$y]\"\n"
	marks := `printf '\033]133;A\007\033]133;B\007\033]133;C\007\033]133;D;0\007\033]133;A\007\033]133;B\007'; `
	for w, line := range map[string]string{raceW: race, raceAskW: race, raceRunW: marks + race} {
		if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", w, line); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}

	dir := approvalPlugin(t, base, "e2e.writer", "Writer probe", `"write"`, writerScript)
	if err := os.WriteFile(filepath.Join(dir, "targets.sh"), []byte("SHELLW='"+shellW+"'\nPROMPTW='"+promptW+"'\nRACEW='"+raceW+"'\nRACEASKW='"+raceAskW+"'\nRACERUNW='"+raceRunW+"'\n"), 0o644); err != nil {
		t.Fatal(err)
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
	// cut: every daemon checks the caller by the kernel.
	paneOut := filepath.Join(t.TempDir(), "pane-approve")
	line := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID '" + tuiosBin + "' plugins approve e2e.writer --yes >'" + paneOut + ".out' 2>&1; echo $? >'" + paneOut + ".code'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", shellW, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	code := waitFileText(t, paneOut+".code", "\n")
	paneText, _ := os.ReadFile(paneOut + ".out")
	_ = os.WriteFile(filepath.Join(art, "pane-approve.txt"), paneText, 0o644)
	if strings.TrimSpace(code) == "0" || !strings.Contains(string(paneText), "only you, from a terminal outside tuios") {
		t.Fatalf("a pane answered for the person (exit %s):\n%s", strings.TrimSpace(code), paneText)
	}

	// The daemon refuses an answer for an old trust hash, and an Allow
	// that does not type admin for a plugin that asks for it.
	admDir := approvalPlugin(t, base, "e2e.adm", "Admin probe", `"admin"`, "sleep 1\n")
	if code := herdrErrorCode(approvalHerdrCall(t, base, "plugin.approve", map[string]any{"plugin_id": "e2e.adm", "trust": pluginTrust(t, base, "e2e.adm"), "allow": true})); code != "plugin_admin_not_typed" {
		t.Errorf("allow of an admin plugin without admin answered %q, want plugin_admin_not_typed", code)
	}
	old := pluginTrust(t, base, "e2e.adm")
	writeApprovalManifest(t, admDir, "e2e.adm", "Admin probe", `"admin", "read"`)
	if code := herdrErrorCode(approvalHerdrCall(t, base, "plugin.approve", map[string]any{"plugin_id": "e2e.adm", "trust": old, "allow": true, "admin": true})); code != "plugin_changed" {
		t.Errorf("an answer for an old trust hash answered %q, want plugin_changed", code)
	}
	if out, err := tuiosCLI(t, base, "plugins", "disable", "e2e.adm"); err != nil {
		t.Fatalf("disable: %v\n%s", err, out)
	}

	dialog := func(name string, env []string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{args: []string{"attach", "work"}, env: env})
		if err := term.WaitForText("Allow the plugin Writer probe?", bootTimeout); err != nil {
			t.Fatalf("no approval dialog (%s): %v\n%s", name, err, term.Snapshot())
		}
		if err := term.WaitForText("Types into your panes and shells", uiTimeout); err != nil {
			t.Fatalf("the dialog does not say what write does (%s): %v\n%s", name, err, term.Snapshot())
		}
		saveArtifact(t, term, art, "dialog-"+name)
		savePNG(t, term.Screen(), hostPalette(t, ""), art, "dialog-"+name)
		// Past the half second the dialog ignores keys for.
		time.Sleep(700 * time.Millisecond)
		return term
	}
	keys := func(term *tuitest.Terminal, ks ...string) {
		t.Helper()
		for _, k := range ks {
			if err := term.SendKeys(k); err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	notRun := func(why string) {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		if got := readPluginFile(base, "e2e.writer", "ordinary.code"); got != "" {
			t.Fatalf("%s let the plugin run: ordinary.code is %q", why, got)
		}
	}

	// Truecolor. Keys meant for a shell allow nothing: Up, Up and Enter
	// reach the Allow row and ask for the word, and Tab and Enter do not
	// wrap past it.
	term := dialog("truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor"})
	keys(term, "\x1b[A", "\x1b[A", "\r", "\t", "\r")
	if err := term.WaitForText("Type allow in the field, then press Enter.", uiTimeout); err != nil {
		t.Fatalf("Enter on Allow did not ask for the word: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, art, "dialog-keys")
	notRun("Up, Up, Enter, Tab, Enter")
	// Keys that a tape presses, or that send-keys sends through the
	// client, are not the person's.
	tapePath := filepath.Join(t.TempDir(), "allow.tape")
	if err := os.WriteFile(tapePath, []byte("Press a l l o w Enter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "tape", "exec", "-s", "work", tapePath); err != nil {
		t.Logf("tape exec: %v\n%s", err, out)
	}
	notRun("a tape's Press of allow and Enter")
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "a l l o w Enter"); err != nil {
		t.Logf("send-keys: %v\n%s", err, out)
	}
	notRun("send-keys of allow and Enter")
	if !strings.Contains(term.Screen().Text(), "Allow the plugin Writer probe?") {
		t.Fatalf("the dialog closed under keys that were not the person's:\n%s", term.Snapshot())
	}
	keys(term, "\x1b")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	term.Close()
	notRun("Not now")

	// 16 colours. Typing allow and Enter allows it.
	term = dialog("16", []string{"TERM=xterm", "COLORTERM="})
	keys(term, "a", "l", "l", "o", "w")
	saveArtifact(t, term, art, "dialog-16-typed")
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "dialog-16-typed")
	keys(term, "\r")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("allow did not close the dialog: %v\n%s", err, term.Snapshot())
	}

	// The plugin runs now. "0" is a success, "x" any failure.
	want := map[string]string{"ordinary": "0", "prompt": "x", "keys": "x", "runp": "x", "submit": "x", "askrace": "x", "runrace": "x", "queue": "x", "respond": "x", "regrant": "x", "read": "x", "approve": "x"}
	for name, code := range want {
		got := strings.TrimSpace(waitPluginFile(t, base, "e2e.writer", name+".code", "\n"))
		if (got == "0") != (code == "0") {
			t.Errorf("%s exited %s, want %s (x is any failure). Output:\n%s", name, got, code, readPluginFile(base, "e2e.writer", name+".out"))
		}
		savePluginArtifact(t, base, "e2e.writer", name+".out")
	}
	const atPrompt = "A plugin does not answer a prompt that tuios knows of"
	for name, text := range map[string]string{
		"prompt":  atPrompt,
		"keys":    atPrompt,
		"runp":    atPrompt,
		"submit":  atPrompt,
		"queue":   "a plugin cannot queue",
		"respond": "a plugin cannot answer a prompt",
		"regrant": "set-pane-grants is refused for plugin e2e.writer",
		"read":    "list-sessions needs the read grant",
		"approve": "only you, from a terminal outside tuios",
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
	time.Sleep(500 * time.Millisecond)
	for name, w := range map[string]string{"send-text": raceW, "ask-agent": raceAskW, "run": raceRunW} {
		screen, _ := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", w)
		_ = os.WriteFile(filepath.Join(art, "race-"+name+".txt"), []byte(screen), 0o644)
		// The command line holds approved:[$y] once. An answer prints a
		// second approved:[.
		if strings.Count(screen, "approved:[") > strings.Count(screen, "approved:[$y]") {
			t.Errorf("the submit key of %s answered the prompt that came up during the paste:\n%s", name, screen)
		}
	}
	info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.writer")
	_ = os.WriteFile(filepath.Join(art, "info-allowed.txt"), []byte(info), 0o644)
	if !strings.Contains(info, "grants    asks write, holds write") {
		t.Errorf("plugins info after allow:\n%s", info)
	}

	// A changed grant in the manifest asks again.
	writeApprovalManifest(t, dir, "e2e.writer", "Writer probe", `"read", "write"`)
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

// TestPluginTokenHoldsTheCaller: a process that carries a plugin's token,
// outside the plugin's process session, is held to the plugin: on the verb
// socket through the CLI, and on the herdr socket, where nothing presents
// it. It cannot act as the person, so it approves and disables nothing. A
// token that does not prove the plugin fails the CLI.
func TestPluginTokenHoldsTheCaller(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	approvalPlugin(t, base, "e2e.tok", "Token probe", `"read"`, `printf '%s\n%s\n' "$TUIOS_PLUGIN_ID" "$TUIOS_PLUGIN_TOKEN" >"$HERDR_PLUGIN_STATE_DIR/token"
sleep 120
`)
	approvalPlugin(t, base, "e2e.victim", "Victim", `"admin"`, "sleep 1\n")
	if out, err := tuiosCLI(t, base, "plugins", "approve", "e2e.tok", "--yes"); err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	tok := strings.Fields(waitPluginFile(t, base, "e2e.tok", "token", "e2e.tok\n"))
	if len(tok) != 2 {
		t.Fatalf("token file: %q", tok)
	}
	// As a plugin's process has them: its id, its token and its daemon's
	// socket.
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	env := []string{"TUIOS_PLUGIN_ID=" + tok[0], "TUIOS_PLUGIN_TOKEN=" + tok[1], "TUIOS_SOCKET=" + sock}
	art := artifactDir(t)
	for _, c := range []struct {
		why  string
		args []string
		ok   bool
		want string
	}{
		{"read, which the plugin holds", []string{"list-sessions"}, true, "work"},
		{"typing, which it does not", []string{"send-text", "-s", "work", "echo by-token\n"}, false, "refused for plugin e2e.tok"},
		{"disable over the herdr socket", []string{"plugins", "disable", "e2e.tok"}, false, "for the person"},
		{"approve another plugin", []string{"plugins", "approve", "e2e.victim", "--grant", "admin", "--yes"}, false, "only you, from a terminal outside tuios"},
	} {
		out, err := tuiosCLIEnv(t, base, env, c.args...)
		_ = os.WriteFile(filepath.Join(art, strings.ReplaceAll(c.why, " ", "-")+".txt"), []byte(out), 0o644)
		if (err == nil) != c.ok || !strings.Contains(out, c.want) {
			t.Errorf("%s with the token: %v, want ok=%v and %q\n%s", c.why, err, c.ok, c.want, out)
		}
	}
	// With TUIOS_SOCKET cut too, nothing but the token says it is not
	// the person.
	if out, err := tuiosCLIEnv(t, base, env[:2], "plugins", "disable", "e2e.tok"); err == nil || !strings.Contains(out, "for the person") {
		t.Errorf("disable over the herdr socket with the token and no TUIOS_SOCKET: %v\n%s", err, out)
	}
	if info, _ := tuiosCLI(t, base, "plugins", "info", "e2e.victim"); !strings.Contains(info, "waiting for approval") {
		t.Errorf("the victim left the waiting state:\n%s", info)
	}
	out, err := tuiosCLIEnv(t, base, []string{"TUIOS_PLUGIN_ID=e2e.tok", "TUIOS_PLUGIN_TOKEN=00" + tok[1][2:], "TUIOS_SOCKET=" + sock}, "list-sessions")
	if err == nil || !strings.Contains(out, "did not take its token") {
		t.Errorf("a token that does not prove the plugin: %v\n%s", err, out)
	}
}

// TestPluginApprovalAsksEveryDaemon: a pane cannot approve through a second
// daemon it starts, nor under a --server that names no daemon. Every daemon
// that runs is asked whether the caller is the person.
func TestPluginApprovalAsksEveryDaemon(t *testing.T) {
	base := t.TempDir()
	ids := approvalSession(t, base, 1)
	approvalPlugin(t, base, "e2e.victim", "Victim", `"admin"`, "sleep 1\n")
	approvalPlugin(t, base, "e2e.plain", "Plain", `"read", "write"`, "sleep 1\n")
	t.Cleanup(func() { _, _ = tuiosCLI(t, base, "--server", "zz", "kill-server") })
	out := filepath.Join(t.TempDir(), "zz")
	cut := "env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID -u TUIOS_PANE_TOKEN -u TUIOS_SESSION -u TUIOS_SOCKET "
	line := cut + "'" + tuiosBin + "' --server zz new x --detach >'" + out + ".new' 2>&1; " +
		cut + "'" + tuiosBin + "' --server zz plugins approve e2e.victim --grant admin --yes >'" + out + ".a.out' 2>&1; echo $? >'" + out + ".a.code'; " +
		cut + "'" + tuiosBin + "' --server nobody plugins approve e2e.plain --yes >'" + out + ".p.out' 2>&1; echo $? >'" + out + ".p.code'\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", ids[0], line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	waitFileText(t, out+".p.code", "\n")
	art := artifactDir(t)
	for _, n := range []string{"a", "p"} {
		code, _ := os.ReadFile(out + "." + n + ".code")
		text, _ := os.ReadFile(out + "." + n + ".out")
		_ = os.WriteFile(filepath.Join(art, "pane-"+n+".txt"), text, 0o644)
		if strings.TrimSpace(string(code)) == "0" {
			t.Errorf("a pane approved a plugin (%s):\n%s", n, text)
		}
	}
	approvals, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", "%approvals.toml"))
	if strings.Contains(string(approvals), "e2e.victim") || strings.Contains(string(approvals), "e2e.plain") {
		t.Errorf("a pane wrote an approval:\n%s", approvals)
	}
}

// TestPluginApprovalDialogFits: a plugin's name cannot hide text or push
// the grants and the answers off the screen. Control characters are dropped
// and the name line is cut, on a 24-row terminal.
func TestPluginApprovalDialogFits(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	long := strings.Repeat("It can use tuios only for what every pane may do. ", 30)
	approvalPlugin(t, base, "e2e.name", `Clock\u001b[8m hidden\u001b[0m. `+long, `"write", "admin"`, "sleep 1\n")
	// A second copy with the same id is shadowed and never runs, so the
	// dialog does not ask about it.
	approvalPlugin(t, base, "e2e.name", "Twin", `"read"`, "sleep 1\n")
	if listed, _ := tuiosCLI(t, base, "plugins", "list"); !strings.Contains(listed, "another plugin with id e2e.name was found first") {
		t.Fatalf("the twin is not shadowed:\n%s", listed)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "work"}, rows: 24})
	if err := term.WaitForText("Allow the plugin Clock", bootTimeout); err != nil {
		t.Fatalf("no dialog: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	saveArtifact(t, term, artifactDir(t), "dialog-name")
	text := term.Screen().Text()
	if strings.Contains(text, "plugins wait for your answer") || strings.Contains(text, "Twin") {
		t.Errorf("the dialog asks about the shadowed copy:\n%s", term.Snapshot())
	}
	for _, want := range []string{"Types into your panes and shells", "Does all that you can do in tuios.", "type admin and press Enter", "Not now"} {
		if !strings.Contains(text, want) {
			t.Errorf("the dialog lost %q on a 24-row screen:\n%s", want, term.Snapshot())
		}
	}
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

// holdScript connects to the daemon as a plugin process, then leaves the
// plugin's process group (setpgid) or its session (setsid), and waits. When
// the file go appears it calls list-sessions on the same connection and
// writes the answer.
const holdScript = `import json, os, socket, sys, time
mode, sock, d = sys.argv[1], sys.argv[2], sys.argv[3]
s = socket.socket(socket.AF_UNIX)
s.connect(sock)
f = s.makefile('rwb')
def call(i, verb, params=None):
    req = {"id": i, "verb": verb}
    if params is not None:
        req["params"] = params
    f.write((json.dumps(req) + "\n").encode())
    f.flush()
    return f.readline().decode()
call(1, "hello", {})
if mode == "setsid":
    os.setsid()
else:
    os.setpgid(0, 0)
open(os.path.join(d, mode + ".pid"), "w").write(str(os.getpid()) + "\n")
while not os.path.exists(os.path.join(d, "go")):
    time.sleep(0.05)
open(os.path.join(d, mode + ".out"), "w").write(call(2, "list-sessions"))
`

// TestPluginStopHoldsItsProcesses: a disabled plugin leaves nothing that
// holds more than it did. A child that moved to a process group of its own
// is killed with the plugin's session. A child that connected as the plugin
// and then left the session keeps its connection, which still holds the
// plugin's grants, now none, and never the default grants of a process the
// daemon started (admin under open). The children run with the plugin's
// token cut from their environment, so only the kernel's answer at connect
// places them.
func TestPluginStopHoldsItsProcesses(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	sock := filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
	script := `D="$HERDR_PLUGIN_STATE_DIR"
for m in setpgid setsid; do
  env -u TUIOS_PLUGIN_ID -u TUIOS_PLUGIN_TOKEN python3 -I hold.py $m '` + sock + `' "$D" </dev/null >"$D/$m.err" 2>&1 &
done
sleep 120
`
	dir := approvalPlugin(t, base, "e2e.hold", "Hold probe", `"read"`, script)
	if err := os.WriteFile(filepath.Join(dir, "hold.py"), []byte(holdScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "approve", "e2e.hold", "--yes"); err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	pid := func(mode string) int {
		var n int
		_, _ = fmt.Sscan(strings.TrimSpace(waitPluginFile(t, base, "e2e.hold", mode+".pid", "\n")), &n)
		return n
	}
	pgPID, sidPID := pid("setpgid"), pid("setsid")
	t.Cleanup(func() { _ = syscall.Kill(sidPID, syscall.SIGKILL); _ = syscall.Kill(pgPID, syscall.SIGKILL) })
	if out, err := tuiosCLI(t, base, "plugins", "disable", "e2e.hold"); err != nil {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for syscall.Kill(pgPID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child that called setpgid outlived the disable (pid %d)", pgPID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(pluginState(base, "e2e.hold"), "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := waitPluginFile(t, base, "e2e.hold", "setsid.out", "\n")
	savePluginArtifact(t, base, "e2e.hold", "setsid.out")
	if !strings.Contains(got, "forbidden") || !strings.Contains(got, "refused for plugin e2e.hold") {
		t.Errorf("a connection of the stopped plugin was not held to it:\n%s", got)
	}
}
