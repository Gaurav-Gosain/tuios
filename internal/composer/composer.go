// Package composer is the editor a person writes to an agent in: a draft of
// many lines with soft wrap, cursor and word motions, and undo and redo.
//
// A long paste becomes one block in the draft, drawn as a label such as
// [Pasted text #1 · 42 lines]. The block is one cell: the cursor steps over it
// in one move and a delete takes all of it. Its text is kept with the cell and
// put back in its place when the message is sent, so the label is never sent
// and literal text that looks like a label is never replaced.
//
// The composer holds no tuios state and never talks to the daemon. The host
// gives it keys and pastes, draws what Render returns, and acts on the Event
// Update returns: it decides where a message goes and how.
//
// It costs nothing at rest. It has no timer, and it does work only when a key
// or a paste arrives or when the host draws a frame.
package composer

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Defaults for Options.
const (
	DefaultMaxRows       = 8
	DefaultCollapseChars = 800
	DefaultCollapseLines = 3
	// DefaultMaxBytes bounds the whole draft, blocks included.
	DefaultMaxBytes = 1 << 20
)

// Options configure a Model. A zero field takes its default.
type Options struct {
	// MaxRows is how many rows the editor grows to before it scrolls.
	MaxRows int
	// CollapseChars and CollapseLines: a paste of more characters or more
	// lines than these becomes a block.
	CollapseChars int
	CollapseLines int
	// MaxBytes bounds the draft. A paste that would take the draft past it
	// is refused.
	MaxBytes int
	// Placeholder is drawn in an empty draft.
	Placeholder string
}

// Event is what a key asks the host to do.
type Event int

const (
	// EventNone: the composer handled the key itself, or it did nothing.
	EventNone Event = iota
	// EventSend: the person wants the message sent. The host calls Submit.
	EventSend
	// EventClose: the person closed the editor. The draft is still there.
	EventClose
	// EventPaste: the person asked to paste from the clipboard. The host
	// reads it and calls Paste.
	EventPaste
)

// Model is one draft and its editor.
type Model struct {
	opt Options

	cells []cell
	// cur is the cursor, an index into cells from 0 to len(cells).
	cur int
	// ver changes with every edit, for the layout cache.
	ver uint64
	// size is the draft's bytes, blocks included.
	size int
	lay  layout

	undo, redo []edit
	undoSize   int
	// joinTyping says the next typed character may join the last undo step.
	joinTyping bool

	// goal is the column up and down aim for, -1 when the last key was not
	// up or down.
	goal int
	// top is the first row shown when the draft is taller than MaxRows.
	top int
	// width is the text width of the last frame, which up, down, home and
	// end move by.
	width int

	// automated says a key that did not come from the keyboard touched the
	// draft. The host must not send such a draft as the person.
	automated bool
	// notice is a line under the editor that says what just happened. The
	// next edit clears it.
	notice string
}

// New returns an empty draft.
func New(o Options) *Model {
	if o.MaxRows <= 0 {
		o.MaxRows = DefaultMaxRows
	}
	if o.CollapseChars <= 0 {
		o.CollapseChars = DefaultCollapseChars
	}
	if o.CollapseLines <= 0 {
		o.CollapseLines = DefaultCollapseLines
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	return &Model{opt: o, goal: -1, width: 80}
}

// Automated reports whether a key that did not come from the keyboard
// touched the draft.
func (m *Model) Automated() bool { return m.automated }

// MarkAutomated records that a key that did not come from the keyboard
// reached the draft.
func (m *Model) MarkAutomated() { m.automated = true }

// Empty reports whether the draft holds nothing but spaces.
func (m *Model) Empty() bool {
	for i := range m.cells {
		if !m.cells[i].isSpace() {
			return false
		}
	}
	return true
}

// Notice is the line under the editor, empty for none.
func (m *Model) Notice() string { return m.notice }

// SetNotice puts a line under the editor until the next edit.
func (m *Model) SetNotice(s string) { m.notice = s }

// Blocks is how many paste blocks the draft holds.
func (m *Model) Blocks() int {
	n := 0
	for _, c := range m.cells {
		if c.block != nil {
			n++
		}
	}
	return n
}

// Text is the message: the draft with every block's text in its place.
func (m *Model) Text() string {
	var b strings.Builder
	b.Grow(m.size)
	for _, c := range m.cells {
		if c.block != nil {
			b.WriteString(c.block.text)
			continue
		}
		b.WriteString(c.s)
	}
	return b.String()
}

// Submit returns the message to send. It first removes invisible characters
// from the draft, blocks included: when it removed any, it returns false and
// the notice says how many, so the person reads the cleaned draft before it
// goes. The removal is one undo step.
func (m *Model) Submit() (string, bool) {
	if n := m.stripInvisible(); n > 0 {
		chars := "characters"
		if n == 1 {
			chars = "character"
		}
		m.notice = "Removed " + strconv.Itoa(n) + " invisible " + chars + ". Read the message and press enter to send."
		return "", false
	}
	return m.Text(), true
}

// stripInvisible removes invisible characters from the draft as one undo
// step and returns how many went. A block with any gets a new block in its
// place, so undo brings back the block as it was.
func (m *Model) stripInvisible() int {
	found := false
	for _, c := range m.cells {
		if c.block != nil && hasInvisible(c.block.text) || c.block == nil && hasInvisible(c.s) {
			found = true
			break
		}
	}
	if !found {
		return 0
	}
	total := 0
	out := make([]cell, 0, len(m.cells))
	cur := m.cur
	for i, c := range m.cells {
		if c.block != nil {
			text, n := stripInvisible(c.block.text)
			if n > 0 {
				total += n
				c = blockCell(newBlock(c.block.n, text))
			}
			out = append(out, c)
			continue
		}
		s, n := stripInvisible(c.s)
		total += n
		if s == "" {
			if i < m.cur {
				cur--
			}
			continue
		}
		if n > 0 {
			out = segment(out, s)
			continue
		}
		out = append(out, c)
	}
	m.change(0, len(m.cells), out, min(cur, len(out)))
	return total
}

// Clear empties the draft as one undo step.
func (m *Model) Clear() {
	if len(m.cells) == 0 {
		return
	}
	m.change(0, len(m.cells), nil, 0)
	m.top = 0
}

// Insert types text at the cursor as one undo step, as keys would. Line
// breaks are kept.
func (m *Model) Insert(text string, remote bool) {
	if remote {
		m.automated = true
	}
	text = clean(text)
	if text == "" {
		return
	}
	if m.size+len(text) > m.opt.MaxBytes {
		m.notice = "The message is too long to add more."
		return
	}
	m.edited()
	ins := segment(nil, text)
	m.change(m.cur, 0, ins, m.cur+len(ins))
}

// Paste puts pasted text at the cursor: as text when it is short, and as one
// block when it has more characters or lines than the options allow. Line
// breaks are kept either way.
func (m *Model) Paste(text string, remote bool) {
	if remote {
		m.automated = true
	}
	text = clean(text)
	if text == "" {
		return
	}
	if m.size+len(text) > m.opt.MaxBytes {
		m.notice = "The paste is larger than " + sizeWords(m.opt.MaxBytes-m.size) + ". Save it to a file and type the path."
		return
	}
	lines, chars := measure(text)
	if chars <= m.opt.CollapseChars && lines <= m.opt.CollapseLines {
		m.Insert(text, false)
		return
	}
	m.edited()
	b := newBlock(m.nextBlock(), text)
	m.change(m.cur, 0, []cell{blockCell(b)}, m.cur+1)
}

// measure counts text's lines, a final line break not making a line of its
// own, and its characters.
func measure(text string) (lines, chars int) {
	body := strings.TrimRight(text, "\n")
	return strings.Count(body, "\n") + 1, len([]rune(text))
}

// nextBlock is the number a new block takes: the highest in the draft plus
// one.
func (m *Model) nextBlock() int {
	n := 0
	for _, c := range m.cells {
		if c.block != nil {
			n = max(n, c.block.n)
		}
	}
	return n + 1
}

// newBlock makes block n of text.
func newBlock(n int, text string) *block {
	lines, chars := measure(text)
	b := &block{n: n, text: text, lines: lines, chars: chars}
	b.label = "[Pasted text #" + strconv.Itoa(n) + " · " + b.measureWords() + "]"
	return b
}

// measureWords is the block's measure as its label says it: lines when it has
// more than one, characters when it is one long line.
func (b *block) measureWords() string {
	if b.lines > 1 {
		return thousands(b.lines) + " lines"
	}
	return thousands(b.chars) + " chars"
}

// blockCell is the cell that holds b.
func blockCell(b *block) cell {
	return cell{block: b, w: ansi.StringWidth(b.label)}
}

// thousands writes n with a comma between each group of three digits.
func thousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// sizeWords says a byte count the way a person reads it: in MB from one
// megabyte, in KB below.
func sizeWords(n int) string {
	if n >= 1<<20 {
		return strconv.Itoa(n>>20) + " MB"
	}
	return strconv.Itoa(max(n>>10, 1)) + " KB"
}

// edited clears what an edit makes stale: the notice and the column up and
// down aim for.
func (m *Model) edited() {
	m.notice = ""
	m.goal = -1
}

// selectedBlock is the block just before the cursor, the one its keys act
// on, or nil.
func (m *Model) selectedBlock() *block {
	if m.cur > 0 && m.cur <= len(m.cells) {
		return m.cells[m.cur-1].block
	}
	return nil
}

// Update handles one key. remote says the key did not come from the keyboard
// (send-keys, a tape): the draft is then marked, and the host must not send
// it as the person.
func (m *Model) Update(msg tea.KeyPressMsg, remote bool) Event {
	if remote {
		m.automated = true
	}
	key := msg.String()
	if key != "up" && key != "down" {
		m.goal = -1
	}
	switch key {
	case "enter":
		// A backslash right before the cursor asks for a new line, for a
		// terminal that reports no modifier with enter.
		if m.cur > 0 && m.cells[m.cur-1].s == `\` && m.cells[m.cur-1].block == nil {
			m.edited()
			m.change(m.cur-1, 1, []cell{{s: "\n", space: true}}, m.cur)
			return EventNone
		}
		return EventSend
	case "ctrl+j", "shift+enter", "alt+enter", "ctrl+enter":
		m.typeKey("\n")
	case "esc":
		return EventClose
	case "ctrl+v":
		return EventPaste
	case "ctrl+c":
		m.edited()
		m.Clear()
	case "left":
		m.move(m.cur - 1)
	case "right":
		m.move(m.cur + 1)
	case "alt+left", "ctrl+left", "alt+b":
		m.move(m.wordLeft())
	case "alt+right", "ctrl+right", "alt+f":
		m.move(m.wordRight())
	case "home", "ctrl+a":
		m.move(m.lineStart())
	case "end":
		m.move(m.lineEnd())
	case "ctrl+e":
		m.move(m.lineEnd())
	case "ctrl+home":
		m.move(0)
	case "ctrl+end":
		m.move(len(m.cells))
	case "up":
		m.vertical(-1)
	case "down":
		m.vertical(1)
	case "backspace", "ctrl+h":
		m.deleteTo(m.cur - 1)
	case "delete", "ctrl+d":
		m.deleteTo(m.cur + 1)
	case "ctrl+w", "alt+backspace", "ctrl+backspace":
		m.deleteTo(m.wordLeft())
	case "alt+d", "alt+delete", "ctrl+delete":
		m.deleteTo(m.wordRight())
	case "ctrl+u":
		to := m.logicalStart()
		if to == m.cur {
			to = m.cur - 1
		}
		m.deleteTo(to)
	case "ctrl+k":
		to := m.logicalEnd()
		if to == m.cur {
			to = m.cur + 1
		}
		m.deleteTo(to)
	case "ctrl+z", "ctrl+_":
		m.edited()
		m.Undo()
	case "ctrl+shift+z", "alt+z":
		m.edited()
		m.Redo()
	case "space":
		m.typeKey(" ")
	case "tab", "shift+tab":
		// Kept for completion.
	default:
		if msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper|tea.ModMeta|tea.ModHyper) != 0 {
			return EventNone
		}
		m.typeKey(msg.Text)
	}
	return EventNone
}

// typeKey types text one cluster at a time, joining the run of typing for
// undo. An input method can commit several characters in one key.
func (m *Model) typeKey(text string) {
	text = clean(text)
	if text == "" {
		return
	}
	if m.size+len(text) > m.opt.MaxBytes {
		m.notice = "The message is too long to add more."
		return
	}
	m.edited()
	// A key that continues the cluster before the cursor, a combining mark
	// or the rest of an emoji sequence, joins it, so one character on
	// screen stays one cell.
	if m.cur > 0 {
		if prev := m.cells[m.cur-1]; prev.block == nil && prev.s != "\n" && prev.s != "\t" {
			if joined := segment(nil, prev.s+text); len(joined) > 0 && len(joined[0].s) > len(prev.s) {
				m.change(m.cur-1, 1, joined, m.cur-1+len(joined))
				return
			}
		}
	}
	var buf [4]cell
	for _, c := range segment(buf[:0], text) {
		m.typeCell(c)
	}
}

// move puts the cursor at i, kept inside the draft.
func (m *Model) move(i int) {
	m.cur = min(max(i, 0), len(m.cells))
	m.joinTyping = false
}

// deleteTo deletes the cells between the cursor and i, on either side, as
// one undo step.
func (m *Model) deleteTo(i int) {
	i = min(max(i, 0), len(m.cells))
	if i == m.cur {
		return
	}
	m.edited()
	lo, hi := min(i, m.cur), max(i, m.cur)
	m.change(lo, hi-lo, nil, lo)
}

// wordLeft is where a word motion to the left lands: past the spaces before
// the cursor, then to the start of the word. A block is a word by itself.
func (m *Model) wordLeft() int {
	i := m.cur
	for i > 0 && m.cells[i-1].isSpace() {
		i--
	}
	if i > 0 && m.cells[i-1].block != nil {
		return i - 1
	}
	for i > 0 && !m.cells[i-1].isSpace() && m.cells[i-1].block == nil {
		i--
	}
	return i
}

// wordRight is where a word motion to the right lands: past the spaces after
// the cursor, then to the end of the word.
func (m *Model) wordRight() int {
	i, n := m.cur, len(m.cells)
	for i < n && m.cells[i].isSpace() {
		i++
	}
	if i < n && m.cells[i].block != nil {
		return i + 1
	}
	for i < n && !m.cells[i].isSpace() && m.cells[i].block == nil {
		i++
	}
	return i
}

// logicalStart and logicalEnd are the ends of the line the cursor is on,
// between line breaks, however it wraps.
func (m *Model) logicalStart() int {
	i := m.cur
	for i > 0 && m.cells[i-1].s != "\n" {
		i--
	}
	return i
}

func (m *Model) logicalEnd() int {
	i := m.cur
	for i < len(m.cells) && m.cells[i].s != "\n" {
		i++
	}
	return i
}

// lineStart and lineEnd are the ends of the row the cursor is on, as the
// draft was last drawn.
func (m *Model) lineStart() int {
	rows := m.rows(m.width)
	return rows[rowOf(rows, m.cur)].start
}

func (m *Model) lineEnd() int {
	rows := m.rows(m.width)
	r := rows[rowOf(rows, m.cur)]
	if r.soft && r.end > r.start {
		return r.end - 1
	}
	return r.end
}

// vertical moves the cursor by d rows, as the draft was last drawn, keeping
// the column it started from. Past the first or the last row it goes to the
// start or the end of the draft.
func (m *Model) vertical(d int) {
	width := m.width
	rows := m.rows(width)
	r := rowOf(rows, m.cur)
	if m.goal < 0 {
		m.goal = m.colOf(rows[r], m.cur, width)
	}
	switch t := r + d; {
	case t < 0:
		m.cur = 0
	case t >= len(rows):
		m.cur = len(m.cells)
	default:
		m.cur = m.indexAt(rows[t], m.goal, width)
	}
	m.joinTyping = false
}
