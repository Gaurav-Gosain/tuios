package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TML surfaces of a session on another machine, drawn and answered here.
//
// The report this file exists for: a task system built with TML, with a
// sidebar, popups, and tasks to review, approve or send back. It worked on
// the machine that ran it and in tuios-web there. From the terminal on the
// person's own machine, with that machine reached over a host link, "TML etc
// does not function at all": every TML window showed the placeholder line.
//
// The far daemon holds the surfaces. This client attaches the far session
// over the link, draws the far daemon's pushes, and sends what the person does
// back to the far daemon over the same link. Everything goes through the real
// transport: the ssh stand-in runs `tuios stdio-proxy` against a second real
// daemon. See host_attach_test.go.
//
// NEGATIVE CONTROL in NEGATIVE_CONTROLS.md, "TML surfaces over a host link".

// tmlTasksDoc is the far program's sidebar: two tasks and a button that asks
// the program to review the first.
const tmlTasksDoc = `<tml version="1" title="Tasks">
  <state>{"t1": "pending", "t2": "pending", "filter": "all"}</state>
  <col pad="1" gap="1">
    <text>Fix login bug: <b>{state.t1}</b></text>
    <text>Add retry: <b>{state.t2}</b></text>
    <text>Filter: {state.filter}</text>
    <row gap="2">
      <button key="v" kind="primary" on-press="emit('review', 't1')">Review</button>
      <button key="m" on-press="set('state.filter', 'mine')">Mine</button>
    </row>
  </col>
</tml>
`

// tmlReviewDoc is the popup the far program opens for a review.
const tmlReviewDoc = `<tml version="1" title="Review">
  <col pad="1" gap="1">
    <text>Review the fix for the login bug.</text>
    <row gap="2">
      <button key="a" kind="primary" on-press="emit('approve')">Approve</button>
      <button key="b" on-press="emit('send-back')">Send back</button>
      <button key="c" on-press="close('cancel')">Cancel</button>
    </row>
  </col>
</tml>
`

// tmlTasksProgram is the task system: it runs in a pane on build, opens the
// sidebar as a pane, opens the review popup when the person asks, and answers
// approve and send back with a patch to the sidebar and a close of the popup.
// It logs every line it reads, so the test sees what reached it.
const tmlTasksProgram = `#!/bin/sh
T='@TUIOS@'
D='@DIR@'
"$T" tml show "$D/tasks.tml" --pane --name Tasks --events | while IFS= read -r line; do
  printf 'sidebar %s\n' "$line" >> "$D/events.log"
  case "$line" in
  *'"type":"tml_opened"'*)
    printf '%s' "$line" | sed 's/.*"surface":"\([^"]*\)".*/\1/' > "$D/sidebar.id" ;;
  *'"name":"review"'*)
    "$T" tml show "$D/review.tml" --width 64 --height 9 --events | while IFS= read -r ev; do
      printf 'popup %s\n' "$ev" >> "$D/events.log"
      case "$ev" in
      *'"type":"tml_opened"'*)
        printf '%s' "$ev" | sed 's/.*"surface":"\([^"]*\)".*/\1/' > "$D/popup.id" ;;
      *'"name":"approve"'*)
        "$T" tml patch "$(cat "$D/sidebar.id")" '[{"op":"replace","path":"/t1","value":"approved"}]' >> "$D/patch.log" 2>&1
        "$T" tml close "$(cat "$D/popup.id")" '"approved"' >> "$D/patch.log" 2>&1 ;;
      *'"name":"send-back"'*)
        "$T" tml patch "$(cat "$D/sidebar.id")" '[{"op":"replace","path":"/t1","value":"sent back"}]' >> "$D/patch.log" 2>&1
        "$T" tml close "$(cat "$D/popup.id")" '"sent back"' >> "$D/patch.log" 2>&1 ;;
      esac
    done & ;;
  esac
done
`

// pressTMLButton clicks a button of a surface a pane opened. Such a surface
// does not take the focus, and the first click only focuses it, so the first
// click lands on text of the same surface that runs nothing.
func pressTMLButton(t *testing.T, term *tuitest.Terminal, plain, button string) {
	t.Helper()
	col, row := tmlLocate(t, term, plain)
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	time.Sleep(gestureGap)
	col, row = tmlLocate(t, term, button)
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
}

// TestTMLSurfacesCrossAHostLink is the task system above, used from this
// machine: the far program's sidebar and popup are drawn here, a click here
// reaches the far program, and the far program's patch is drawn here. The
// link then drops. A patch on build while it is down is drawn when it is
// back, and a click after it is back still reaches the far program. A value
// the person set while the link was down, which build never got, gives way
// to build's value when the link is back.
func TestTMLSurfacesCrossAHostLink(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-tasks", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	dir := filepath.Join(remote, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTMLDoc(t, dir, "tasks.tml", tmlTasksDoc)
	writeTMLDoc(t, dir, "review.tml", tmlReviewDoc)
	program := strings.NewReplacer("@TUIOS@", tuiosBin, "@DIR@", dir).Replace(tmlTasksProgram)
	script := filepath.Join(dir, "program.sh")
	if err := os.WriteFile(script, []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	// The program runs in a pane on build, as a task system would.
	if out, err := tuiosCLI(t, remote, "new-window", "-s", "far-tasks", "tasks", "--no-focus", "--", "/bin/sh", script); err != nil {
		t.Fatalf("start the far program: %v\n%s", err, out)
	}
	events := filepath.Join(dir, "events.log")
	sidebar := strings.TrimSpace(waitFile(t, filepath.Join(dir, "sidebar.id"), func(s string) bool {
		return strings.HasPrefix(s, "tml-")
	}, "the far program never opened its sidebar"))

	// This machine's client, switched onto the far session from the rail.
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "far-tasks")
	time.Sleep(insertGuard)
	clickRailSession(t, term, "far-tasks")
	waitRailCurrent(t, term, "far-tasks")
	// The panes tile, so the sidebar is a column of its own. The far daemon
	// made them with no client, and this client does not tile them itself.
	leaveTerminalMode(t, term)
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}
	if err := term.WaitForText("Tiling on", uiTimeout); err != nil {
		t.Fatalf("tiling never turned on: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("i"); err != nil {
		t.Fatalf("enter terminal mode: %v", err)
	}
	time.Sleep(gestureGap)

	// 1. The far sidebar is drawn here, and not as the placeholder line.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Fix login bug: pending", "Add retry: pending", "Review")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the far program's sidebar is not drawn here: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "This pane shows a TML surface") {
		t.Fatalf("ASSERTION: the far sidebar shows the placeholder line\n%s", term.Snapshot())
	}
	saveTMLFrame(t, term, "1-far-sidebar")

	// 2. A click on Review reaches the far program, which opens a popup.
	pressTMLButton(t, term, "Add retry", "Review")
	waitFile(t, events, func(s string) bool { return strings.Contains(s, `"name":"review"`) },
		"ASSERTION: the click on Review never reached the far program")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Review the fix for the login bug.", "Approve", "Send back")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the far program's review popup is not drawn here: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "2-far-popup")

	// 3. Approve reaches the far program. Its patch to the sidebar is drawn
	// here, and its close takes the popup away.
	pressTMLButton(t, term, "Review the fix", "Approve")
	waitFile(t, events, func(s string) bool { return strings.Contains(s, `"name":"approve"`) },
		"ASSERTION: the click on Approve never reached the far program")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Fix login bug: approved") && !screenHas(s, "Review the fix for the login bug.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the far program's patch and close are not drawn here: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "3-approved")

	// 4. The link drops. The far daemon keeps the surface, and a patch it
	// takes while the link is down is drawn once the link is back.
	breakTheLink(t)
	if err := term.WaitForText("Reconnecting to build", uiTimeout); err != nil {
		t.Fatalf("the client did not say it was dialing the link again: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, remote, "tml", "patch", sidebar, `[{"op":"replace","path":"/t2","value":"blocked"}]`); err != nil {
		t.Fatalf("patch the far sidebar while the link is down: %v\n%s", err, out)
	}
	saveTMLFrame(t, term, "4-link-down")
	if err := term.WaitForText("The link to build is back", 60*time.Second); err != nil {
		t.Fatalf("the client never got the link back: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Fix login bug: approved", "Add retry: blocked")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the patch the far sidebar took while the link was down is not drawn here: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "5-link-back")

	// 5. A click after the link is back reaches the far program too.
	pressTMLButton(t, term, "Add retry", "Review")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Review the fix for the login bug.", "Send back")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after the link came back, the click on Review never opened the far popup: %v\n%s", err, term.Snapshot())
	}
	pressTMLButton(t, term, "Review the fix", "Send back")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Fix login bug: sent back", "Add retry: blocked") && !screenHas(s, "Review the fix for the login bug.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after the link came back, Send back did not reach the far program: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "6-sent-back")

	// 6. The link drops again, and the person sets a value the far daemon
	// never gets. When the link is back, the far daemon's value is drawn,
	// with no change on build to carry it.
	breakTheLink(t)
	if err := term.WaitForText("Reconnecting to build", uiTimeout); err != nil {
		t.Fatalf("the client did not say it was dialing the link again: %v\n%s", err, term.Snapshot())
	}
	pressTMLButton(t, term, "Add retry", "Mine")
	if err := term.WaitForText("Filter: mine", uiTimeout); err != nil {
		t.Fatalf("the local set did not draw while the link was down: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "7-set-while-down")
	if err := term.WaitForText("The link to build is back", 60*time.Second); err != nil {
		t.Fatalf("the client never got the link back again: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Filter: all", "Fix login bug: sent back")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after the link came back, the client kept a value the far daemon never got: %v\n%s", err, term.Snapshot())
	}
	saveTMLFrame(t, term, "8-far-value-wins")
	alive(t, term, "after TML surfaces crossed a host link")
}
