package app

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The rail's files section, drawn beside the other sections rather than instead
// of them. sidebar_files.go says why it is a section and how its listing is
// read; this draws it.
//
// It borrows the rail's grammar wholesale: the same edge rule, the same name
// spine, the same one-cell gutter, the same one-cell glyph column, the same
// right-aligned figures inset one cell from the rail's own edge. A listing that
// looked like a different program bolted to the side of the rail would be one,
// and the point of putting it here rather than in a pane is that it is not.
//
// Every rectangle it publishes is recorded as it draws, exactly as the other
// sections do. Nothing here is recomputed by a handler, and nothing here reads
// the filesystem: the rows come from whatever the last finished read left in
// the model.

// fileTokenCd is the header's one word control. A word rather than a glyph:
// there is no folder-and-arrow symbol that means "take the pane there" and
// survives a terminal with no nerd font, and the rail is wide enough to say it.
const fileTokenCd = "cd"

// sidebarFilesLabel is the footer control that puts the section on the rail,
// and its width in cells.
const (
	sidebarFilesLabel  = "files"
	sidebarFilesLabelW = len(sidebarFilesLabel)
)

// fileRowSpec is one drawn row of the files section, resolved before the budget
// so the section can be counted, scrolled and hit-tested by the same machinery
// the other three use.
type fileRowSpec struct {
	// Kind is the row's click target, meaningful only when Note is false.
	Kind sidebarRowKind
	// Index is the entry's position in the listing, for an entry row.
	Index int
	// Key identifies the row across a relayout, so the keyboard cursor stays on
	// the name it was on when the listing scrolls or a sibling appears. It
	// rides the nav row's WindowID field, which nothing in this section uses.
	Key  string
	Name string
	Dir  bool
	// Icon is the file type's mark, carried from the listing so the render
	// path does no lookup.
	Icon fileIcon
	// Note marks a row that says something about the listing rather than being
	// part of it: the reason a read failed, that a read is running, that the
	// folder is empty, or that the listing was cut short. It is drawn quiet and
	// publishes no rectangle, because there is nothing for a click to do.
	Note bool
	// Warn raises a note row out of the quiet tier. A read that failed is the
	// one thing this section has to say that the user has to act on, and it
	// would otherwise be drawn in the same ink as the word "empty".
	Warn bool
}

// sidebarFileRows is the section's rows, in drawn order.
//
// The ".." row is part of the same list rather than pinned above it, so it
// scrolls with the names it belongs to and the section needs one offset like
// every other. It comes first, which is where a person looks for the way out.
func (m *OS) sidebarFileRows() []fileRowSpec {
	if !m.filesSectionEnabled() {
		return nil
	}
	switch {
	case m.filesView.Want == "":
		// Nothing has ever been asked for, which on a fresh client means no pane
		// has said where it is. The section draws nothing at all rather than a
		// header over a word: a permanent "no folder" row on every rail whose
		// shell does not report its directory would cost two lines to say
		// nothing. The footer control is where a user who goes looking for it is
		// told why, by name, once.
		return nil
	case m.filesView.Err != "":
		// A directory that could not be read says so where its names would have
		// been, and draws nothing else. There is no listing to scroll and no row
		// to click, so publishing either would be publishing a lie.
		return []fileRowSpec{{Note: true, Warn: true, Name: m.filesView.Err}}
	case m.filesView.Dir == "":
		// Nothing has come back yet. Either a first read is running or the rail
		// has just been switched on; both say the same thing to the user.
		return []fileRowSpec{{Note: true, Name: "loading"}}
	}

	rows := make([]fileRowSpec, 0, len(m.filesView.Entries)+2)
	if filepath.Dir(m.filesView.Dir) != m.filesView.Dir {
		rows = append(rows, fileRowSpec{Kind: sidebarRowFileUp, Index: -1, Key: "..", Name: "..", Dir: true})
	}
	for i, e := range m.filesView.Entries {
		rows = append(rows, fileRowSpec{
			Kind:  sidebarRowFileEntry,
			Index: i,
			Key:   e.Name,
			Name:  e.Name,
			Dir:   e.Dir,
			Icon:  e.Icon,
		})
	}
	switch {
	case m.filesView.Loading:
		// A reload of a directory already on screen keeps the old names up and
		// says a new answer is coming, rather than blanking the section for as
		// long as the read takes.
		rows = append(rows, fileRowSpec{Note: true, Name: "loading"})
	case m.filesView.Capped:
		// A capped listing counts nothing, because the number below the fold is
		// not the number of names left in the directory. It says what it did
		// instead, which is the honest fact and the shorter one.
		rows = append(rows, fileRowSpec{
			Note: true,
			Name: "first " + strconv.Itoa(fileViewMaxEntries) + " shown",
		})
	case len(m.filesView.Entries) == 0:
		// The names, not the rows. A folder with nothing in it still draws a
		// ".." above the gap, so counting rows here made "empty" reachable only
		// at a filesystem root with nothing in it, which is the one directory
		// nobody opens the section on.
		rows = append(rows, fileRowSpec{Note: true, Name: "empty"})
	}
	return rows
}

// sidebarFilesHeaderCd places the header's cd control on the same spine every
// other trailing figure lands on, one cell in from the rail's edge, and says
// which columns it took. It is drawn only when there is a pane the cd could
// mean, and refused when the header has no room for it beside its own label,
// since half a control is half a click target.
func (m *OS) sidebarFilesHeaderCd(cw int, pal overlay.Palette, hoverX int, cursor bool) (string, sidebarTokenSpan, bool) {
	if m.fileViewOriginWindow() == nil {
		return "", sidebarTokenSpan{}, false
	}
	tw := lipgloss.Width(fileTokenCd)
	x0 := cw - 1 - tw
	if x0 < sidebarHeaderLabelW(sidebarFilesLabel)+1 {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{Kind: sidebarRowFileCd, X0: x0, X1: x0 + tw}
	ink := pal.FgMute
	if cursor || (hoverX >= span.X0 && hoverX < span.X1) {
		ink = pal.Fg
	}
	return sidebarStyle(nil, ink).Render(fileTokenCd), span, true
}

// sidebarFilesHeaderRow is the section's one line of chrome: the label, the
// directory being listed, and the cd control.
//
// One line, not two. Every section on the rail costs its header before a single
// row of content appears, and a fourth section that spent two of them would
// take the chrome floor from four lines to six on a rail that has already once
// overrun its region. The path is cut from the front rather than the back, so
// what survives on a narrow rail is the folder you are in rather than the disk
// it is on.
//
// The path is also the only thing on the rail that says which machine this is a
// listing of, and it does not say so outright: under `tuios ssh` and `tuios-web`
// the panes and this client both run on the server, so this is the server's
// filesystem. That is the right answer to "what is in the pane's directory" and
// there is nothing to correct, but a remote viewer is not looking at their own
// disk.
func (m *OS) sidebarFilesHeaderRow(cdTok string, hasCd bool, cw int, pal overlay.Palette) string {
	room := cw - sidebarHeaderLabelW(sidebarFilesLabel) - 2
	if hasCd {
		room -= lipgloss.Width(fileTokenCd) + 1
	}
	right := ""
	if room > 0 {
		if path := truncPathLeft(shortenHome(m.filesView.Dir), room); path != "" {
			right = sidebarStyle(nil, pal.FgDim).Render(path)
		}
	}
	if hasCd {
		if right != "" {
			right += sidebarStyle(nil, nil).Render(" ")
		}
		right += cdTok
	}
	return sidebarHeaderRow(sidebarFilesLabel, right, cw, pal)
}

// sidebarFileRow draws one row of the listing on the rail's spine.
//
// A directory wears the primary ink and a trailing slash; a file wears neither.
// That is `ls -F`'s distinction, and it is what the row still says on a terminal
// with no colour and no icon at all. The icon in the glyph column is the layer
// on top of it, not instead of it.
func (m *OS) sidebarFileRow(row fileRowSpec, cw int, pal overlay.Palette, hovered bool) string {
	var bg color.Color
	if hovered {
		bg = pal.Surface
	}

	if row.Note {
		ink := pal.FgMute
		if row.Warn {
			ink = pal.Warning
		}
		return sidebarFit(sidebarStyle(bg, nil).Render(strings.Repeat(" ", sidebarNameCol))+
			sidebarStyle(bg, ink).Render(overlay.Truncate(row.Name, sidebarNameAvail(cw, 0))), cw, bg)
	}

	// A name off a filesystem is foreign data in the same sense a window title
	// is: it can hold a control character, a private-use codepoint or an emoji,
	// and the rail launders all three out of every other name it draws. A name
	// that is nothing but those keeps its raw form, because a blank row is
	// worse than a tofu box.
	shown := printableTitle(row.Name)
	if shown == "" {
		shown = row.Name
	}
	ink := pal.FgDim
	if row.Dir {
		// The ".." row is already spelled the way every shell spells it, so a
		// slash on it would be one character of decoration on the one row that
		// needs none.
		if row.Kind != sidebarRowFileUp {
			shown += "/"
		}
		ink = pal.Fg
	}

	gutter := sidebarGutter(false, "", bg, pal)
	// The glyph is the one cell on the row allowed an ink of its own. A colour
	// off the icon table is absolute, so it is measured against the ground this
	// row actually draws on before it is burned; with the colour off, and on
	// every row the table has nothing to say about, the mark wears the row's own
	// ink like every other glyph on the rail.
	mark := fileRowMark(row.Icon, row.Dir, row.Kind == sidebarRowFileUp)
	glyphInk := ink
	if mark.Hex != "" {
		glyphInk = theme.FileIconInkOn(mark.Hex, sidebarGroundOr(bg))
	}
	glyph := sidebarStyle(bg, glyphInk).Render(mark.Glyph)
	body := sidebarStyle(bg, ink).Render(overlay.Truncate(shown, sidebarNameAvail(cw, 0)))
	return sidebarComposeRow(gutter, glyph, body, "", cw, bg)
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
