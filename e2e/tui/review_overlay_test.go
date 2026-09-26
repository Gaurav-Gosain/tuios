package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The review overlay, end to end through the binary a person runs: a fan of
// two fake agents in a throwaway repository, a client attached to one of
// them, and the keys a person presses. Nothing here touches any repository
// but the one fanFixture makes.

// reviewFan starts a fan of two fake agents named try/NAME, waits for both
// prompts to be typed, and changes README in the second attempt. It returns
// the second attempt's session.
func reviewFan(t *testing.T, base, repo, name string) string {
	t.Helper()
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/"+name, "Do the thing."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	second := "repo-try-" + name + "-2"
	var path string
	deadline := time.Now().Add(30 * time.Second)
	for {
		sent := 0
		for _, r := range worktreeRows(t, base, "--group", "try/"+name) {
			if r["prompt_status"] == "sent" {
				sent++
			}
			if r["session"] == second {
				path, _ = r["path"].(string)
			}
		}
		if sent == 2 && path != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first prompts were never sent")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("hello\nretry three times\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return second
}

// sendKeys presses keys one after another, a moment apart, as a person types.
func sendKeys(t *testing.T, term *tuitest.Terminal, keys ...any) {
	t.Helper()
	for _, k := range keys {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("press %q: %v", k, err)
		}
		time.Sleep(insertGuard)
	}
}

// waitScreen waits for every marker to be on screen.
func waitScreen(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		for _, m := range markers {
			if !strings.Contains(text, m) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, term.Snapshot())
	}
}

// TestReviewOverlayNotesSendCompareAndKeep drives the whole review from the
// keyboard: ctrl+b v on an attempt's agent opens its diff against the fan's
// base, c and C leave a note on a line and one on the hunk, S sends both to
// the agent as one message from the person once it rests, and the queue is
// empty after. w shows both attempts; V runs a check that passes in one and
// fails in the other, and the rows say so; K and y keep one, and the other
// session is gone.
//
// Negative controls: with ReviewSend sending no human_nonce, the message
// says "from a script" and the wait for "from the person" times out; with
// reviewTickIfRunning scheduling no read, the rows stay "running"; with
// ReviewCompareConfirm ignoring y, the other session is still listed.
func TestReviewOverlayNotesSendCompareAndKeep(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "ov")
	other := "repo-try-ov"

	term := attachIn(t, base, session, startOpts{})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "1 file", "vs main", "M README", "retry three times", "c note")
	saveFrame(t, term, "review-overlay-120x40")

	// The cursor starts on the hunk; two rows down is the added line.
	sendKeys(t, term, "j", "j", "c")
	waitScreen(t, term, "c did not open the note line", "save", "drop")
	if err := term.SendKeys("say why three"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the note never showed under its line", "note: say why three", "1 note")
	sendKeys(t, term, "C")
	if err := term.SendKeys("add a test"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the hunk note never showed", "note (hunk): add a test", "S send 2 notes")
	saveFrame(t, term, "review-overlay-notes")

	sendKeys(t, term, "S")
	deadline := time.Now().Add(30 * time.Second)
	for {
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		if strings.Contains(pane, "Review notes on your changes (vs main), from the person:") &&
			strings.Contains(pane, "say why three") && strings.Contains(pane, "add a test") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never received the notes as the person's:\n%s", pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for {
		out, err := tuiosCLI(t, base, "queue", "ls", "-s", session)
		if err == nil && strings.Contains(out, "Nothing is queued") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queue never emptied: %v\n%s", err, out)
		}
		time.Sleep(500 * time.Millisecond)
	}
	waitScreen(t, term, "the notes never read as sent", "sent ")

	// The compare view.
	sendKeys(t, term, "w")
	waitScreen(t, term, "the compare view never showed both attempts", "Compare  try/ov in repo, 2 attempts", other+" ", session, "not run")
	waitScreen(t, term, "the compare view never counted the change", "+1 -0")
	saveFrame(t, term, "review-compare-120x40")

	sendKeys(t, term, "V")
	waitScreen(t, term, "V did not open the command line", "Run in every attempt:")
	if err := term.SendKeys("grep -q three README"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(insertGuard)
	sendKeys(t, term, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "grep -q three README passed") && strings.Contains(text, "grep -q three README failed, exit 1")
	}, shellTimeout); err != nil {
		t.Fatalf("the rows never said passed and failed: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "review-compare-verified")

	// The cursor is on the attempt the review came from, the one kept.
	sendKeys(t, term, "K")
	waitScreen(t, term, "K did not ask", "Keep "+session+" and remove "+other+"?")
	sendKeys(t, term, "y")
	waitScreen(t, term, "keep never answered", "Kept "+session+".")
	deadline = time.Now().Add(30 * time.Second)
	for {
		out, _ := tuiosCLI(t, base, "ls")
		if !strings.Contains(out, other+" ") && !strings.Contains(out, other+"\n") && strings.Contains(out, session) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the other attempt is still there:\n%s", out)
		}
		time.Sleep(500 * time.Millisecond)
	}
	sendKeys(t, term, tuitest.Esc, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "next hunk") && !strings.Contains(s.Text(), "esc back")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the review: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the review")
}

// TestReviewOverlayAt80x24 opens the review and the compare view on an
// 80x24 terminal: both fit, no row runs past the edge, and the compare
// view's check column uses its short words.
func TestReviewOverlayAt80x24(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "sm")

	term := attachIn(t, base, session, startOpts{cols: 80, rows: 24})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M README", "retry three times", "esc close")
	assertNoLineOverflow(t, term.Screen(), 80, "the review at 80x24")
	saveFrame(t, term, "review-overlay-80x24")

	sendKeys(t, term, "w")
	waitScreen(t, term, "the compare view never opened", "Compare", "repo-try-sm ", session, "esc back")
	assertNoLineOverflow(t, term.Screen(), 80, "the compare view at 80x24")
	saveFrame(t, term, "review-compare-80x24")
	sendKeys(t, term, tuitest.Esc, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "esc close")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the review: %v\n%s", err, term.Snapshot())
	}
}

// TestReviewFrameAndStatusLine is the review's frame and its status line on
// the real terminal. The rules under the header and above the footer meet the
// frame's sides; a dock message from anything else, here another client
// opening a window, stays off the review's status line and the compare
// view's; and a message the review raised itself is shown there. The frames
// are saved under artifactDir.
//
// How this could pass wrongly, written down first: the other client's window
// might not reach this client before the check ends, so the check waits for
// the window to be listed and then watches the screen for as long as the
// message lives; and the status line might show nothing at all, so the
// review's own message is asserted to show.
func TestReviewFrameAndStatusLine(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "fr")
	dir := artifactDir(t)

	term := attachIn(t, base, session, startOpts{})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M README", "esc close")
	joined := 0
	for _, l := range strings.Split(term.Snapshot(), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "├") && strings.HasSuffix(l, "┤") {
			joined++
		}
		if strings.HasPrefix(l, "│─") {
			t.Errorf("a rule stops short of the frame: %q", l)
		}
	}
	if joined < 2 {
		t.Errorf("%d rules meet the frame, want the header's and the footer's\n%s", joined, term.Snapshot())
	}
	saveArtifact(t, term, dir, "review")

	if out, err := tuiosCLI(t, base, "new-window", "extra", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("open a window from another client: %v\n%s", err, out)
	}
	watchAbsent := func(where string) {
		t.Helper()
		deadline := time.Now().Add(2500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if text := term.Screen().Text(); strings.Contains(text, "Window created") {
				t.Fatalf("the %s shows a dock message it did not raise\n%s", where, term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	watchAbsent("review")

	sendKeys(t, term, "w")
	waitScreen(t, term, "the compare view never opened", "Compare", "esc back")
	if out, err := tuiosCLI(t, base, "new-window", "extra-2", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("open a window from another client: %v\n%s", err, out)
	}
	watchAbsent("compare view")
	sendKeys(t, term, "d")
	waitScreen(t, term, "the compare view did not show its own message", "Mark two attempts with m, then d diffs them")
	saveArtifact(t, term, dir, "compare-own-message")
	t.Logf("frames in %s", dir)
}

// TestFanSurvivesADaemonRestart: a fan's sessions come back from kill-server
// and a restore still in their group and still managed, so the review diffs
// against the fan's base and compare lists both attempts. The worktree rows
// after the restore and the compare view are saved under artifactDir.
//
// How this could pass wrongly, written down first: the restore might not
// have run when the rows are read, so the rows are polled until both
// sessions are back; and a group read from the branch name alone would pass
// the rows check, so compare is opened too, which needs the managed mark.
func TestFanSurvivesADaemonRestart(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "rs")
	dir := artifactDir(t)

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "after", "--detach"); err != nil {
		t.Fatalf("start the daemon again: %v\n%s", err, out)
	}
	var rows []map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows = worktreeRows(t, base, "--group", "try/rs")
		managed := 0
		for _, r := range rows {
			if r["group"] == "try/rs" && r["managed"] == true {
				managed++
			}
		}
		if managed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fan did not come back in its group and managed: %v", rows)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if data, err := json.MarshalIndent(rows, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "worktree-rows-after-restart.json"), data, 0o644)
	}

	term := attachIn(t, base, session, startOpts{})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened after the restart", "Review", "vs main", "M README")
	sendKeys(t, term, "w")
	waitScreen(t, term, "compare found no fan after the restart", "Compare  try/rs in repo, 2 attempts", "repo-try-rs ", session)
	saveArtifact(t, term, dir, "compare-after-restart")
	t.Logf("artifacts in %s", dir)
}

// TestReviewWrappedNoteIsOneStop: a note too long for the diff column wraps
// onto several rows, and the cursor takes it as one row. j lands on its first
// row and the mark is drawn there; the next j goes to the line after the note
// rather than onto its second row, and k comes back to its first row. The
// frames are saved under artifactDir.
//
// How this could pass wrongly, written down first: the note might fit on one
// row at this width, so the test checks that its last word is on a later row
// than its first; a note on the last row gives j nowhere to go, so it sits on
// line 1 with line 2 after it; and a mark elsewhere on screen would be
// counted, so the mark is asserted on exactly one row.
//
// Negative control: with ReviewMove stepping one row at a time, j from the
// note's first row stays inside the note and the wait for line 2 times out.
func TestReviewWrappedNoteIsOneStop(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "wn")
	dir := artifactDir(t)
	if out, err := tuiosCLI(t, base, "review", "note", "-s", session, "README:1",
		"firstword of a long note that wraps onto more rows than one in this narrow column so the cursor takes it whole lastword"); err != nil {
		t.Fatalf("review note: %v: %s", err, out)
	}

	term := attachIn(t, base, session, startOpts{cols: 80, rows: 24})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M README", "firstword", "lastword", "retry three times", "esc close")

	lines := func() []string { return strings.Split(term.Snapshot(), "\n") }
	rowOf := func(word string) int {
		for i, l := range lines() {
			if strings.Contains(l, word) {
				return i
			}
		}
		return -1
	}
	markRow := func() int {
		t.Helper()
		row := -1
		for i, l := range lines() {
			if strings.Contains(l, "›") {
				if row >= 0 {
					t.Fatalf("the mark is on more than one row\n%s", term.Snapshot())
				}
				row = i
			}
		}
		return row
	}
	if first, last := rowOf("firstword"), rowOf("lastword"); last <= first {
		t.Fatalf("the note did not wrap: first word on row %d, last on row %d\n%s", first, last, term.Snapshot())
	}
	waitMark := func(what string, want func() int) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for markRow() != want() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: mark on row %d, want row %d\n%s", what, markRow(), want(), term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// The hunk header, the context line, then the note on it.
	sendKeys(t, term, "j", "j")
	waitMark("j onto the note", func() int { return rowOf("firstword") })
	saveArtifact(t, term, dir, "note-selected")
	sendKeys(t, term, "j")
	waitMark("j past the note", func() int { return rowOf("retry three times") })
	sendKeys(t, term, "k")
	waitMark("k back onto the note", func() int { return rowOf("firstword") })
	saveArtifact(t, term, dir, "note-selected-again")
	t.Logf("frames in %s", dir)
}

// TestReviewFooterKeepsItsRightMargin opens the split view at 120 columns,
// where its keys fill the footer to within a cell. The footer kept a cell of
// margin on the left only, so a strip that fitted to the cell ran into the
// frame: "esc close│". The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The view could be unified, whose keys are two cells shorter and leave
//     room either way, so the test waits for "s unified", which only the
//     split view offers.
//   - The footer could lose its last key instead, so the line has to end in
//     "esc close" before the margin.
//
// Negative control: with reviewHints fitting against width-1, the footer
// line ends "esc close│" and the test fails.
func TestReviewFooterKeepsItsRightMargin(t *testing.T) {
	base, repo := fanFixture(t)
	session := reviewFan(t, base, repo, "fm")
	term := attachIn(t, base, session, startOpts{cols: 120, rows: 40})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "M README", "s split")
	sendKeys(t, term, "s")
	waitScreen(t, term, "s did not switch to the split view", "s unified", "esc close")
	saveArtifact(t, term, artifactDir(t), "review-split-footer")
	s := term.Screen()
	line := strings.TrimRight(s.Line(rowWith(s, "esc close")), " ")
	if !strings.HasSuffix(line, "esc close │") {
		t.Fatalf("the footer does not end in a cell of margin before the frame: %q\n%s", line, term.Snapshot())
	}
}
