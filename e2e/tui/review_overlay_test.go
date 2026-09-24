package tuie2e

import (
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
