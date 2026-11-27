package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewOriginsReachTheCLI runs two turns of an agent in a repository,
// with a change of the person's between them, and reads the review with
// tuios review --origins, as a tool outside tuios would: each hunk names who
// made it, in JSON and in the text, with the words the review view uses.
//
// How it could pass wrongly, written down first:
//   - The hunks could carry no origin at all, and a check that only looks
//     for the field's absence would pass. Every hunk must have one.
//   - Every hunk could name the agent. The person's hunk must say "you".
//   - Every hunk could name the newest turn. Turn 1's hunk and turn 2's are
//     different hunks, and each must name its own turn.
//   - origin could point at the wrong author. It must index authors, from
//     1, to the agent turn the words name.
//
// Artifact: the JSON and the text answer.
func TestReviewOriginsReachTheCLI(t *testing.T) {
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
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
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
	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	started := 0
	turn := func(n, at int) {
		t.Helper()
		cli("set-agent-state", "working", "-s", "agent", "--harness", "claude-code")
		started++
		waitUntil(t, fmt.Sprintf("turn start %d is looked at", started), uiTimeout, func() (bool, string) {
			b, _ := os.ReadFile(daemonLog)
			return strings.Count(string(b), "Turn start of window") >= started, string(b)
		})
		lines[at] = fmt.Sprintf("line %d TURN-%d", at, n)
		write()
		cli("set-agent-state", "done", "-s", "agent", "--harness", "claude-code", "-m", fmt.Sprintf("turn %d", n))
		checkpoints(n)
	}
	turn(1, 10)
	lines[30] = "line 30 BY-YOU"
	write()
	turn(2, 50)

	raw := cli("review", "-s", "agent", "--origins", "--json")
	var got struct {
		Authors []struct {
			Kind  string `json:"kind"`
			Agent string `json:"agent"`
			Turn  uint64 `json:"turn"`
		} `json:"authors"`
		Files []struct {
			Path  string `json:"path"`
			Hunks []struct {
				Origin int    `json:"origin"`
				Who    string `json:"who"`
				Lines  []struct {
					Op   string `json:"op"`
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"hunks"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("tuios review --origins --json is not JSON: %v\n%s", err, raw)
	}
	want := map[string]struct {
		who  string
		kind string
		turn uint64
	}{
		"TURN-1": {"claude-code · turn 1", "agent", 1},
		"TURN-2": {"claude-code · turn 2", "agent", 2},
		"BY-YOU": {"you · not in a turn", "person", 0},
	}
	seen := map[string]bool{}
	for _, f := range got.Files {
		for _, h := range f.Hunks {
			var mark string
			for _, l := range h.Lines {
				for m := range want {
					if l.Op == "add" && strings.HasSuffix(l.Text, " "+m) {
						mark = m
					}
				}
			}
			if mark == "" {
				continue
			}
			seen[mark] = true
			w := want[mark]
			if h.Who != w.who {
				t.Errorf("ASSERTION: the hunk of %s says who %q, want %q\n%s", mark, h.Who, w.who, raw)
			}
			if h.Origin < 1 || h.Origin > len(got.Authors) {
				t.Errorf("ASSERTION: the hunk of %s has origin %d, not a place in the %d authors\n%s", mark, h.Origin, len(got.Authors), raw)
				continue
			}
			a := got.Authors[h.Origin-1]
			if a.Kind != w.kind || a.Turn != w.turn {
				t.Errorf("ASSERTION: the hunk of %s points at author %+v, want kind %s turn %d\n%s", mark, a, w.kind, w.turn, raw)
			}
		}
	}
	for m := range want {
		if !seen[m] {
			t.Errorf("ASSERTION: no hunk holds %s\n%s", m, raw)
		}
	}

	text := cli("review", "-s", "agent", "--origins")
	for _, w := range want {
		found := false
		for l := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(l, "@@") && strings.HasSuffix(l, "  "+w.who) {
				found = true
			}
		}
		if !found {
			t.Errorf("ASSERTION: no hunk header of the text review ends with %q\n%s", w.who, text)
		}
	}
	art := artifactDir(t)
	for name, body := range map[string]string{"review-origins.json": raw, "review-origins.txt": text} {
		if err := os.WriteFile(filepath.Join(art, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
