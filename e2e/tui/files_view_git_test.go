package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// filesGitRepo makes a repository with a.go and sub/b.go committed, a
// session agent in it, and a Claude Code turn that changes line 10 of a.go.
// The person then changes sub/b.go and makes new.txt.
func filesGitRepo(t *testing.T, base string) string {
	t.Helper()
	repo := testutil.GitRepo(t)
	lines := func(name string, at map[int]string) string {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			if s, ok := at[i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "%s line %d", name, i)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", lines("a.go", nil))
	write("sub/b.go", lines("b.go", nil))
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "two files")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	cli("set-agent-state", "working", "-s", "agent", "-w", "0", "--harness", "claude-code")
	waitUntil(t, "the turn start is looked at", uiTimeout, func() (bool, string) {
		b, _ := os.ReadFile(daemonLog)
		return strings.Contains(string(b), "Turn start of window"), string(b)
	})
	write("a.go", lines("a.go", map[int]string{10: "a.go A-CLAUDE"}))
	cli("set-agent-state", "done", "-s", "agent", "-w", "0", "--harness", "claude-code", "-m", "done")
	waitUntil(t, "the checkpoint is taken", uiTimeout, func() (bool, string) {
		out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent","window":"0"}`)
		var got struct {
			Checkpoints []struct{} `json:"checkpoints"`
		}
		_ = json.Unmarshal([]byte(out), &got)
		return len(got.Checkpoints) >= 1, out
	})
	write("sub/b.go", lines("b.go", map[int]string{5: "b.go B-YOU"}))
	write("new.txt", "fresh\n")
	return repo
}

// TestFilesViewMarksChangesAndReviewsThem opens the Files view on a
// repository and works its changes with the mouse:
//
//   - a.go, changed by Claude Code, is marked M with Claude Code's dot, new.txt
//     is marked U, and sub/ holds a change. An unchanged file has no mark.
//   - A file changed while the view is open is marked with no key pressed.
//   - Quick look on a.go opens on its Changes: the hunk, with the chip
//     "claude-code · turn 1".
//   - "v review" on new.txt opens the review on new.txt, and "O in files" in
//     the review goes back to the Files view with the cursor on new.txt.
//
// How it could pass wrongly, written down first:
//   - A mark could be drawn on every row. The row of an unchanged file must
//     have none.
//   - v could open the review on its first file, a.go. The review must show
//     new.txt's line and not a.go's.
func TestFilesViewMarksChangesAndReviewsThem(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := filesGitRepo(t, base)
	if err := os.WriteFile(filepath.Join(repo, "same.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "same.txt")
	testutil.Git(t, repo, "commit", "-q", "-m", "same")

	const rows = 44
	term := attachIn(t, base, "agent", startOpts{cols: 160, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	waitScreen(t, term, "the view never opened", "this machine")
	sendKeys(t, term, ":")
	sendKeys(t, term, repo, tuitest.Enter)
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}
	until := func(what string, ok func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(ok, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", what, err, term.Snapshot())
		}
	}
	rowOf := func(s tuitest.Screen, name string) string {
		_, h := s.Size()
		for y := range h {
			if l := s.Line(y); strings.Contains(l, " "+name+" ") || strings.HasSuffix(strings.TrimRight(l, " │"), " "+name) {
				return l
			}
		}
		return ""
	}
	marked := func(s tuitest.Screen, name, status string) bool {
		l := rowOf(s, name)
		return l != "" && strings.Contains(l, "● "+status)
	}
	until("the marks never showed", func(s tuitest.Screen) bool {
		return marked(s, "a.go", "M") && marked(s, "new.txt", "U") && strings.Contains(rowOf(s, "sub/"), "●") &&
			rowOf(s, "same.txt") != "" && !strings.Contains(rowOf(s, "same.txt"), "●")
	})
	frame("1-marks")

	// Live: a file changed now is marked with no key pressed.
	if err := os.WriteFile(filepath.Join(repo, "same.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	until("a file changed with the view open was never marked", func(s tuitest.Screen) bool { return marked(s, "same.txt", "M") })

	// Quick look on a.go opens on its changes.
	row, col := findText(t, term, "a.go")
	leftClick(t, term, col, row)
	sendKeys(t, term, " ")
	until("quick look never showed a.go's changes", func(s tuitest.Screen) bool {
		return screenHas(s, "Changes", "@@ -7,7 +7,7 @@", "A-CLAUDE", "claude-code · turn 1")
	})
	frame("2-quick-look-changes")
	sendKeys(t, term, " ")

	footer := rows - 2
	hint := func(words string) {
		t.Helper()
		var x, y int
		until("the footer never offered "+words, func(s tuitest.Screen) bool {
			for _, fy := range []int{footer, footer + 1, footer - 1} {
				if i := strings.Index(s.Line(fy), words); i >= 0 {
					x, y = len([]rune(s.Line(fy)[:i])), fy
					return true
				}
			}
			return false
		})
		leftClick(t, term, x, y)
	}
	// new.txt is not the review's first file: v must open the review on it.
	row, col = findText(t, term, "new.txt")
	leftClick(t, term, col, row)
	hint("v review")
	until("v never opened the review on new.txt", func(s tuitest.Screen) bool {
		return screenHas(s, "Review", "+ fresh") && !screenHas(s, "A-CLAUDE") && !screenHas(s, "this machine")
	})
	frame("3-review-from-files")

	hint("O in files")
	until("O never showed the Files view again", func(s tuitest.Screen) bool {
		return screenHas(s, "this machine") && marked(s, "new.txt", "U")
	})
	frame("4-back-in-files")
	// The cursor is on new.txt: v reviews it again.
	sendKeys(t, term, "v")
	until("the cursor was not on new.txt after O", func(s tuitest.Screen) bool { return screenHas(s, "Review", "+ fresh") && !screenHas(s, "A-CLAUDE") })
}

// TestFilesViewGitMarksAddNoIdleCost holds the Files view open on a
// repository with changes, marks shown, to the idle budget of the closed
// view: the marks are read when a file changes, and nothing runs at rest.
//
// Negative control: with filesGitListen reporting a change on every tick of
// a 200 ms timer, the open view reads the repository again and again, and
// the check fails.
func TestFilesViewGitMarksAddNoIdleCost(t *testing.T) {
	requireReal(t, "asserts on OS processes (pids, signals, /proc)")
	base := t.TempDir()
	killDaemon(t, base)
	repo := filesGitRepo(t, base)
	wire := &wireRecorder{}
	statsPath := filepath.Join(base, "tickstats")
	term := attachIn(t, base, "agent", startOpts{cols: 160, rows: 44, out: wire, env: []string{"TUIOS_STATS_FILE=" + statsPath}, noSettleProbe: true})
	var reads uint64
	window := func(name string) idleBudget {
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("%s: screen never settled: %v", name, err)
		}
		pause(t, "let the screen's last frame land before the idle window starts", time.Second)
		pid := term.Pid()
		start := sampleIdle(t, pid, statsPath)
		before := wire.n.Load()
		pause(t, "the idle window: count what the client does while nothing happens", idleWindow)
		end := sampleIdle(t, pid, statsPath)
		reads = end.stats["filesGitReads"] - start.stats["filesGitReads"]
		got := idleBudget{
			wire:        wire.n.Load() - before,
			ticks:       end.stats["ticks"] - start.stats["ticks"],
			work:        end.stats["work"] - start.stats["work"],
			tickerWakes: end.stats["tickerWakes"] - start.stats["tickerWakes"],
			ctxsw:       end.ctxsw - start.ctxsw,
		}
		t.Logf("%s, idle window %v: wire=%dB ticks=%d work=%d tickerWakes=%d ctxsw=%d gitReads=%d",
			name, idleWindow, got.wire, got.ticks, got.work, got.tickerWakes, got.ctxsw, reads)
		return got
	}
	closed := window("Files view closed")
	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	waitScreen(t, term, "the view never opened", "this machine")
	sendKeys(t, term, ":")
	sendKeys(t, term, repo, tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, "● M", "● U") }, uiTimeout); err != nil {
		t.Fatalf("the marks never showed: %v\n%s", err, term.Snapshot())
	}
	open := window("Files view open on a repository, marks shown")
	var over []string
	if reads > 0 {
		over = append(over, fmt.Sprintf("read the repository %d times with nothing changing", reads))
	}
	if open.wire > closed.wire+idleWireBudget/4 {
		over = append(over, fmt.Sprintf("wrote %d bytes, %d with it closed", open.wire, closed.wire))
	}
	if open.ticks > closed.ticks+2 || open.work > closed.work+2 {
		over = append(over, fmt.Sprintf("ran %d ticks (%d full), %d (%d) with it closed", open.ticks, open.work, closed.ticks, closed.work))
	}
	if open.tickerWakes > closed.tickerWakes+1 {
		over = append(over, fmt.Sprintf("woke the frame ticker %d times, %d with it closed", open.tickerWakes, closed.tickerWakes))
	}
	if closed.ctxsw >= 0 && open.ctxsw > closed.ctxsw*3/2+200 {
		over = append(over, fmt.Sprintf("made %d context switches, %d with it closed", open.ctxsw, closed.ctxsw))
	}
	if len(over) > 0 {
		t.Fatalf("the open Files view with git marks costs more at idle than the closed one:\n  %s\n%s", strings.Join(over, "\n  "), term.Snapshot())
	}
}
