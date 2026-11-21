package app

import (
	"image/color"
	"reflect"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func workspacePillFg(active bool, pal overlay.Palette, ground color.Color) color.Color {
	if active {
		return theme.Readable(pal.Accent, ground)
	}
	return theme.Readable(pal.FgDim, ground)
}

func dockStripArrowFg(pal overlay.Palette) color.Color {
	return theme.Readable(pal.FgDim, pal.Canvas)
}

func workspacePill(label string, active, dragged bool, t *config.DockTabsConfig, pal overlay.Palette, caps dockCaps, ink color.Color) string {
	fg, ground := tabColor(t.InactiveFg, &pal), tabColor(t.InactiveBg, &pal)
	if active {
		fg, ground = tabColor(t.ActiveFg, &pal), tabColor(t.ActiveBg, &pal)
	}
	if ground == nil {
		ground = pal.Panel
	}
	if fg == nil {
		fg = workspacePillFg(active, pal, ground)
	}
	if dragged {
		ground = pal.Surface
	}
	if ink != nil {
		fg = ink
	}
	body := sidebarStyle(ground, fg)
	if active {
		body = body.Bold(true)
		if t.Underlined() {
			body = body.Underline(true)
		}
	} else if t.InactiveBold {
		body = body.Bold(true)
	}
	return capTab(body.Render(" "+label+" "), ground, caps)
}

func capTab(tab string, ground color.Color, caps dockCaps) string {
	lc, rc := caps.Left, caps.Right
	if lc == "" && rc == "" {
		return tab
	}
	if ground == nil || overlay.IsNoColor(ground) {
		return strings.Repeat(" ", lipgloss.Width(lc)) + tab + strings.Repeat(" ", lipgloss.Width(rc))
	}
	ink := lipgloss.NewStyle().Foreground(ground)
	return ink.Render(lc) + tab + ink.Render(rc)
}

func tabColor(s string, pal *overlay.Palette) color.Color {
	return theme.ConfigColorOn(s, pal)
}

func flatWorkspaceTab(label string, active, dragged bool, t *config.DockTabsConfig, pal overlay.Palette, caps dockCaps, ink color.Color) string {
	tab, ground := flatTabBody(label, active, dragged, t, pal, ink)
	if !t.Caps {
		return tab
	}
	return capTab(tab, ground, caps)
}

func flatTabBody(label string, active, dragged bool, t *config.DockTabsConfig, pal overlay.Palette, ink color.Color) (string, color.Color) {
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
		bold = true
		if fg == nil && theme.Depth() == overlay.Depth16 {
			st = st.Bold(true).Foreground(pal.Accent).Reverse(true)
			if t.Underlined() {
				st = st.Underline(true)
			}
			return st.Render(" " + label + " "), nil
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
	if active && t.Underlined() {
		st = st.Underline(true)
	}
	if bg != nil {
		st = st.Background(bg)
	}
	return st.Render(" " + label + " "), bg
}

func groundOr(c, fallback color.Color) color.Color {
	if c == nil {
		return fallback
	}
	return c
}

func dockTabGapText(t *config.DockTabsConfig, pal overlay.Palette) string {
	if !t.Flat() || t.Separator == "" {
		return strings.Repeat(" ", dockWorkspacePillGap)
	}
	if fg := tabColor(t.SeparatorFg, &pal); fg != nil {
		return lipgloss.NewStyle().Foreground(fg).Render(t.Separator)
	}
	return t.Separator
}

func dockTab(t *config.DockTabsConfig, label string, active, dragged, attention bool, pal overlay.Palette, caps dockCaps) string {
	var ink color.Color
	if attention && !active {
		ink = tabColor(t.AttentionFg, &pal)
	}
	if t.Flat() {
		return flatWorkspaceTab(label, active, dragged, t, pal, caps, ink)
	}
	return workspacePill(label, active, dragged, t, pal, caps, ink)
}

type dockCaps struct{ Left, Right string }

func dockCapsOf(s *config.Settings) dockCaps {
	return dockCaps{s.GetDockWorkspaceCapLeft(), s.GetDockWorkspaceCapRight()}
}

type dockStripKey struct {
	pills []dockWorkspaceTab
	tabs  config.DockTabsConfig
	look  dockStripLook
}

type dockStripLook struct {
	pal       overlay.Palette
	depth     overlay.Depth
	caps      dockCaps
	moreLeft  string
	moreRight string
	add       dockWorkspaceTab
	hasAdd    bool
	moreL     bool
	moreR     bool
	scrolls   bool
	inner     int
}

func (k *dockStripKey) same(o *dockStripKey) bool {
	return k.look == o.look && slices.Equal(k.pills, o.pills) &&
		reflect.DeepEqual(&k.tabs, &o.tabs)
}

func newDockStripKey(s dockWorkspaceStrip, tabs config.DockTabsConfig, look dockStripLook) dockStripKey {
	look.moreL, look.moreR, look.scrolls, look.inner = s.MoreLeft, s.MoreRight, s.Scrolls, s.Inner
	if s.Add != nil {
		look.add, look.hasAdd = *s.Add, true
	}
	return dockStripKey{pills: s.Pills, tabs: tabs, look: look}
}

func (k dockStripKey) clone() dockStripKey {
	k.pills = slices.Clone(k.pills)
	if k.tabs.ActiveUnderline != nil {
		u := *k.tabs.ActiveUnderline
		k.tabs.ActiveUnderline = &u
	}
	k.tabs.NumberGlyphs = slices.Clone(k.tabs.NumberGlyphs)
	return k
}

type dockStripDrawn struct {
	text   string
	hits   []dockWorkspaceHit
	arrows []dockWorkspaceArrowHit
}

type dockStripMemo struct {
	ok    bool
	key   dockStripKey
	drawn dockStripDrawn
	next  dockStripKey
}

func (m *OS) renderDockWorkspaceStrip(s dockWorkspaceStrip, startX int) string {
	m.dockWorkspaceHits = m.dockWorkspaceHits[:0]
	m.dockWorkspaceArrowHits = m.dockWorkspaceArrowHits[:0]
	if len(s.Pills) == 0 && s.Add == nil {
		return ""
	}
	memo := &m.dockStrip
	memo.next = newDockStripKey(s, m.dockTabs(), dockStripLook{
		pal: m.groundUI(), depth: theme.Depth(), caps: dockCapsOf(&m.Settings),
		moreLeft: m.Settings.GetDockWorkspaceMoreLeft(), moreRight: m.Settings.GetDockWorkspaceMoreRight(),
	})
	if !memo.ok || !memo.key.same(&memo.next) {
		memo.key, memo.drawn, memo.ok = memo.next.clone(), drawDockWorkspaceStrip(&memo.next), true
	}
	y := m.GetDockbarContentYPosition()
	for _, h := range memo.drawn.hits {
		h.X0, h.X1, h.Y = h.X0+startX, h.X1+startX, y
		m.dockWorkspaceHits = append(m.dockWorkspaceHits, h)
	}
	for _, h := range memo.drawn.arrows {
		h.X0, h.X1, h.Y = h.X0+startX, h.X1+startX, y
		m.dockWorkspaceArrowHits = append(m.dockWorkspaceArrowHits, h)
	}
	return memo.drawn.text
}

func drawDockWorkspaceStrip(k *dockStripKey) dockStripDrawn {
	l, pal, style := &k.look, k.look.pal, &k.tabs
	var d dockStripDrawn
	arrow := lipgloss.NewStyle().Foreground(dockStripArrowFg(pal))

	var b strings.Builder
	b.WriteString(" ")
	x := 1

	gutter := func(glyph string, live bool, delta int) {
		if !l.scrolls {
			return
		}
		if live {
			b.WriteString(arrow.Render(glyph))
			b.WriteString(" ")
			d.arrows = append(d.arrows, dockWorkspaceArrowHit{
				X0: x, X1: x + dockWorkspaceArrowWidth, Delta: delta,
			})
		} else {
			b.WriteString(strings.Repeat(" ", dockWorkspaceArrowWidth))
		}
		x += dockWorkspaceArrowWidth
	}

	gutter(l.moreLeft, l.moreL, -1)

	drawn := 0
	gapText := dockTabGapText(style, pal)
	for i, t := range k.pills {
		if i > 0 {
			b.WriteString(gapText)
			x, drawn = x+t.Gap, drawn+t.Gap
		}
		b.WriteString(dockTab(style, t.Label, t.Active, t.Dragged, t.Attention, pal, l.caps))
		d.hits = append(d.hits, dockWorkspaceHit{X0: x, X1: x + t.Width, Workspace: t.Workspace})
		x, drawn = x+t.Width, drawn+t.Width
	}
	if l.scrolls && drawn < l.inner {
		b.WriteString(strings.Repeat(" ", l.inner-drawn))
		x += l.inner - drawn
	}

	gutter(l.moreRight, l.moreR, 1)

	if l.hasAdd {
		if len(k.pills) > 0 {
			b.WriteString(gapText)
			x += l.add.Gap
		}
		b.WriteString(dockTab(style, l.add.Label, false, false, false, pal, l.caps))
		d.hits = append(d.hits, dockWorkspaceHit{X0: x, X1: x + l.add.Width, Workspace: 0})
	}
	d.text = b.String()
	return d
}

func (m *OS) renderDock() *lipgloss.Layer {
	fullDock, dockbarYPos := m.renderDockString()
	return lipgloss.NewLayer(fullDock).X(0).Y(dockbarYPos).Z(config.ZIndexDock).ID("dock")
}

func (m *OS) renderDockString() (string, int) {
	m.ensureDockPlan()
	layout := m.CalculateDockLayout()
	pal := m.groundUI()

	sysInfoStyle := lipgloss.NewStyle().
		Foreground(pal.FgMute).
		MarginRight(2)

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

	type itemSpan struct {
		windowIndex, x0, x1 int
	}
	var itemSpans []itemSpan
	var centerCustom []dockCustomHit
	relX := 0
	overflowX0, overflowX1 := 0, 0

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

		if layout.TruncatedCount > 0 {
			marker := " ..."
			dockItemsStr.WriteString(lipgloss.NewStyle().Foreground(dockStripArrowFg(pal)).Render(marker))
			overflowX0, overflowX1 = relX, relX+lipgloss.Width(marker)
			relX = overflowX1
		}
	}

	m.dockCustomHits = m.dockCustomHits[:0]
	dockY := m.GetDockbarContentYPosition()
	m.dockWorkspaceHits = m.dockWorkspaceHits[:0]
	m.dockWorkspaceArrowHits = m.dockWorkspaceArrowHits[:0]

	var leftB strings.Builder
	leftX := 0
	chip := m.spotlightChip()
	chipW := lipgloss.Width(chip)
	m.recordSpotlightChip(0, 0, dockY)
	calm := m.calmChip()
	calm += m.keyModeChip()
	if !m.dockPlan.Has(config.DockComponentMode) {
		if chip != "" {
			m.recordSpotlightChip(leftX, chipW, dockY)
		}
		leftB.WriteString(chip)
		leftB.WriteString(calm)
		leftX += chipW + lipgloss.Width(calm)
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
			chunk += calm
		case config.DockComponentWorkspaces:
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

	sessionStrip, sessionCells := m.buildDockSessionStrip()
	stripW := lipgloss.Width(sessionStrip)
	barWidth := max(renderWidth-stripW, 0)

	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr.String())
	anchored := centerWidth > 0 && layout.CustomCenter > 0
	centerX := actualLeftWidth
	if anchored {
		centerX = max(actualLeftWidth, min((renderWidth-centerWidth)/2, barWidth-centerWidth-layout.CenterRightReserve))
	}
	rightRoom := max(barWidth-centerX-centerWidth, 0)
	rightWidth := min(layout.RightWidth, rightRoom)

	var rightInfo string
	var notifRule string

	var notif notifBlock
	var hasNotif bool
	if rightRoom > 0 {
		notif, hasNotif = m.dockNotificationBlock(barWidth, rightRoom)
	}

	var rightCustom []dockCustomHit

	legend := m.dockModeLegend()
	switch {
	case hasNotif:
		m.notifDrawn = notif.drawn
		rightInfo = notif.Text
		notifRule = notif.Rule
		rightWidth = notif.Width
	case len(legend) > 0:
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

	notifX0 := actualLeftWidth + leftSpacer + centerWidth + rightSpacer
	m.notifHit = notifHitZones{
		Active:    hasNotif,
		X0:        notifX0,
		X1:        notifX0 + rightWidth,
		DismissX0: notifX0 + rightWidth - notif.DismissW,
		Y:         m.GetDockbarContentYPosition(),
		Cut:       hasNotif && notif.Cut,
	}
	if hasNotif {
		for _, c := range notif.Choices {
			m.notifHit.Choices = append(m.notifHit.Choices, notifSpan{X0: notifX0 + c.X0, X1: notifX0 + c.X1})
		}
	}
	if hasNotif && len(m.Notifications) > 0 {
		m.notifHit.Drawn = m.drawnCopy(m.Notifications[len(m.Notifications)-1])
		if notif.Cut {
			m.rememberMessage(m.notificationEntry(m.notifHit.Drawn))
		}
	}

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
	rightBlockX := notifX0 + rightWidth - lipgloss.Width(rightInfo)
	for _, h := range rightCustom {
		m.dockCustomHits = append(m.dockCustomHits, dockCustomHit{
			X0: rightBlockX + h.X0, X1: rightBlockX + h.X1, Y: itemY, Name: h.Name, Button: h.Button,
		})
	}

	m.dockOverflowHit = dockOverflowHit{}
	if overflowX1 > overflowX0 {
		m.dockOverflowHit = dockOverflowHit{
			Active: true, X0: itemsX + overflowX0, X1: itemsX + overflowX1,
			Y: itemY, Overflowed: layout.TruncatedCount,
		}
	}

	dockBar, barW := assembleDockBar(leftInfo, actualLeftWidth, leftSpacer, dockItemsStr.String(), centerWidth,
		rightSpacer, rightInfo, rightWidth)

	if barW > barWidth {
		dockBar, barW = truncateToWidth(dockBar, barWidth), barWidth
	}

	m.dockSessionHits = m.dockSessionHits[:0]
	sessionX := barWidth + 1
	for _, c := range sessionCells {
		m.dockSessionHits = append(m.dockSessionHits, dockSessionHit{
			X0: sessionX, X1: sessionX + c.Width, Y: itemY, Action: c.Action,
		})
		sessionX += c.Width
	}
	dockBar += sessionStrip
	barW += stripW

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

	if ruleWidth := lipgloss.Width(notifRule); ruleWidth > 0 && notifX0 < renderWidth {
		if room := renderWidth - notifX0; ruleWidth > room {
			notifRule, ruleWidth = truncateToWidth(notifRule, room), room
		}
		hairline := lipgloss.NewStyle().Foreground(ruleColor)
		separator = hairline.Render(strings.Repeat(sepChar, notifX0)) + notifRule +
			hairline.Render(strings.Repeat(sepChar, renderWidth-notifX0-ruleWidth))
	}

	if !m.Settings.DockCompact {
		if pad := renderWidth - barW; pad > 0 {
			dockBar += strings.Repeat(" ", pad)
		} else if pad < 0 {
			separator += strings.Repeat(" ", -pad)
		}
	}

	dockbarYPos := m.GetRenderHeight() - m.Settings.DockHeight()
	switch {
	case m.Settings.DockCompact:
		if m.Settings.DockbarPosition == "top" {
			dockbarYPos = 0
		}
		return dockBar, dockbarYPos
	case m.Settings.DockbarPosition == "top":
		return dockBar + "\n" + separator, 0
	}
	return separator + "\n" + dockBar, dockbarYPos
}

func assembleDockBar(left string, leftW, leftSpacer int, items string, itemsW, rightSpacer int, right string, rightW int) (string, int) {
	if strings.ContainsAny(left+items+right, "\t\n") {
		bar := lipgloss.JoinHorizontal(lipgloss.Top,
			left,
			lipgloss.NewStyle().Width(leftSpacer).Render(""),
			lipgloss.NewStyle().Render(items),
			lipgloss.NewStyle().Width(rightSpacer).Render(""),
			lipgloss.NewStyle().Width(rightW).Align(lipgloss.Right).Render(right),
		)
		return bar, lipgloss.Width(bar)
	}
	rightPad := max(rightW-lipgloss.Width(right), 0)
	var b strings.Builder
	b.Grow(len(left) + len(items) + len(right) + leftSpacer + rightSpacer + rightPad)
	b.WriteString(left)
	b.WriteString(strings.Repeat(" ", leftSpacer))
	b.WriteString(items)
	b.WriteString(strings.Repeat(" ", rightSpacer+rightPad))
	b.WriteString(right)
	return b.String(), leftW + leftSpacer + itemsW + rightSpacer + rightPad + lipgloss.Width(right)
}
