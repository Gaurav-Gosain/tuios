package app

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	gotoPickerWidth = 80
	// gotoTagWidth is the kind tag's column: the longest tag and two spaces.
	gotoTagWidth = len("session") + 2
)

// renderGotoPicker renders the go-to picker on the shared list overlay.
func (m *OS) renderGotoPicker() (string, overlay.Geometry, []overlayRowHit) {
	g := &m.gotoPicker
	rows := m.gotoFiltered()
	if len(rows) > 0 {
		g.selected = clampInt(g.selected, 0, len(rows)-1)
	}
	empty := "Nowhere to go"
	if g.parseErr != "" {
		empty = g.parseErr
	} else if strings.TrimSpace(g.query) != "" {
		empty = "No place matches '" + g.query + "'"
	}
	return m.renderListOverlay(listOverlay{
		Title:      "Go to",
		Width:      gotoPickerWidth,
		MaxVisible: listOverlayRows,
		Search:     true,
		Query:      g.query,
		Count:      len(rows),
		Selected:   g.selected,
		Scroll:     &g.scroll,
		EmptyMsg:   empty,
		Hints: []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "go"},
			{Key: "@ # !", Label: "agent session ws"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return m.gotoPickerRow(rows[i], selected, rowBg, pal, width)
		},
	})
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
		right = overlay.Style(rowBg).Foreground(pal.FgMute).Render(panePlural(r.panes))
	}
	if r.current {
		right = overlay.Style(rowBg).Foreground(pal.Success).Render("current  ") + right
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
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
