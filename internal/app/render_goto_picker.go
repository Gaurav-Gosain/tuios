package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

const (
	gotoPickerWidth = 80
	// gotoTagWidth is the kind tag's column: the longest tag and two spaces.
	gotoTagWidth = len("workspace") + 2
	// gotoMinRows is room for the empty message and its hint.
	gotoMinRows = 3
)

// renderGotoPicker renders the go-to picker on the shared list overlay.
func (m *OS) renderGotoPicker() (string, overlay.Geometry, []overlayRowHit) {
	g := &m.gotoPicker
	rows := m.gotoFiltered()
	visible := m.gotoVisible()
	g.tallest = visible
	g.list.Fit(len(rows), visible)
	query := g.list.Query.String()
	empty := "Nowhere to go"
	if g.parseErr != "" {
		empty = g.parseErr
	} else if name, ok := gotoNewSessionName(query); ok {
		empty = "No session '" + name + "': enter makes it"
	} else if strings.TrimSpace(query) != "" {
		empty = "No place matches '" + query + "'"
	}
	return m.renderListOverlay(listOverlay{
		Title:      "Go to",
		Width:      gotoPickerWidth,
		MaxVisible: visible,
		Search:     true,
		SearchLine: func(width int) string { return gotoPicker().searchLine(m, width) },
		Count:      len(rows),
		Selected:   g.list.Cursor,
		Scroll:     &g.list.Scroll,
		EmptyMsg:   empty,
		Hints: []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "go"},
			{Key: "@ # !", Label: "filter"},
			{Key: "ctrl+r", Label: "rename"},
			{Key: "ctrl+d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		DetailFor: func(width int) []string {
			if g.killing.node.ID == "" {
				return nil
			}
			pal := m.overlayUI()
			ask := "Delete session " + printableTitle(gotoRowLabel(g.killing)) + "? y yes, n no"
			return []string{overlay.Style(pal.Surface).Foreground(pal.Warning).Render(overlay.Truncate(ask, width))}
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.gotoPickerRow(rows[i], selected, rowBg, pal, width)
		},
	})
}

// gotoVisible is how many rows the go-to list shows: as many as match, up to
// listOverlayRows, and never fewer than it has shown since it opened (see
// gotoPickerState.tallest). A small session gets a small picker.
func (m *OS) gotoVisible() int {
	n := max(len(m.gotoFiltered()), m.gotoPicker.tallest, gotoMinRows)
	return min(n, listOverlayRows)
}

// gotoPickerRow draws one place: its kind, where it is, and what it is doing.
func (m *OS) gotoPickerRow(r gotoRow, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	tag := gotoKindNames[r.kind][0]
	tagColor := pal.FgMute
	if r.needsYou() {
		tagColor = pal.Warning
	}
	labelColor := pal.FgDim
	if selected {
		labelColor = pal.Fg
	}
	left := overlay.Style(rowBg).Foreground(tagColor).Render(tag+strings.Repeat(" ", gotoTagWidth-len(tag))) +
		overlay.Style(rowBg).Foreground(labelColor).Bold(selected).Render(printableTitle(gotoRowLabel(r)))
	if r.kind == gotoSession && r.space != "" {
		left += overlay.Style(rowBg).Foreground(pal.FgMute).Render(" [space: " + printableTitle(r.space) + "]")
	}
	// A session's return note follows its label in muted ink, as on the rail.
	// The row gives way from its end, so the note is what is cut first.
	if r.kind == gotoSession && r.node.Note != "" {
		left += overlay.Style(rowBg).Foreground(pal.FgMute).Render("  " + printableTitle(r.node.Note))
	}

	var right string
	switch r.kind {
	case gotoAgent:
		words := agentRowStateWords(sidebarAgentEntry{State: r.state, AgentKind: r.agentKind})
		if r.message != "" {
			words += ": " + printableTitle(r.message)
		}
		right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(overlay.Truncate(words, max(width/3, 8)))
		if glyph, glyphColor := agentMark(r.state, r.doneSeen, r.subagents, pal); glyph != "" {
			right += overlay.Style(rowBg).Foreground(glyphColor).Bold(r.needsYou()).Render(" " + glyph)
		}
	case gotoSession, gotoWorkspace:
		right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(plural.Count(r.panes, "pane"))
	case gotoPane:
		return m.gotoPaneRow(r, selected, left, rowBg, pal, width)
	}
	right += overlay.Style(rowBg).Render("  ") + gotoCurrentCell(r, rowBg, pal)
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}

// gotoCurrentWord marks the place the client is on. It is the last column of
// every row, and every row keeps its room, so the facts before it end in one
// column whichever row the client is on.
const gotoCurrentWord = "current"

// gotoCurrentCell is a row's last column: "current" on the place the client
// is on, blanks of the same width on every other row.
func gotoCurrentCell(r gotoRow, rowBg color.Color, pal overlay.Palette) string {
	if r.current {
		return overlay.Style(rowBg).Foreground(pal.Success).Render(gotoCurrentWord)
	}
	return overlay.Style(rowBg).Render(strings.Repeat(" ", len(gotoCurrentWord)))
}

// gotoDirMin is the fewest cells a pane's folder is drawn in. With less room
// the folder is left out, before the label gives way.
const gotoDirMin = 10

// gotoPaneRow ends a pane's row with its folder, dim, and then "current" when
// the client is on it. The folder takes the room the label leaves, cut in the
// middle so its start and its last folder both stay, and it goes first when
// the row is narrow.
func (m *OS) gotoPaneRow(r gotoRow, selected bool, left string, rowBg color.Color, pal overlay.Palette, width int) string {
	marker := listRowMarker(selected)
	current := gotoCurrentCell(r, rowBg, pal)
	right := current
	if r.dir != "" {
		// Two-cell gaps: label to folder, and folder to "current".
		room := width - lipgloss.Width(marker) - lipgloss.Width(left) - 2 - len(gotoCurrentWord) - 2
		if room >= gotoDirMin {
			right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(shortenMiddle(printableTitle(r.dir), room)) +
				overlay.Style(rowBg).Render("  ") + current
		}
	}
	return listRowSpans(width, marker, left, right, rowBg, pal)
}

// gotoRowLabel is where a row's place is, as a path from its session:
// "payments", "payments › 2 · api-tests", "payments › 1 › nvim", and for an
// agent its harness first, "claude · payments › 1 › api".
func gotoRowLabel(r gotoRow) string {
	where := r.sessionLabel
	if r.host != "" {
		where += " @ " + r.host
	}
	switch r.kind {
	case gotoWorkspace:
		where += " › " + gotoWsWords(r)
	case gotoPane:
		where += " › " + r.wsLabel + " › " + r.title
	case gotoAgent:
		if r.wsLabel != "" {
			where += " › " + r.wsLabel
		}
		where += " › " + r.title
		if h := gotoHarnessWord(r.harness); h != "" {
			where = h + " · " + where
		}
	}
	return where
}

// gotoWsWords is a workspace as its row names it: the number, and the name
// after it when it has one.
func gotoWsWords(r gotoRow) string {
	n := r.wsLabel
	if num := strconv.Itoa(r.ws); n != num {
		return num + " · " + n
	}
	return n
}

// gotoHarnessWord is a harness id as a word: claude-code is claude.
func gotoHarnessWord(h string) string {
	word, _, _ := strings.Cut(h, "-")
	return word
}
