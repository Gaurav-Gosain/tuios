package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// `tuios hosts sync --local --restart`, the shape of `just deploy`, run from
// a real pane of this machine's daemon. The daemon may only be upgraded live,
// since a restart would end the pane the command runs in, and from a pane the
// upgrade waits for the person's Yes in the dialog on an attached client
// (internal/session/upgrade_confirm.go). Each run writes the sync's JSON and
// stderr, and each test writes handoff.json and the client's screens under
// artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff", "hosts
// sync from a pane, confirmed at a client").

const paneSyncSession = "work"

// paneSyncRig is this machine: a live-capable daemon of the old version, the
// tuios on PATH that sync replaces, and a session with a shell pane that runs
// the sync and a sleeper whose program a restart would end.
type paneSyncRig struct {
	base, binDir, local, next string
	agent                     string
	img                       daemonImage
	panes                     map[string]int
	art                       string
	runs                      int
}

func newPaneSyncRig(t *testing.T) *paneSyncRig {
	t.Helper()
	r := &paneSyncRig{next: syncNextBinary(t), art: artifactDir(t)}
	r.base = newSyncFleet(t, nil, nil).here
	r.binDir = filepath.Join(r.base, "localbin")
	r.local = filepath.Join(r.binDir, "tuios")
	installCopy(t, tuiosBin, r.local)
	startHandoffDaemon(t, r.base)
	r.agent = newCommandWindow(t, r.base, paneSyncSession, "agent", false, "sh")
	newCommandWindow(t, r.base, paneSyncSession, "sleeper", false, "sleep", "600")
	r.img, r.panes = helloImage(t, r.base), panePIDs(t, r.base, paneSyncSession)
	return r
}

func (r *paneSyncRig) attach(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, r.base, startOpts{args: []string{"attach", paneSyncSession}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	return term
}

// paneSyncRun is one sync run from the agent pane.
type paneSyncRun struct {
	Code    string        `json:"exit"`
	Seconds float64       `json:"seconds"`
	Row     syncResultRow `json:"row"`
	Stderr  string        `json:"stderr"`
}

// sync has the agent pane run the sync, and returns a function that waits
// for it to exit and reads its row.
func (r *paneSyncRig) sync(t *testing.T) func() paneSyncRun {
	t.Helper()
	r.runs++
	name := "sync" + strconv.Itoa(r.runs)
	out, errOut := filepath.Join(r.art, name+".json"), filepath.Join(r.art, name+".stderr")
	tag := strings.ToUpper(name) + "_EXIT"
	started := time.Now()
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": paneSyncSession, "window": r.agent,
		"text": "PATH=" + r.binDir + ":/usr/bin:/bin " + r.local + " hosts sync --local --binary " + r.next +
			" --restart --yes --json >" + out + " 2>" + errOut + "; echo " + tag + "=$?\n"})
	return func() paneSyncRun {
		m := waitHandoffPane(t, r.base, paneSyncSession, r.agent, regexp.MustCompile(tag+`=(\d+)`), 240*time.Second)
		run := paneSyncRun{Code: m[1], Seconds: time.Since(started).Seconds()}
		data, _ := os.ReadFile(out)
		said, _ := os.ReadFile(errOut)
		run.Stderr = string(said)
		var rep struct {
			Hosts []syncResultRow `json:"hosts"`
		}
		if err := json.Unmarshal(data, &rep); err != nil || len(rep.Hosts) != 1 {
			t.Fatalf("read the JSON of %s (exit %s): %v\n%s\n%s", name, run.Code, err, data, said)
		}
		run.Row = rep.Hosts[0]
		return run
	}
}

// waitDialog waits for the upgrade dialog on term and past the moment it
// ignores keys for.
func (r *paneSyncRig) waitDialog(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.WaitForText(upgradeDialogTitle, 90*time.Second); err != nil {
		t.Fatalf("ASSERTION: no upgrade dialog on the client (%s): %v\n%s", name, err, term.Snapshot())
	}
	saveArtifact(t, term, r.art, "dialog-"+name)
	time.Sleep(700 * time.Millisecond)
}

// TestHostsSyncInAPaneUpgradesLiveAfterYes: sync from a pane installs the new
// binary and asks for the upgrade in the dialog. Down then Enter at the
// client says Yes, and the daemon is upgraded live: the same pid, the new
// version, running the binary sync installed, and every pane's program kept.
func TestHostsSyncInAPaneUpgradesLiveAfterYes(t *testing.T) {
	r := newPaneSyncRig(t)
	term := r.attach(t)
	done := r.sync(t)
	r.waitDialog(t, term, "yes")
	pressKeys(t, term, keyDown, keyEnter)
	run := done()
	after := awaitNewImage(t, r.base, r.img, 30*time.Second)
	exe := executableOf(t, after.PID)
	afterPanes := panePIDs(t, r.base, paneSyncSession)
	writeHandoffArtifact(t, map[string]any{"run": run, "before": r.img, "after": after, "executable_after": exe,
		"panes_before": r.panes, "panes_after": afterPanes})

	if run.Code != "0" || !run.Row.Installed || !sameFile(run.Row.InstallPath, r.local) {
		t.Fatalf("ASSERTION: sync did not install at %s (exit %s): %+v\n%s", r.local, run.Code, run.Row, run.Stderr)
	}
	if !strings.Contains(run.Stderr, "Answer Yes in the tuios dialog") {
		t.Errorf("ASSERTION: sync did not say the upgrade waits for a Yes in the dialog:\n%s", run.Stderr)
	}
	if !run.Row.Daemon.Upgraded || run.Row.Daemon.Restarted || run.Row.RestartNeeded || !strings.Contains(run.Row.Action, "daemon upgraded live") {
		t.Errorf("ASSERTION: the row does not say the daemon was upgraded live: %+v", run.Row)
	}
	if after.PID != r.img.PID || after.Version != syncNextVersion || !sameFile(exe, r.local) {
		t.Errorf("ASSERTION: after a Yes the daemon is %+v running %s, from %+v. Want the same pid running %s at %s", after, exe, r.img, syncNextVersion, r.local)
	}
	if len(r.panes) != 2 || len(changedPIDs(r.panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: the panes were %v and are %v after the upgrade", r.panes, afterPanes)
	}
}

// TestHostsSyncInAPaneLeavesTheDaemonWithoutYes: sync from a pane with no
// client attached, then again with a client whose person answers No. Each
// time the daemon must stay the image it was, with its pane's programs, and
// never be restarted. The row must say the binary is installed, the daemon
// still runs the old version, and how to move it. The first run installs the
// binary, so the second takes sync's restart-only path.
func TestHostsSyncInAPaneLeavesTheDaemonWithoutYes(t *testing.T) {
	r := newPaneSyncRig(t)
	runs := map[string]paneSyncRun{}
	runs["no client"] = r.sync(t)()
	term := r.attach(t)
	done := r.sync(t)
	r.waitDialog(t, term, "no")
	pressKeys(t, term, keyEnter)
	runs["no"] = done()
	saveArtifact(t, term, r.art, "client-after-no")
	after, afterPanes := helloImage(t, r.base), panePIDs(t, r.base, paneSyncSession)
	exe := executableOf(t, after.PID)
	writeHandoffArtifact(t, map[string]any{"runs": runs, "before": r.img, "after": after, "executable_after": exe,
		"panes_before": r.panes, "panes_after": afterPanes})

	for what, run := range runs {
		row := run.Row
		if run.Code != "0" || what == "no client" && !row.Installed {
			t.Errorf("ASSERTION: the sync with %s failed or did not install (exit %s): %+v\n%s", what, run.Code, row, run.Stderr)
		}
		if row.Daemon.Upgraded || row.Daemon.Restarted || !row.RestartNeeded {
			t.Errorf("ASSERTION: with %s the row says the daemon moved, or that it needs nothing: %+v", what, row)
		}
		if !slices.ContainsFunc(row.Notes, func(n string) bool {
			return strings.Contains(n, "The new binary is installed.") && strings.Contains(n, "needs a Yes in the tuios dialog on an attached client, and none came") &&
				strings.Contains(n, "not restarted") && strings.Contains(n, "It still runs "+r.img.Version+".") &&
				strings.Contains(n, "tuios daemon upgrade from a terminal outside tuios")
		}) {
			t.Errorf("ASSERTION: with %s the row does not say what happened and what to do: %v", what, row.Notes)
		}
	}
	if after.Instance != r.img.Instance || after.PID != r.img.PID || after.Version != r.img.Version {
		t.Errorf("ASSERTION: the daemon changed without a Yes: %+v then %+v", r.img, after)
	}
	if len(r.panes) != 2 || len(changedPIDs(r.panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: the panes were %v and are %v, so the daemon was restarted", r.panes, afterPanes)
	}
}

// TestOrRestartFromAPaneNeverRestarts: `tuios daemon upgrade --or-restart`,
// which `just restart` runs, from a pane with no client attached. The daemon
// refuses the pane's upgrade, and --or-restart must not turn that into a
// restart, which would end the pane the command runs in: the command fails,
// says the daemon was not restarted, and the daemon and its panes are as
// they were.
func TestOrRestartFromAPaneNeverRestarts(t *testing.T) {
	r := newConfirmRig(t, "e2e-or-restart-pane")
	panes := panePIDs(t, r.base, r.sess)
	_ = mustCall(t, r.base, "send-text", map[string]any{"session": r.sess, "window": r.agent,
		"text": tuiosBin + " daemon upgrade --binary " + r.second + " --or-restart; echo OR_EXIT=$?\n"})
	m := waitHandoffPane(t, r.base, r.sess, r.agent, regexp.MustCompile(`OR_EXIT=(\d+)`), 150*time.Second)
	lines := mustScrollback(t, r.base, r.sess, r.agent)
	after, afterPanes := helloImage(t, r.base), panePIDs(t, r.base, r.sess)
	writeHandoffArtifact(t, map[string]any{"exit": m[1], "screen": lines, "before": r.img, "after": after, "panes_before": panes, "panes_after": afterPanes})

	if m[1] == "0" || !screenLinesHave(lines, "The daemon was not restarted, because a restart would end the pane this command runs in.") {
		t.Errorf("ASSERTION: --or-restart from a pane did not fail saying the daemon was not restarted (exit %s):\n%s", m[1], strings.Join(lines, "\n"))
	}
	if after != r.img || len(changedPIDs(panes, afterPanes)) != 0 {
		t.Errorf("ASSERTION: --or-restart from a pane moved the daemon: %+v then %+v, panes %v then %v", r.img, after, panes, afterPanes)
	}
}
