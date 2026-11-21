package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const fileTokenCd = "cd"

const fileSpoofRow = "read only: wrong folder"

const (
	sidebarFilesLabel  = "files"
	sidebarFilesLabelW = len(sidebarFilesLabel)
)

type fileRowSpec struct {
	Kind  sidebarRowKind
	Index int
	Key   string
	Name  string
	Dir   bool
	Icon  fileIcon
	Note  bool
	Warn  bool
	Tree  bool
	Depth int
	Guide string
	Open  bool
}

func (m *OS) sidebarFileRows() []fileRowSpec {
	if !m.filesSectionEnabled() {
		return nil
	}
	return m.fileRowsBuilt()
}

type fileRowsKey struct {
	want, dir, elsewhere, err string
	spoofed, loading, capped  bool
	away                      bool
	tree, ascii               bool
	gen, treeGen              uint64
	first                     *fileEntry
	n                         int
}

type fileRowsCache struct {
	key   fileRowsKey
	valid bool
	rows  []fileRowSpec
}

func (m *OS) fileRowsBuilt() []fileRowSpec {
	v := &m.filesView
	key := fileRowsKey{
		want: v.Want, dir: v.Dir, elsewhere: v.Elsewhere, err: v.Err,
		spoofed: v.Spoofed, loading: v.Loading, capped: v.Capped,
		away: m.fileViewAway(),
		tree: m.filesTreeOn(), ascii: m.Settings.UseASCIIOnly,
		gen: v.Gen, treeGen: m.filesTree.gen,
		n: len(v.Entries),
	}
	if len(v.Entries) > 0 {
		key.first = &v.Entries[0]
	}
	c := &m.filesRows
	if c.valid && c.key == key {
		return c.rows
	}
	c.key, c.valid, c.rows = key, true, m.buildFileRows()
	return c.rows
}

func (m *OS) buildFileRows() []fileRowSpec {
	switch {
	case m.filesView.Want == "":
		return nil
	case m.filesView.Elsewhere != "":
		return []fileRowSpec{
			{Note: true, Warn: true, Name: "Shell is on " + printableTitle(m.filesView.Elsewhere) + "."},
			{Note: true, Name: "Its files are not shown."},
		}
	case m.filesView.Err != "":
		return []fileRowSpec{{Note: true, Warn: true, Name: m.filesView.Err}}
	case m.filesView.Dir == "":
		return []fileRowSpec{{Note: true, Name: "loading"}}
	}

	n := len(m.filesView.Entries) + 3
	if m.filesTreeOn() {
		for _, d := range m.filesTree.dirs {
			n += len(d.Entries) + 1
		}
	}
	rows := make([]fileRowSpec, 0, n)
	if m.FileViewSpoofed() {
		rows = append(rows, fileRowSpec{Note: true, Warn: true, Name: fileSpoofRow})
	}
	if m.fileViewAway() {
		rows = append(rows, fileRowSpec{Kind: sidebarRowFileBack, Index: -1, Key: fileBackKey, Name: fileBackLabel, Dir: true, Tree: m.filesTreeOn()})
	}
	if filepath.Dir(m.filesView.Dir) != m.filesView.Dir {
		rows = append(rows, fileRowSpec{Kind: sidebarRowFileUp, Index: -1, Key: "..", Name: "..", Dir: true, Tree: m.filesTreeOn()})
	}
	if m.filesTreeOn() {
		rows = m.fileTreeRows(rows, m.filesView.Dir, "", m.filesView.Entries, nil, false)
	} else {
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
	}
	switch {
	case m.filesView.Loading:
		rows = append(rows, fileRowSpec{Note: true, Name: "loading"})
	case m.filesView.Capped:
		rows = append(rows, fileRowSpec{
			Note: true,
			Name: "first " + strconv.Itoa(fileViewMaxEntries) + " shown",
		})
	case len(m.filesView.Entries) == 0:
		rows = append(rows, fileRowSpec{Note: true, Name: "empty"})
	}
	return rows
}

func (m *OS) sidebarFilesHeaderCd(cw int, pal overlay.Palette, hoverX int, cursor bool) (string, sidebarTokenSpan, bool) {
	if m.fileViewOriginWindow() == nil || m.filesView.Elsewhere != "" {
		return "", sidebarTokenSpan{}, false
	}
	tw := lipgloss.Width(fileTokenCd)
	x0 := cw - 1 - tw
	if x0 < sidebarHeaderLabelW(sidebarFilesLabel, &m.Settings)+sidebarHeaderGap {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{Kind: sidebarRowFileCd, X0: x0, X1: x0 + tw}
	ink := pal.FgMute
	if cursor || (hoverX >= span.X0 && hoverX < span.X1) {
		ink = pal.Fg
	}
	return sidebarStyle(nil, ink).Render(fileTokenCd), span, true
}

const fileTokenClose = "x"

func (m *OS) sidebarFilesHeaderClose(cw int, pal overlay.Palette, hoverX int, cursor bool) (string, sidebarTokenSpan, bool) {
	if m.filesView.Show <= 0 || sidebarLayoutHas(sidebarSectionFiles, &m.Settings) {
		return "", sidebarTokenSpan{}, false
	}
	tw := lipgloss.Width(fileTokenClose)
	x0 := cw - 1 - tw
	if x0 < sidebarHeaderLabelW(sidebarFilesLabel, &m.Settings)+sidebarHeaderGap {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{Kind: sidebarRowFileClose, X0: x0, X1: x0 + tw}
	ink := pal.FgMute
	if cursor || (hoverX >= span.X0 && hoverX < span.X1) {
		ink = pal.Fg
	}
	return sidebarStyle(nil, ink).Render(fileTokenClose), span, true
}

func (m *OS) sidebarFilesHeaderRow(cdTok string, hasCd bool, closeTok string, hasClose bool, cw int, pal overlay.Palette) string {
	room := sidebarHeaderRightRoom(cw, sidebarHeaderLabelW(sidebarFilesLabel, &m.Settings))
	if hasCd {
		room -= lipgloss.Width(fileTokenCd) + 1
	}
	if hasClose {
		room -= lipgloss.Width(fileTokenClose) + 1
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
	if hasClose {
		if right != "" {
			right += sidebarStyle(nil, nil).Render(" ")
		}
		right += closeTok
	}
	return sidebarHeaderRowRuled(sidebarFilesLabel, right, cw, pal, &m.Settings)
}

func sidebarFilesEmptyRow(cw int, pal overlay.Palette) string {
	return sidebarFit(sidebarStyle(nil, pal.FgMute).
		Render(" "+overlay.Truncate("no directory yet", max(cw-2, 1))), cw, nil)
}

func (m *OS) sidebarFileRow(row fileRowSpec, cw int, pal overlay.Palette, st sidebarRowState) string {
	if row.Tree {
		return m.sidebarFileTreeRow(row, cw, pal, st)
	}
	bg := sidebarRowBg(st, pal)

	if row.Note {
		ink := pal.FgMute
		if row.Warn {
			ink = pal.Warning
		}
		return sidebarFit(sidebarStyle(bg, nil).Render(strings.Repeat(" ", sidebarNameCol()))+
			sidebarStyle(bg, ink).Render(overlay.Truncate(row.Name, sidebarNameAvail(cw, 0))), cw, bg)
	}

	shown := printableTitle(row.Name)
	if shown == "" {
		shown = row.Name
	}
	ink := pal.FgDim
	if row.Dir {
		if row.Kind != sidebarRowFileUp && row.Kind != sidebarRowFileBack {
			shown += "/"
		}
		ink = pal.Fg
	}
	if m.fileRowHeld(row) {
		ink = theme.ReadableAt(pal.Accent, m.sidebarGroundOr(bg), theme.ContrastFloor)
	}

	gutter := sidebarGutter(false, "", bg, pal, &m.Settings)
	mark := fileRowMark(row.Icon, row.Dir, row.Kind == sidebarRowFileUp || row.Kind == sidebarRowFileBack, &m.Settings)
	glyphInk := ink
	if mark.Hex != "" {
		glyphInk = theme.FileIconInkOn(mark.Hex, m.sidebarGroundOr(bg))
	}
	glyph := sidebarStyle(bg, glyphInk).Render(mark.Glyph)
	body := sidebarStyle(bg, ink).Render(overlay.Truncate(shown, sidebarNameAvail(cw, 0)))
	return sidebarComposeRow(gutter, glyph, body, "", cw, bg)
}

func (m *OS) sidebarFileTreeRow(row fileRowSpec, cw int, pal overlay.Palette, st sidebarRowState) string {
	bg := sidebarRowBg(st, pal)
	s := &m.Settings
	gutter := sidebarGutter(false, "", bg, pal, s)
	guide := ""
	if row.Guide != "" {
		guide = sidebarStyle(bg, sidebarRuleInk(bg, pal)).Render(row.Guide)
	}
	gap := sidebarStyle(bg, nil).Render(" ")
	col := m.fileTreeNameCol(row)

	if row.Note {
		ink := pal.FgMute
		if row.Warn {
			ink = pal.Warning
		}
		at := 1 + lipgloss.Width(row.Guide) + 2
		return sidebarFit(gutter+guide+gap+gap+
			sidebarStyle(bg, ink).Render(overlay.Truncate(row.Name, max(cw-at-1, 1))), cw, bg)
	}

	shown := printableTitle(row.Name)
	if shown == "" {
		shown = row.Name
	}
	up := row.Kind == sidebarRowFileUp || row.Kind == sidebarRowFileBack
	ink := pal.FgDim
	if row.Dir {
		if !up {
			shown += "/"
		}
		ink = pal.Fg
	}
	if m.fileRowHeld(row) {
		ink = theme.ReadableAt(pal.Accent, m.sidebarGroundOr(bg), theme.ContrastFloor)
	}
	icons := fileIconsOn(s) && s.SidebarShowGlyphs
	chevron := " "
	switch {
	case up || !row.Dir:
	case icons && row.Open:
		chevron = fileTreeChevronOpen
	case icons:
		chevron = fileTreeChevronShut
	case row.Open:
		chevron = s.GetRailFoldOpenGlyph()
	default:
		chevron = s.GetRailFoldShutGlyph()
	}
	marks := sidebarStyle(bg, ink).Render(chevron) + gap
	if icons {
		mark := fileRowMark(row.Icon, row.Dir, up, s)
		if row.Dir && row.Open && mark.Glyph == fileIconDir.Glyph {
			mark.Glyph = fileTreeOpenDirGlyph
		}
		glyphInk := ink
		if mark.Hex != "" {
			glyphInk = theme.FileIconInkOn(mark.Hex, m.sidebarGroundOr(bg))
		}
		marks += sidebarStyle(bg, glyphInk).Render(mark.Glyph) + gap
	}
	body := sidebarStyle(bg, ink).Render(overlay.Truncate(shown, max(cw-col-1, 1)))
	return sidebarFit(gutter+guide+marks+body, cw, bg)
}

func (m *OS) fileTreeNameCol(row fileRowSpec) int {
	col := 1 + lipgloss.Width(row.Guide) + 2
	if fileIconsOn(&m.Settings) && m.Settings.SidebarShowGlyphs {
		col += 2
	}
	return col
}

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
		return lipgloss.NewStyle().MaxWidth(w).Render(ell)
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

func (m *OS) fileRowHeld(row fileRowSpec) bool {
	held := m.fileDragHeldKey()
	return held != "" && row.Kind == sidebarRowFileEntry && row.Key == held
}
