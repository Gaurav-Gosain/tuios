package scrollback

import (
	"strings"
	"time"
	"unicode"
)

// VimMode represents the current editing mode.
type VimMode int

const (
	VimNormal     VimMode = iota
	VimVisualChar         // character-wise visual
	VimVisualLine         // line-wise visual
	VimSearch             // typing search query
)

// VimSearchMatch represents a search match position within a line.
type VimSearchMatch struct {
	Line   int
	StartX int // rune index (inclusive)
	EndX   int // rune index (exclusive)
}

// VimState holds cursor, selection, and search state for vim-style navigation
// over plain text lines. Used by the scrollback browser output mode.
type VimState struct {
	// Cursor position (rune indices)
	CursorX int
	CursorY int

	// Viewport
	ScrollY    int // first visible line index
	ViewHeight int // visible rows (set by renderer)

	// Mode
	Mode VimMode

	// Visual selection anchor
	VisualStartX int
	VisualStartY int

	// Search
	SearchQuery   string
	SearchMatches []VimSearchMatch
	SearchCurrent int

	// Count prefix
	PendingCount int

	// Character search (f/F/t/T)
	PendingCharSearch  bool
	LastCharSearch     rune
	LastCharSearchDir  int  // 1=forward, -1=backward
	LastCharSearchTill bool // true for t/T

	// gg detection
	PendingG  bool
	LastGTime time.Time

	// Text data
	Lines       []string // plain text (used for cursor/search logic)
	StyledLines []string // ANSI-styled text (used for rendering with colors)
}

// NewVimState creates a VimState for the given text lines, with cursor at startY.
// styledLines may be nil  - if provided, they are used for colored rendering.
func NewVimState(lines, styledLines []string, startY int) *VimState {
	v := &VimState{
		Lines:       lines,
		StyledLines: styledLines,
		CursorY:     startY,
	}
	v.clampCursor()
	return v
}

// --- Helpers ---

func (v *VimState) lineLen(y int) int {
	if y < 0 || y >= len(v.Lines) {
		return 0
	}
	return len([]rune(v.Lines[y]))
}

func (v *VimState) runeAt(x, y int) rune {
	if y < 0 || y >= len(v.Lines) {
		return 0
	}
	runes := []rune(v.Lines[y])
	if x < 0 || x >= len(runes) {
		return 0
	}
	return runes[x]
}

func (v *VimState) clampCursor() {
	if len(v.Lines) == 0 {
		v.CursorX = 0
		v.CursorY = 0
		return
	}
	v.CursorY = max(min(v.CursorY, len(v.Lines)-1), 0)
	v.clampCursorX()
}

func (v *VimState) clampCursorX() {
	ll := v.lineLen(v.CursorY)
	if ll == 0 {
		v.CursorX = 0
	} else {
		v.CursorX = max(min(v.CursorX, ll-1), 0)
	}
}

// charType classifies a rune: 0=space/null, 1=word, 2=punctuation.
func vimCharType(r rune) int {
	if r == 0 || unicode.IsSpace(r) {
		return 0
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return 1
	}
	return 2
}

// ConsumeCount returns the pending count (minimum 1) and resets it.
func (v *VimState) ConsumeCount() int {
	c := v.PendingCount
	v.PendingCount = 0
	if c < 1 {
		return 1
	}
	return c
}

// --- Viewport ---

// EnsureVisible adjusts ScrollY to keep cursor in viewport.
func (v *VimState) EnsureVisible() {
	if v.ViewHeight <= 0 {
		return
	}
	if v.CursorY < v.ScrollY {
		v.ScrollY = v.CursorY
	}
	if v.CursorY >= v.ScrollY+v.ViewHeight {
		v.ScrollY = v.CursorY - v.ViewHeight + 1
	}
	v.ScrollY = max(v.ScrollY, 0)
}

// CenterView centers the viewport on the cursor.
func (v *VimState) CenterView() {
	if v.ViewHeight <= 0 {
		return
	}
	v.ScrollY = max(v.CursorY-v.ViewHeight/2, 0)
}

// --- Basic movement ---

func (v *VimState) MoveLeft() {
	if v.CursorX > 0 {
		v.CursorX--
	}
}

func (v *VimState) MoveRight() {
	ll := v.lineLen(v.CursorY)
	if ll > 0 && v.CursorX < ll-1 {
		v.CursorX++
	}
}

func (v *VimState) MoveDown() {
	if v.CursorY < len(v.Lines)-1 {
		v.CursorY++
		v.clampCursorX()
	}
}

func (v *VimState) MoveUp() {
	if v.CursorY > 0 {
		v.CursorY--
		v.clampCursorX()
	}
}

// --- Line movement ---

func (v *VimState) MoveToLineStart() {
	v.CursorX = 0
}

func (v *VimState) MoveToFirstNonBlank() {
	if v.CursorY >= len(v.Lines) {
		return
	}
	for i, r := range []rune(v.Lines[v.CursorY]) {
		if !unicode.IsSpace(r) {
			v.CursorX = i
			return
		}
	}
	v.CursorX = 0
}

func (v *VimState) MoveToLineEnd() {
	ll := v.lineLen(v.CursorY)
	if ll > 0 {
		v.CursorX = ll - 1
	}
}

// --- Jump movement ---

func (v *VimState) MoveToTop() {
	v.CursorY = 0
	v.CursorX = 0
}

func (v *VimState) MoveToBottom() {
	if len(v.Lines) > 0 {
		v.CursorY = len(v.Lines) - 1
	}
	v.clampCursorX()
}

func (v *VimState) MoveToLine(n int) {
	v.CursorY = n - 1 // 1-indexed
	v.clampCursor()
}

// --- Screen position ---

func (v *VimState) MoveToScreenTop() {
	v.CursorY = v.ScrollY
	v.clampCursor()
}

func (v *VimState) MoveToScreenMiddle() {
	v.CursorY = v.ScrollY + v.ViewHeight/2
	v.clampCursor()
}

func (v *VimState) MoveToScreenBottom() {
	v.CursorY = v.ScrollY + v.ViewHeight - 1
	v.clampCursor()
}

// --- Page movement ---

func (v *VimState) HalfPageDown() {
	v.CursorY += max(v.ViewHeight/2, 1)
	v.clampCursor()
}

func (v *VimState) HalfPageUp() {
	v.CursorY -= max(v.ViewHeight/2, 1)
	v.clampCursor()
}

func (v *VimState) PageDown() {
	v.CursorY += max(v.ViewHeight-1, 1)
	v.clampCursor()
}

func (v *VimState) PageUp() {
	v.CursorY -= max(v.ViewHeight-1, 1)
	v.clampCursor()
}

// --- Paragraph movement ---

func (v *VimState) ParagraphDown() {
	n := len(v.Lines)
	if n == 0 {
		return
	}
	i := v.CursorY + 1
	if i >= n {
		return
	}
	// Skip blank lines at current position
	for i < n && strings.TrimSpace(v.Lines[i]) == "" {
		i++
	}
	// Skip non-blank lines to find next boundary
	for i < n && strings.TrimSpace(v.Lines[i]) != "" {
		i++
	}
	v.CursorY = min(i, n-1)
	v.clampCursorX()
}

func (v *VimState) ParagraphUp() {
	if len(v.Lines) == 0 {
		return
	}
	i := v.CursorY - 1
	if i <= 0 {
		v.CursorY = 0
		v.clampCursorX()
		return
	}
	for i > 0 && strings.TrimSpace(v.Lines[i]) == "" {
		i--
	}
	for i > 0 && strings.TrimSpace(v.Lines[i]) != "" {
		i--
	}
	v.CursorY = i
	v.clampCursorX()
}

// --- Word movement ---

func (v *VimState) WordForward() {
	x, y := v.CursorX, v.CursorY
	if y >= len(v.Lines) {
		return
	}
	runes := []rune(v.Lines[y])

	// Get current char type
	ct := 0
	if x < len(runes) {
		ct = vimCharType(runes[x])
	}

	// Phase 1: skip current word/punct group (if not on whitespace)
	if ct != 0 {
		for x < len(runes) && vimCharType(runes[x]) == ct {
			x++
		}
	}

	// Phase 2: skip whitespace, wrapping across lines
	for {
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
		runes = []rune(v.Lines[y])
		for x < len(runes) {
			if !unicode.IsSpace(runes[x]) {
				v.CursorX = x
				v.CursorY = y
				return
			}
			x++
		}
		// End of line, wrap
		y++
		x = 0
	}
}

func (v *VimState) WordBackward() {
	x, y := v.CursorX, v.CursorY

	// Move back at least one
	x--
	if x < 0 {
		y--
		if y < 0 {
			v.CursorX = 0
			v.CursorY = 0
			return
		}
		x = max(v.lineLen(y)-1, 0)
	}

	// Phase 1: skip whitespace backward, wrapping
	for {
		if y < 0 {
			v.CursorX = 0
			v.CursorY = 0
			return
		}
		runes := []rune(v.Lines[y])
		for x >= 0 {
			if x < len(runes) && !unicode.IsSpace(runes[x]) {
				goto skipDone
			}
			x--
		}
		y--
		if y >= 0 {
			x = max(v.lineLen(y)-1, 0)
		}
	}
skipDone:

	// Phase 2: find start of this word group
	runes := []rune(v.Lines[y])
	ct := vimCharType(runes[x])
	for x > 0 && vimCharType(runes[x-1]) == ct {
		x--
	}

	v.CursorX = x
	v.CursorY = y
}

func (v *VimState) WordEnd() {
	x, y := v.CursorX, v.CursorY

	// Move forward at least one
	x++
	runes := []rune(v.Lines[y])
	if x >= len(runes) {
		y++
		x = 0
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
	}

	// Phase 1: skip whitespace forward, wrapping
	for {
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
		runes = []rune(v.Lines[y])
		for x < len(runes) {
			if !unicode.IsSpace(runes[x]) {
				goto skip2Done
			}
			x++
		}
		y++
		x = 0
	}
skip2Done:

	// Phase 2: find end of this word group
	runes = []rune(v.Lines[y])
	ct := vimCharType(runes[x])
	for x < len(runes)-1 && vimCharType(runes[x+1]) == ct {
		x++
	}

	v.CursorX = x
	v.CursorY = y
}

func (v *VimState) WORDForward() {
	x, y := v.CursorX, v.CursorY
	if y >= len(v.Lines) {
		return
	}

	// Phase 1: skip current WORD (non-whitespace)
	runes := []rune(v.Lines[y])
	for x < len(runes) && !unicode.IsSpace(runes[x]) {
		x++
	}

	// Phase 2: skip whitespace, wrapping
	for {
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
		runes = []rune(v.Lines[y])
		for x < len(runes) {
			if !unicode.IsSpace(runes[x]) {
				v.CursorX = x
				v.CursorY = y
				return
			}
			x++
		}
		y++
		x = 0
	}
}

func (v *VimState) WORDBackward() {
	x, y := v.CursorX, v.CursorY

	x--
	if x < 0 {
		y--
		if y < 0 {
			v.CursorX = 0
			v.CursorY = 0
			return
		}
		x = max(v.lineLen(y)-1, 0)
	}

	// Skip whitespace backward
	for {
		if y < 0 {
			v.CursorX = 0
			v.CursorY = 0
			return
		}
		runes := []rune(v.Lines[y])
		for x >= 0 {
			if x < len(runes) && !unicode.IsSpace(runes[x]) {
				goto wbSkipDone
			}
			x--
		}
		y--
		if y >= 0 {
			x = max(v.lineLen(y)-1, 0)
		}
	}
wbSkipDone:

	// Find start of WORD
	runes := []rune(v.Lines[y])
	for x > 0 && !unicode.IsSpace(runes[x-1]) {
		x--
	}

	v.CursorX = x
	v.CursorY = y
}

func (v *VimState) WORDEnd() {
	x, y := v.CursorX, v.CursorY

	x++
	runes := []rune(v.Lines[y])
	if x >= len(runes) {
		y++
		x = 0
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
	}

	// Skip whitespace forward
	for {
		if y >= len(v.Lines) {
			v.CursorY = len(v.Lines) - 1
			v.clampCursorX()
			return
		}
		runes = []rune(v.Lines[y])
		for x < len(runes) {
			if !unicode.IsSpace(runes[x]) {
				goto weSkipDone
			}
			x++
		}
		y++
		x = 0
	}
weSkipDone:

	// Find end of WORD
	runes = []rune(v.Lines[y])
	for x < len(runes)-1 && !unicode.IsSpace(runes[x+1]) {
		x++
	}

	v.CursorX = x
	v.CursorY = y
}

// --- Character search (f/F/t/T) ---

// FindChar searches for a character, crossing line boundaries.
// dir: 1=forward, -1=backward. till: true=stop before char.
func (v *VimState) FindChar(ch rune, dir int, till bool) bool {
	if len(v.Lines) == 0 {
		return false
	}
	x, y := v.CursorX+dir, v.CursorY

	for y >= 0 && y < len(v.Lines) {
		runes := []rune(v.Lines[y])
		for x >= 0 && x < len(runes) {
			if runes[x] == ch {
				if till {
					tx := x - dir
					// Clamp to current line bounds for edge cases at line boundaries
					if tx < 0 {
						tx = 0
					} else if tx >= len(runes) {
						tx = len(runes) - 1
					}
					x = tx
				}
				v.CursorX = x
				v.CursorY = y
				v.LastCharSearch = ch
				v.LastCharSearchDir = dir
				v.LastCharSearchTill = till
				return true
			}
			x += dir
		}
		// Move to next/previous line
		y += dir
		if y >= 0 && y < len(v.Lines) {
			if dir > 0 {
				x = 0
			} else {
				x = max(v.lineLen(y)-1, 0)
			}
		}
	}
	return false
}

// RepeatCharSearch repeats the last f/F/t/T search. reverse flips direction.
func (v *VimState) RepeatCharSearch(reverse bool) bool {
	if v.LastCharSearch == 0 {
		return false
	}
	dir := v.LastCharSearchDir
	if reverse {
		dir = -dir
	}
	return v.FindChar(v.LastCharSearch, dir, v.LastCharSearchTill)
}

// --- Bracket matching ---

func (v *VimState) MatchBracket() {
	if v.CursorY >= len(v.Lines) {
		return
	}
	runes := []rune(v.Lines[v.CursorY])
	if v.CursorX >= len(runes) {
		return
	}

	ch := runes[v.CursorX]
	var match rune
	var dir int

	switch ch {
	case '(':
		match, dir = ')', 1
	case ')':
		match, dir = '(', -1
	case '[':
		match, dir = ']', 1
	case ']':
		match, dir = '[', -1
	case '{':
		match, dir = '}', 1
	case '}':
		match, dir = '{', -1
	case '<':
		match, dir = '>', 1
	case '>':
		match, dir = '<', -1
	default:
		return
	}

	depth := 1
	x, y := v.CursorX, v.CursorY

	for i := 0; i < 10000 && depth > 0; i++ {
		// Advance position
		if dir > 0 {
			x++
			runes = []rune(v.Lines[y])
			if x >= len(runes) {
				y++
				x = 0
				if y >= len(v.Lines) {
					return
				}
			}
		} else {
			x--
			if x < 0 {
				y--
				if y < 0 {
					return
				}
				x = max(v.lineLen(y)-1, 0)
			}
		}

		switch v.runeAt(x, y) {
		case ch:
			depth++
		case match:
			depth--
		}
	}

	if depth == 0 {
		v.CursorX = x
		v.CursorY = y
	}
}

// --- Search ---

// SearchExecute runs the search query and jumps to the first match at or after cursor.
func (v *VimState) SearchExecute() {
	if v.SearchQuery == "" {
		v.SearchMatches = nil
		return
	}
	query := strings.ToLower(v.SearchQuery)
	v.SearchMatches = nil

	for y, line := range v.Lines {
		lower := strings.ToLower(line)
		runes := []rune(line)
		lowerRunes := []rune(lower)
		queryRunes := []rune(query)
		qLen := len(queryRunes)

		// Find all matches in this line by rune index
		for x := 0; x <= len(lowerRunes)-qLen; x++ {
			if string(lowerRunes[x:x+qLen]) == string(queryRunes) {
				// Convert rune position
				_ = runes // ensure runes is used for consistency
				v.SearchMatches = append(v.SearchMatches, VimSearchMatch{
					Line:   y,
					StartX: x,
					EndX:   x + qLen,
				})
			}
		}
	}

	if len(v.SearchMatches) > 0 {
		// Jump to first match at or after cursor
		v.SearchCurrent = 0
		for i, m := range v.SearchMatches {
			if m.Line > v.CursorY || (m.Line == v.CursorY && m.StartX >= v.CursorX) {
				v.SearchCurrent = i
				break
			}
		}
		v.jumpToCurrentMatch()
	}
}

// SearchNext jumps to the next search match.
func (v *VimState) SearchNext() {
	if len(v.SearchMatches) == 0 {
		return
	}
	v.SearchCurrent = (v.SearchCurrent + 1) % len(v.SearchMatches)
	v.jumpToCurrentMatch()
}

// SearchPrev jumps to the previous search match.
func (v *VimState) SearchPrev() {
	if len(v.SearchMatches) == 0 {
		return
	}
	v.SearchCurrent--
	if v.SearchCurrent < 0 {
		v.SearchCurrent = len(v.SearchMatches) - 1
	}
	v.jumpToCurrentMatch()
}

func (v *VimState) jumpToCurrentMatch() {
	m := v.SearchMatches[v.SearchCurrent]
	v.CursorY = m.Line
	v.CursorX = m.StartX
	v.clampCursor()
}

// --- Visual mode ---

// EnterVisualChar enters character-wise visual mode.
func (v *VimState) EnterVisualChar() {
	v.Mode = VimVisualChar
	v.VisualStartX = v.CursorX
	v.VisualStartY = v.CursorY
}

// EnterVisualLine enters line-wise visual mode.
func (v *VimState) EnterVisualLine() {
	v.Mode = VimVisualLine
	v.VisualStartX = 0
	v.VisualStartY = v.CursorY
}

// ExitVisual returns to normal mode.
func (v *VimState) ExitVisual() {
	v.Mode = VimNormal
}

// SelectedText returns the text of the current selection (visual) or current line (normal).
func (v *VimState) SelectedText() string {
	switch v.Mode {
	case VimVisualLine:
		lo, hi := v.VisualStartY, v.CursorY
		if lo > hi {
			lo, hi = hi, lo
		}
		lo = max(lo, 0)
		hi = min(hi, len(v.Lines)-1)
		return strings.Join(v.Lines[lo:hi+1], "\n")

	case VimVisualChar:
		sx, sy := v.VisualStartX, v.VisualStartY
		ex, ey := v.CursorX, v.CursorY
		// Normalize
		if sy > ey || (sy == ey && sx > ex) {
			sx, sy, ex, ey = ex, ey, sx, sy
		}
		if sy == ey {
			runes := []rune(v.Lines[sy])
			ex = min(ex, len(runes)-1)
			sx = max(sx, 0)
			return string(runes[sx : ex+1])
		}
		// Multi-line
		var parts []string
		runes := []rune(v.Lines[sy])
		sx = max(sx, 0)
		if sx < len(runes) {
			parts = append(parts, string(runes[sx:]))
		}
		for y := sy + 1; y < ey; y++ {
			parts = append(parts, v.Lines[y])
		}
		runes = []rune(v.Lines[ey])
		ex = min(ex, len(runes)-1)
		parts = append(parts, string(runes[:ex+1]))
		return strings.Join(parts, "\n")

	default:
		if v.CursorY >= 0 && v.CursorY < len(v.Lines) {
			return v.Lines[v.CursorY]
		}
		return ""
	}
}

// VisualBounds returns the normalized selection bounds for rendering.
// Returns (startX, startY, endX, endY) where start <= end.
func (v *VimState) VisualBounds() (int, int, int, int) {
	sx, sy := v.VisualStartX, v.VisualStartY
	ex, ey := v.CursorX, v.CursorY
	if sy > ey || (sy == ey && sx > ex) {
		sx, sy, ex, ey = ex, ey, sx, sy
	}
	return sx, sy, ex, ey
}

// SearchMatchesOnLine returns all search matches on the given line.
func (v *VimState) SearchMatchesOnLine(y int) []VimSearchMatch {
	var matches []VimSearchMatch
	for _, m := range v.SearchMatches {
		if m.Line == y {
			matches = append(matches, m)
		}
	}
	return matches
}
