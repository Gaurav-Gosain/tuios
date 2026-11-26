package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A change to which plugins run, asked for from inside a pane, waits for the
// person to say yes in the dialog a pane's live upgrade uses, on the attached
// clients (internal/session/plugin_confirm.go, internal/app/upgrade_confirm.go).
// Only a key pressed at a client answers it, No is the default, and anything
// but Yes refuses with a message that says what to do.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A pane's plugin change
// confirmed at a client").

const (
	trustDialogTitle = "Install the plugin Trust probe?"
	trustRefusal     = "tuios plugins install is for the person"
)

const trustManifest = `api = 1
id = "e2e.trust"
name = "Trust probe"
version = "1.0.0"

[permissions]
grants = ["ui"]

[[process]]
id = "hello"
run = "action"
title = "Hello"
command = ["sh", "-c", "echo hello"]
`

// trustPlugin writes the test plugin's folder.
func trustPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tuios-plugin.toml"), []byte(trustManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// paneInstall has the agent pane run tuios plugins install on dir, and
// returns a function that waits for its exit status and the time it took.
func (r *confirmRig) paneInstall(t *testing.T, dir string) func() (string, time.Duration) {
	t.Helper()
	r.runs++
	tag := "PI" + strconv.Itoa(r.runs) + "_EXIT"
	started := time.Now()
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.agent,
		"text": tuiosBin + " plugins install " + dir + " --yes; echo " + tag + "=$?\n"})
	return func() (string, time.Duration) {
		m := waitHandoffPane(t, r.base, r.sess, r.agent, regexp.MustCompile(tag+`=(\d+)`), 200*time.Second)
		return m[1], time.Since(started)
	}
}

// waitTrustDialog waits for the plugin dialog on term, saves it, and waits
// past the moment it ignores keys for.
func (r *confirmRig) waitTrustDialog(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.WaitForText(trustDialogTitle, 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: no plugin dialog on the client (%s): %v\n%s", name, err, term.Snapshot())
	}
	saveArtifact(t, term, r.art, "trust-dialog-"+name)
	pause(t, "the dialog ignores keys for its first moment", 700*time.Millisecond)
}

func waitNoTrustDialog(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), trustDialogTitle) }, 15*time.Second); err != nil {
		t.Fatalf("ASSERTION: the plugin dialog stayed open after %s: %v\n%s", what, err, term.Snapshot())
	}
}

// trustInstalled reports whether tuios plugins list, run outside every pane,
// shows the plugin, and whether it is on.
func trustInstalled(t *testing.T, base string) (listed, on bool) {
	t.Helper()
	out, _ := tuiosCLI(t, base, "plugins", "list")
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "e2e.trust" {
			return true, f[1] == "on"
		}
	}
	return false, false
}

// paneSaid is the pane's scrollback with every space taken out, so a
// message wrapped at the pane's width still reads as one.
func paneSaid(t *testing.T, r *confirmRig) (string, []string) {
	t.Helper()
	lines := mustScrollback(t, r.base, r.sess, r.agent)
	return strings.Join(strings.Fields(strings.Join(lines, "")), ""), lines
}

// squashed is s with every space and border character taken out, so text
// the dialog wraps reads as one line.
func squashed(s string) string {
	return strings.Map(func(c rune) rune {
		if c == ' ' || c == '\n' || c == '\t' || strings.ContainsRune("│╭╮╰╯─┃┏┓┗┛━", c) {
			return -1
		}
		return c
	}, s)
}

// TestPanePluginInstallConfirmedAtTheClient: a pane installs a local plugin,
// the attached client shows the dialog with the plugin, the pane that asks,
// what the plugin runs and its grants in the approval dialog's words, and
// Down then Enter at the client says yes. The install goes through, and the
// plugin is enabled.
func TestPanePluginInstallConfirmedAtTheClient(t *testing.T) {
	r := newConfirmRig(t, "e2e-trust-yes")
	term := r.attach(t)
	dir := trustPlugin(t)
	done := r.paneInstall(t, dir)
	r.waitTrustDialog(t, term, "yes")
	screen := squashed(term.Screen().Text())
	for what, want := range map[string]string{
		"pane":   "agent(pane" + r.agent[:min(8, len(r.agent))] + "insessione2e-trust-yes)askstoinstallthisplugin",
		"id":     "e2e.trust1.0.0",
		"runs":   "Itrunsonitsown,withyourrights:",
		"grants": "Itcanusetuioswiththesegrants:",
		"ui":     "uiShows",
		"note":   "Grantsarenotasandbox.",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("ASSERTION: the dialog does not show the %s (%q):\n%s", what, want, term.Snapshot())
		}
	}
	pressKeys(t, term, keyDown, keyEnter)
	code, _ := done()
	waitNoTrustDialog(t, term, "Yes")
	said, lines := paneSaid(t, r)
	if code != "0" || !strings.Contains(said, "Thepersonsaidyes.") {
		t.Errorf("ASSERTION: the pane's confirmed install exited %s:\n%s", code, strings.Join(lines, "\n"))
	}
	if listed, on := trustInstalled(t, r.base); !listed || !on {
		out, _ := tuiosCLI(t, r.base, "plugins", "list")
		t.Errorf("ASSERTION: after a Yes the plugin is listed=%v on=%v:\n%s", listed, on, out)
	}
}

// TestPanePluginInstallRefusedWithoutYes: Enter on the default row and Esc
// each refuse the install with a message that says the person answered No,
// and nothing is installed.
func TestPanePluginInstallRefusedWithoutYes(t *testing.T) {
	r := newConfirmRig(t, "e2e-trust-no")
	term := r.attach(t)
	dir := trustPlugin(t)
	for _, tc := range []struct {
		name string
		keys []string
	}{{"enter", []string{keyEnter}}, {"esc", []string{keyEsc}}} {
		done := r.paneInstall(t, dir)
		r.waitTrustDialog(t, term, tc.name)
		pressKeys(t, term, tc.keys...)
		code, _ := done()
		waitNoTrustDialog(t, term, tc.name)
		said, lines := paneSaid(t, r)
		if code == "0" || !strings.Contains(said, squashed(trustRefusal)) || !strings.Contains(said, "ThepersonansweredNo") {
			t.Errorf("ASSERTION: %s did not refuse the install as one the person said no to (exit %s):\n%s", tc.name, code, strings.Join(lines, "\n"))
		}
		if listed, _ := trustInstalled(t, r.base); listed {
			t.Fatalf("ASSERTION: after %s the plugin is installed", tc.name)
		}
	}
}

// TestPanePluginConfirmTakesOnlyKeysAtTheClient: while the dialog is up,
// nothing that is not a key pressed at the client answers it: the other
// pane printing the keys of Yes, send-keys of Down and Enter through the
// client, a bracketed paste of them, and answer-ask with Yes called from the
// other pane, marked as a key in the dialog. The dialog stays, nothing is
// installed, and Esc then refuses.
func TestPanePluginConfirmTakesOnlyKeysAtTheClient(t *testing.T) {
	r := newConfirmRig(t, "e2e-trust-keys")
	term := r.attach(t)
	dir := trustPlugin(t)
	done := r.paneInstall(t, dir)
	r.waitTrustDialog(t, term, "keys")
	still := func(what string) {
		t.Helper()
		pause(t, "give a stray answer the time to land", time.Second)
		if listed, _ := trustInstalled(t, r.base); listed {
			t.Fatalf("ASSERTION: %s installed the plugin", what)
		}
		screen := term.Screen().Text()
		if !strings.Contains(screen, trustDialogTitle) {
			t.Fatalf("ASSERTION: the dialog closed under %s:\n%s", what, term.Snapshot())
		}
		if strings.Contains(screen, "Sending your answer") {
			t.Fatalf("ASSERTION: the dialog took %s as an answer:\n%s", what, term.Snapshot())
		}
	}
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.other,
		"text": `printf '\033[B\ry\rYes\r\n'` + "\n"})
	still("the other pane printing Down, Enter, y and Yes")

	_, _ = tuiosCLI(t, r.base, "send-keys", "-s", r.sess, "Down Enter")
	still("send-keys of Down and Enter")

	pressKeys(t, term, "\x1b[200~\x1b[B\r\x1b[201~")
	still("a paste of Down and Enter")

	items := mustCall(t, r.base, "list-attention", map[string]any{})
	var request string
	if list, ok := items["items"].([]any); ok {
		for _, raw := range list {
			if it, ok := raw.(map[string]any); ok && it["plugin"] != nil {
				request, _ = it["request_id"].(string)
			}
		}
	}
	if request == "" {
		t.Fatalf("list-attention shows no plugin ask: %v", items)
	}
	answerOut := filepath.Join(t.TempDir(), "answer")
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.other,
		"text": tuiosBin + ` call answer-ask '{"request_id":"` + request + `","answer":"Yes","upgrade_dialog":true,"human_nonce":"0123456789abcdef0123456789abcdef"}' >'` + answerOut + `' 2>&1; echo ANSWER_EXIT=$?` + "\n"})
	m := waitHandoffPane(t, r.base, r.sess, r.other, regexp.MustCompile(`ANSWER_EXIT=(\d+)`), 30*time.Second)
	answered, _ := os.ReadFile(answerOut)
	still("answer-ask from the other pane")
	if m[1] == "0" {
		t.Errorf("ASSERTION: answer-ask from a pane was taken (exit 0):\n%s", answered)
	}
	saveArtifact(t, term, r.art, "trust-dialog-after-attempts")

	pressKeys(t, term, keyEsc)
	code, _ := done()
	waitNoTrustDialog(t, term, "Esc")
	if code == "0" {
		t.Errorf("ASSERTION: Esc after the attempts let the install through")
	}
}

// TestPanePluginConfirmTimesOut: nobody answers, the wait runs out, and the
// install is refused with a message that says so.
func TestPanePluginConfirmTimesOut(t *testing.T) {
	r := newConfirmRig(t, "e2e-trust-wait", knob("plugin_confirm_ms=3000"))
	term := r.attach(t)
	done := r.paneInstall(t, trustPlugin(t))
	r.waitTrustDialog(t, term, "waiting")
	code, took := done()
	waitNoTrustDialog(t, term, "the wait ran out")
	said, lines := paneSaid(t, r)
	if code == "0" || !strings.Contains(said, "Nobodyansweredthedialog") {
		t.Errorf("ASSERTION: the refusal after the wait does not say nobody answered (exit %s):\n%s", code, strings.Join(lines, "\n"))
	}
	if took < 3*time.Second {
		t.Errorf("ASSERTION: the install ended %s after it asked, before the 3 s wait ran out", took)
	}
	if listed, _ := trustInstalled(t, r.base); listed {
		t.Errorf("ASSERTION: an unanswered dialog installed the plugin")
	}
}

// TestPanePluginConfirmNeedsAClient: with no client attached, the install
// is refused at once with a message that says to attach a client or to run
// the command outside tuios.
func TestPanePluginConfirmNeedsAClient(t *testing.T) {
	r := newConfirmRig(t, "e2e-trust-alone")
	code, took := r.paneInstall(t, trustPlugin(t))()
	said, lines := paneSaid(t, r)
	if code == "0" || !strings.Contains(said, "Noclientisattachedtoshowthedialog.Runthecommandfromaterminaloutsidetuios,orattachaclientandrunitagain") {
		t.Errorf("ASSERTION: with no client the install was not refused with the message (exit %s):\n%s", code, strings.Join(lines, "\n"))
	}
	if took > 30*time.Second {
		t.Errorf("ASSERTION: the refusal took %s, as if it waited for an answer", took)
	}
}
