package app

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The rail's file view, drawn instead of the three sections rather than
// alongside them. sidebar_files.go says why it is a mode; this draws it.
//
// It borrows the expanded rail's grammar wholesale: the same edge rule, the same
// name spine, the same one-cell gutter carrying "this is where the pointer is",
// the same right-aligned word tokens the agents header uses for its controls.
// A listing that looked like a different program bolted to the side of the rail
// would be one, and the point of putting it here rather than in a pane is that
// it is not.
//
// Every rectangle it publishes is recorded as it draws, exactly as the sections
// do. Nothing here is recomputed by a handler.

// The word tokens the file view's two header rows carry. Words rather than
// glyphs: there are three of them, they mean things a symbol would have to be
// learned for, and the rail is wide enough to say them.
const (
	fileTokenBack = "back"
	fileTokenCd   = "cd"
)

// fileViewChrome is how many lines the two header rows and the blank under them
// take before a single name is drawn.
const fileViewChrome = 3

// sidebarFilesLabel is the footer control that opens the view, and its width in
// cells. A word rather than a glyph, for the same reason the view's own controls
// are words: the rail has an ASCII mode to answer to, and there is no folder
// symbol that survives it.
const (
	sidebarFilesLabel  = "files"
	sidebarFilesLabelW = len(sidebarFilesLabel)
)

// sidebarFilesLines draws the file view over the whole rail and returns its
// lines. It is called in place of the sections, from the same point in
// sidebarPanelLinesForTree that the collapsed strip is called from.
func (m *OS) sidebarFilesLines(
	w, cw, height, topMargin, sidebarX, contentX0 int,
	pal overlay.Palette,
	compose func(string) string,
	blank string,
) []string {
	lines := make([]string, 0, height)
	nav := make([]sidebarNavRow, 0, len(m.filesView.Entries)+4)

	// The pointer's row and the keyboard's, resolved the way the sections
	// resolve theirs: by the absolute row, against what this pass is about to
	// draw. hoverRow is -1 when the pointer is not in the band.
	hoverRow := -1
	if m.SidebarHoverActive && m.SidebarHoverY >= topMargin {
		hoverRow = m.SidebarHoverY - topMargin
	}
	hoverX := -1
	if m.SidebarHoverActive {
		hoverX = m.SidebarHoverX - contentX0
	}

	recordHit := func(kind sidebarRowKind, index int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + 1,
			Kind:        kind,
			WindowIndex: index,
		})
		nav = append(nav, sidebarNavRow{Kind: kind, WindowIndex: index})
	}
	recordToken := func(kind sidebarRowKind, x0, x1 int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: contentX0 + x0, X1: contentX0 + x1,
			Y0: y, Y1: y + 1,
			Kind:        kind,
			WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: kind, WindowIndex: -1})
	}

	// Row 0: the section label and the way out.
	//
	// The way out is a word and it is on the first row, because this mode
	// replaces everything the rail normally shows: a user who arrived here by
	// clicking something small has to be able to see, without hunting, that the
	// sessions are still there and how to get back to them.
	backTok, backSpan, hasBack := railWordToken(fileTokenBack, cw, sidebarHeaderLabelW("files"), pal, hoverX)
	if hasBack {
		recordToken(sidebarRowFileBack, backSpan.X0, backSpan.X1)
	}
	lines = append(lines, compose(sidebarHeaderRow("files", backTok, cw, pal)))

	// Row 1: where the listing is, and the control that takes the pane there.
	//
	// The path is the only thing on the rail that says which machine this is a
	// listing of, and it does not say so outright: under `tuios ssh` and
	// `tuios-web` the panes and this client both run on the server, so this is
	// the server's filesystem. That is the right answer to "the pwd of the
	// currently selected window" and there is nothing to correct, but it is
	// worth knowing that a remote viewer is not looking at their own disk.
	cdTok, cdSpan, hasCd := "", sidebarTokenSpan{}, false
	if m.fileViewOriginWindow() != nil {
		cdTok, cdSpan, hasCd = railWordToken(fileTokenCd, cw, 1, pal, hoverX)
	}
	if hasCd {
		recordToken(sidebarRowFileCd, cdSpan.X0, cdSpan.X1)
	}
	pathW := cw - 2
	if hasCd {
		pathW -= lipgloss.Width(fileTokenCd) + 2
	}
	path := sidebarStyle(nil, pal.Fg).Render(truncPathLeft(shortenHome(m.filesView.Dir), max(pathW, 1)))
	lines = append(lines, compose(sidebarHeaderRow2(path, cdTok, cw)))

	lines = append(lines, blank)

	// A directory that could not be read says so where its names would have
	// been, and draws nothing else. There is no listing to scroll and no row to
	// click, so publishing either would be publishing a lie.
	if m.filesView.Err != "" {
		lines = append(lines, compose(sidebarFit(
			strings.Repeat(" ", sidebarNameCol)+
				sidebarStyle(nil, pal.Warning).Render(overlay.Truncate(m.filesView.Err, max(cw-sidebarNameCol, 1))),
			cw, nil)))
		return m.finishFilesLines(lines, nav, height, blank)
	}

	// The rows: ".." first where there is a parent, then the listing. They are
	// one list for scrolling, so the ".." never scrolls away from the names it
	// belongs to.
	atRoot := filepath.Dir(m.filesView.Dir) == m.filesView.Dir
	total := len(m.filesView.Entries)
	if !atRoot {
		total++
	}

	avail := max(height-fileViewChrome, 0)
	start, shown, hidden := sidebarWindowSection(m.filesView.Scroll, total, avail)
	m.filesView.Scroll = start

	for i := start; i < start+shown; i++ {
		row := i
		if !atRoot {
			if row == 0 {
				hovered := hoverRow == len(lines)
				recordHit(sidebarRowFileUp, -1)
				lines = append(lines, compose(m.fileRow("..", true, cw, pal, hovered)))
				continue
			}
			row--
		}
		if row >= len(m.filesView.Entries) {
			break
		}
		e := m.filesView.Entries[row]
		hovered := hoverRow == len(lines)
		recordHit(sidebarRowFileEntry, row)
		lines = append(lines, compose(m.fileRow(e.Name, e.Dir, cw, pal, hovered)))
	}

	if hidden > 0 {
		// A capped listing counts nothing, because the number below the fold is
		// not the number of names left in the directory. It says what it did
		// instead, which is the honest fact and the shorter one.
		more := overlay.Ellipsis() + " +" + strconv.Itoa(hidden)
		if m.filesView.Capped {
			more = overlay.Ellipsis() + " first " + strconv.Itoa(fileViewMaxEntries) + " shown"
		}
		lines = append(lines, compose(sidebarFit(strings.Repeat(" ", sidebarNameCol)+
			sidebarStyle(nil, pal.FgMute).Render(more), cw, nil)))
	} else if len(m.filesView.Entries) == 0 {
		// The names, not the rows. A folder with nothing in it still draws a
		// ".." above the gap, so counting rows here made "empty" reachable only
		// at a filesystem root with nothing in it, which is the one directory
		// nobody opens the view on.
		lines = append(lines, compose(sidebarFit(strings.Repeat(" ", sidebarNameCol)+
			sidebarStyle(nil, pal.FgMute).Render("empty"), cw, nil)))
	}

	return m.finishFilesLines(lines, nav, height, blank)
}

// finishFilesLines pads to the rail's height, drops anything past it, and
// publishes the nav rows.
//
// The prune matters for the same reason it matters in the sections: a rectangle
// recorded for a line the rail then truncated away is a click target on top of
// whatever is really drawn there.
func (m *OS) finishFilesLines(lines []string, nav []sidebarNavRow, height int, blank string) []string {
	for len(lines) < height {
		lines = append(lines, blank)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	limit := m.GetTopMargin() + height
	kept := m.SidebarHits[:0]
	for _, h := range m.SidebarHits {
		if h.Y0 < limit {
			kept = append(kept, h)
		}
	}
	m.SidebarHits = kept
	if len(nav) > len(m.SidebarHits) {
		nav = nav[:len(m.SidebarHits)]
	}
	m.SidebarNav = nav
	// The cursor stays inside the rows this frame published, and a rail with no
	// navigable rows parks it at zero rather than at -1, which is what the rest
	// of the rail means by "nothing to steer". Clamped in both directions
	// because the mode is entered from a rail that had its own rows, and the
	// cursor arrives from there.
	m.SidebarCursor = max(min(m.SidebarCursor, len(nav)-1), 0)
	return lines
}

// fileRow draws one name on the rail's spine.
//
// A directory wears a trailing slash and the primary ink; a file wears neither.
// That is `ls -F`'s distinction and it needs no glyph, which matters because a
// glyph would have to be a nerd-font one to say "folder" and the rail already
// has an ASCII mode to answer to.
func (m *OS) fileRow(name string, dir bool, cw int, pal overlay.Palette, hovered bool) string {
	var bg color.Color
	if hovered {
		bg = pal.Surface
	}

	shown := name
	ink := pal.FgDim
	if dir {
		shown += "/"
		ink = pal.Fg
	}

	gutter := sidebarGutter(hovered, "", bg, pal)
	glyph := sidebarStyle(bg, nil).Render(" ")
	body := sidebarStyle(bg, ink).Render(overlay.Truncate(shown, sidebarNameAvail(cw, 0)))
	return sidebarComposeRow(gutter, glyph, body, "", cw, bg)
}

// railWordToken right-aligns one word control on a header row, the way the
// agents header aligns its pair, and says where it landed. It refuses to draw
// when the row's label leaves no room, because half a control is half a click
// target.
func railWordToken(word string, cw, labelW int, pal overlay.Palette, hoverX int) (string, sidebarTokenSpan, bool) {
	ww := lipgloss.Width(word)
	x0 := cw - 1 - ww
	if x0 < labelW+1 {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{X0: x0, X1: x0 + ww}
	ink := pal.FgMute
	if hoverX >= span.X0 && hoverX < span.X1 {
		ink = pal.Fg
	}
	return sidebarStyle(nil, ink).Render(word), span, true
}

// sidebarHeaderRow2 is the header's second line: an already-styled body on the
// same one-cell inset the label uses, and an already-styled control on the same
// right-hand spine every trailing figure lands on.
func sidebarHeaderRow2(body, right string, cw int) string {
	row := sidebarStyle(nil, nil).Render(" ") + body
	if rw := lipgloss.Width(right); rw > 0 {
		gap := max(cw-lipgloss.Width(row)-rw-1, 0)
		row += strings.Repeat(" ", gap) + right + " "
	}
	return sidebarFit(row, cw, nil)
}

// shortenHome writes a path under the home directory as ~/..., which is how the
// user wrote it and how a shell prompt shows it. On a twenty-eight column rail
// it is often the difference between seeing the project and seeing "/home/".
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// truncPathLeft cuts a path from the front rather than the back.
//
// Every other truncation on the rail keeps the head, because a name's first
// characters are what identify it. A path is the other way round: the last
// component is the directory you are in and the ones before it are context, so
// cutting the tail off "/home/u/dev/tuios/internal" would leave "/home/u/dev…"
// and answer nothing.
func truncPathLeft(path string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(path) <= w {
		return path
	}
	ell := overlay.Ellipsis()
	room := w - lipgloss.Width(ell)
	if room <= 0 {
		return lipgloss.NewStyle().MaxWidth(w).Render(path)
	}
	runes := []rune(path)
	for i := range runes {
		tail := string(runes[i:])
		if lipgloss.Width(tail) <= room {
			return ell + tail
		}
	}
	return ell
}
