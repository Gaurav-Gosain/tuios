package tuie2e

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// tuios pane-view shows one pane in the terminal it runs in, with no tuios
// around it, over stream-pane. A terminal that draws its own panes (Tern)
// runs one per tuios pane.
//
// What it must do, and how each check could pass wrongly:
//   - Output and input. The marker is computed by the shell, so the echo of
//     the keystrokes cannot satisfy the wait.
//   - Answer each terminal request once. The daemon's emulator answers DA1,
//     and the terminal pane-view runs in (tuitest here, Tern for real) would
//     answer it too, the second answer arriving as typed input. The pane
//     counts the answers it reads.
//   - Pass Ctrl-C to the program. A sleep 30 that Ctrl-C did not stop holds
//     the next command for 30 seconds, past the wait.
//   - Follow the terminal's size while no client shows the session. tput reads
//     the pane's own size, and the terminal's size changes to one the pane
//     never had.
//   - Leave the session running when the viewer goes.
//   - Exit with the pane's status when the pane exits.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "pane-view"):
//   - with queryFilter.Write returning its input unchanged, the pane reads two
//     DA1 answers and TestPaneView fails at "answered twice".
//   - with leasedSizeLocked ignoring noClients (no viewer lease sizes the
//     pane), the pane keeps the session's size and TestPaneView fails at
//     "did not follow the terminal".

func TestPaneView(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "pv", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	win := firstWindow(t, base, "pv").ID

	view := startIn(t, base, startOpts{cols: 90, rows: 25, animations: true,
		args: []string{"pane-view", "-s", "pv", "-w", win}})

	if err := view.SendKeys("printf 'PV-%s-OK\\n' $((6*7))", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForText("PV-42-OK", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: typing in pane-view never reached the pane and back: %v\n%s", err, view.Snapshot())
	}

	// The pane asks DA1 once and counts the answers that reach it. Each
	// answer ends in "c"; the shell reads them with echo off, so none is
	// drawn or run.
	countDA := `bash -c 'stty -echo; printf "\033[c"; sleep 1; IFS= read -rs -t 1 -d X x; stty echo; n=$(printf %s "$x" | tr -cd c | wc -c); echo DA-COUNT-$((n+0))-$((100+1))'`
	if err := view.SendKeys(countDA, tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForMatch(regexp.MustCompile(`DA-COUNT-\d+-101`), tuitest.ScopeScreen, shellTimeout); err != nil {
		t.Fatalf("the DA count never printed: %v\n%s", err, view.Snapshot())
	}
	if snap := view.Snapshot(); !strings.Contains(snap, "DA-COUNT-1-101") {
		t.Fatalf("ASSERTION: the pane's DA1 request was answered twice, or not at all: the terminal pane-view runs in answered it as well as the daemon\n%s", snap)
	}

	if err := view.SendKeys("sleep 30", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := view.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("ctrl-c: %v", err)
	}
	if err := view.SendKeys("echo AFTER-$((1+1))-CTRLC", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForText("AFTER-2-CTRLC", 5*time.Second); err != nil {
		t.Fatalf("ASSERTION: Ctrl-C did not reach the program in the pane: %v\n%s", err, view.Snapshot())
	}

	if err := view.Resize(160, 50); err != nil {
		t.Fatalf("resize: %v", err)
	}
	time.Sleep(time.Second)
	if err := view.SendKeys(`echo SIZE-$(tput cols)x$(tput lines)-END`, tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForText("SIZE-160x50-END", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: the pane did not follow the terminal to 160x50, larger than the session: %v\n%s", err, view.Snapshot())
	}

	// The viewer goes; the session and its pane stay.
	_ = view.Close()
	if got := firstWindow(t, base, "pv").ID; got != win {
		t.Fatalf("ASSERTION: the pane changed after the viewer left: %s, was %s", got, win)
	}

	again := startIn(t, base, startOpts{cols: 90, rows: 25, animations: true,
		args: []string{"pane-view", "-s", "pv", "-w", win}})
	if err := again.SendKeys("echo AGAIN-$((2*21))", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := again.WaitForText("AGAIN-42", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: a second pane-view could not use the pane the first one left: %v\n%s", err, again.Snapshot())
	}
	if err := again.SendKeys("exit 3", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if st, done := again.ExitStatus(); done {
			if st.Code != 3 {
				t.Fatalf("ASSERTION: pane-view exited %d, want 3, the pane's status", st.Code)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: pane-view did not exit when its pane did\n%s", again.Snapshot())
}
