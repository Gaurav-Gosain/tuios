package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestNarrowRailKeepsWhatAnAgentIsDoing is issue 181 on a real client. With
// the state listed after the name, a 24-column rail read
// "deploy-the-api-gate…": the long name took every cell and the word saying
// what the pane was doing went, because the row dropped its last token first.
// The row now budgets its cells. The name holds eight cells and an ellipsis
// while the tokens beside it are placed, so the state survives, and the
// harness prefix, which needs the whole name beside it, goes.
//
// How this could pass wrongly, written down first:
//   - A rail that dropped the prefix everywhere would pass the long row. The
//     short row in the same frame is the positive half: its whole name fits,
//     so it must still say "claude/web · working".
//   - A rail that drew the state by cutting inside it would show "work…". The
//     row is read for the whole word.
//   - The pane names are also on the dock, so rows are read in the rail's
//     columns, under the agents header.
//   - The name's keep is eight cells and an ellipsis, so a harness prefix
//     would fit beside a long name cut to its keep. The shipped row, with no
//     state, checks that the prefix still waits for the whole name.
func TestNarrowRailKeepsWhatAnAgentIsDoing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens string
		state  bool
	}{
		{"state", `["session", "harness", "name", "state", "elapsed", "need", "context", "meta", "now", "message"]`, true},
		{"shipped", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) { narrowRailAgentRows(t, tc.tokens, tc.state) })
	}
}

func narrowRailAgentRows(t *testing.T, tokens string, state bool) {
	const cols, rows, width = 100, 24, 24
	const long, short = "deploy-the-api-gateway", "web"
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	config := "[appearance.sidebar]\nwidth = 24\n"
	if tokens != "" {
		config += "\n[appearance.sidebar.agent_row]\ntokens = " + tokens + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", "rail", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "rail", "--name", long); err != nil {
		t.Fatalf("name the first pane: %v\n%s", err, out)
	}
	// Enough agents that each row has one line, which is the line the
	// harness and the state share with the name.
	names := []string{long, short, "db", "api", "ci", "docs"}
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
	railCol := cols - width
	railText := func(s tuitest.Screen, y int) string {
		var b strings.Builder
		for x := railCol; x < cols; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		return b.String()
	}
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
	saveArtifact(t, term, artifactDir(t), "rail-agents-24")

	s := term.Screen()
	if row := rowOf(s, "claude/"+short); state && !strings.Contains(row, "claude/"+short+" · working") {
		t.Errorf("ASSERTION: the short agent should keep its harness and its state: %q", row)
	}
	// The name keeps at least its first eight cells.
	row := rowOf(s, long[:8])
	if row == "" {
		t.Fatalf("ASSERTION: the long agent has no row with the start of its name; agent rows: %q\n%s", agentRows(s), term.Snapshot())
	}
	if state && !strings.Contains(row, " · working") {
		t.Errorf("ASSERTION: the long name took the cells of the state beside it: %q", row)
	}
	if strings.Contains(row, "claude/") {
		t.Errorf("ASSERTION: the row kept a prefix beside a name it had cut: %q", row)
	}
}

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
