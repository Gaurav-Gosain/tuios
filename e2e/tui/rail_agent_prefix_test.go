package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestNarrowRailKeepsTheAgentNameBeforeItsHarness: on the rail an 80 column
// screen gets, an agent called deploy read "claude/de…". The harness prefix
// was kept as long as two cells of the name were left, so the one word a
// person scans the rail for was the word that got cut. The prefix now gives
// way first, and the name is cut only when it alone does not fit. The frame
// is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The rail could draw no prefix at any width, and the long row would read
//     right for the wrong reason. The short row in the same frame is the
//     positive half: its name leaves room, so it must still say "claude/".
//   - The pane names are also on the dock and in the terminals section, so the
//     rows are read inside the rail's columns, under the agents header.
//   - The rows could be read before the harness reaches them, so the test
//     waits for the short row's prefix first.
//   - A name too long to fit alone would be cut either way, so the long name
//     is one that fits the row by itself and not with "claude/" in front.
func TestNarrowRailKeepsTheAgentNameBeforeItsHarness(t *testing.T) {
	const cols, rows = 80, 24
	const long, tooLong, short = "deploy", "migrate-billing", "db"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "rail", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "rail", "--name", long); err != nil {
		t.Fatalf("name the first pane: %v\n%s", err, out)
	}
	// Enough agents that the section has no room for a second line per row,
	// which is where the harness goes when it can. A one-line row is the one
	// that puts the harness in front of the name.
	names := []string{long, tooLong, short, "web", "api", "ci"}
	for _, name := range names[1:] {
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "rail", "--no-focus"); err != nil {
			t.Fatalf("open pane %s: %v\n%s", name, err, out)
		}
	}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "rail", "-w", name, "working", "--harness", "claude-code"); err != nil {
			t.Fatalf("set-agent-state on %s: %v\n%s", name, err, out)
		}
	}
	term := attachIn(t, base, "rail", startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - narrowRailWidth
	railText := func(s tuitest.Screen, y int) string {
		var b strings.Builder
		for x := railCol; x < cols; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		return b.String()
	}
	// The rows under the agents section's header, up to the next blank row.
	agentRows := func(s tuitest.Screen) []string {
		var out []string
		for y := range rows - 1 {
			if !strings.HasPrefix(strings.TrimSpace(strings.Trim(railText(s, y), "│ ")), "agents") {
				continue
			}
			for r := y + 1; r < rows; r++ {
				row := railText(s, r)
				if strings.TrimSpace(strings.Trim(row, "│ ")) == "" {
					break
				}
				out = append(out, row)
			}
			break
		}
		return out
	}
	rowOf := func(s tuitest.Screen, want string) string {
		for _, row := range agentRows(s) {
			if strings.Contains(row, want) {
				return row
			}
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return rowOf(s, "claude/"+short) != "" }, uiTimeout); err != nil {
		t.Fatalf("the rail never drew the short agent with its harness: %v\nagent rows: %q\n%s", err, agentRows(term.Screen()), term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-80x24")

	s := term.Screen()
	row := rowOf(s, long)
	if row == "" {
		t.Fatalf("ASSERTION: the rail cut the agent's name %q to make room for its harness; agent rows: %q\n%s", long, agentRows(s), term.Snapshot())
	}
	if strings.Contains(row, "claude/") {
		t.Errorf("ASSERTION: the row kept a prefix beside a name it had no room for: %q", row)
	}
	// A name too long for the row on its own is the one that is cut, and it
	// is cut with no prefix in front of it.
	cut := rowOf(s, tooLong[:8])
	if cut == "" || strings.Contains(cut, "claude/") {
		t.Errorf("ASSERTION: the name too long to fit alone should fill the row, cut, with no prefix: %q", cut)
	}
}
