package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// titleAgentScript is a stand-in agent that sets the window title to each line
// it reads, followed by a line of output a moment later: a client reads a title
// on the output that carries it, and the daemon on the output after it. It clears
// the screen first, so a frame shows no path from the command line.
const titleAgentScript = `#!/bin/sh
stty -echo
printf '\033[2J\033[H'
printf 'TITLE-AGENT-UP\n'
while IFS= read -r line; do
	printf '\033]2;%s\007' "$line"
	sleep 0.1
	printf 'T\n'
done
`

// spinningTitleScript is a stand-in agent at work: the title's spinner moves
// four times a second, and the name after it stays. It writes the title and
// nothing else, which is how a real agent's title moves during a long turn.
const spinningTitleScript = `#!/bin/sh
stty -echo
printf '\033[2J\033[H'
printf 'TITLE-AGENT-UP\n'
while :; do
	for f in '⠋' '⠙' '⠹' '⠸'; do
		printf '\033]2;%s spinning-agent\007' "$f"
		sleep 0.25
	done
done
`

// titleAgentRig is a folder of stand-in harnesses, one program for each name,
// and the scripts they run. A pane starts one by its full path, so the test works
// on a daemon that started before the rig was built.
type titleAgentRig struct {
	base, script, spinner string
}

// newTitleAgentRig builds the stand-in harnesses. The foreground-process
// detector names each one by its program name, the way it names the real ones.
func newTitleAgentRig(t *testing.T, base string, programs ...string) titleAgentRig {
	t.Helper()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "fakeclaude")
	if out, err := exec.Command("go", "build", "-o", fake, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	// Some manifests want more than the name: the executable's own path has to
	// look like the program's install, and a symlink's process reads as its
	// target. A hard link at that path gives the process that executable.
	exeAt := map[string]string{
		"pi":    "node-install/node",
		"letta": "node-install/node",
		"amp":   ".amp/amp",
		"grok":  ".grok/grok",
	}
	for _, name := range programs {
		target := fake
		if rel, ok := exeAt[name]; ok {
			target = filepath.Join(base, "installs", rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(fake, target); err != nil && !os.IsExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	rig := titleAgentRig{base: base, script: filepath.Join(base, "title-agent.sh"), spinner: filepath.Join(base, "spinning-agent.sh")}
	if err := os.WriteFile(rig.script, []byte(titleAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rig.spinner, []byte(spinningTitleScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return rig
}

// run starts program with the script in the window and waits until the
// detector has named the harness.
func (r titleAgentRig) run(t *testing.T, session, window, program, script, harnessID string) {
	t.Helper()
	if out, err := tuiosCLI(t, r.base, "send-text", "-s", session, "-w", window, filepath.Join(r.base, "bin", program)+" "+script+"\n"); err != nil {
		t.Fatalf("start %s: %v\n%s", program, err, out)
	}
	waitCapture(t, r.base, session, window, "TITLE-AGENT-UP")
	end := time.Now().Add(uiTimeout)
	for {
		st := readAgentState(t, r.base, session, window)
		if st.Harness == harnessID {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("the detector never named %s in %s, last read %+v", harnessID, window, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// title has the stand-in in the window set its title.
func (r titleAgentRig) title(t *testing.T, session, window, title string) {
	t.Helper()
	if out, err := tuiosCLI(t, r.base, "send-text", "-s", session, "-w", window, title+"\n"); err != nil {
		t.Fatalf("set the title %q: %v\n%s", title, err, out)
	}
}

// TestRailNamesAnAgentByItsTitle is issue 1384 on a real client and daemon. The
// rail read "claude 1" to "claude 11" for panes the person had named with
// claude -n, and the numbers moved when an earlier pane closed.
//
// The titles in the table are the ones each harness writes: measured from the
// installed program for claude, opencode, pi and crush, and from the manifest's
// [title] comment for the others. See internal/harness/title_label.go.
//
// How this could pass wrongly, written down first:
//   - The rail keeps the last label it adopted, so a title that "fell back to
//     the program" could simply never have been read. Every title follows a
//     different anchor title, and the anchor has to leave the rail.
//   - A row's name is also on the dock and the pane's border, so only the rail's
//     columns are read.
//   - The second line of a row names the harness, so a program's name is on the
//     rail whatever the label says. Rows are read by the end of the first line.
//   - A state change sends a state sync, and a sync once put the last title back.
//     A title that changes the state is followed straight away by an anchor, so
//     that race is in the test. It needs two lanes on two cores to show.
//   - Two rows with the same name would pass a Contains check on one of them.
//     The suffix test reads each row by the id of its own pane.
//   - A row that shows the spinner would pass a check for the name after it.
//     Each sample also has to hold no spinner glyph, and the same text after
//     the state mark as the first.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Rail labels an agent by its title".
func TestRailNamesAnAgentByItsTitle(t *testing.T) {
	const session = "rail"
	const cols, rows, width = 150, 50, 46
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	programs := []struct {
		program, harness string
		steps            []titleStep
	}{
		{"claude", "claude-code", []titleStep{
			{"✳ Claude Code", ""}, {"◐ Claude Code", ""}, {"Claude Code", ""}, {"claude", ""},
			{"~/dev/SENTINEL - fish", ""}, {"user@host:~/dev/SENTINEL", ""}, {"~/dev/SENTINEL", ""},
			{"✳ bob-dol-discover", "bob-dol-discover"}, {"◐ cody-1", "cody-1"}, {"⠂ Fix the test", "Fix the test"},
		}},
		{"opencode", "opencode", []titleStep{{"OpenCode", ""}, {"OC | fix the test", "fix the test"}}},
		{"pi", "pi", []titleStep{{"π - SENTINEL", ""}, {"π - fix-the-test - SENTINEL", "fix-the-test"}}},
		{"crush", "crush", []titleStep{{"crush ~/src/SENTINEL", ""}, {"fix the test", "fix the test"}}},
		{"amp", "amp", []titleStep{
			{"fix the test - amp - ~/src/SENTINEL", "fix the test"},
			{"⠹ fix the test", "fix the test"},
			{"Plugin confirmation needed - amp - ~/src/SENTINEL", ""},
		}},
		{"grok", "grok", []titleStep{
			{"grok", ""}, {"review - grok", "review"}, {"review ⠧ Running SENTINEL", "review"},
			{"⚠ Action Required - review - grok", "review"},
		}},
		{"codex", "codex", []titleStep{{"⠋ codex", ""}, {"Action Required - codex", ""}, {"Codex - Action Required", ""}}},
		{"letta", "letta", []titleStep{{"⠹ letta", ""}, {"[ ! ] Action Required | letta", ""}}},
		{"gemini", "gemini-cli", []titleStep{
			{"✦  SENTINEL thought (demo)", ""}, {"◇  Ready (demo)", ""}, {"Gemini CLI (demo)", ""},
		}},
	}
	names := make([]string, 0, len(programs))
	for _, p := range programs {
		names = append(names, p.program)
	}
	rig := newTitleAgentRig(t, base, names...)
	writeConfig(t, base, fmt.Sprintf("[daemon]\nagent_detect_seconds = 1\n\n[appearance.sidebar]\nwidth = %d\n", width))
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for range programs[1:] {
		if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--no-focus"); err != nil {
			t.Fatalf("open a pane: %v\n%s", err, out)
		}
	}
	ids := windowIDs(t, base, session)
	if len(ids) != len(programs) {
		t.Fatalf("got %d panes, want %d", len(ids), len(programs))
	}
	term := attachIn(t, base, session, startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - width
	hasRow := func(want string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			for _, row := range railAgentRows(s, railCol) {
				if strings.Contains(row, want) {
					return true
				}
			}
			return false
		}
	}
	rowsOf := func() []string { return railAgentRows(term.Screen(), railCol) }
	// A row's first line ends in its name, after its state mark. The second line
	// names the harness on every row, so a Contains check on "pi" says nothing.
	hasName := func(want string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			for _, row := range railAgentRows(s, railCol) {
				if strings.HasSuffix(row, " "+want) {
					return true
				}
			}
			return false
		}
	}

	// Each program starts with an anchor, so that its first title has to change
	// something the test can see.
	for i, p := range programs {
		rig.run(t, session, ids[i], p.program, rig.script, p.harness)
		// No title yet: the pane still has the one tuios gave it, "Terminal <id>".
		if err := term.WaitFor(hasName(p.program), uiTimeout); err != nil {
			t.Fatalf("%s: an agent with no title has no row with the program's name: %v\nrows: %q", p.program, err, rowsOf())
		}
		if p.harness == "gemini-cli" {
			// Gemini CLI writes only status there, so no title can be an anchor.
			// The state its title rule gives proves the title was read.
			for _, step := range p.steps {
				rig.title(t, session, ids[i], step.title)
			}
			rig.title(t, session, ids[i], "✦  SENTINEL thought (demo)")
			end := time.Now().Add(uiTimeout)
			for readAgentState(t, base, session, ids[i]).State != "working" {
				if time.Now().After(end) {
					t.Fatalf("the title never reached the daemon: %+v", readAgentState(t, base, session, ids[i]))
				}
				time.Sleep(100 * time.Millisecond)
			}
			time.Sleep(500 * time.Millisecond)
			if hasRow("SENTINEL")(term.Screen()) || !hasName(p.program)(term.Screen()) {
				t.Fatalf("a row took Gemini CLI's status for a name\nrows: %q", rowsOf())
			}
			continue
		}
		for n, step := range p.steps {
			anchor := fmt.Sprintf("anchor-%d-%d", i, n)
			rig.title(t, session, ids[i], "✳ "+anchor)
			if err := term.WaitFor(hasRow(anchor), uiTimeout); err != nil {
				t.Fatalf("%s: the anchor title never reached the rail: %v\nrows: %q", p.program, err, rowsOf())
			}
			rig.title(t, session, ids[i], step.title)
			want := step.want
			if want == "" {
				want = p.program
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !hasRow(anchor)(s) && hasName(want)(s)
			}, uiTimeout); err != nil {
				t.Fatalf("%s title %q: want a row with %q: %v\nrows: %q", p.program, step.title, want, err, rowsOf())
			}
			if hasRow("SENTINEL")(term.Screen()) {
				t.Fatalf("%s title %q: the rail shows what the title should have lost\nrows: %q", p.program, step.title, rowsOf())
			}
		}
		rig.title(t, session, ids[i], "✳ "+p.program+"-rest")
	}
	saveArtifact(t, term, artifactDir(t), "rail-every-harness")

	// A spinner frame does not move the row.
	rig.title(t, session, ids[0], "⠋ bob-dol-discover")
	if err := term.WaitFor(hasRow("bob-dol-discover"), uiTimeout); err != nil {
		t.Fatalf("the named agent never reached the rail: %v\nrows: %q", err, rowsOf())
	}
	rowWith := func(want string) string {
		for _, row := range rowsOf() {
			if strings.Contains(row, want) {
				return row
			}
		}
		return ""
	}
	// The state mark at the front may move, because the spinner is what the title
	// rules read. The name after it must not.
	name := func() string {
		row := rowWith("bob-dol-discover")
		return row[strings.Index(row, "bob-dol-discover"):]
	}
	first := name()
	for _, frame := range []string{"⠙", "⠹", "⠸", "⠼", "◐", "◓", "✳"} {
		rig.title(t, session, ids[0], frame+" bob-dol-discover")
		time.Sleep(300 * time.Millisecond)
		row := rowWith("bob-dol-discover")
		if got := name(); got != first {
			t.Fatalf("spinner frame %s moved the row: %q, was %q", frame, got, first)
		}
		if strings.ContainsAny(row, "⠋⠙⠹⠸⠼◐◓✳") {
			t.Fatalf("the row shows the title's spinner: %q", row)
		}
	}

	// A custom name still comes first, and a later title does not take it back.
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "-w", ids[0], "--name", "my-own-name"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	rig.title(t, session, ids[0], "✳ title-under-a-name")
	if err := term.WaitFor(hasRow("my-own-name"), uiTimeout); err != nil {
		t.Fatalf("the custom name never reached the rail: %v\nrows: %q", err, rowsOf())
	}
	time.Sleep(500 * time.Millisecond)
	if hasRow("title-under-a-name")(term.Screen()) {
		t.Fatalf("a title took the place of the custom name\nrows: %q", rowsOf())
	}
}

// titleStep is one title a harness writes, and the name the rail has to show
// for it. An empty want is the program's own name.
type titleStep struct{ title, want string }

// TestRailRepeatedLabelStaysWithItsPane closes the first of several agents that
// show only the program. The rows of the others must not change, because the
// suffix belongs to the pane. Before, it was the row's position, and every later
// row took a new number. A title on a pane this client does not watch still
// reaches the rail. Repeated rows of any other kind keep their position: two
// agents with one name, and two bare shells.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Rail labels an agent by its title".
func TestRailRepeatedLabelStaysWithItsPane(t *testing.T) {
	const session = "rail"
	const cols, rows, width = 130, 36, 40
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	rig := newTitleAgentRig(t, base, "claude")
	writeConfig(t, base, fmt.Sprintf("[daemon]\nagent_detect_seconds = 1\n\n[appearance.sidebar]\nwidth = %d\n", width))
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for range 4 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--no-focus"); err != nil {
			t.Fatalf("open a pane: %v\n%s", err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--workspace", "2", "--no-focus"); err != nil {
		t.Fatalf("open a pane on workspace 2: %v\n%s", err, out)
	}
	ids := windowIDs(t, base, session)
	if len(ids) != 6 {
		t.Fatalf("got %d panes, want 6", len(ids))
	}
	for _, id := range ids {
		rig.run(t, session, id, "claude", rig.script, "claude-code")
		rig.title(t, session, id, "✳ Claude Code")
	}
	term := attachIn(t, base, session, startOpts{cols: cols, rows: rows, shippedLooks: true})
	railCol := cols - width
	rowsOf := func(s tuitest.Screen) []string { return railAgentRows(s, railCol) }
	rowOf := func(s tuitest.Screen, id string) string {
		for _, row := range rowsOf(s) {
			if strings.Contains(row, "claude "+id[:4]) {
				return row
			}
		}
		return ""
	}
	allThere := func(ids []string) func(tuitest.Screen) bool {
		return func(s tuitest.Screen) bool {
			for _, id := range ids {
				if rowOf(s, id) == "" {
					return false
				}
			}
			return true
		}
	}
	if err := term.WaitFor(allThere(ids), uiTimeout); err != nil {
		t.Fatalf("a row does not carry the start of its window id: %v\nids: %q\nrows: %q", err, ids, rowsOf(term.Screen()))
	}
	dir := artifactDir(t)
	railShot(t, term, "rail-repeated-before-close")

	// What the row shows is enough to address the pane, as the docs say.
	if out, err := tuiosCLI(t, base, "focus-window", "-s", session, "--json", ids[3][:4]); err != nil || !strings.Contains(out, ids[3]) {
		t.Fatalf("focus-window with the suffix from the row: %v\n%s", err, out)
	}

	// Two panes take a name, and the name is what the frame for the issue shows.
	rig.title(t, session, ids[1], "✳ bob-dol-discover")
	rig.title(t, session, ids[2], "◐ cody-1")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return allThere([]string{ids[0], ids[3], ids[4], ids[5]})(s) &&
			strings.Contains(strings.Join(rowsOf(s), "\n"), "bob-dol-discover") &&
			strings.Contains(strings.Join(rowsOf(s), "\n"), "cody-1")
	}, uiTimeout); err != nil {
		t.Fatalf("the named panes never reached the rail: %v\nrows: %q", err, rowsOf(term.Screen()))
	}
	railShot(t, term, "rail-named-agents")

	// The window that goes is the first on the rail.
	if out, err := tuiosCLI(t, base, "close-window", "-s", session, ids[0]); err != nil {
		t.Fatalf("close the first pane: %v\n%s", err, out)
	}
	left := []string{ids[3], ids[4], ids[5]}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return rowOf(s, ids[0]) == "" && allThere(left)(s)
	}, uiTimeout); err != nil {
		t.Fatalf("a row changed its suffix when an earlier pane closed: %v\nids: %q\nrows: %q", err, left, rowsOf(term.Screen()))
	}
	railShot(t, term, "rail-repeated-after-close")

	// ids[5] is on workspace 2, and this client does not watch it. Its title
	// reaches the rail through the daemon's listing.
	rig.title(t, session, ids[5], "✳ on-another-workspace")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(strings.Join(rowsOf(s), "\n"), "on-another-workspace")
	}, uiTimeout); err != nil {
		t.Fatalf("the title of an unwatched pane never reached the rail: %v\nrows: %q", err, rowsOf(term.Screen()))
	}
	saveArtifact(t, term, dir, "rail-unwatched-title")

	// A title gave these two a name, so a position is enough. ids[0] has closed,
	// so ids[3] and ids[4] are the third and fourth panes.
	rig.title(t, session, ids[3], "✳ twin")
	rig.title(t, session, ids[4], "◐ twin")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		rows := strings.Join(rowsOf(s), "\n")
		return strings.Contains(rows, "twin 3") && strings.Contains(rows, "twin 4")
	}, uiTimeout); err != nil {
		t.Fatalf("two agents with one name do not show their positions: %v\nrows: %q", err, rowsOf(term.Screen()))
	}

	// Two shells in one folder keep plain numbers. A new shell's title is unique,
	// so each one writes the title of a shell in that folder. They are the sixth
	// and seventh of the panes left.
	known := map[string]bool{}
	for _, id := range windowIDs(t, base, session) {
		known[id] = true
	}
	for range 2 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", session, "--no-focus"); err != nil {
			t.Fatalf("open a shell: %v\n%s", err, out)
		}
	}
	for _, id := range windowIDs(t, base, session) {
		if known[id] {
			continue
		}
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", id, `printf '\033]2;~/dev/app - fish\007'`+"\n"); err != nil {
			t.Fatalf("set the shell's title: %v\n%s", err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		c, r := s.Size()
		found := 0
		for y := range r {
			var b strings.Builder
			for x := railCol; x < c; x++ {
				b.WriteString(s.Cell(x, y).Content)
			}
			row := strings.TrimRight(b.String(), " ")
			if strings.HasSuffix(row, " app 6") || strings.HasSuffix(row, " app 7") {
				found++
			}
		}
		return found == 2
	}, uiTimeout); err != nil {
		t.Fatalf("repeated shells do not show plain numbers: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "rail-repeated-shells")
}
