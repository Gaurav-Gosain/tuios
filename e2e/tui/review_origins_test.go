package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewNamesWhoMadeEachHunk runs two turns of an agent in a repository,
// with a change of the person's between them, and opens the review: each
// hunk's header names the turn it came from, or "you". Then the person
// changes one more line, a third turn starts and writes a line, and the live
// review names that turn as writing while both of the person's hunks still
// say "you".
//
// How it could pass wrongly, written down first:
//   - Every hunk could name the agent. The person's changes are made between
//     turns, and their hunks must say "you", also once the next turn ended
//     (turn 2's checkpoint holds the first) or runs (the third turn).
//   - Every hunk could name the newest turn. Turn 1's hunk and turn 2's are
//     different hunks, and each chip must name its own turn.
//   - The chip could come from the window's state, not the lines. Only the
//     line written while turn 3 runs says "writing".
//
// A change the person makes while a turn runs is credited to that turn,
// which review_origins.go writes down; it is not checked here.
//
// Artifact: a frame of the review, as text, styled text and PNG.
func TestReviewNamesWhoMadeEachHunk(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	file := filepath.Join(repo, "main.go")
	lines := map[int]string{}
	write := func() {
		t.Helper()
		var b strings.Builder
		for i := 1; i <= 80; i++ {
			if s, ok := lines[i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "line %d", i)
			}
			b.WriteByte('\n')
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	checkpoints := func(n int) {
		t.Helper()
		waitUntil(t, fmt.Sprintf("checkpoint %d is taken", n), uiTimeout, func() (bool, string) {
			out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent"}`)
			var got struct {
				Checkpoints []struct{} `json:"checkpoints"`
			}
			_ = json.Unmarshal([]byte(out), &got)
			return len(got.Checkpoints) >= n, out
		})
	}
	// starts waits for the daemon to have looked at n turn starts, so a line
	// written after it is the turn's and not the person's.
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	starts := func(n int) {
		t.Helper()
		waitUntil(t, fmt.Sprintf("turn start %d is looked at", n), uiTimeout, func() (bool, string) {
			b, _ := os.ReadFile(daemonLog)
			return strings.Count(string(b), "Turn start of window") >= n, string(b)
		})
	}
	started := 0
	begin := func() {
		t.Helper()
		cli("set-agent-state", "working", "-s", "agent", "--harness", "claude-code")
		started++
		starts(started)
	}
	turn := func(n, at int) {
		t.Helper()
		begin()
		lines[at] = fmt.Sprintf("line %d TURN-%d", at, n)
		write()
		cli("set-agent-state", "done", "-s", "agent", "--harness", "claude-code", "-m", fmt.Sprintf("turn %d", n))
		checkpoints(n)
	}
	turn(1, 10)
	lines[30] = "line 30 BY-YOU"
	write()
	turn(2, 50)

	term := attachIn(t, base, "agent", startOpts{cols: 150, rows: 60})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "TURN-1", "BY-YOU")
	chipOn := func(s tuitest.Screen, hunkLine, chip string) bool {
		// The chip is on the header row above the hunk's changed line.
		row, _, ok := textAt(s, hunkLine, 0)
		for y := row - 1; ok && y >= 0 && y > row-6; y-- {
			if strings.Contains(s.Line(y), "@@") {
				return strings.Contains(s.Line(y), chip)
			}
		}
		return false
	}
	check := func(cases ...[2]string) {
		t.Helper()
		for _, c := range cases {
			if err := term.WaitFor(func(s tuitest.Screen) bool { return chipOn(s, c[0], c[1]) }, uiTimeout); err != nil {
				t.Errorf("ASSERTION: the hunk of %s does not say %q: %v\n%s", c[0], c[1], err, term.Snapshot())
			}
		}
	}
	check([2]string{"TURN-1", "turn 1"}, [2]string{"TURN-2", "turn 2"}, [2]string{"BY-YOU", "you · not in a turn"})
	art := artifactDir(t)
	saveArtifact(t, term, art, "1-who-made-each-hunk")
	savePNG(t, term.Screen(), shot.XTermPalette(), art, "1-who-made-each-hunk")

	// One more change of the person's, then turn 3 runs and writes a line:
	// the live review names it as writing, and both of the person's hunks
	// still say "you".
	lines[60] = "line 60 BY-YOU-AGAIN"
	write()
	begin()
	lines[70] = "line 70 WRITING-NOW"
	write()
	waitScreen(t, term, "the live review never showed turn 3's line", "WRITING-NOW")
	check([2]string{"WRITING-NOW", "writing"}, [2]string{"TURN-1", "turn 1"}, [2]string{"BY-YOU", "you · not in a turn"}, [2]string{"BY-YOU-AGAIN", "you · not in a turn"})
	saveArtifact(t, term, art, "2-turn-writing")
	savePNG(t, term.Screen(), shot.XTermPalette(), art, "2-turn-writing")
}
