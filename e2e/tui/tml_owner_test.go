package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tmlOwnerDoc is a surface whose owner is a shell script: r emits rerun with
// the run count, and the script answers with a patch.
const tmlOwnerDoc = `<tml version="1" title="Owner">
  <state>{"runs": 1, "note": "waiting"}</state>
  <col pad="1" gap="1">
    <text>Runs: <b>{state.runs}</b></text>
    <text role="dim">Note: {state.note}</text>
    <row gap="2">
      <button key="r" kind="primary" on-press="emit('rerun', state.runs)">Rerun</button>
      <button key="q" on-press="close('done')">Done</button>
    </row>
  </col>
</tml>
`

// tmlReplacedDoc replaces tmlOwnerDoc and keeps its state.
const tmlReplacedDoc = `<tml version="1" title="Owner">
  <state>{"runs": 0, "note": "replaced"}</state>
  <col pad="1" gap="1">
    <text>Replaced. Runs: <b>{state.runs}</b></text>
    <row gap="2"><button key="q" on-press="close('done')">Done</button></row>
  </col>
</tml>
`

// tmlOwnerScript is the owner: it opens the surface with --events, keeps
// every line it reads, and answers each rerun with a patch that counts the
// run. It writes the surface id to a file for the test.
const tmlOwnerScript = `#!/bin/sh
"$TUIOS" tml show "$DOC" --events -s e2e-tmlown | while IFS= read -r line; do
  printf '%s\n' "$line" >> "$DIR/events.log"
  case "$line" in
    *'"type":"tml_opened"'*)
      printf '%s' "$line" | sed 's/.*"surface":"\([^"]*\)".*/\1/' > "$DIR/id" ;;
    *'"name":"rerun"'*)
      "$TUIOS" tml patch "$(cat "$DIR/id")" '[{"op":"replace","path":"/runs","value":2},{"op":"replace","path":"/note","value":"rerun by the owner"}]' >> "$DIR/patch.log" 2>&1 ;;
  esac
done
echo exited > "$DIR/owner-done"
`

// startShBackground runs a shell script against the suite's daemon, with
// TUIOS naming the binary under test.
func startShBackground(t *testing.T, base, script string, env ...string) <-chan askRun {
	t.Helper()
	pinPreV080Looks(t, base)
	path := filepath.Join(base, "owner.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS="+tuiosBin)
	cmd.Env = append(cmd.Env, env...)
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// The script's pipeline has children that hold the output pipe, so the
	// whole group is killed, and Wait gives up on the pipe soon after.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the owner script: %v", err)
	}
	done := make(chan askRun, 1)
	finished := make(chan struct{})
	go func() {
		err := cmd.Wait()
		done <- askRun{out: out.String(), err: err}
		close(finished)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-finished
	})
	return done
}

// TestTMLOwnerScriptAnswersEvents is the owner loop from end to end: a shell
// script opens a surface with --events, the person presses a button, the
// script reads the emit and answers with tml patch, and the frame shows the
// new values. A subscriber of tml-event hears the same emit. A replace keeps
// the state, a patch with a stale test is refused with a hint, and the close
// is the owner's last event.
//
// Negative controls, in NEGATIVE_CONTROLS.md: emits not sent by the client
// (the script never patches); the owner stream not opened (the script reads
// no events); the patch not pushed (the frame keeps Runs: 1).
func TestTMLOwnerScriptAnswersEvents(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-tmlown", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 100, rows: 30, args: []string{"attach", "e2e-tmlown"}})
	waitWindowCount(t, term, 1, "the first pane")

	subscribed := startCLIBackground(t, base, "subscribe", "--types", "tml-event", "--count", "2")
	doc := writeTMLDoc(t, base, "owner.tml", tmlOwnerDoc)
	owner := startShBackground(t, base, tmlOwnerScript, "DOC="+doc, "DIR="+base)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Runs: 1", "Note: waiting", "Rerun")
	}, uiTimeout); err != nil {
		t.Fatalf("the owner's surface never drew: %v\n%s", err, term.Snapshot())
	}
	idBytes, err := os.ReadFile(filepath.Join(base, "id"))
	if err != nil || len(idBytes) == 0 {
		t.Fatalf("the owner never read its surface id: %v", err)
	}
	id := string(idBytes)
	saveTMLFrame(t, term, "1-owner-open")

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("send r: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Runs: 2", "Note: rerun by the owner")
	}, uiTimeout); err != nil {
		events, _ := os.ReadFile(filepath.Join(base, "events.log"))
		patch, _ := os.ReadFile(filepath.Join(base, "patch.log"))
		t.Fatalf("the owner's patch never reached the frame: %v\nevents:\n%s\npatch:\n%s\n%s", err, events, patch, term.Snapshot())
	}
	saveTMLFrame(t, term, "2-owner-patched")

	// A patch whose test reads a value the state no longer has is refused,
	// and the answer says to read the state again.
	out, err := tuiosCLI(t, base, "tml", "patch", id, `[{"op":"test","path":"/runs","value":1},{"op":"replace","path":"/runs","value":9}]`)
	if err == nil || !strings.Contains(out, "Read the state again") {
		t.Fatalf("a stale patch was not refused with a hint: %v\n%s", err, out)
	}
	got, err := tuiosCLI(t, base, "tml", "get", id)
	if err != nil || !strings.Contains(got, `"runs":2`) {
		t.Fatalf("tml get does not show runs 2: %v\n%s", err, got)
	}

	// A replace with --keep-state draws the new tree with the state as it is.
	replaced := writeTMLDoc(t, base, "replaced.tml", tmlReplacedDoc)
	if out, err := tuiosCLI(t, base, "tml", "replace", id, replaced, "--keep-state"); err != nil {
		t.Fatalf("tml replace: %v\n%s", err, out)
	}
	if err := term.WaitForText("Replaced. Runs: 2", uiTimeout); err != nil {
		t.Fatalf("the replaced tree never drew with the kept state: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "3-owner-replaced")

	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("send q: %v", err)
	}
	select {
	case run := <-owner:
		if run.err != nil {
			t.Fatalf("the owner script failed: %v\n%s", run.err, run.out)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the owner's stream did not end after the close\n%s", term.Snapshot())
	}
	type line struct {
		Type string `json:"type"`
		Seq  uint64 `json:"seq"`
		TML  *struct {
			Name   string          `json:"name"`
			Value  json.RawMessage `json:"value"`
			Reason string          `json:"reason"`
		} `json:"tml"`
	}
	data, _ := os.ReadFile(filepath.Join(base, "events.log"))
	var names []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var ev line
		if json.Unmarshal([]byte(l), &ev) != nil || ev.TML == nil {
			continue
		}
		if ev.Seq == 0 {
			t.Errorf("an event carries no seq: %s", l)
		}
		names = append(names, ev.TML.Name+"="+string(ev.TML.Value)+":"+ev.TML.Reason)
	}
	if want := []string{"rerun=1:", `close="done":closed`}; strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("the owner read events %v, want %v\n%s", names, want, data)
	}
	select {
	case run := <-subscribed:
		if !strings.Contains(run.out, `"name":"rerun"`) || !strings.Contains(run.out, `"name":"close"`) {
			t.Fatalf("the subscriber did not hear the surface's events:\n%s", run.out)
		}
	case <-time.After(uiTimeout):
		t.Fatalf("the subscriber never got its events")
	}
	waitWindowCount(t, term, 1, "after the surface closed")
	alive(t, term, "after the owner's surface closed")
}
