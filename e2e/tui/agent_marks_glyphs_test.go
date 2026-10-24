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

// markLayout is where one run draws hand-pane's agent mark and name, as cell
// columns: on its rail row, and on its row in the cockpit's fleet column.
type markLayout struct {
	railMark, railName, ckMark, ckName int
}

// cellLeftOf is the column of the nearest cell left of col on row that holds
// content, or -1.
func cellLeftOf(s tuitest.Screen, row, col int, content string) int {
	for c := col - 1; c >= 0; c-- {
		if s.Cell(c, row).Content == content {
			return c
		}
	}
	return -1
}

// cockpitRows opens the cockpit and returns the row and the cell column of
// each name in its fleet column.
func cockpitRows(t *testing.T, term *tuitest.Terminal, names ...string) (tuitest.Screen, map[string][2]int) {
	t.Helper()
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, name := range names {
			if fleetRowWith(s, name) < 0 {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("the cockpit never listed %v: %v\n%s", names, err, term.Snapshot())
	}
	s := term.Screen()
	out := map[string][2]int{}
	for _, name := range names {
		r := fleetRowWith(s, name)
		line, at := markRowCells(s, r)
		out[name] = [2]int{r, at[strings.Index(line, name)]}
	}
	return s, out
}

// defaultMarkLayout is where the shipped marks put hand-pane's mark and name:
// the rail's ▲ and the cockpit's own ●, both one cell. A glyph set's marks
// are held to these columns.
func defaultMarkLayout(t *testing.T) markLayout {
	t.Helper()
	term, _ := marksClient(t, "", "")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x := railColumn(s)
		return x > 0 && len(railNames(s, x, "▲ hand-pane")) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the default rail never drew ▲ hand-pane: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	rail := railNames(s, railColumn(s), "hand-pane")["hand-pane"]
	var l markLayout
	l.railName = rail[1]
	l.railMark = cellLeftOf(s, rail[0], rail[1], "▲")
	s, ck := cockpitRows(t, term, "hand-pane")
	l.ckName = ck["hand-pane"][1]
	l.ckMark = cellLeftOf(s, ck["hand-pane"][0], l.ckName, "●")
	if l.railMark < 0 || l.ckMark < 0 {
		t.Fatalf("no default mark in front of hand-pane: %+v\n%s", l, term.Snapshot())
	}
	return l
}

// TestAgentMarksFollowTheGlyphSet: a glyph set names a two-cell mark for
// needs_input and a narrow one for working. The rail, the title bar and the
// cockpit draw the set's marks. The rail's mark column is as wide as the
// set's widest mark and one blank, so with ✋ in the set every rail name
// starts one column right of where it starts with the shipped marks, all in
// one column: ✋ keeps one blank before its name and a narrow mark two.
// Nothing is pushed past the rail's right edge or the title bar's corner. The
// rail and cockpit frames are saved under artifactDir, as text and PNG, before
// anything is asserted on them.
//
// How this could pass wrongly, written down first:
//   - The mark could be found anywhere, the dock's message included, so the
//     rail's marks are read right of the rail's header column and only on the
//     row of the pane's own name.
//   - The names could share a column that moved by some other amount, or
//     not at all, which leaves ✋ against its name. The columns are compared
//     with a run on the shipped marks, not only with each other.
//   - ✋ could overdraw the blank it is meant to have. It has to be a
//     two-cell cell in the column the shipped mark is drawn in, with one
//     blank cell and then the name.
//   - The title could fit by luck of the window's width. The front window's
//     title-bar corner has to be in the column of its own bottom corner.
//   - The cockpit could draw the rail's marks through some other path, so its
//     working row has to wear the set's ◆ and not its own Nerd Font glyph,
//     and its idle row its own design's ○.
//
// Negative controls are in NEGATIVE_CONTROLS.md under "Agent-state marks from
// the glyph set".
func TestAgentMarksFollowTheGlyphSet(t *testing.T) {
	want := defaultMarkLayout(t)
	term, _ := marksClient(t, "[appearance]\nglyphs = \"hands\"\n", marksSet)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x := railColumn(s)
		got := railNames(s, x, "hand-pane", "◆", "rest-pane")
		return x > 0 && len(got) == 3 && len(markedAt(s, "✋")) > 0
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never drew the set's marks in front of the agent rows: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "agent-marks-rail")
	s := term.Screen()
	cols, _ := s.Size()
	railX := railColumn(s)
	rows := railNames(s, railX, "hand-pane", "work-pane", "rest-pane")
	if len(rows) != 3 {
		t.Fatalf("the rail's agents section did not list all three panes: %v\n%s", rows, term.Snapshot())
	}
	for name, at := range rows {
		if at[1] != want.railName+1 {
			t.Errorf("ASSERTION: %s starts at column %d, and at %d with the shipped marks: want one column further right for the set's two-cell mark\n%s",
				name, at[1], want.railName, term.Snapshot())
		}
	}
	hand := s.Cell(want.railMark, rows["hand-pane"][0])
	if hand.Content != "✋" || hand.Width != 2 || want.railMark+3 != rows["hand-pane"][1] {
		t.Errorf("ASSERTION: column %d holds %q, %d cells, and hand-pane starts at %d; want ✋ in two cells, one blank, then the name\n%s",
			want.railMark, hand.Content, hand.Width, rows["hand-pane"][1], term.Snapshot())
	}
	if c := s.Cell(want.railMark+2, rows["hand-pane"][0]); c.Content != " " && c.Content != "" {
		t.Errorf("ASSERTION: hand-pane has %q in the blank after ✋\n%s", c.Content, term.Snapshot())
	}
	for name, mark := range map[string]string{"work-pane": "◆", "rest-pane": "○"} {
		row := rows[name][0]
		if c := s.Cell(want.railMark, row); c.Content != mark {
			t.Errorf("ASSERTION: %s wears %q in the mark column, want %q\n%s", name, c.Content, mark, term.Snapshot())
		}
		for col := want.railMark + 1; col <= want.railMark+2; col++ {
			if c := s.Cell(col, row); c.Content != " " && c.Content != "" {
				t.Errorf("ASSERTION: %s has %q at column %d, in the two blanks after its narrow mark\n%s", name, c.Content, col, term.Snapshot())
			}
		}
	}
	// The sessions row's pane count is the right-hand figure, and it sits in
	// the column of the header's add control when the row fits the rail.
	var plus, count int
	for _, at := range markedAt(s, "sessions") {
		line, idx := markRowCells(s, at[0])
		plus = idx[strings.LastIndex(line, "+")]
	}
	homes := markedAt(s, "✋ home")
	for _, at := range homes {
		line, idx := markRowCells(s, at[0])
		count = idx[len(strings.TrimRight(line, " "))-1]
	}
	if len(homes) == 0 {
		t.Errorf("ASSERTION: no rail row reads ✋ home, the session's mark one blank before its name\n%s", term.Snapshot())
	} else if plus == 0 || count != plus || plus >= cols {
		t.Errorf("ASSERTION: the ✋ home row ends at column %d and the header's + at %d: the row overran the rail\n%s",
			count, plus, term.Snapshot())
	}

	// The title bar of the front window, hand-pane's. A title is not a column,
	// so its mark keeps its space.
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

	// The cockpit lists the same three agents in its fleet column, each name
	// where the shipped marks put it, each mark in the shipped mark's column.
	s, cockpit := cockpitRows(t, term, "hand-pane", "work-pane", "rest-pane")
	railShot(t, term, "agent-marks-cockpit")
	for name, at := range cockpit {
		if at[1] != want.ckName {
			t.Errorf("ASSERTION: the cockpit starts %s at column %d, and at %d with the shipped marks\n%s", name, at[1], want.ckName, term.Snapshot())
		}
	}
	for name, mark := range map[string]string{"hand-pane": "✋", "work-pane": "◆", "rest-pane": "○"} {
		row := cockpit[name][0]
		c := s.Cell(want.ckMark, row)
		if c.Content != mark {
			line, _ := markRowCells(s, row)
			t.Errorf("ASSERTION: the cockpit's %s row has %q in the mark column, want %q; the row reads %q\n%s", name, c.Content, mark, line, term.Snapshot())
			continue
		}
		for col := want.ckMark + max(c.Width, 1); col < want.ckName; col++ {
			if g := s.Cell(col, row).Content; g != " " && g != "" {
				t.Errorf("ASSERTION: the cockpit's %s row has %q at column %d, between its mark and its name\n%s", name, g, col, term.Snapshot())
			}
		}
	}
	// The counts over the fleet wear the same marks as its rows.
	if head := markedAt(s, "✋ 1"); len(head) == 0 || len(markedAt(s, "◆ 1")) == 0 {
		t.Errorf("ASSERTION: the cockpit's counts do not wear the set's marks\n%s", term.Snapshot())
	}
	alive(t, term, "after the cockpit drew the set's marks")
}

// namesInBox is the first place each name is drawn inside a box of the screen,
// rows top to bottom and columns left to right, both exclusive, as row and
// cell column.
func namesInBox(s tuitest.Screen, top, bottom, left, right int, names ...string) map[string][2]int {
	out := map[string][2]int{}
	for _, name := range names {
		for _, at := range markedAt(s, name) {
			if at[0] > top && at[0] < bottom && at[1] > left && at[1] < right {
				out[name] = at
				break
			}
		}
	}
	return out
}

// markCells is how many cells each mark the lists draw takes.
var markCells = map[string]int{"✋": 2, "◆": 1, "○": 1}

// markLeads holds the rows of one list to the rail's rule: every name in one
// column, every mark the lead's width in front of it, ✋ in two cells and a
// narrow mark in one, and the cell before each name blank. marks names the
// mark each row wears, "" for none.
func markLeads(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, list string, at map[string][2]int, marks map[string]string) {
	t.Helper()
	if len(at) != len(marks) {
		t.Fatalf("ASSERTION: the %s lists %d of the %d panes: %v\n%s", list, len(at), len(marks), at, term.Snapshot())
	}
	col := at["hand-pane"][1]
	for name, mark := range marks {
		row, x := at[name][0], at[name][1]
		if x != col {
			t.Errorf("ASSERTION: the %s starts %s (mark %q) at column %d and hand-pane (mark ✋) at %d: the names are not in one column\n%s",
				list, name, mark, x, col, term.Snapshot())
			continue
		}
		c := s.Cell(col-3, row)
		switch {
		case mark == "" && c.Content != " " && c.Content != "":
			t.Errorf("ASSERTION: the %s's %s row has %q in the mark column, want a blank\n%s", list, name, c.Content, term.Snapshot())
		case mark != "" && (c.Content != mark || c.Width != markCells[mark]):
			t.Errorf("ASSERTION: the %s's %s row has %q, %d cells, three columns before its name, want %q\n%s",
				list, name, c.Content, c.Width, mark, term.Snapshot())
		}
		if g := s.Cell(col-1, row).Content; g != " " && g != "" {
			t.Errorf("ASSERTION: the %s's %s row has %q in the blank before its name\n%s", list, name, g, term.Snapshot())
		}
	}
}

// TestAgentMarksLineUpInEveryList: the navigator and the aggregate view lead
// their pane rows with the agent mark the way the rail does, so with ✋ in the
// set their names sit in one column whether the row wears ✋, a one-cell mark
// or no mark: three cells after the start of the mark column, the widest mark
// and one blank. The navigator's preview puts its location line under the
// name in its header. The frames are saved under artifactDir, as text and
// PNG, before anything is asserted on them.
//
// How this could pass wrongly, written down first:
//   - A name could be read off the rail, a title bar or the preview rather
//     than the list. Each list is read only inside its own box: the
//     navigator's Panes box, left of its Preview box, and the aggregate
//     view's rows between its title and its hint, left of the rail.
//   - The names could share a column because every row wears the same kind
//     of mark. The list has to hold a ✋ row, two one-cell rows (◆ and ○) and
//     a row with no mark at all (plain-a, a pane with no agent), and the cell
//     three columns before each name has to be that row's mark.
//   - The names could line up because the marks overdraw the blank in front
//     of them. The cell before each name has to be blank.
//   - The preview's location line could line up with a header that has no
//     mark only by luck of the lead. Its first character has to be in the
//     column of the header's name.
//
// Negative controls are in NEGATIVE_CONTROLS.md under "Agent-state marks from
// the glyph set".
func TestAgentMarksLineUpInEveryList(t *testing.T) {
	term, base := marksClient(t, "[appearance]\nglyphs = \"hands\"\n", marksSet)
	for _, name := range []string{"plain-a", "plain-b"} {
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "home", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", name, err, out)
		}
	}
	waitScreen(t, term, "the rail never listed the plain panes", "plain-a", "plain-b")
	marks := map[string]string{"hand-pane": "✋", "work-pane": "◆", "rest-pane": "○", "plain-a": ""}
	names := []string{"hand-pane", "work-pane", "rest-pane", "plain-a"}

	openNavigator(t, term, names...)
	railShot(t, term, "agent-marks-navigator")
	s := term.Screen()
	cols, rows := s.Size()
	panes, preview := markedAt(s, "─ Panes ─"), markedAt(s, "╭─ Preview")
	if len(panes) != 1 || len(preview) != 1 {
		t.Fatalf("the navigator's Panes and Preview boxes are not on screen: %v %v\n%s", panes, preview, term.Snapshot())
	}
	markLeads(t, term, s, "navigator", namesInBox(s, panes[0][0], rows, -1, preview[0][1], names...), marks)

	// The preview's header is the selected pane's mark and name, and the line
	// under it is where the pane is, in the column of the name.
	head := preview[0][0] + 1
	line, at := markRowCells(s, head)
	first := func(line string, at []int, from int) int {
		for i, r := range line {
			if at[i] > from && r != ' ' && r != '│' {
				return at[i]
			}
		}
		return -1
	}
	where, whereAt := markRowCells(s, head+1)
	nameCol := first(line, at, preview[0][1])
	if c := s.Cell(nameCol, head); strings.ContainsAny(c.Content, "✋◆○▲") {
		nameCol = first(line, at, nameCol+c.Width)
	}
	if got := first(where, whereAt, preview[0][1]); nameCol < 0 || got != nameCol {
		t.Errorf("ASSERTION: the preview's location line starts at column %d and its header's name at %d\n%s", got, nameCol, term.Snapshot())
	}

	sendKeys(t, term, tuitest.Esc)
	windowManagementMode(t, term)
	openAggregateView(t, term)
	railShot(t, term, "agent-marks-aggregate")
	s = term.Screen()
	title, hint := markedAt(s, aggregateTitle), markedAt(s, "↵ jump")
	if len(title) == 0 || len(hint) == 0 {
		t.Fatalf("the aggregate view's title and hint are not on screen\n%s", term.Snapshot())
	}
	top := -1
	for _, at := range title {
		if at[0] < hint[0][0] {
			top = at[0]
		}
	}
	right := railColumn(s)
	if right < 0 {
		right = cols
	}
	markLeads(t, term, s, "aggregate view", namesInBox(s, top, hint[0][0], -1, right, names...), marks)
	alive(t, term, "after the navigator and the aggregate view drew the set's marks")
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
