package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The agent-state marks come from the glyph set, one role per state, and a
// mark may be two cells. These tests write a set file the way a person does,
// report states over the CLI the way a pane's shim does, and read the cells
// the host terminal shows.

// marksClient starts a daemon with one attached session holding three named
// agent panes, one needing the person, one working and one at rest, with the
// glyph set file and the config given. set is written as glyphs/hands.json
// when it is not empty.
func marksClient(t *testing.T, cfg, set string, args ...string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, cfg)
	if set != "" {
		dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "glyphs")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hands.json"), []byte(set), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	for _, p := range [][2]string{{"work-pane", "working"}, {"rest-pane", "idle"}, {"hand-pane", "needs_input"}} {
		if out, err := tuiosCLI(t, base, "new-window", p[0], "-s", "home", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", p[0], err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", p[1], "-s", "home", "-w", p[0], "--harness", "claude-code"); err != nil {
			t.Fatalf("report %s: %v\n%s", p[0], err, out)
		}
	}
	opts := startOpts{cols: 140, rows: 44, args: append([]string{"attach", "home"}, args...), shippedLooks: true}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "hand-pane") && strings.Contains(text, "rest-pane")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the three agent panes: %v\n%s", err, term.Snapshot())
	}
	return term, base
}

// markRowCells is one screen row as the text its cells hold, and the cell column
// each byte of that text sits in, so a wide mark in front of a name does not
// move the column the name is reported at.
func markRowCells(s tuitest.Screen, row int) (string, []int) {
	cols, _ := s.Size()
	var line strings.Builder
	at := make([]int, 0, cols)
	for c := range cols {
		content := s.Cell(c, row).Content
		if content == "" {
			continue
		}
		for range len(content) {
			at = append(at, c)
		}
		line.WriteString(content)
	}
	return line.String(), at
}

// markedAt is every place text is drawn on screen, as row and cell column.
func markedAt(s tuitest.Screen, text string) [][2]int {
	_, rows := s.Size()
	var out [][2]int
	for r := range rows {
		line, at := markRowCells(s, r)
		from := 0
		for {
			i := strings.Index(line[from:], text)
			if i < 0 {
				break
			}
			out = append(out, [2]int{r, at[from+i]})
			from += i + len(text)
		}
	}
	return out
}

// marksSet is the set the custom tests select: a two-cell mark for an agent
// that needs the person, a narrow one of its own for a working agent, and
// every other state left to the default.
const marksSet = `{"inherits": "default", "state_needs_input": "✋", "state_working": "◆"}`

// railNames finds the rows of the rail that draw each name, right of the
// rail's own header column, and returns the cell column each name starts on.
func railNames(s tuitest.Screen, railX int, names ...string) map[string][2]int {
	out := map[string][2]int{}
	for _, name := range names {
		for _, at := range markedAt(s, name) {
			if at[1] > railX {
				out[name] = at
				break
			}
		}
	}
	return out
}

// railColumn is the cell column of the rail's "sessions" header, the left
// edge every rail row's text is right of.
func railColumn(s tuitest.Screen) int {
	for _, at := range markedAt(s, "sessions") {
		return at[1] - 1
	}
	return -1
}

// TestAgentMarksFollowTheGlyphSet: a glyph set names a two-cell mark for
// needs_input and a narrow one for working. The rail, the title bar and the
// cockpit draw the set's marks, the rows they are on keep their names in one
// column with a row whose mark is one cell, and nothing is pushed past the
// rail's right edge or the title bar's corner. The last frames are saved
// under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The mark could be found anywhere, the dock's message included, so the
//     rail's marks are read right of the rail's header column and only on the
//     row of the pane's own name, two cells in front of it.
//   - Names in one column could be the default one-cell layout with the wide
//     mark overdrawing the air after it. The cell after the mark has to be
//     blank, and the wide mark has to be a two-cell cell.
//   - The title could fit by luck of the window's width. The front window's
//     title-bar corner has to be in the column of its own bottom corner.
//   - The cockpit could draw the rail's marks through some other path, so its
//     working row has to wear the set's ◆ and not its own Nerd Font glyph,
//     and its idle row its own design's ○.
//
// Negative controls are in NEGATIVE_CONTROLS.md under "Agent-state marks from
// the glyph set".
func TestAgentMarksFollowTheGlyphSet(t *testing.T) {
	term, _ := marksClient(t, "[appearance]\nglyphs = \"hands\"\n", marksSet)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x := railColumn(s)
		got := railNames(s, x, "✋ hand-pane", "◆", "rest-pane")
		return x > 0 && len(got) == 3
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never drew the set's marks in front of the agent rows: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	cols, _ := s.Size()
	railX := railColumn(s)
	rows := railNames(s, railX, "hand-pane", "work-pane", "rest-pane")
	if len(rows) != 3 {
		t.Fatalf("the rail's agents section did not list all three panes: %v\n%s", rows, term.Snapshot())
	}
	nameCol := rows["hand-pane"][1]
	for name, at := range rows {
		if at[1] != nameCol {
			t.Errorf("ASSERTION: %s starts at column %d and hand-pane at %d: a two-cell mark moved its row's name off the spine\n%s",
				name, at[1], nameCol, term.Snapshot())
		}
	}
	hand := s.Cell(nameCol-3, rows["hand-pane"][0])
	if hand.Content != "✋" || hand.Width != 2 {
		t.Errorf("ASSERTION: the needs-input mark is %q, %d cells, two cells before the name; want ✋ in two cells\n%s",
			hand.Content, hand.Width, term.Snapshot())
	}
	for name, mark := range map[string]string{"work-pane": "◆", "rest-pane": "○"} {
		row := rows[name][0]
		if c := s.Cell(nameCol-3, row); c.Content != mark {
			t.Errorf("ASSERTION: %s wears %q in the mark column, want %q\n%s", name, c.Content, mark, term.Snapshot())
		}
		if c := s.Cell(nameCol-2, row); c.Content != " " && c.Content != "" {
			t.Errorf("ASSERTION: %s has %q where its narrow mark's padding goes\n%s", name, c.Content, term.Snapshot())
		}
	}
	// The sessions row's pane count is the right-hand figure, and it sits in
	// the column of the header's add control when the row fits the rail.
	var plus, count int
	for _, at := range markedAt(s, "sessions") {
		line, idx := markRowCells(s, at[0])
		plus = idx[strings.LastIndex(line, "+")]
	}
	for _, at := range markedAt(s, "✋ home") {
		line, idx := markRowCells(s, at[0])
		count = idx[len(strings.TrimRight(line, " "))-1]
	}
	if plus == 0 || count != plus || plus >= cols {
		t.Errorf("ASSERTION: the ✋ home row ends at column %d and the header's + at %d: the row overran the rail\n%s",
			count, plus, term.Snapshot())
	}

	// The title bar of the front window, hand-pane's.
	var title [2]int
	for _, at := range markedAt(s, "✋ hand-pane") {
		if at[1] < railX {
			title = at
		}
	}
	if title == [2]int{} {
		t.Fatalf("ASSERTION: no title bar wears ✋ hand-pane\n%s", term.Snapshot())
	}
	line, idx := markRowCells(s, title[0])
	corner := idx[strings.LastIndex(line[:strings.LastIndex(line, "│")+1], "╮")]
	bottom := -1
	_, rowsN := s.Size()
	for r := title[0] + 1; r < rowsN; r++ {
		l, at := markRowCells(s, r)
		if i := strings.Index(l, "╯"); i >= 0 && at[i] < railX {
			bottom = at[i]
		}
	}
	if corner != bottom || corner < title[1]+len("  hand-pane") {
		t.Errorf("ASSERTION: the title bar's corner is at column %d and the frame's bottom corner at %d\n%s",
			corner, bottom, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "agent-marks-rail")

	// The cockpit lists the same three agents in its fleet column.
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "hand-pane") >= 0 && fleetRowWith(s, "work-pane") >= 0 && fleetRowWith(s, "rest-pane") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("the cockpit never listed the three agents: %v\n%s", err, term.Snapshot())
	}
	s = term.Screen()
	cockpit := map[string][2]int{}
	for _, name := range []string{"hand-pane", "work-pane", "rest-pane"} {
		r := fleetRowWith(s, name)
		line, at := markRowCells(s, r)
		cockpit[name] = [2]int{r, at[strings.Index(line, name)]}
	}
	col := cockpit["hand-pane"][1]
	for name, at := range cockpit {
		if at[1] != col {
			t.Errorf("ASSERTION: the cockpit starts %s at column %d and hand-pane at %d\n%s", name, at[1], col, term.Snapshot())
		}
	}
	for name, mark := range map[string]string{"hand-pane": "✋", "work-pane": "◆", "rest-pane": "○"} {
		line, _ := markRowCells(s, cockpit[name][0])
		before := line[:strings.Index(line, name)]
		if !strings.Contains(before, mark) {
			t.Errorf("ASSERTION: the cockpit's %s row reads %q before the name, want the mark %q\n%s", name, before, mark, term.Snapshot())
		}
	}
	// The counts over the fleet wear the same marks as its rows.
	if head := markedAt(s, "✋ 1"); len(head) == 0 || len(markedAt(s, "◆ 1")) == 0 {
		t.Errorf("ASSERTION: the cockpit's counts do not wear the set's marks\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "agent-marks-cockpit")
	alive(t, term, "after the cockpit drew the set's marks")
}

// TestAgentMarksKeepTheirDefaults: with no glyph set the rail draws the
// shipped ▲ and the cockpit its own design's ●, and under --ascii-only or the
// built-in ascii set both draw "!". It is the half of the custom-set test that
// must not move: a set that names nothing changes nothing, and ASCII mode
// still wins.
func TestAgentMarksKeepTheirDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, mark, cockpit string
		args                     []string
	}{
		{"unicode", "", "▲", "●", nil},
		{"ascii-only", "", "!", "!", []string{"--ascii-only"}},
		{"ascii-set", "[appearance]\nglyphs = \"ascii\"\n", "!", "!", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := marksClient(t, tc.cfg, "", tc.args...)
			want := tc.mark + " hand-pane"
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				x := railColumn(s)
				return x > 0 && len(railNames(s, x, want)) == 1
			}, uiTimeout); err != nil {
				t.Fatalf("the rail never drew %q: %v\n%s", want, err, term.Snapshot())
			}
			s := term.Screen()
			rows := railNames(s, railColumn(s), "hand-pane", "work-pane", "rest-pane")
			for name, at := range rows {
				if at[1] != rows["hand-pane"][1] {
					t.Errorf("ASSERTION: %s starts at column %d and hand-pane at %d\n%s", name, at[1], rows["hand-pane"][1], term.Snapshot())
				}
			}
			windowManagementMode(t, term)
			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool { return fleetRowWith(s, "hand-pane") >= 0 }, uiTimeout); err != nil {
				t.Fatalf("the cockpit never listed hand-pane: %v\n%s", err, term.Snapshot())
			}
			s = term.Screen()
			line, _ := markRowCells(s, fleetRowWith(s, "hand-pane"))
			if before := line[:strings.Index(line, "hand-pane")]; !strings.Contains(before, tc.cockpit) {
				t.Errorf("ASSERTION: the cockpit's hand-pane row reads %q before the name, want its own %q\n%s", before, tc.cockpit, term.Snapshot())
			}
		})
	}
}

// TestAgentMarksRefuseWhatTheyCannotDraw: a set file names a mark four cells
// wide, one wide only through the emoji variation selector, and one carrying
// an escape. Each is dropped back to the default with a line in list-glyphs'
// problems naming its role, and the rail draws the default ▲.
//
// How this could pass wrongly: the default could draw because the set was not
// read at all, so the set's one good mark, state_working, has to draw too.
func TestAgentMarksRefuseWhatTheyCannotDraw(t *testing.T) {
	set := `{"inherits": "default", "state_needs_input": "✋✋", "state_idle": "⚠\ufe0f", "state_done": "\u001b[31mx", "state_working": "◆"}`
	term, base := marksClient(t, "[appearance]\nglyphs = \"hands\"\n", set)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x := railColumn(s)
		return x > 0 && len(railNames(s, x, "▲ hand-pane", "◆ work-pane", "○ rest-pane")) == 3
	}, uiTimeout); err != nil {
		t.Fatalf("the rail did not fall back to ▲ and ○ beside the set's ◆: %v\n%s", err, term.Snapshot())
	}
	out, err := tuiosCLI(t, base, "list-glyphs", "hands", "--json")
	if err != nil {
		t.Fatalf("list-glyphs: %v\n%s", err, out)
	}
	var got struct {
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list-glyphs --json did not parse: %v\n%s", err, out)
	}
	joined := strings.Join(got.Problems, "\n")
	for _, role := range []string{"state_needs_input", "state_idle", "state_done"} {
		if !strings.Contains(joined, "glyph set hands: "+role+" ") {
			t.Errorf("ASSERTION: list-glyphs reports no problem for %s:\n%s", role, joined)
		}
	}
	if strings.Contains(joined, "state_working") {
		t.Errorf("ASSERTION: list-glyphs refused the good mark:\n%s", joined)
	}
}
