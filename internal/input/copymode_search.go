package input

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// Search-related functions for copy mode (/, ?, n, N, etc.)

// maxSearchMatches bounds how many matches one search keeps. The renderer
// walks every match on every frame, so the bound keeps a one-letter query over
// a full scrollback cheap.
const maxSearchMatches = 1000

// matchesOnLine finds every occurrence of query (already lower-cased when the
// search ignores case) in one line's text, left to right. cells gives the
// line's cells, read only when there is a match, to turn character offsets
// into columns.
func matchesOnLine(text, query string, caseSensitive bool, absLine int, cells func() []uv.Cell) []terminal.SearchMatch {
	if !caseSensitive {
		text = strings.ToLower(text)
	}
	// strings.Index returns byte positions, not character positions.
	queryCharLen := len([]rune(query))
	var out []terminal.SearchMatch
	var lineCells []uv.Cell
	byteIdx := 0
	for byteIdx <= len(text) {
		idx := strings.Index(text[byteIdx:], query)
		if idx == -1 {
			break
		}
		if lineCells == nil {
			lineCells = cells()
		}
		bytePos := byteIdx + idx
		charStart := byteIndexToCharIndex(text, bytePos)
		out = append(out, terminal.SearchMatch{
			Line:   absLine,
			StartX: charIndexToColumn(lineCells, charStart),
			EndX:   charIndexToColumn(lineCells, charStart+queryCharLen),
		})
		byteIdx = bytePos + len(query)
	}
	return out
}

// executeSearch performs a search operation and updates matches
func executeSearch(cm *terminal.CopyMode, window *terminal.Window) {
	// Check cache
	if cm.SearchQuery != "" && cm.SearchQuery == cm.SearchCache.Query && cm.SearchCache.Valid {
		cm.SearchMatches = cm.SearchCache.Matches
		jumpFromOrigin(cm, window)
		return
	}

	cm.SearchMatches = nil
	if cm.SearchQuery == "" {
		restoreSearchOrigin(cm, window)
		return
	}

	query := cm.SearchQuery
	if !cm.CaseSensitive {
		query = strings.ToLower(query)
	}

	scrollbackLen := window.ScrollbackLen()
	screenHeight := window.Terminal.Height()

	// The buffer is scanned newest line first, the screen and then the
	// scrollback, so when the match limit is reached the matches kept are the
	// ones nearest the live screen, where copy mode starts. Scanning oldest
	// first kept the oldest matches, and a ? search from the prompt then
	// skipped every recent match. The lines are put back in buffer order below.
	var lines [][]terminal.SearchMatch
	total := 0
	for y := screenHeight - 1; y >= 0 && total < maxSearchMatches; y-- {
		text := extractScreenLineText(window.Terminal, y)
		found := matchesOnLine(text, query, cm.CaseSensitive, scrollbackLen+y, func() []uv.Cell {
			return getScreenLineCells(window.Terminal, y)
		})
		if len(found) > 0 {
			lines = append(lines, found)
			total += len(found)
		}
	}
	for i := scrollbackLen - 1; i >= 0 && total < maxSearchMatches; i-- {
		line := window.ScrollbackLine(i)
		if line == nil {
			continue
		}
		found := matchesOnLine(extractLineTextFromCells(line), query, cm.CaseSensitive, i, func() []uv.Cell {
			return line
		})
		if len(found) > 0 {
			lines = append(lines, found)
			total += len(found)
		}
	}
	cm.SearchMatches = make([]terminal.SearchMatch, 0, total)
	for i := len(lines) - 1; i >= 0; i-- {
		cm.SearchMatches = append(cm.SearchMatches, lines[i]...)
	}

	// Update cache
	cm.SearchCache.Query = cm.SearchQuery
	cm.SearchCache.Matches = cm.SearchMatches
	cm.SearchCache.CacheTime = time.Now()
	cm.SearchCache.Valid = true

	jumpFromOrigin(cm, window)
}

// jumpFromOrigin moves the cursor to the match the typed query picks. The
// search runs from the origin saved when the prompt opened, so typing one more
// character refines the match instead of skipping past it. / takes the first
// match after the origin and ? the last one before it, each wrapping round the
// buffer. With no match the cursor goes back to the origin, as vim does.
func jumpFromOrigin(cm *terminal.CopyMode, window *terminal.Window) {
	o := cm.SearchOrigin
	originAbsY := window.ScrollbackLen() - o.ScrollOffset + o.CursorY
	idx, _ := searchFrom(cm.SearchMatches, originAbsY, o.CursorX, cm.SearchBackward)
	if idx < 0 {
		restoreSearchOrigin(cm, window)
		return
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, window, idx)
}

// restoreSearchOrigin puts the cursor and the view back where they were when
// the search prompt opened.
func restoreSearchOrigin(cm *terminal.CopyMode, window *terminal.Window) {
	o := cm.SearchOrigin
	cm.CursorX, cm.CursorY, cm.ScrollOffset = o.CursorX, o.CursorY, o.ScrollOffset
	window.ScrollbackOffset = o.ScrollOffset
}

// searchFrom returns the index of the match a search from (absY, x) lands on.
// Forward it is the first match that starts after the position, backward the
// last match that starts before it. When there is none in that direction the
// search wraps to the other end of the buffer and wrapped is true. It returns
// -1 when there are no matches at all. matches is in buffer order, oldest line
// first, which is the order executeSearch builds it in.
func searchFrom(matches []terminal.SearchMatch, absY, x int, backward bool) (idx int, wrapped bool) {
	if len(matches) == 0 {
		return -1, false
	}
	if backward {
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			if m.Line < absY || (m.Line == absY && m.StartX < x) {
				return i, false
			}
		}
		return len(matches) - 1, true
	}
	for i, m := range matches {
		if m.Line > absY || (m.Line == absY && m.StartX > x) {
			return i, false
		}
	}
	return 0, true
}

// stepMatch is n and N: it moves to the next match from the cursor in the
// given direction, wrapping round the buffer. n passes the direction of the
// last search and N the opposite one, so after ? the n key goes up. It runs
// from the cursor rather than from the last match, so a match is found from
// wherever the cursor was moved to in between.
func stepMatch(cm *terminal.CopyMode, window *terminal.Window, backward bool) (wrapped bool) {
	idx, wrapped := searchFrom(cm.SearchMatches, getAbsoluteY(cm, window), cm.CursorX, backward)
	if idx < 0 {
		return false
	}
	cm.CurrentMatch = idx
	jumpToMatch(cm, window, idx)
	return wrapped
}

// jumpToMatch jumps cursor to a specific match
func jumpToMatch(cm *terminal.CopyMode, window *terminal.Window, matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(cm.SearchMatches) {
		return
	}

	match := cm.SearchMatches[matchIdx]
	scrollbackLen := window.ScrollbackLen()

	if match.Line < scrollbackLen {
		// Match is in scrollback
		cm.ScrollOffset = scrollbackLen - match.Line
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = 0
	} else {
		// Match is in current screen
		screenLine := match.Line - scrollbackLen
		cm.ScrollOffset = 0
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = min(screenLine, window.Height-3)
	}

	cm.CursorX = match.StartX
}

// searchPrompt is the character the search prompt starts with: ? for a
// backward search and / for a forward one.
func searchPrompt(backward bool) string {
	if backward {
		return "?"
	}
	return "/"
}

// searchWrapMessage says that n or N went past the end of the buffer and
// started again at the other end.
func searchWrapMessage(backward bool) string {
	if backward {
		return "Search reached the top. It continues at the bottom."
	}
	return "Search reached the bottom. It continues at the top."
}
