package app

import (
	"image/color"
	"slices"
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
// and it put a workspace you can switch to at 2.19:1, present but not readable,
// which the quiet tier's move up the ramp lifts to 3.71:1 without making it text.
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
func workspacePill(label string, active, dragged bool, pal overlay.Palette, s *config.Settings, ink color.Color) string {
	// A picked-up pill comes up onto Surface, the same step the rail lifts a
	// dragged row onto, so the gesture reads the same wherever it is made. It is
	// a ground change and not an ink one, which leaves the accent free to go on
	// saying which workspace is current while it is the one being moved.
	ground := pal.Panel
	if dragged {
		ground = pal.Surface
	}
	fg := workspacePillFg(active, pal)
	if ink != nil {
		fg = ink
	}
	body := sidebarStyle(ground, fg)
	if active {
		body = body.Bold(true).Underline(true)
	}
	// The caps take the fill's colour as their foreground, which is how a half
	// circle reads as the rounded end of the pill rather than as a glyph beside
	// it. ASCII has none, and styling nothing still costs the frame the escape
	// sequences around it.
	lc, rc := s.GetDockWorkspaceCapLeft(), s.GetDockWorkspaceCapRight()
	pill := body.Render(" " + label + " ")
	if lc == "" && rc == "" {
		return pill
	}
	// At 16 colours the fill is the terminal's own ground, so there is no
	// colour for a cap to take: drawn in the default ink it would read as a
	// bracket round the label. The caps become blanks of the same width, so
	// the strip's geometry and its hit boxes do not change with the depth.
	if overlay.IsNoColor(ground) {
		return strings.Repeat(" ", lipgloss.Width(lc)) + pill + strings.Repeat(" ", lipgloss.Width(rc))
	}
	caps := lipgloss.NewStyle().Foreground(ground)
	return caps.Render(lc) + pill + caps.Render(rc)
}

// tabColor is a configured [dock.tabs] colour on the dock's palette, or nil
// when it is unset or not a colour. lipgloss turns a value it cannot parse
// into no colour at all, which would paint the terminal's default in the
// middle of the bar, so a bad value is dropped here, where validateDock has
// already said it is ignored. A role such as accent is the dock's own.
func tabColor(s string, pal *overlay.Palette) color.Color {
	return theme.ConfigColorOn(s, pal)
}

// flatWorkspaceTab renders one [dock.tabs] style = "flat" tab: the label with a
// column of padding either side. A colour the table does not set comes from the
// palette the way the pills' do, so the strip still says where you are with no
// colours configured: the current tab sits on the accent, as the mode chip
// does, and the others are FgDim on the bar's own ground. At 16 colours the
// accent ground is the slot drawn in reverse video, the same thing overlay.Chip
// does there. A picked-up tab comes up onto Surface, the same lift a dragged
// pill gets.
func flatWorkspaceTab(label string, active, dragged bool, t *config.DockTabsConfig, pal overlay.Palette, ink color.Color) string {
	fg, bg, bold := tabColor(t.InactiveFg, &pal), tabColor(t.InactiveBg, &pal), t.InactiveBold
	if ink != nil {
		fg = ink
	}
	if active {
		fg, bg, bold = tabColor(t.ActiveFg, &pal), tabColor(t.ActiveBg, &pal), t.ActiveBold
	}
	st := lipgloss.NewStyle()
	onAccent := false
	switch {
	case dragged:
		bg = pal.Surface
	case active && bg == nil:
		onAccent = true
		// The accent ground is what marks the current tab when nothing else
		// does, so it is bold with it.
		bold = true
		if fg == nil && theme.Depth() == overlay.Depth16 {
			return st.Bold(true).Foreground(pal.Accent).Reverse(true).Render(" " + label + " ")
		}
		bg = pal.Accent
	}
	if fg == nil {
		switch {
		case !active:
			fg = theme.Readable(pal.FgDim, groundOr(bg, pal.Canvas))
		case dragged:
			fg = theme.Readable(pal.Accent, bg)
		case onAccent:
			fg = theme.Readable(pal.PillFg, bg)
		default:
			fg = theme.Readable(pal.Fg, bg)
		}
	}
	st = st.Bold(bold).Foreground(fg)
	if bg != nil {
		st = st.Background(bg)
	}
	return st.Render(" " + label + " ")
}

// groundOr is c, or fallback when c is unset.
func groundOr(c, fallback color.Color) color.Color {
	if c == nil {
		return fallback
	}
	return c
}

// dockTabGapText is what is drawn between two tabs: the flat style's styled
// separator, or bare columns.
func dockTabGapText(t *config.DockTabsConfig, pal overlay.Palette) string {
	if !t.Flat() || t.Separator == "" {
		return strings.Repeat(" ", dockWorkspacePillGap)
	}
	if fg := tabColor(t.SeparatorFg, &pal); fg != nil {
		return lipgloss.NewStyle().Foreground(fg).Render(t.Separator)
	}
	return t.Separator
}

// dockTab renders one tab in whichever style [dock.tabs] asks for.
func (m *OS) dockTab(t *config.DockTabsConfig, label string, active, dragged, attention bool, pal overlay.Palette) string {
	// A workspace that wants a person takes [dock.tabs] attention_fg as its
	// ink. Only a tab that is not current is marked: the current one is on
	// screen already.
	var ink color.Color
	if attention && !active {
		ink = tabColor(t.AttentionFg, &pal)
	}
	if t.Flat() {
		return flatWorkspaceTab(label, active, dragged, t, pal, ink)
	}
	return workspacePill(label, active, dragged, pal, &m.Settings, ink)
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

	pal := m.groundUI()
	style := m.dockTabs()
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

	gutter(m.Settings.GetDockWorkspaceMoreLeft(), s.MoreLeft, -1)

	drawn := 0
	gapText := dockTabGapText(&style, pal)
	for i, t := range s.Pills {
		if i > 0 {
			b.WriteString(gapText)
			x, drawn = x+t.Gap, drawn+t.Gap
		}
		b.WriteString(m.dockTab(&style, t.Label, t.Active, t.Dragged, t.Attention, pal))
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

	gutter(m.Settings.GetDockWorkspaceMoreRight(), s.MoreRight, 1)

	if s.Add != nil {
		if len(s.Pills) > 0 {
			b.WriteString(gapText)
			x += s.Add.Gap
		}
		b.WriteString(m.dockTab(&style, s.Add.Label, false, false, false, pal))
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
	m.ensureDockPlan()
	layout := m.CalculateDockLayout()
	pal := m.groundUI()

	sysInfoStyle := lipgloss.NewStyle().
		Foreground(pal.FgMute).
		MarginRight(2)

	// The mode label arrives without its caps, so the pill is assembled here
	// rather than found again by searching the string for the cap glyphs. It is
	// the dock's only filled element, so it is the only one that has to earn its
	// contrast: the foreground is picked against whatever colour the mode is,
	// and bold buys the last of the legibility a saturated fill costs.
	modeColor := layout.ModeInfo.Color
	styledModeText := ""
	if layout.ModeLabel != "" {
		fill := lipgloss.NewStyle().Background(modeColor).Foreground(theme.ContrastText(modeColor)).Bold(true)
		styledModeText = fill.Render(layout.ModeLabel)
		if lc, rc := m.Settings.GetDockModeCapLeft(), m.Settings.GetDockModeCapRight(); lc != "" && rc != "" {
			caps := lipgloss.NewStyle().Foreground(modeColor)
			styledModeText = caps.Render(lc) + fill.Render(layout.ModeLabel) + caps.Render(rc)
		}
	}

	passive := lipgloss.NewStyle().Foreground(pal.FgMute)
	styledTrailText, styledTapeText := "", ""
	if layout.TrailText != "" {
		styledTrailText = passive.Render(layout.TrailText)
	}
	if layout.TapeText != "" {
		styledTapeText = passive.Render(layout.TapeText)
	}

	var dockItemsStr strings.Builder
	itemNumber := 1

	// Where each entry lands inside the centre block, measured off the styled
	// string as it is built. Turned into screen columns below, once the centring
	// spacer this block is placed against is known.
	type itemSpan struct {
		windowIndex, x0, x1 int
	}
	var itemSpans []itemSpan
	var centerCustom []dockCustomHit
	relX := 0
	overflowX0, overflowX1 := 0, 0

	// The centre block walks the plan too. Anything the plan puts before
	// "windows" is drawn before the minimized entries, and the entries' relative
	// spans carry the offset that produces.
	for _, name := range m.dockPlan.Center {
		if name != config.DockComponentWindows {
			cell := m.dockCustomCell(name)
			if cell == "" {
				continue
			}
			w := lipgloss.Width(cell)
			dockItemsStr.WriteString(cell)
			if dockCellClickable(name) {
				centerCustom = append(centerCustom, dockCustomHit{X0: relX, X1: relX + w, Name: name})
			}
			relX += w
			continue
		}

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
			case windowIndex == m.FocusedWindow:
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
			chunk := caps.Render(m.Settings.GetDockPillLeftChar()) +
				nameLabel + caps.Render(m.Settings.GetDockPillRightChar())
			dockItemsStr.WriteString(chunk)

			w := lipgloss.Width(chunk)
			itemSpans = append(itemSpans, itemSpan{windowIndex, relX, relX + w})
			relX += w

			itemNumber++
		}

		// The marker's own columns, measured as it is written for the same
		// reason the entries' are: it is a target, and a target the renderer
		// did not record is one the click path has to guess at.
		if layout.TruncatedCount > 0 {
			marker := " ..."
			// Drawn in the same ink as the strip's overflow arrows: it wears no
			// fill and, now that it opens the aggregate view, it is a control
			// rather than a separator. FgMute measured 2.60:1 against the bare
			// canvas.
			dockItemsStr.WriteString(lipgloss.NewStyle().Foreground(dockStripArrowFg(pal)).Render(marker))
			overflowX0, overflowX1 = relX, relX+lipgloss.Width(marker)
			relX = overflowX1
		}
	}

	// The left block, assembled by walking the plan rather than by naming three
	// segments in a fixed order. Columns are accumulated as the string is built,
	// which is what lets the workspace strip be told where it starts and a custom
	// cell record the rectangle that was just drawn rather than one derived
	// afterwards from a layout that may have moved.
	m.dockCustomHits = m.dockCustomHits[:0]
	dockY := m.GetDockbarContentYPosition()
	m.dockWorkspaceHits = m.dockWorkspaceHits[:0]
	m.dockWorkspaceArrowHits = m.dockWorkspaceArrowHits[:0]

	var leftB strings.Builder
	leftX := 0
	// The spotlight chip follows the mode pill, or opens the block when the
	// plan has no mode pill. See spotlight_exit.go.
	chip := m.spotlightChip()
	chipW := lipgloss.Width(chip)
	m.recordSpotlightChip(0, 0, dockY)
	if chip != "" && !m.dockPlan.Has(config.DockComponentMode) {
		m.recordSpotlightChip(leftX, chipW, dockY)
		leftB.WriteString(chip)
		leftX += chipW
	}
	for _, name := range m.dockPlan.Left {
		var chunk string
		custom := false
		switch name {
		case config.DockComponentMode:
			chunk = styledModeText
			if chip != "" {
				m.recordSpotlightChip(leftX+lipgloss.Width(chunk), chipW, dockY)
				chunk += chip
			}
		case config.DockComponentWorkspaces:
			// Both dock paths render through here, so the tab hit rects are the
			// drawn geometry rather than a second guess at it.
			chunk = m.renderDockWorkspaceStrip(layout.WorkspaceStrip, leftX)
		case config.DockComponentTrail:
			chunk = styledTrailText
		case config.DockComponentTape:
			chunk = styledTapeText
		default:
			chunk, custom = m.dockCustomCell(name), dockCellClickable(name)
		}
		if chunk == "" {
			continue
		}
		w := lipgloss.Width(chunk)
		if custom {
			m.dockCustomHits = append(m.dockCustomHits,
				dockCustomHit{X0: leftX, X1: leftX + w, Y: dockY, Name: name})
		}
		leftB.WriteString(chunk)
		leftX += w
	}
	leftInfo := leftB.String()

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
	// A center with a custom cell (a clock, say) is anchored to the screen, so
	// a message fits beside it rather than moving it. It clamps only when the
	// bar itself is crowded. A center of minimized entries alone keeps sitting
	// in the room the right block leaves, so a message keeps its full width.
	anchored := centerWidth > 0 && layout.CustomCenter > 0
	centerX := actualLeftWidth
	if anchored {
		centerX = max(actualLeftWidth, min((renderWidth-centerWidth)/2, barWidth-centerWidth-layout.CenterRightReserve))
	}
	rightRoom := max(barWidth-centerX-centerWidth, 0)
	// The right block never takes more room than the left block and the dock
	// items leave, so the bar as a whole stays inside the screen.
	rightWidth := min(layout.RightWidth, rightRoom)

	var rightInfo string
	// notifRule is the run of hairline the message burns down over, drawn into
	// the right-hand end of the separator row below. Empty when nothing is live.
	var notifRule string

	// The message is built against the room the left block and the dock items
	// have actually left, not against an estimate, so its width needs no
	// correction afterwards. Correcting it afterwards is what the generic
	// truncation below would do, and the first thing that would cut is the
	// block's trailing columns: the gap that keeps the message off the bar's
	// right-hand end.
	var notif notifBlock
	var hasNotif bool
	if rightRoom > 0 {
		notif, hasNotif = m.dockNotificationBlock(barWidth, rightRoom)
	}

	// Where the right block's custom cells landed inside it. Converted to screen
	// columns below, once the block's own first column is known.
	var rightCustom []dockCustomHit

	legend := m.dockModeLegend()
	switch {
	case hasNotif:
		// What the tick compares against to tell whether the burn has moved.
		m.notifDrawn = notif.drawn
		// The message outranks the legend for its duration. A mode is a thing
		// the user is holding and can read the keys for again in a moment; a
		// message is a thing that just happened and will not be repeated.
		rightInfo = notif.Text
		notifRule = notif.Rule
		rightWidth = notif.Width
	case len(legend) > 0:
		// The legend is fitted to the room by dropping whole keys, so the
		// cut below never reaches it.
		rightInfo = renderModeLegend(legend, rightWidth, pal)
	default:
		rightInfo, rightCustom = m.renderDockRightCells(rightWidth, sysInfoStyle)
	}
	if w := lipgloss.Width(rightInfo); w > rightWidth {
		rightInfo = truncateToWidth(rightInfo, rightWidth)
	}

	if !anchored {
		centerX = actualLeftWidth + max(barWidth-actualLeftWidth-centerWidth-rightWidth, 0)/2
	}
	leftSpacer := max(centerX-actualLeftWidth, 0)
	rightSpacer := max(barWidth-centerX-centerWidth-rightWidth, 0)

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
		Cut:       hasNotif && notif.Cut,
	}
	if hasNotif && len(m.Notifications) > 0 {
		m.notifHit.Drawn = m.drawnCopy(m.Notifications[len(m.Notifications)-1])
		// A message this dock had to cut is one prefix+N can reopen, even a
		// short notice cut by a narrow screen.
		if notif.Cut {
			m.rememberMessage(m.notificationEntry(m.notifHit.Drawn))
		}
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
	for _, h := range centerCustom {
		m.dockCustomHits = append(m.dockCustomHits, dockCustomHit{
			X0: itemsX + h.X0, X1: itemsX + h.X1, Y: itemY, Name: h.Name,
		})
	}
	// The right block is drawn right-aligned inside the room it was given, so
	// its cells sit against its right-hand end rather than its first column.
	rightBlockX := notifX0 + rightWidth - lipgloss.Width(rightInfo)
	for _, h := range rightCustom {
		m.dockCustomHits = append(m.dockCustomHits, dockCustomHit{
			X0: rightBlockX + h.X0, X1: rightBlockX + h.X1, Y: itemY, Name: h.Name,
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

	// The styled hairline is kept until its width, glyph or colour changes.
	// Keyed on the glyph as well as the width: the separator character follows
	// the border style, which is switchable from the settings menu, and a
	// width-only key would serve the old hairline until the next resize. The
	// colour follows the theme. Styling with a width wraps the whole row by
	// grapheme, which is close to half of what the dock would cost per frame.
	sepChar, ruleColor := m.Settings.GetWindowSeparatorChar(), m.railRule()
	if m.cachedSeparatorWidth != renderWidth || m.cachedSeparatorChar != sepChar || m.cachedSeparatorColor != ruleColor {
		m.cachedSeparator = lipgloss.NewStyle().
			Width(renderWidth).
			Foreground(ruleColor).
			Render(strings.Repeat(sepChar, renderWidth))
		m.cachedSeparatorWidth = renderWidth
		m.cachedSeparatorChar = sepChar
		m.cachedSeparatorColor = ruleColor
	}
	separator := m.cachedSeparator

	// The message burns down over the hairline directly above it, across the
	// block's own columns. The lit run replaces that stretch of the separator
	// rather than being drawn on top of it, so the row is still exactly one
	// screen wide.
	//
	// It is placed at the block's recorded first column, not pinned to the right
	// edge of the screen: the session controls hold the last columns, so a burn
	// pinned there would be drawn under them, a rule's width away from the
	// message it belongs to.
	if ruleWidth := lipgloss.Width(notifRule); ruleWidth > 0 && notifX0 < renderWidth {
		if room := renderWidth - notifX0; ruleWidth > room {
			notifRule, ruleWidth = truncateToWidth(notifRule, room), room
		}
		hairline := lipgloss.NewStyle().Foreground(ruleColor)
		separator = hairline.Render(strings.Repeat(sepChar, notifX0)) + notifRule +
			hairline.Render(strings.Repeat(sepChar, renderWidth-notifX0-ruleWidth))
	}

	// A compact dock is the bar alone. The rule, and the burn a message draws
	// on it, are not drawn.
	dockbarYPos := m.GetRenderHeight() - m.Settings.DockHeight()
	dockbarParts := []string{separator, dockBar}
	if m.Settings.DockCompact {
		dockbarParts = dockbarParts[1:]
	}
	if m.Settings.DockbarPosition == "top" {
		dockbarYPos = 0
		slices.Reverse(dockbarParts)
	}

	fullDock := lipgloss.JoinVertical(lipgloss.Left, dockbarParts...)
	return fullDock, dockbarYPos
}
