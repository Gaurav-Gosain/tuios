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

// A live upgrade asked for from inside a pane waits for the person to say yes
// in a dialog on the attached clients (internal/session/upgrade_confirm.go,
// internal/app/upgrade_confirm.go). Only a key pressed at a client answers
// it, No is the default, and anything but Yes refuses with the refusal a pane
// always got. Each test writes handoff.json and the client's screens under
// artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff", "A pane's
// upgrade confirmed at a client").

const (
	upgradeDialogTitle = "Upgrade the daemon?"
	upgradeRefusal     = "daemon-upgrade is for the person"
	keyDown            = "\x1b[B"
	keyUp              = "\x1b[A"
	keyEnter           = "\r"
	keyEsc             = "\x1b"
)

// confirmRig is a daemon with a session of two shell panes: agent, which
// asks for the upgrade, and other, a second pane that tries to answer.
type confirmRig struct {
	base, sess   string
	agent, other string
	second       string
	img          daemonImage
	art          string
	runs         int
}

func newConfirmRig(t *testing.T, sess string, env ...string) *confirmRig {
	t.Helper()
	r := &confirmRig{base: t.TempDir(), sess: sess, art: artifactDir(t)}
	startHandoffDaemon(t, r.base, env...)
	r.agent = newCommandWindow(t, r.base, sess, "agent", false, "sh")
	r.other = newCommandWindow(t, r.base, sess, "other", false, "sh")
	r.second = copyTuios(t)
	r.img = helloImage(t, r.base)
	return r
}

// attach starts a client on the session and waits until it shows the panes.
func (r *confirmRig) attach(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, r.base, startOpts{args: []string{"attach", r.sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	return term
}

// paneUpgrade has the agent pane run tuios daemon upgrade, and returns a
// function that waits for its exit status and the time it took.
func (r *confirmRig) paneUpgrade(t *testing.T) func() (string, time.Duration) {
	t.Helper()
	r.runs++
	tag := "UP" + strconv.Itoa(r.runs) + "_EXIT"
	started := time.Now()
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.agent,
		"text": tuiosBin + " daemon upgrade --binary " + r.second + "; echo " + tag + "=$?\n"})
	return func() (string, time.Duration) {
		m := waitHandoffPane(t, r.base, r.sess, r.agent, regexp.MustCompile(tag+`=(\d+)`), 150*time.Second)
		return m[1], time.Since(started)
	}
}

// waitDialog waits for the upgrade dialog on term, saves it, and waits past
// the moment it ignores keys for.
func (r *confirmRig) waitDialog(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.WaitForText(upgradeDialogTitle, 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: no upgrade dialog on the client (%s): %v\n%s", name, err, term.Snapshot())
	}
	saveArtifact(t, term, r.art, "dialog-"+name)
	time.Sleep(700 * time.Millisecond)
}

// waitNoDialog waits until the dialog is gone from term.
func waitNoDialog(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), upgradeDialogTitle) }, 15*time.Second); err != nil {
		t.Fatalf("ASSERTION: the dialog stayed open after %s: %v\n%s", what, err, term.Snapshot())
	}
}

func pressKeys(t *testing.T, term *tuitest.Terminal, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// dialogShows reports whether the dialog on screen holds s, read across its
// wrapped lines with the borders and every space taken out. Only the text
// from the dialog's title to its note counts, since the pane behind it shows
// the command line, path and all.
func dialogShows(screen, s string) bool {
	squash := func(v string) string {
		return strings.Map(func(c rune) rune {
			if c == ' ' || c == '\n' || c == '\t' || strings.ContainsRune("│╭╮╰╯─┃┏┓┗┛━", c) {
				return -1
			}
			return c
		}, v)
	}
	all := squash(screen)
	start := strings.Index(all, squash(upgradeDialogTitle))
	if start < 0 {
		return false
	}
	end := strings.Index(all[start:], squash("Down or Tab moves to Yes"))
	if end < 0 {
		return false
	}
	return strings.Contains(all[start:start+end], squash(s))
}

// unchanged fails the test when the daemon is not the image it was.
func (r *confirmRig) unchanged(t *testing.T, what string) daemonImage {
	t.Helper()
	now := helloImage(t, r.base)
	if now.Instance != r.img.Instance || now.PID != r.img.PID {
		t.Fatalf("ASSERTION: %s upgraded the daemon (%+v then %+v)", what, r.img, now)
	}
	return now
}

// refused fails the test when the pane's upgrade did not end with the
// refusal for the person.
func (r *confirmRig) refused(t *testing.T, code, what string) []string {
	t.Helper()
	lines := mustScrollback(t, r.base, r.sess, r.agent)
	if code == "0" || !screenLinesHave(lines, upgradeRefusal) {
		t.Errorf("ASSERTION: %s did not refuse the pane's upgrade as one for the person (exit %s):\n%s", what, code, strings.Join(lines, "\n"))
	}
	return lines
}

// TestPaneUpgradeConfirmedAtTheClient: a pane asks for an upgrade, the
// attached client shows the dialog with the binary's full path, its version
// and the pane's name, and Down then Enter, pressed at the client, says yes.
// The upgrade goes through: the daemon keeps its pid, runs the copy, and the
// pane's command exits 0.
func TestPaneUpgradeConfirmedAtTheClient(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-yes")
	term := r.attach(t)
	done := r.paneUpgrade(t)
	r.waitDialog(t, term, "yes")
	screen := term.Screen().Text()
	shows := map[string]bool{
		"path":    dialogShows(screen, r.second),
		"version": dialogShows(screen, "version "+r.img.Version),
		"pane":    dialogShows(screen, "agent (pane "+r.agent[:min(8, len(r.agent))]+" in session "+r.sess+") asks"),
	}
	pressKeys(t, term, keyDown, keyEnter)
	code, took := done()
	after := awaitNewImage(t, r.base, r.img, 30*time.Second)
	exe := executableOf(t, after.PID)
	writeHandoffArtifact(t, map[string]any{"exit": code, "seconds": took.Seconds(), "dialog": screen, "shows": shows,
		"before": r.img, "after": after, "executable_after": exe, "screen": mustScrollback(t, r.base, r.sess, r.agent)})

	for what, ok := range shows {
		if !ok {
			t.Errorf("ASSERTION: the dialog does not show the %s:\n%s", what, screen)
		}
	}
	if code != "0" {
		t.Errorf("ASSERTION: the pane's confirmed upgrade exited %s", code)
	}
	if after.PID != r.img.PID || exe != r.second {
		t.Errorf("ASSERTION: after a confirmed upgrade the daemon is %+v running %s, from %+v", after, exe, r.img)
	}
}

// TestPaneUpgradeRefusedWithoutYes: Enter on the default row, Esc, Enter
// after moving to Yes and back to No, and Up then Enter (a shell's "run
// that again", which a toggle would have turned into Yes) each refuse the pane's upgrade
// with the refusal for the person, close the dialog, and leave the daemon as
// it was.
func TestPaneUpgradeRefusedWithoutYes(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-no")
	term := r.attach(t)
	results := map[string]string{}
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"default", []string{keyEnter}},
		{"esc", []string{keyEsc}},
		{"no", []string{keyDown, keyUp, keyEnter}},
		{"up", []string{keyUp, keyEnter}},
	} {
		done := r.paneUpgrade(t)
		r.waitDialog(t, term, tc.name)
		pressKeys(t, term, tc.keys...)
		code, _ := done()
		waitNoDialog(t, term, tc.name)
		r.unchanged(t, tc.name)
		lines := r.refused(t, code, tc.name)
		if said := strings.Join(strings.Fields(strings.Join(lines, "")), ""); !strings.Contains(said, "ThepersonansweredNo") {
			t.Errorf("ASSERTION: the refusal after %s does not say the person answered No:\n%s", tc.name, strings.Join(lines, "\n"))
		}
		results[tc.name] = code
	}
	writeHandoffArtifact(t, map[string]any{"exits": results, "image": r.img, "screen": mustScrollback(t, r.base, r.sess, r.agent)})
}

// TestPaneUpgradeRefusesAFileChangedAfterTheDialog: while the dialog shows
// the copy, its last byte is written over in place with the same byte and
// its modification time set back, so the path, the inode, the size and the
// modification time are what the person was shown. Only the change time
// moved. Yes must then refuse the upgrade, since the file the person
// confirmed is not the one that would run.
func TestPaneUpgradeRefusesAFileChangedAfterTheDialog(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-changed")
	term := r.attach(t)
	done := r.paneUpgrade(t)
	r.waitDialog(t, term, "changed")
	info, err := os.Stat(r.second)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(r.second, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(last, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chtimes(r.second, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	pressKeys(t, term, keyDown, keyEnter)
	code, _ := done()
	waitNoDialog(t, term, "a Yes for a changed file")
	r.unchanged(t, "a Yes for a file changed after the dialog opened")
	lines := mustScrollback(t, r.base, r.sess, r.agent)
	writeHandoffArtifact(t, map[string]any{"exit": code, "screen": lines})
	if said := strings.Join(strings.Fields(strings.Join(lines, "")), ""); code == "0" || !strings.Contains(said, "waschangedaftertheupgradetoitwasconfirmed") {
		t.Errorf("ASSERTION: a Yes for a file changed in place after the dialog opened was not refused as changed (exit %s):\n%s", code, strings.Join(lines, "\n"))
	}
}

// TestPaneUpgradeTimesOut: nobody answers, and once the wait (3 s here, under
// TUIOS_TEST_UPGRADE_CONFIRM_MS) runs out the upgrade is refused and the
// dialog closes on the client.
func TestPaneUpgradeTimesOut(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-wait", "TUIOS_TEST_UPGRADE_CONFIRM_MS=3000")
	term := r.attach(t)
	done := r.paneUpgrade(t)
	r.waitDialog(t, term, "waiting")
	code, took := done()
	waitNoDialog(t, term, "the wait ran out")
	r.unchanged(t, "an unanswered dialog")
	lines := r.refused(t, code, "the wait running out")
	if said := strings.Join(strings.Fields(strings.Join(lines, "")), ""); !strings.Contains(said, "Nobodyansweredthedialog") {
		t.Errorf("ASSERTION: the refusal after the wait does not say nobody answered:\n%s", strings.Join(lines, "\n"))
	}
	writeHandoffArtifact(t, map[string]any{"exit": code, "seconds": took.Seconds(), "screen": lines})
	if took < 3*time.Second {
		t.Errorf("ASSERTION: the upgrade ended %s after it asked, before the 3 s wait ran out", took)
	}
}

// TestPaneUpgradeTakesOnlyKeysAtTheClient: while the dialog is up, nothing
// that is not a key pressed at the client answers it: the other pane printing
// the keys of Yes, send-keys and a tape's Press of j and Enter through the
// client, a bracketed paste of them, and answer-ask called from the other
// pane. The dialog stays, the daemon is unchanged, and Esc then refuses. Each
// attempt ends in Enter, so a single one that got through would answer the
// dialog and close it.
func TestPaneUpgradeTakesOnlyKeysAtTheClient(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-keys")
	term := r.attach(t)
	done := r.paneUpgrade(t)
	r.waitDialog(t, term, "keys")
	still := func(what string) {
		t.Helper()
		time.Sleep(time.Second)
		r.unchanged(t, what)
		if !strings.Contains(term.Screen().Text(), upgradeDialogTitle) {
			t.Fatalf("ASSERTION: the dialog closed under %s:\n%s", what, term.Snapshot())
		}
	}
	attempts := map[string]string{}

	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.other,
		"text": `printf 'j\r\033[B\rYes\r\n'` + "\n"})
	still("the other pane printing j, Down, Enter and Yes")

	out, err := tuiosCLI(t, r.base, "send-keys", "-s", r.sess, "j Enter")
	attempts["send-keys"] = out + errString(err)
	still("send-keys of j and Enter")

	tape := filepath.Join(t.TempDir(), "yes.tape")
	if err := os.WriteFile(tape, []byte("Press j Enter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = tuiosCLI(t, r.base, "tape", "exec", "-s", r.sess, tape)
	attempts["tape"] = out + errString(err)
	still("a tape's Press of j and Enter")

	pressKeys(t, term, "\x1b[200~j\r\x1b[201~")
	still("a paste of j and Enter")

	items := mustCall(t, r.base, "list-attention", map[string]any{})
	var request string
	if list, ok := items["items"].([]any); ok {
		for _, raw := range list {
			if it, ok := raw.(map[string]any); ok && it["upgrade"] != nil {
				request, _ = it["request_id"].(string)
			}
		}
	}
	if request == "" {
		t.Fatalf("list-attention shows no upgrade ask: %v", items)
	}
	answerOut := filepath.Join(t.TempDir(), "answer")
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.other,
		"text": tuiosBin + ` call answer-ask '{"request_id":"` + request + `","answer":"Yes","human_nonce":"0123456789abcdef0123456789abcdef"}' >'` + answerOut + `' 2>&1; echo ANSWER_EXIT=$?` + "\n"})
	m := waitHandoffPane(t, r.base, r.sess, r.other, regexp.MustCompile(`ANSWER_EXIT=(\d+)`), 30*time.Second)
	answered, _ := os.ReadFile(answerOut)
	attempts["answer-ask"] = string(answered)
	still("answer-ask from the other pane")
	if m[1] == "0" {
		t.Errorf("ASSERTION: answer-ask from a pane was taken (exit 0):\n%s", answered)
	}
	saveArtifact(t, term, r.art, "dialog-after-attempts")

	pressKeys(t, term, keyEsc)
	code, _ := done()
	waitNoDialog(t, term, "Esc")
	r.unchanged(t, "Esc after the attempts")
	lines := r.refused(t, code, "Esc after the attempts")
	writeHandoffArtifact(t, map[string]any{"exit": code, "attempts": attempts, "answer_ask_exit": m[1], "screen": lines})
}

// TestPaneUpgradeRefusesAnAnswerFromTheGUIBridge: a renderer behind
// tuios gui-bridge holds an attached client's nonce, since the bridge adds it
// to every person verb. Its answer-ask with Yes must not answer the dialog,
// and neither must one that also claims upgrade_dialog, which the bridge
// drops. The dialog stays, the daemon is unchanged, and Esc at the client
// then refuses.
func TestPaneUpgradeRefusesAnAnswerFromTheGUIBridge(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-bridge")
	term := r.attach(t)
	b := startBridgeWith(t, r.base, bridgeOpts{args: []string{"--session", r.sess}, cols: 120, rows: 40, keepDaemon: true, name: r.sess})
	done := r.paneUpgrade(t)
	r.waitDialog(t, term, "bridge")
	items := mustCall(t, r.base, "list-attention", map[string]any{})
	var request string
	if list, ok := items["items"].([]any); ok {
		for _, raw := range list {
			if it, ok := raw.(map[string]any); ok && it["upgrade"] != nil {
				request, _ = it["request_id"].(string)
			}
		}
	}
	if request == "" {
		t.Fatalf("list-attention shows no upgrade ask: %v", items)
	}
	answers := map[string]wireVerbResult{}
	for name, params := range map[string]map[string]any{
		"plain":          {"request_id": request, "answer": "Yes"},
		"upgrade_dialog": {"request_id": request, "answer": "Yes", "upgrade_dialog": true},
	} {
		res := b.verb("answer-ask", params)
		answers[name] = res
		time.Sleep(time.Second)
		r.unchanged(t, "the bridge's answer-ask ("+name+")")
		if res.OK || res.Code != "not_human" {
			t.Errorf("ASSERTION: the bridge's answer-ask (%s) was not refused as not_human: ok=%v code=%s %s", name, res.OK, res.Code, res.Error)
		}
		if !strings.Contains(term.Screen().Text(), upgradeDialogTitle) {
			t.Fatalf("ASSERTION: the dialog closed under the bridge's answer-ask (%s):\n%s", name, term.Snapshot())
		}
	}
	saveArtifact(t, term, r.art, "dialog-after-bridge")

	pressKeys(t, term, keyEsc)
	code, _ := done()
	waitNoDialog(t, term, "Esc")
	r.unchanged(t, "Esc after the bridge's answers")
	lines := r.refused(t, code, "Esc after the bridge's answers")
	writeHandoffArtifact(t, map[string]any{"exit": code, "answers": answers, "screen": lines})
}

// TestPaneUpgradeFirstAnswerWins: two clients show the dialog. One says no;
// the dialog closes on the other at once, and a yes typed there right after
// upgrades nothing.
func TestPaneUpgradeFirstAnswerWins(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-two")
	first := r.attach(t)
	second := r.attach(t)
	done := r.paneUpgrade(t)
	r.waitDialog(t, first, "first")
	r.waitDialog(t, second, "second")
	pressKeys(t, first, keyEsc)
	pressKeys(t, second, keyDown, keyEnter)
	waitNoDialog(t, second, "the first client answered")
	saveArtifact(t, second, r.art, "second-after-answer")
	code, _ := done()
	time.Sleep(time.Second)
	r.unchanged(t, "a yes after the first client's no")
	lines := r.refused(t, code, "the first client's no")
	writeHandoffArtifact(t, map[string]any{"exit": code, "screen": lines})
}

// TestPluginUpgradeRefusedWithoutADialog: a plugin's command asks for an
// upgrade with a client attached. It is refused at once, with the refusal for
// the person, and no dialog opens.
func TestPluginUpgradeRefusedWithoutADialog(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(t.TempDir(), "upgrader")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "plugin")
	second := copyTuios(t)
	script := tuiosBin + " daemon upgrade --binary " + second + " >" + out + ".out 2>&1; echo $? >" + out + ".code"
	manifest := "id = \"e2e.upgrader\"\nname = \"E2E upgrader\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.5\"\n\n" +
		"[[actions]]\nid = \"up\"\ntitle = \"Upgrade\"\ncontexts = [\"pane\"]\ncommand = [\"sh\", \"-c\", " + strconv.Quote(script) + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[plugins]\nenabled = [\"e2e.upgrader\"]\ndirs = ["+strconv.Quote(dir)+"]\n")
	startHandoffDaemon(t, base)
	const sess = "e2e-confirm-plugin"
	newCommandWindow(t, base, sess, "anchor", false, "sh")
	img := helloImage(t, base)
	term := startIn(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}

	started := time.Now()
	if text, err := handoffCLI(t, base, 20*time.Second, "plugins", "run", "e2e.upgrader", "up"); err != nil {
		t.Fatalf("plugins run: %v\n%s", err, text)
	}
	sawDialog := false
	var code string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if strings.Contains(term.Screen().Text(), upgradeDialogTitle) {
			sawDialog = true
			break
		}
		if b, err := os.ReadFile(out + ".code"); err == nil && strings.HasSuffix(string(b), "\n") {
			code = strings.TrimSpace(string(b))
			break
		}
	}
	took := time.Since(started)
	said, _ := os.ReadFile(out + ".out")
	saveArtifact(t, term, artifactDir(t), "client")
	after := helloImage(t, base)
	writeHandoffArtifact(t, map[string]any{"exit": code, "seconds": took.Seconds(), "output": string(said), "saw_dialog": sawDialog, "before": img, "after": after})

	if sawDialog {
		t.Fatalf("ASSERTION: a plugin's upgrade opened the dialog:\n%s", term.Snapshot())
	}
	if code == "" {
		t.Fatalf("ASSERTION: the plugin's upgrade did not end within 60 s")
	}
	if after.Instance != img.Instance {
		t.Fatalf("ASSERTION: a plugin upgraded the daemon (%+v then %+v)", img, after)
	}
	if code == "0" || !strings.Contains(string(said), upgradeRefusal) {
		t.Errorf("ASSERTION: the plugin's upgrade was not refused as one for the person (exit %s):\n%s", code, said)
	}
}

// TestOutsideUpgradeNeedsNoDialog: with a client attached, an upgrade from
// outside every pane goes through, and the client never shows the dialog
// before it is told to attach again.
func TestOutsideUpgradeNeedsNoDialog(t *testing.T) {
	r := newConfirmRig(t, "e2e-confirm-outside")
	term := r.attach(t)
	stop := make(chan struct{})
	saw := make(chan bool, 1)
	go func() {
		seen := false
		for {
			select {
			case <-stop:
				saw <- seen
				return
			case <-time.After(20 * time.Millisecond):
				if strings.Contains(term.Screen().Text(), upgradeDialogTitle) {
					seen = true
				}
			}
		}
	}()
	res, out, err := daemonUpgrade(t, r.base, r.second)
	close(stop)
	sawDialog := <-saw
	after := helloImage(t, r.base)
	writeHandoffArtifact(t, map[string]any{"result": res, "output": out, "error": errString(err), "saw_dialog": sawDialog, "before": r.img, "after": after})
	if err != nil || after.Instance == r.img.Instance || after.PID != r.img.PID {
		t.Errorf("ASSERTION: the upgrade from outside every pane did not go through (%+v then %+v): %v\n%s", r.img, after, err, out)
	}
	if sawDialog {
		t.Errorf("ASSERTION: an upgrade from outside every pane opened the dialog")
	}
}
