package composer

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The composer is a state machine whose faults are quiet: a block cut in
// half sends half a stack trace, an undo that loses a block's payload sends
// its label, and a wrap that misjudges a wide character puts the cursor in
// the wrong place. These tests hold the four things that would go wrong
// without a sound: block atomicity, undo and redo, wrap with wide characters,
// and the expansion at send.

// testStyles is a truecolor look for the tests and the benchmarks. A host
// fills Styles from its own theme.
func testStyles() Styles {
	return Styles{
		Field:    lipgloss.Color("#1e1e2e"),
		Text:     lipgloss.Color("#cdd6f4"),
		Muted:    lipgloss.Color("#a6adc8"),
		Dim:      lipgloss.Color("#7f849c"),
		Key:      lipgloss.Color("#b4befe"),
		Accent:   lipgloss.Color("#89b4fa"),
		Warning:  lipgloss.Color("#f9e2af"),
		Selected: lipgloss.Color("#45475a"),
		Sigil:    "›",
	}
}

// testSurface is the ground around the editor in the tests.
var testSurface = lipgloss.Color("#181825")

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "alt+left":
		return tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}
	case "alt+z":
		return tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt}
	case "shift+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	}
	if c, ok := strings.CutPrefix(s, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: rune(c[0]), Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func typeText(m *Model, s string) {
	for _, r := range s {
		if r == ' ' {
			m.Update(key("space"), false)
			continue
		}
		m.Update(key(string(r)), false)
	}
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		m.Update(key(k), false)
	}
}

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %02d of the paste\n", i)
	}
	return b.String()
}

func TestPasteBlockIsOneUnit(t *testing.T) {
	m := New(Options{})
	typeText(m, "see")
	press(m, "space")
	paste := lines(42)
	m.Paste(paste, false)
	typeText(m, " ok")
	if got := m.Blocks(); got != 1 {
		t.Fatalf("a 42 line paste made %d blocks, want 1", got)
	}
	if m.cells[4].block.label != "[Pasted text #1 · 42 lines]" {
		t.Fatalf("label = %q", m.cells[4].block.label)
	}

	// Step left from after " ok": three moves to the block, one over it.
	press(m, "left", "left", "left", "left")
	if m.cur != 4 {
		t.Fatalf("one left over the block left the cursor at %d, want 4", m.cur)
	}
	// Delete forward from before the block takes all of it, not a byte.
	press(m, "delete")
	if m.Text() != "see  ok" {
		t.Fatalf("delete before the block left %q", m.Text())
	}
	press(m, "ctrl+z")
	if m.Text() != "see "+paste+" ok" {
		t.Fatalf("undo did not bring the block back whole: %q", m.Text())
	}

	// A word delete right after the block takes the block and nothing else.
	press(m, "right")
	press(m, "ctrl+w")
	if m.Text() != "see  ok" {
		t.Fatalf("ctrl+w after the block left %q", m.Text())
	}
	press(m, "ctrl+z")

	// Backspace after the block removes it whole.
	press(m, "backspace")
	if m.Text() != "see  ok" || m.Blocks() != 0 {
		t.Fatalf("backspace after the block left %q", m.Text())
	}
}

func TestShortPasteKeepsLineBreaks(t *testing.T) {
	m := New(Options{})
	m.Paste("a\r\nb\rc\x1b[201~d", false)
	if got := m.Text(); got != "a\nb\nc[201~d" {
		t.Fatalf("short paste = %q, want line breaks kept and ESC gone", got)
	}
	if m.Blocks() != 0 {
		t.Fatal("a 3 line paste became a block")
	}
	m.Clear()
	long := strings.Repeat("x", 801)
	m.Paste(long, false)
	if m.Blocks() != 1 || m.cells[0].block.label != "[Pasted text #1 · 801 chars]" {
		t.Fatalf("an 801 character line: blocks %d", m.Blocks())
	}
}

func TestBlockNumbers(t *testing.T) {
	m := New(Options{})
	m.Paste(lines(5), false)
	m.Paste(lines(6), false)
	m.Paste(lines(7), false)
	// Remove #3, the last; a new paste is #3 again. Remove #1; the next
	// is #4, past the highest still there.
	press(m, "backspace")
	m.Paste(lines(8), false)
	if n := m.cells[2].block.n; n != 3 {
		t.Fatalf("after removing the last block a new one is #%d, want 3", n)
	}
	m.cur = 1
	press(m, "backspace")
	m.cur = len(m.cells)
	m.Paste(lines(9), false)
	if n := m.cells[len(m.cells)-1].block.n; n != 4 {
		t.Fatalf("a new block is #%d, want 4", n)
	}
}

func TestUndoRedo(t *testing.T) {
	m := New(Options{})
	typeText(m, "hello world")
	// Typing is undone a word at a time.
	press(m, "ctrl+z")
	if got := m.Text(); got != "hello" {
		t.Fatalf("one undo = %q, want the last word gone", got)
	}
	press(m, "alt+z")
	if got := m.Text(); got != "hello world" {
		t.Fatalf("redo = %q", got)
	}
	m.Paste(lines(10), false)
	press(m, "backspace")
	if m.Blocks() != 0 || m.Text() != "hello world" {
		t.Fatalf("remove: blocks %d text %q", m.Blocks(), m.Text())
	}
	press(m, "ctrl+z")
	if m.Blocks() != 1 || m.Text() != "hello world"+lines(10) {
		t.Fatal("undo of a remove did not bring the block back with its text")
	}
	press(m, "ctrl+z")
	if m.Text() != "hello world" {
		t.Fatalf("undo of the paste = %q", m.Text())
	}
	press(m, "alt+z", "alt+z")
	if m.Text() != "hello world" || m.Blocks() != 0 {
		t.Fatalf("two redos = %q blocks %d", m.Text(), m.Blocks())
	}
	// A new edit drops the redo steps.
	press(m, "ctrl+z")
	typeText(m, "x")
	if m.Redo() {
		t.Fatal("redo after a new edit did something")
	}
	// The stack is bounded.
	for range undoSteps + 50 {
		press(m, "ctrl+j")
	}
	if len(m.undo) > undoSteps {
		t.Fatalf("undo holds %d steps, bound %d", len(m.undo), undoSteps)
	}
}

func TestNewLineKeys(t *testing.T) {
	m := New(Options{})
	typeText(m, "a")
	press(m, "ctrl+j")
	typeText(m, "b")
	press(m, "shift+enter")
	typeText(m, `c\`)
	if ev := m.Update(key("enter"), false); ev != EventNone {
		t.Fatalf("enter after a backslash = %v, want a new line", ev)
	}
	typeText(m, "d")
	if got := m.Text(); got != "a\nb\nc\nd" {
		t.Fatalf("text = %q", got)
	}
	if ev := m.Update(key("enter"), false); ev != EventSend {
		t.Fatalf("enter = %v, want send", ev)
	}
}

func TestWrapWithWideCharacters(t *testing.T) {
	m := New(Options{})
	// 8 cells of text: four wide characters. A row of 7 holds three.
	typeText(m, "日本語版")
	rows := m.rows(7)
	if len(rows) != 2 || rows[0].end != 3 {
		t.Fatalf("rows at width 7 = %+v, want 3 wide characters then 1", rows)
	}
	// The cursor at the end is on the second row, two cells in.
	if r := rowOf(rows, m.cur); r != 1 || m.colOf(rows[r], m.cur, 7) != 2 {
		t.Fatalf("cursor row %d", r)
	}
	// Up keeps the column: two cells is the end of the first wide
	// character.
	m.width = 7
	press(m, "up")
	if m.cur != 1 {
		t.Fatalf("up from column 2 put the cursor at %d, want 1", m.cur)
	}
	// Words wrap at spaces, and an emoji with a joiner is one cell.
	m.Clear()
	typeText(m, "ab cd ")
	m.Update(tea.KeyPressMsg{Code: '👩', Text: "👩"}, false)
	m.Update(tea.KeyPressMsg{Code: 0x200d, Text: "\u200d"}, false)
	m.Update(tea.KeyPressMsg{Code: '💻', Text: "💻"}, false)
	typeText(m, " ef")
	rows = m.rows(6)
	got := []string{}
	for _, r := range rows {
		var b strings.Builder
		for _, c := range m.cells[r.start:r.end] {
			b.WriteString(c.s)
		}
		got = append(got, b.String())
	}
	if strings.Join(got, "|") != "ab cd |👩\u200d💻 ef" {
		t.Fatalf("rows = %q", got)
	}
}

func TestSubmitExpandsBlocksInPlace(t *testing.T) {
	m := New(Options{})
	paste := lines(42)
	typeText(m, "before ")
	m.Paste(paste, false)
	// Literal text that looks like a label stays literal.
	typeText(m, " [Pasted text #1 · 42 lines]")
	text, ok := m.Submit()
	if !ok {
		t.Fatal("submit refused a clean draft")
	}
	want := "before " + paste + " [Pasted text #1 · 42 lines]"
	if text != want {
		t.Fatalf("message = %q, want %q", text, want)
	}
	if strings.Count(text, "\n") != 42 {
		t.Fatalf("message has %d line breaks, want 42", strings.Count(text, "\n"))
	}
}

func TestSubmitStopsOnInvisibleCharacters(t *testing.T) {
	m := New(Options{})
	typeText(m, "run it")
	m.Paste("x\u200b\u202ey"+strings.Repeat("\n", 5)+"\U000E0041", false)
	if _, ok := m.Submit(); ok {
		t.Fatal("submit sent a draft with invisible characters")
	}
	if !strings.Contains(m.Notice(), "Removed 3 invisible characters") {
		t.Fatalf("notice = %q", m.Notice())
	}
	text, ok := m.Submit()
	if !ok || text != "run itxy"+strings.Repeat("\n", 5) {
		t.Fatalf("second submit = %q %v", text, ok)
	}
	// The joiner in an emoji is kept.
	m.Clear()
	typeText(m, "👩\u200d💻")
	if text, ok := m.Submit(); !ok || text != "👩\u200d💻" {
		t.Fatalf("emoji = %q %v", text, ok)
	}
}

func TestRemoteKeysMarkTheDraft(t *testing.T) {
	m := New(Options{})
	m.Update(key("a"), true)
	if !m.Automated() {
		t.Fatal("a key from send-keys did not mark the draft")
	}
	m = New(Options{})
	m.Paste("x", true)
	if !m.Automated() {
		t.Fatal("a paste from send-keys did not mark the draft")
	}
}
