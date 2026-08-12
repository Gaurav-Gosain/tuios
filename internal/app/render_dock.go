package app

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// workspacePillFg is the ink one pill state is drawn in, over the Panel step
// every pill rests on.
//
// Both states go through theme.Readable, so the strip is legible by
// measurement rather than by whichever theme it was last looked at under. At
// rest that is FgDim: FgMute is the token for separators and disabled things
// and it put a workspace you can switch to at 2.19:1, present but not readable.
// Active is the accent, which follows the terminal theme and so cannot be
// trusted on its own; Charple alone measures 2.76:1 on Panel.
func workspacePillFg(active bool, pal overlay.Palette) color.Color {
	if active {
		return theme.Readable(pal.Accent, pal.Panel)
	}
	return theme.Readable(pal.FgDim, pal.Panel)
}

// dockStripArrowFg is the ink the overflow arrows are drawn in. They wear no
// fill, so they are read against the bare canvas the bar sits on, where FgMute
// measured 2.60:1. They are controls, not separators, and an overflow arrow
// nobody can see is a strip that looks like it ends.
func dockStripArrowFg(pal overlay.Palette) color.Color {
	return theme.Readable(pal.FgDim, pal.Canvas)
}

// workspacePill renders one workspace pill for the dock strip. Every pill rests
// on the same Panel step the minimized entries do, with a column of padding
// either side of its label: the fill is what gives it a shape, and the bare
// column between two of them is what keeps them apart.
//
// Active is the accent label, bold and underlined across the whole pill, on
// that same quiet fill. An inverse slab is the loudest mark the grammar has and
// it was being spent on the thing the user already knows; the underline says
// the same in one attribute, survives ASCII mode and monochrome (both are
// attributes, not glyphs), and leaves the mode pill as the bar's only saturated
// element. Both states measure workspacePillWidth(label), so the strip does not
// reflow as the current workspace moves along it.
//
// The label is passed in rather than derived: the tab that carries it also
// carries the width the hit rectangle was cut to, and the two must be the same
// string.
func workspacePill(label string, active bool, pal overlay.Palette) string {
	body := sidebarStyle(pal.Panel, workspacePillFg(active, pal))
	if active {
		body = body.Bold(true).Underline(true)
	}
	// The caps take the fill's colour as their foreground, which is how a half
	// circle reads as the rounded end of the pill rather than as a glyph beside
	// it. ASCII has none, and styling nothing still costs the frame the escape
	// sequences around it.
	lc, rc := config.GetDockWorkspaceCapLeft(), config.GetDockWorkspaceCapRight()
	pill := body.Render(" " + label + " ")
	if lc == "" && rc == "" {
		return pill
	}
	caps := lipgloss.NewStyle().Foreground(pal.Panel)
	return caps.Render(lc) + pill + caps.Render(rc)
}

// renderDockWorkspaceStrip draws the strip starting at column startX and records
// every pill and arrow it draws into m.dockWorkspaceHits and
// m.dockWorkspaceArrowHits.
//
// The layout is: a bare column, the left gutter, the run of pills, the right
// gutter, then the "+". The gutters exist only while the strip scrolls, and
// hold their columns whether or not there is anything that way, so an arrow
// appearing does not shift the pill under the pointer.
func (m *OS) renderDockWorkspaceStrip(s dockWorkspaceStrip, startX int) string {
	m.dockWorkspaceHits = m.dockWorkspaceHits[:0]
	m.dockWorkspaceArrowHits = m.dockWorkspaceArrowHits[:0]
	if len(s.Pills) == 0 && s.Add == nil {
		return ""
	}

	pal := theme.UI()
	y := m.GetDockbarContentYPosition()
	arrow := lipgloss.NewStyle().Foreground(dockStripArrowFg(pal))

	var b strings.Builder
	b.WriteString(" ")
	x := startX + 1

	gutter := func(glyph string, live bool, delta int) {
		if !s.Scrolls {
			return
		}
		if live {
			b.WriteString(arrow.Render(glyph) + " ")
			m.dockWorkspaceArrowHits = append(m.dockWorkspaceArrowHits, dockWorkspaceArrowHit{
				X0: x, X1: x + dockWorkspaceArrowWidth, Y: y, Delta: delta,
			})
		} else {
			b.WriteString(strings.Repeat(" ", dockWorkspaceArrowWidth))
		}
		x += dockWorkspaceArrowWidth
	}

	gutter(config.GetDockWorkspaceMoreLeft(), s.MoreLeft, -1)

	drawn := 0
	for i, t := range s.Pills {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", dockWorkspacePillGap))
			x, drawn = x+dockWorkspacePillGap, drawn+dockWorkspacePillGap
		}
		b.WriteString(workspacePill(t.Label, t.Active, pal))
		m.dockWorkspaceHits = append(m.dockWorkspaceHits, dockWorkspaceHit{
			X0: x, X1: x + t.Width, Y: y, Workspace: t.Workspace,
		})
		x, drawn = x+t.Width, drawn+t.Width
	}
	// A scrolling strip holds its viewport open, so the "+" and the readout
	// behind it stay put as the pills move under them.
	if s.Scrolls && drawn < s.Inner {
		b.WriteString(strings.Repeat(" ", s.Inner-drawn))
		x += s.Inner - drawn
	}

	gutter(config.GetDockWorkspaceMoreRight(), s.MoreRight, 1)

	if s.Add != nil {
		if len(s.Pills) > 0 {
			b.WriteString(strings.Repeat(" ", dockWorkspacePillGap))
			x += dockWorkspacePillGap
		}
		b.WriteString(workspacePill(s.Add.Label, false, pal))
		m.dockWorkspaceHits = append(m.dockWorkspaceHits, dockWorkspaceHit{
			X0: x, X1: x + s.Add.Width, Y: y, Workspace: 0,
		})
	}
	return b.String()
}

func (m *OS) renderDock() *lipgloss.Layer {
	fullDock, dockbarYPos := m.renderDockString()
	return lipgloss.NewLayer(fullDock).X(0).Y(dockbarYPos).Z(config.ZIndexDock).ID("dock")
}

// renderDockString returns the dock content and its top row, used both by the
// layer path and the fullscreen fast path.
func (m *OS) renderDockString() (string, int) {
	layout := m.CalculateDockLayout()
	pal := theme.UI()

	sysInfoStyle := lipgloss.NewStyle().
		Foreground(pal.FgMute).
		MarginRight(2)

	// The mode label arrives without its caps, so the pill is assembled here
	// rather than found again by searching the string for the cap glyphs. It is
	// the dock's only filled element, so it is the only one that has to earn its
	// contrast: the foreground is picked against whatever colour the mode is,
	// and bold buys the last of the legibility a saturated fill costs.
	modeColor := lipgloss.Color(layout.ModeInfo.Color)
	fill := lipgloss.NewStyle().Background(modeColor).Foreground(theme.ContrastText(modeColor)).Bold(true)
	styledModeText := fill.Render(layout.ModeLabel)
	if lc, rc := config.GetDockModeCapLeft(), config.GetDockModeCapRight(); lc != "" && rc != "" {
		caps := lipgloss.NewStyle().Foreground(modeColor)
		styledModeText = caps.Render(lc) + fill.Render(layout.ModeLabel) + caps.Render(rc)
	}

	styledTrailText := lipgloss.NewStyle().Foreground(pal.FgMute).Render(layout.TrailText)

	var dockItemsStr strings.Builder
	itemNumber := 1

	// Where each entry lands inside the items block, measured off the styled
	// string as it is built. Turned into screen columns below, once the centring
	// spacer this block is placed against is known.
	type itemSpan struct {
		windowIndex, x0, x1 int
	}
	var itemSpans []itemSpan
	relX := 0

	for _, dockItem := range layout.VisibleItems {
		windowIndex := dockItem.WindowIndex
		window := m.Windows[windowIndex]

		// A minimized entry rests on the same Panel step the rest of the chrome
		// uses; only the two states worth a saturated fill get one.
		bgColor, fgColor := color.Color(pal.Panel), color.Color(pal.FgDim)
		emphasis := false

		isHighlighted := time.Now().Before(window.MinimizeHighlightUntil)

		switch {
		case isHighlighted:
			bgColor, emphasis = pal.Success, true
		case windowIndex == m.FocusedWindow && !window.Minimizing:
			bgColor, emphasis = pal.Accent, true
		}
		if emphasis {
			fgColor = theme.ContrastText(bgColor)
		}

		// Flat by default: the caps repeated on every minimized window turned
		// the row into beads. getDockItems pads the label, so the fill alone
		// still reads as a cell.
		caps := lipgloss.NewStyle().Foreground(bgColor)
		nameLabel := lipgloss.NewStyle().
			Background(bgColor).
			Foreground(fgColor).
			Bold(emphasis).
			Render(dockItem.Label)

		if itemNumber > 1 {
			dockItemsStr.WriteString(" ")
			relX++
		}
		chunk := caps.Render(config.GetDockPillLeftChar()) +
			nameLabel + caps.Render(config.GetDockPillRightChar())
		dockItemsStr.WriteString(chunk)

		w := lipgloss.Width(chunk)
		itemSpans = append(itemSpans, itemSpan{windowIndex, relX, relX + w})
		relX += w

		itemNumber++
	}

	// The marker's own columns, measured as it is written for the same reason
	// the entries' are: it is a target, and a target the renderer did not record
	// is one the click path has to guess at.
	overflowX0, overflowX1 := 0, 0
	if layout.TruncatedCount > 0 {
		marker := " ..."
		// Drawn in the same ink as the strip's overflow arrows: it wears no fill
		// and, now that it opens the aggregate view, it is a control rather than
		// a separator. FgMute measured 2.60:1 against the bare canvas.
		dockItemsStr.WriteString(lipgloss.NewStyle().Foreground(dockStripArrowFg(pal)).Render(marker))
		overflowX0, overflowX1 = relX, relX+lipgloss.Width(marker)
		relX = overflowX1
	}

	// The strip sits between the mode pill and the stats, and records where each
	// tab landed as it goes: both dock paths render through here, so the hit
	// rects are the drawn geometry rather than a second guess at it.
	styledTabs := m.renderDockWorkspaceStrip(layout.WorkspaceStrip, lipgloss.Width(styledModeText))

	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		styledModeText,
		styledTabs,
		styledTrailText,
	)

	renderWidth := m.GetRenderWidth()

	// The session controls take the bar's right-hand end before anything else is
	// measured, and barWidth is the bar every other block is fitted into. They
	// are built first because whether the leave control is there at all depends
	// on the run path, not on the width, so their span is not something the rest
	// of the layout can infer.
	sessionStrip, sessionCells := m.buildDockSessionStrip()
	barWidth := max(renderWidth-lipgloss.Width(sessionStrip), 0)

	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr.String())
	// The right block never takes more room than the left block and the dock
	// items leave, so the bar as a whole stays inside the screen.
	rightWidth := max(min(layout.RightWidth, barWidth-actualLeftWidth-centerWidth), 0)

	var rightInfo string
	// notifRule is the run of hairline the message burns down over, drawn into
	// the right-hand end of the separator row below. Empty when nothing is live.
	var notifRule string
	focusedWindow := m.GetFocusedWindow()

	// The message is built against the room the left block and the dock items
	// have actually left, not against an estimate, so its width needs no
	// correction afterwards. Correcting it afterwards is what the generic
	// truncation below would do, and the first thing that would cut is the
	// closing cap: the block would lose the shape that makes it part of the bar.
	notif, hasNotif := m.renderNotificationBlock(barWidth, max(barWidth-actualLeftWidth-centerWidth, 0))

	inCopyMode := focusedWindow.CopyModeVisible()
	switch {
	case hasNotif:
		// The message outranks the help line for its duration. Copy mode is a
		// mode the user is holding and can read the keys for again in a moment;
		// a message is a thing that just happened and will not be repeated.
		rightInfo = notif.Text
		notifRule = notif.Rule
		rightWidth = notif.Width
	case inCopyMode:
		helpTexts := copyModeHelpTexts(focusedWindow.CopyMode.State)

		helpStyle := lipgloss.NewStyle().
			Foreground(pal.FgDim).
			Background(pal.Panel).
			Padding(0, 1)
		// Take the longest help line that fits; the copy-mode keys are worth a
		// dock's width but not worth spilling off the end of it.
		for i, text := range helpTexts {
			rightInfo = helpStyle.Render(text)
			if lipgloss.Width(rightInfo) <= rightWidth || i == len(helpTexts)-1 {
				break
			}
		}
	default:
		var sysInfoParts []string
		if config.ShowCPU {
			sysInfoParts = append(sysInfoParts, m.GetCPUGraph())
		}
		if config.ShowRAM {
			sysInfoParts = append(sysInfoParts, m.GetRAMUsage())
		}
		// The CPU graph is the first thing dropped on a dock too narrow for
		// both readouts, then the RAM figure; a clipped graph reads as noise.
		for len(sysInfoParts) > 0 {
			rightInfo = sysInfoStyle.Render(strings.Join(sysInfoParts, " "))
			if lipgloss.Width(rightInfo) <= rightWidth {
				break
			}
			sysInfoParts = sysInfoParts[1:]
			rightInfo = ""
		}
	}
	if w := lipgloss.Width(rightInfo); w > rightWidth {
		rightInfo = truncateToWidth(rightInfo, rightWidth)
	}

	availableSpace := barWidth - actualLeftWidth - rightWidth - centerWidth
	leftSpacer := availableSpace / 2
	rightSpacer := availableSpace - leftSpacer

	if leftSpacer < 0 {
		leftSpacer = 0
		rightSpacer = 0
	}
	if rightSpacer < 0 {
		rightSpacer = 0
	}

	// The message block's own columns, taken from the spacers this pass drew
	// rather than from the bar's right-hand end. The block is the last thing in
	// the bar, so everything in front of it added up is where it starts, and the
	// burn rule above it is placed from the same number.
	notifX0 := actualLeftWidth + leftSpacer + centerWidth + rightSpacer
	m.notifHit = notifHitZones{
		Active:    hasNotif,
		X0:        notifX0,
		X1:        notifX0 + rightWidth,
		DismissX0: notifX0 + rightWidth - notif.DismissW,
		Y:         m.GetDockbarContentYPosition(),
	}

	// The entries' screen columns, now that the spacer in front of them is known.
	// Recorded from the geometry this pass drew, so a click hit-tests the bar the
	// user is looking at rather than the one a later state would produce.
	m.dockItemHits = m.dockItemHits[:0]
	itemsX, itemY := actualLeftWidth+leftSpacer, m.GetDockbarContentYPosition()
	for _, s := range itemSpans {
		m.dockItemHits = append(m.dockItemHits, dockItemHit{
			X0: itemsX + s.x0, X1: itemsX + s.x1, Y: itemY, WindowIndex: s.windowIndex,
		})
	}
	m.dockOverflowHit = dockOverflowHit{}
	if overflowX1 > overflowX0 {
		m.dockOverflowHit = dockOverflowHit{
			Active: true, X0: itemsX + overflowX0, X1: itemsX + overflowX1,
			Y: itemY, Overflowed: layout.TruncatedCount,
		}
	}

	paddedRightInfo := lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Right).Render(rightInfo)

	dockBar := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftInfo,
		lipgloss.NewStyle().Width(leftSpacer).Render(""),
		lipgloss.NewStyle().Render(dockItemsStr.String()),
		lipgloss.NewStyle().Width(rightSpacer).Render(""),
		paddedRightInfo,
	)

	// Backstop: whatever the parts add up to, the bar stops where the session
	// controls begin. The controls are appended after it, so an overfull bar
	// loses its own right-hand end and never the strip.
	if lipgloss.Width(dockBar) > barWidth {
		dockBar = truncateToWidth(dockBar, barWidth)
	}

	// The strip's screen columns, now that the bar in front of it is drawn.
	// Recorded from this pass for the reason the minimized entries are: whether
	// the leave control exists at all comes from the run path, so a handler that
	// recomputed the columns would need to re-derive that too and could disagree
	// with the frame the user clicked.
	m.dockSessionHits = m.dockSessionHits[:0]
	sessionX := barWidth + 1 // the strip opens with a bare column
	for _, c := range sessionCells {
		m.dockSessionHits = append(m.dockSessionHits, dockSessionHit{
			X0: sessionX, X1: sessionX + c.Width, Y: itemY, Action: c.Action,
		})
		sessionX += c.Width
	}
	dockBar += sessionStrip

	// Keyed on the glyph as well as the width: the separator character follows
	// the border style, which is switchable from the settings menu, and a
	// width-only key served the old hairline until the next resize.
	if sepChar := config.GetWindowSeparatorChar(); m.cachedSeparatorWidth != renderWidth || m.cachedSeparatorChar != sepChar {
		m.cachedSeparator = strings.Repeat(sepChar, renderWidth)
		m.cachedSeparatorWidth = renderWidth
		m.cachedSeparatorChar = sepChar
	}

	separator := lipgloss.NewStyle().
		Width(renderWidth).
		Foreground(theme.NotificationRule()).
		Render(m.cachedSeparator)

	// The message burns down over the hairline directly above it, across the
	// block's own columns. The lit run replaces that stretch of the separator
	// rather than being drawn on top of it, so the row is still exactly one
	// screen wide.
	//
	// It is placed at the block's recorded first column. Pinning it to the right
	// edge of the screen was the same thing only while the bar ran to that edge:
	// the session controls hold the last columns now, so the burn was drawn
	// under them, a rule's width away from the message it belonged to.
	if ruleWidth := lipgloss.Width(notifRule); ruleWidth > 0 && notifX0 < renderWidth {
		if room := renderWidth - notifX0; ruleWidth > room {
			notifRule, ruleWidth = truncateToWidth(notifRule, room), room
		}
		hairline := lipgloss.NewStyle().Foreground(theme.NotificationRule())
		sepChar := config.GetWindowSeparatorChar()
		separator = hairline.Render(strings.Repeat(sepChar, notifX0)) + notifRule +
			hairline.Render(strings.Repeat(sepChar, renderWidth-notifX0-ruleWidth))
	}

	dockbarYPos := m.GetRenderHeight() - config.DockHeight
	dockbarParts := []string{separator, dockBar}
	if config.DockbarPosition == "top" {
		dockbarYPos = 0
		dockbarParts[0], dockbarParts[1] = dockbarParts[1], dockbarParts[0]
	}

	fullDock := lipgloss.JoinVertical(lipgloss.Left, dockbarParts...)
	return fullDock, dockbarYPos
}
