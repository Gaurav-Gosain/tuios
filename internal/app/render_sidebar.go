package app

import (
	"image"
	"image/color"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

const sidebarRestoredTag = session.RestoredTag

type sidebarRowKind int

const (
	sidebarRowSession sidebarRowKind = iota
	sidebarRowWindow
	sidebarRowAgent
	sidebarRowAgentFilter
	sidebarRowAgentSort
	sidebarRowAgentMail
	sidebarRowNewSession
	sidebarRowNewWindow
	sidebarRowCollapse
	sidebarRowFiles
	sidebarRowFileCd
	sidebarRowFileClose
	sidebarRowFileUp
	sidebarRowFileBack
	sidebarRowFileEntry
	sidebarRowHostSession
	sidebarRowHostNew
	sidebarRowHostSignIn
	sidebarRowDivider
	sidebarRowRepo
	sidebarRowHost
	sidebarRowGlobalNew
	sidebarRowAgentFold
	sidebarRowSubagentToggle
	sidebarRowRailPaneStart
	sidebarRowAgentKin
	sidebarRowCustomLink
)

func sidebarAddGlyph(s *config.Settings) string { return s.GetRailAddGlyph() }

type sidebarRowState struct {
	Cursor     bool
	Hover      bool
	Focused    bool
	CursorBg   string
	FoldCursor bool
}

func (st sidebarRowState) lit() bool { return st.Cursor || st.Hover }

func (st sidebarRowState) mark(pal overlay.Palette, row string) string {
	rs := overlay.RowState{Cursor: st.Cursor, Focused: st.Focused, Hover: st.Hover}
	return pal.CursorText(pal.Mark(row, rs), rs, sidebarNameCol()-1)
}

func (m *OS) sidebarCursorTarget() (sidebarNavRow, bool) {
	switch {
	case m.sidebarFollowSession != "":
		return sidebarNavRow{Kind: sidebarRowSession, SessionID: m.sidebarFollowSession}, true
	case m.sidebarCursorWantSet:
		return m.sidebarCursorWant, true
	case m.SidebarCursor >= 0 && m.SidebarCursor < len(m.SidebarNav):
		return m.SidebarNav[m.SidebarCursor], true
	}
	return sidebarNavRow{}, false
}

func (m *OS) railRowState(hover, cursor bool) sidebarRowState {
	return sidebarRowState{Cursor: cursor, Hover: hover, Focused: m.sidebarRailFocused(), CursorBg: m.Settings.SidebarCursorBackground}
}

func railCursorBg(v string, pal overlay.Palette) color.Color {
	if v == "" || pal.Depth == overlay.Depth16 {
		return nil
	}
	return theme.ConfigColorOn(v, &pal)
}

func sidebarRowBg(st sidebarRowState, pal overlay.Palette) color.Color {
	switch {
	case st.Cursor && st.Focused:
		if c := railCursorBg(st.CursorBg, pal); c != nil {
			return c
		}
		return pal.Surface
	case st.Cursor, st.Hover:
		return pal.RowSel
	}
	return nil
}

func sidebarHeaderAdd(kind sidebarRowKind, cw, labelW int, pal overlay.Palette, hoverX int, cursor bool, s *config.Settings, rowBg color.Color) (string, sidebarTokenSpan, bool) {
	gw := lipgloss.Width(sidebarAddGlyph(s))
	x0 := cw - 1 - gw
	if x0 < labelW+sidebarHeaderGap {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{Kind: kind, X0: x0, X1: x0 + gw}
	ink := sidebarRuleInk(rowBg, pal)
	if cursor || (hoverX >= span.X0 && hoverX < span.X1) {
		ink = pal.Fg
	}
	return sidebarStyle(rowBg, ink).Render(sidebarAddGlyph(s)), span, true
}

type sidebarSection int

const (
	sidebarSectionSessions sidebarSection = iota
	sidebarSectionTerminals
	sidebarSectionAgents
	sidebarSectionFiles
	sidebarSectionGit
	sidebarSectionCustom
	sidebarSectionPane
	sidebarSectionCount
)

type sidebarRowHit struct {
	X0, Y0, X1, Y1 int
	Kind           sidebarRowKind
	SessionID      string
	WindowID       string
	WindowIndex    int
}

func (r sidebarRowHit) Contains(x, y int) bool {
	return x >= r.X0 && x < r.X1 && y >= r.Y0 && y < r.Y1
}

const (
	sidebarVariantGlyph = iota
	sidebarVariantNarrow
	sidebarVariantFull
)

func sidebarVariant(w int) int {
	switch {
	case w <= config.SidebarGlyphWidth:
		return sidebarVariantGlyph
	case w <= config.SidebarNarrowWidth:
		return sidebarVariantNarrow
	default:
		return sidebarVariantFull
	}
}

func agentGlyphColor(state string, pal overlay.Palette) color.Color {
	switch state {
	case "working":
		return pal.Busy
	case "needs_input":
		return pal.Attention
	case "idle":
		return pal.FgMute
	case "done":
		return pal.Success
	case "errored", "limit_reached":
		return pal.Warn
	case string(agentStateWaiting):
		return pal.Busy
	default:
		return pal.FgMute
	}
}

func sidebarStateColor(state string, doneSeen bool, pal overlay.Palette) color.Color {
	if state == "done" && doneSeen {
		return pal.FgMute
	}
	return agentGlyphColor(state, pal)
}

func sidebarAttention(state string) bool {
	return state == "needs_input" || state == "errored"
}

func sidebarSeverityColor(state string, pal overlay.Palette) color.Color {
	switch state {
	case "needs_input":
		return pal.Warning
	case "errored":
		return pal.Warn
	default:
		return nil
	}
}

func sidebarGutter(current bool, state string, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	return sidebarGutterTinted(current, state, nil, bg, pal, s)
}

func railFocusTint(tint color.Color, pal overlay.Palette) color.Color {
	if tint != nil {
		return tint
	}
	return pal.Accent
}

func sidebarGutterTinted(current bool, state string, tint, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	switch {
	case current && s.GetRailRowStyle() == config.RailRowMarker:
		return sidebarStyle(bg, railFocusTint(tint, pal)).Bold(true).Render(s.GetRailMarker())
	case current:
		return sidebarStyle(bg, railFocusTint(tint, pal)).Render(s.GetRailFocusMark())
	case sidebarAttention(state):
		return sidebarStyle(bg, sidebarSeverityColor(state, pal)).Render(s.GetRailAttentionMark())
	default:
		return sidebarStyle(bg, nil).Render(" ")
	}
}

func sidebarGutterNameMarked(state string, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	if sidebarAttention(state) {
		return sidebarStyle(bg, sidebarSeverityColor(state, pal)).Render(s.GetRailAttentionMark())
	}
	return sidebarStyle(bg, nil).Render(" ")
}

func railNameMarked(s *config.Settings) (marked bool, extra int) {
	switch s.GetRailRowStyle() {
	case config.RailRowUnderline:
		return true, 0
	case config.RailRowPill:
		return true, lipgloss.Width(s.GetWindowPillLeft()) + lipgloss.Width(s.GetWindowPillRight())
	}
	return false, 0
}

func railNameMarks(s *config.Settings) bool {
	marked, _ := railNameMarked(s)
	return marked
}

func railCurrentName(text string, tint, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	ink := railFocusTint(tint, pal)
	if s.GetRailRowStyle() == config.RailRowUnderline {
		return sidebarStyle(bg, ink).Underline(true).Render(text)
	}
	caps := sidebarStyle(bg, ink)
	fill := lipgloss.NewStyle().Background(ink).Foreground(overlay.ContrastText(ink))
	return caps.Render(s.GetWindowPillLeft()) + fill.Render(text) + caps.Render(s.GetWindowPillRight())
}

func agentElapsedBucket(stateAt int64) int64 {
	if stateAt <= 0 {
		return 0
	}
	return int64(time.Since(time.Unix(0, stateAt)) / time.Minute)
}

func agentElapsed(state string, stateAt int64, now time.Time) string {
	if stateAt <= 0 || state == "idle" || state == "" {
		return ""
	}
	d := now.Sub(time.Unix(0, stateAt))
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

const railAgeFloor = 5 * time.Minute

func railAgentAge(state string, stateAt int64, now time.Time) string {
	if stateAt > 0 && now.Sub(time.Unix(0, stateAt)) < railAgeFloor {
		return ""
	}
	return agentElapsed(state, stateAt, now)
}

type sidebarAgentEntry struct {
	SessionID     string
	WindowID      string
	Title         string
	State         string
	DoneSeen      bool
	StateAt       int64
	WindowIndex   int
	Harness       string
	Message       string
	AgentKind     string
	Reason        string
	Meta          []sessiontree.MetaToken
	Queued        int
	DraftHeld     bool
	Settled       bool
	Quiet         bool
	Subagents     int
	Cwd           string
	Program       []sessiontree.ProgramRecord
	PR            string
	SessionLabel  string
	Foreign       bool
	Host          string
	Focused       bool
	Fold          int
	FoldNames     string
	Children      []sessiontree.Subagent
	ChildRows     int
	Child         *sessiontree.Subagent
	More          int
	Lead          bool
	Head          bool
	HeadKids      []sessiontree.Subagent
	Open          bool
	Last          bool
	Parent        *sessiontree.PaneRef
	Nest          int
	Kin, KinNames string
}

func (e sidebarAgentEntry) sidebarSubRow() bool {
	return e.Child != nil || e.More > 0 || e.Head || e.Kin != ""
}

type sidebarTerminalEntry struct {
	SessionID   string
	WindowID    string
	Title       string
	State       string
	DoneSeen    bool
	Subagents   int
	Focused     bool
	Tag         string
	Quiet       bool
	Host        string
	HostLink    string
	Recording   bool
	WindowIndex int
	workspace   int
}

func sidebarStyle(bg, fg color.Color) lipgloss.Style {
	s := lipgloss.NewStyle()
	if bg != nil {
		s = s.Background(bg)
	}
	if fg != nil {
		s = s.Foreground(fg)
	}
	return s
}

func (m *OS) sidebarGroundOr(bg color.Color) color.Color {
	if bg != nil {
		return bg
	}
	return m.railGround()
}

func sidebarFit(s string, cw int, bg color.Color) string {
	if lipgloss.Width(s) > cw {
		s = lipgloss.NewStyle().MaxWidth(cw).Render(s)
	}
	if d := cw - lipgloss.Width(s); d > 0 {
		s += sidebarStyle(bg, nil).Render(strings.Repeat(" ", d))
	}
	return s
}

func chromeGlyph(r rune) bool {
	return config.IsAgentMarkRune(r, overlay.UseASCII())
}

func printableTitle(s string) string {
	return strings.TrimSpace(printableRunes(s))
}

func printableRunes(s string) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if printableRune(r, ascii) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func printableRune(r rune, ascii bool) bool {
	switch {
	case r == utf8.RuneError:
		return false
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return false
	case session.InvisibleFormatRune(r):
		return false
	case r >= 0xe000 && r <= 0xf8ff, r >= 0xf0000:
		return overlay.UseNerdFonts()
	case r >= 0x25a0 && r <= 0x2bff && !chromeGlyph(r):
		return false
	case r >= 0xfe00 && r <= 0xfe0f:
		return false
	case r >= 0x1f000 && r <= 0x1faff && !chromeGlyph(r):
		return false
	case ascii && r > 0x7e:
		return false
	}
	return true
}

func sidebarNameCol() int { return 1 + agentMarkLead() }

func sidebarGlyph(state string, doneSeen bool, running int, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	if !s.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	shown := displayState(state, doneSeen, running)
	g := agentStateIndicator(shown)
	if g == "" {
		return sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarStyle(bg, agentGlyphColor(shown, pal)).Render(g)
}

func sidebarGlyphState(state string, doneSeen bool) string {
	if state == "done" && doneSeen {
		return "idle"
	}
	return state
}

func sidebarQuietDot(bg color.Color, pal overlay.Palette, s *config.Settings) string {
	if !s.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarQuietDotTinted(pal.FgMute, bg, pal, s)
}

func dotTint(tint color.Color, pal overlay.Palette, stated bool) color.Color {
	if tint == nil || stated {
		return pal.FgMute
	}
	return tint
}

func sidebarQuietDotTinted(tint, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	if !s.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarStyle(bg, tint).Render(s.GetRailBullet())
}

func sidebarEdgeRule(s *config.Settings, rule color.Color) string {
	return lipgloss.NewStyle().Foreground(rule).Render(s.GetWindowBorderLeft())
}

const sidebarHeaderGap = 2

func sidebarHeaderRightRoom(cw, labelW int) int {
	return max(cw-labelW-sidebarHeaderGap-1, 0)
}

func sidebarHeaderRowRuled(label, right string, cw int, pal overlay.Palette, s *config.Settings) string {
	headings := s.GetRailHeaderCase() == config.RailHeaderUppercase
	style := s.GetRailHeaderStyle()
	section := label
	labelStyle := sidebarStyle(nil, pal.FgMute)
	if headings {
		labelStyle = sidebarStyle(nil, pal.FgDim).Bold(true)
		label = strings.ToUpper(label)
	}
	ruled := headings
	switch style {
	case config.RailHeaderPlain, config.RailHeaderPill, config.RailHeaderIcon:
		ruled = false
	case config.RailHeaderRule:
		ruled = true
	}
	decorW := sidebarHeaderDecorW(section, s)
	text := overlay.Truncate(label, max(cw-2-decorW, 1))
	shown := labelStyle.Render(text)
	switch style {
	case config.RailHeaderPill:
		ground := pal.FgMute
		caps := sidebarStyle(nil, ground)
		fill := lipgloss.NewStyle().Background(ground).Foreground(overlay.ContrastText(ground)).Bold(headings)
		shown = caps.Render(s.GetWindowPillLeft()) + fill.Render(text) + caps.Render(s.GetWindowPillRight())
	case config.RailHeaderIcon:
		if icon := s.GetRailSectionIcon(section); icon != "" {
			shown = sidebarStyle(nil, pal.AccentBright).Render(icon) + sidebarStyle(nil, nil).Render(" ") + shown
		}
	}
	headings = ruled
	row := sidebarStyle(nil, nil).Render(" ") + shown
	lw := lipgloss.Width(row)
	rw := lipgloss.Width(right)
	if rw > 0 {
		if room := sidebarHeaderRightRoom(cw, lw); rw > room {
			right = sidebarHeaderCutFront(right, rw, room)
			rw = lipgloss.Width(right)
		}
	}
	if rw == 0 {
		if headings {
			if run := cw - lw - 1; run > 1 {
				row += sidebarStyle(nil, nil).Render(" ") +
					sidebarStyle(nil, sidebarRuleInk(nil, pal)).Render(strings.Repeat(s.GetRailRuleGlyph(), run-1))
			}
		}
		return sidebarFit(row, cw, nil)
	}
	gap := cw - lw - rw - 1
	if headings && gap > sidebarHeaderGap {
		row += sidebarStyle(nil, nil).Render(" ") +
			sidebarStyle(nil, sidebarRuleInk(nil, pal)).Render(strings.Repeat(s.GetRailRuleGlyph(), gap-2)) +
			sidebarStyle(nil, nil).Render(" ")
	} else {
		row += strings.Repeat(" ", gap)
	}
	row += right + " "
	return sidebarFit(row, cw, nil)
}

func sidebarHeaderCutFront(right string, rw, room int) string {
	ell := overlay.Ellipsis()
	ew := lipgloss.Width(ell)
	if room <= ew {
		return ""
	}
	return ansi.TruncateLeft(right, rw-room+ew, ell)
}

func sidebarHeaderLabelW(label string, s *config.Settings) int {
	return 1 + lipgloss.Width(label) + sidebarHeaderDecorW(label, s)
}

func sidebarHeaderDecorW(section string, s *config.Settings) int {
	switch s.GetRailHeaderStyle() {
	case config.RailHeaderPill:
		return lipgloss.Width(s.GetWindowPillLeft()) + lipgloss.Width(s.GetWindowPillRight())
	case config.RailHeaderIcon:
		if icon := s.GetRailSectionIcon(section); icon != "" {
			return lipgloss.Width(icon) + 1
		}
	}
	return 0
}

type sidebarTokenSpan struct {
	Kind     sidebarRowKind
	X0, X1   int
	WindowID string
}

const sidebarCountTokenID = "count"

type sidebarAgentCountInfo struct {
	Blocked, Done int
	Worst         string
}

func sidebarAgentCounts(agents []sidebarAgentEntry) sidebarAgentCountInfo {
	var c sidebarAgentCountInfo
	rank := 0
	for _, e := range agents {
		b, d := sidebarAttentionCounts(e.State, e.DoneSeen)
		if b {
			c.Blocked++
			if r := sessiontree.AgentRank(e.State, e.DoneSeen); r > rank {
				c.Worst, rank = e.State, r
			}
		}
		if d {
			c.Done++
		}
	}
	return c
}

func (c sidebarAgentCountInfo) words() string {
	var parts []string
	if c.Blocked > 0 {
		parts = append(parts, strconv.Itoa(c.Blocked)+" "+agentNeedsYou(c.Blocked))
	}
	if c.Done > 0 {
		parts = append(parts, strconv.Itoa(c.Done)+" done")
	}
	return strings.Join(parts, sidebarAgentSep())
}

const (
	countWords = iota
	countGlyphs
	countBlocked
)

func (c sidebarAgentCountInfo) glyphs(blockedOnly bool) string {
	var parts []string
	if c.Blocked > 0 {
		parts = append(parts, strconv.Itoa(c.Blocked)+agentStateIndicator(c.Worst))
	}
	if c.Done > 0 && !blockedOnly {
		parts = append(parts, strconv.Itoa(c.Done)+agentStateIndicator("done"))
	}
	return strings.Join(parts, " ")
}

func (c sidebarAgentCountInfo) text(form int) string {
	switch form {
	case countWords:
		return c.words()
	case countGlyphs:
		return c.glyphs(false)
	default:
		return c.glyphs(true)
	}
}

func (c sidebarAgentCountInfo) render(form int, hover bool, pal overlay.Palette) string {
	if hover {
		return sidebarStyle(nil, pal.Fg).Render(c.text(form))
	}
	if form == countWords {
		return sidebarStyle(nil, pal.FgMute).Render(c.words())
	}
	var parts []string
	if c.Blocked > 0 {
		parts = append(parts, sidebarStyle(nil, sidebarSeverityColor(c.Worst, pal)).
			Render(strconv.Itoa(c.Blocked)+agentStateIndicator(c.Worst)))
	}
	if c.Done > 0 && form == countGlyphs {
		parts = append(parts, sidebarStyle(nil, pal.Success).Render(strconv.Itoa(c.Done)+agentStateIndicator("done")))
	}
	return strings.Join(parts, " ")
}

func sidebarAttentionCounts(state string, doneSeen bool) (blocked, done bool) {
	return sidebarAttention(state), state == "done" && !doneSeen
}

func (m *OS) sidebarAgentsControls(cw, headerW int, pal overlay.Palette, hoverX int, count sidebarAgentCountInfo) (string, []sidebarTokenSpan) {
	filter, sort := "all", "you"
	filterOn, sortOn := false, false
	if m.sidebarAgentsFilter() == sidebarAgentsSession {
		filter, filterOn = "here", true
	}
	switch m.sidebarAgentsSort() {
	case sidebarAgentsPriority:
		sort, sortOn = "pri", true
	case sidebarAgentsRecent:
		sort, sortOn = "rec", true
	}
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	mail, mailOn := sidebarMailToken(m.AgentMailUnread())

	fw, sw := lipgloss.Width(filter), lipgloss.Width(sort)
	sepW := lipgloss.Width(sep)
	room := sidebarHeaderRightRoom(cw, headerW)
	fits := func(countText, mailText string) bool {
		total := fw + sepW + sw
		if mailText != "" {
			total += sepW + lipgloss.Width(mailText)
		}
		if countText != "" {
			total += lipgloss.Width(countText) + sepW
		}
		return total <= room
	}
	if !fits("", mail) {
		mail = ""
	}
	if !fits("", "") {
		return "", nil
	}
	countText, form := "", countWords
	if count.words() != "" {
		for form = countWords; form <= countBlocked; form++ {
			if text := count.text(form); text != "" && fits(text, mail) {
				countText = text
				break
			}
		}
	}
	mw, kw := lipgloss.Width(mail), lipgloss.Width(countText)
	total := fw + sepW + sw
	if mw > 0 {
		total += sepW + mw
	}
	if kw > 0 {
		total += kw + sepW
	}
	x0 := cw - 1 - total
	var spans []sidebarTokenSpan
	x := x0
	if countText != "" {
		spans = append(spans, sidebarTokenSpan{Kind: sidebarRowAgentFilter, X0: x, X1: x + kw, WindowID: sidebarCountTokenID})
		x += kw + sepW
	}
	filterSpan := sidebarTokenSpan{Kind: sidebarRowAgentFilter, X0: x, X1: x + fw}
	sortSpan := sidebarTokenSpan{Kind: sidebarRowAgentSort, X0: x + fw + sepW, X1: x + fw + sepW + sw}
	spans = append(spans, filterSpan, sortSpan)
	var mailSpan sidebarTokenSpan
	if mail != "" {
		mailSpan = sidebarTokenSpan{Kind: sidebarRowAgentMail, X0: x0 + total - mw, X1: x0 + total}
		spans = append(spans, mailSpan)
	}
	ink := func(on bool, s sidebarTokenSpan) color.Color {
		if on || (hoverX >= s.X0 && hoverX < s.X1) {
			return pal.Fg
		}
		return pal.FgMute
	}
	out := ""
	if countText != "" {
		hover := hoverX >= spans[0].X0 && hoverX < spans[0].X1
		out = count.render(form, hover, pal) + sidebarStyle(nil, pal.FgMute).Render(sep)
	}
	out += sidebarStyle(nil, ink(filterOn, filterSpan)).Render(filter) +
		sidebarStyle(nil, pal.FgMute).Render(sep) +
		sidebarStyle(nil, ink(sortOn, sortSpan)).Render(sort)
	if mail != "" {
		mailInk := ink(mailOn, mailSpan)
		if mailOn {
			mailInk = pal.AccentBright
		}
		out += sidebarStyle(nil, pal.FgMute).Render(sep) + sidebarStyle(nil, mailInk).Render(mail)
	}
	return out, spans
}

func sidebarMailGlyph() string {
	return "@"
}

func sidebarMailToken(unread int) (string, bool) {
	if unread <= 0 {
		return sidebarMailGlyph(), false
	}
	return sidebarMailGlyph() + " " + strconv.Itoa(unread), true
}

func sidebarComposeRow(gutter, glyph, name, right string, cw int, bg color.Color) string {
	return sidebarComposeGroupRow(0, gutter, glyph, name, right, cw, bg)
}

func sidebarComposeGroupRow(indent int, gutter, glyph, name, right string, cw int, bg color.Color) string {
	row := gutter + sidebarStyle(bg, nil).Render(strings.Repeat(" ", max(indent, 0))) +
		sidebarMarkCell(glyph, bg) + name
	if rw := lipgloss.Width(right); rw > 0 {
		gap := max(cw-lipgloss.Width(row)-rw-1, 0)
		row += sidebarStyle(bg, nil).Render(strings.Repeat(" ", gap)) +
			right + sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarFit(row, cw, bg)
}

func sidebarMarkCell(glyph string, bg color.Color) string {
	return glyph + sidebarStyle(bg, nil).Render(agentMarkGap(glyph))
}

func sidebarRuleInk(bg color.Color, pal overlay.Palette) color.Color {
	if bg != nil {
		return pal.FgMute
	}
	return pal.Surface
}

func sidebarComposeRuledRow(indent int, gutter, glyph, name, right string, cw int, bg color.Color, pal overlay.Palette, s *config.Settings) string {
	row := gutter + sidebarStyle(bg, nil).Render(strings.Repeat(" ", max(indent, 0))) +
		sidebarMarkCell(glyph, bg) + name
	rw := lipgloss.Width(right)
	pad := 1
	if rw > 0 {
		pad = 2
	}
	run := cw - lipgloss.Width(row) - rw - pad
	if run > 0 {
		row += sidebarStyle(bg, nil).Render(" ") +
			sidebarStyle(bg, sidebarRuleInk(bg, pal)).Render(strings.Repeat(s.GetRailRuleGlyph(), run-1))
	} else if run == 0 {
		row += sidebarStyle(bg, nil).Render(" ")
	}
	if rw > 0 {
		row += sidebarStyle(bg, nil).Render(" ") + right + sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarFit(row, cw, bg)
}

func sidebarNameAvail(cw, rightW int) int { return sidebarNameAvailIn(cw, rightW, 0) }

func sidebarNameAvailIn(cw, rightW, indent int) int {
	if rightW > 0 {
		rightW += 2
	}
	return max(cw-sidebarNameCol()-max(indent, 0)-rightW, 1)
}

func (m *OS) renderSidebar() *lipgloss.Layer {
	panel, w := m.sidebarPanel()
	if docked, ok := m.sidebarSessionPanel(m.legacySidebarEdge(), w); ok {
		panel = docked
		m.SidebarHits = m.SidebarHits[:0]
	}
	if panel == "" {
		return nil
	}
	sidebarX := sidebarEdgeX(m.legacySidebarEdge(), w, m.GetRenderWidth())
	return lipgloss.NewLayer(panel).X(sidebarX).Y(m.viewReserve().Top).Z(config.ZIndexDock).ID(sidebarLayerID)
}

func (m *OS) renderSecondarySidebar() *lipgloss.Layer {
	w := m.secondarySidebarWidth()
	if w == 0 || m.UserConfig == nil {
		m.secondarySidebarHits = m.secondarySidebarHits[:0]
		return nil
	}
	edge := sidebarLeft
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		edge, cfg = sidebarRight, m.UserConfig.Appearance.Sidebar.Right
	}
	if cfg == nil {
		return nil
	}
	sections := cfg.Sections
	if sections == "" {
		sections = config.SidebarDefaultSections
	}
	primarySections, primaryCache := m.Settings.SidebarSections, m.sidebarCache
	primarySubagents := m.Settings.SidebarSubagents
	m.swapSecondaryRail()
	m.Settings.SidebarSections = sections
	m.Settings.SidebarSubagents = config.ResolveSidebarSubagentView(m.UserConfig.Appearance.Sidebar, cfg)
	m.sidebarCache = m.secondarySidebarCache
	m.sidebarDrawing = true
	m.sidebarDrawingEdge = edge
	m.sidebarDrawingWidth = w
	panel, _ := m.sidebarPanel()
	if docked, ok := m.sidebarSessionPanel(edge, w); ok {
		panel = docked
		m.SidebarHits = m.SidebarHits[:0]
	}
	m.secondarySidebarCache = m.sidebarCache
	m.sidebarCache = primaryCache
	m.Settings.SidebarSections = primarySections
	m.Settings.SidebarSubagents = primarySubagents
	m.sidebarDrawing = false
	m.swapSecondaryRail()
	m.secondarySidebarHits = append(m.secondarySidebarHits[:0], m.secondaryRailView.hits...)
	m.SidebarHits = append(m.SidebarHits, m.secondarySidebarHits...)
	if panel == "" {
		return nil
	}
	return lipgloss.NewLayer(panel).X(sidebarEdgeX(edge, w, m.GetRenderWidth())).Y(m.viewReserve().Top).Z(config.ZIndexDock).ID("sidebar-secondary")
}

func sidebarWindowSection(scroll, rows, lines int) (start, shown, hidden int) {
	if lines <= 0 || rows <= 0 {
		return 0, 0, 0
	}
	if rows <= lines {
		return 0, rows, 0
	}
	maxScroll := rows - lines
	start = max(min(scroll, maxScroll), 0)
	if start == maxScroll {
		return start, lines, 0
	}
	return start, lines - 1, rows - start - (lines - 1)
}

func (m *OS) sidebarPanelLinesForTree(tree sessiontree.Tree) ([]string, int) {
	m.SidebarHits = m.SidebarHits[:0]
	m.motion.rail = m.motion.rail[:0]
	m.SidebarSessionIDs = m.SidebarSessionIDs[:0]
	m.refreshSessionColorsFor(localSessionNodes(tree.Sessions))

	m.sidebarMarqueeSeen = false
	defer func() {
		if !m.sidebarMarqueeSeen {
			m.SidebarMarqueeKey = ""
		}
	}()

	w := m.GetSidebarWidth()
	if w <= 0 {
		return nil, 0
	}
	height := m.ViewUsableHeight()
	if height <= 0 {
		return nil, 0
	}

	topMargin := m.viewReserve().Top
	side := m.legacySidebarEdge()
	if m.sidebarDrawing {
		side = m.sidebarDrawingEdge
	}
	sidebarX := sidebarEdgeX(side, w, m.GetRenderWidth())
	edgeLeft := side == sidebarLeft
	contentX0 := sidebarX
	if !edgeLeft {
		contentX0++
	}

	pal := m.groundUI()
	variant := sidebarVariant(w)
	cw := w - 1
	edge := sidebarEdgeRule(&m.Settings, m.railRule())
	if m.sidebarRailFocused() || !m.sidebarDrawing && m.RailPaneFocused() {
		edge = lipgloss.NewStyle().Foreground(pal.Accent).Render(m.Settings.GetWindowBorderLeft())
	}

	lines := make([]string, 0, height)
	var barCell []string
	compose := func(content string) string {
		rule := edge
		if y := len(lines); y < len(barCell) && barCell[y] != "" {
			rule = barCell[y]
		}
		if edgeLeft {
			return content + rule
		}
		return rule + content
	}
	blank := compose(strings.Repeat(" ", cw))

	sessions := tree.Sessions
	if m.SidebarDrag.Dragging {
		sessions = orderByKey(sessions, func(n sessiontree.Node) string { return n.ID }, m.SidebarDrag.Order)
	}
	for _, s := range sessions {
		if isRemoteNode(s) {
			continue
		}
		m.SidebarSessionIDs = append(m.SidebarSessionIDs, s.ID)
	}

	if variant == sidebarVariantGlyph {
		return m.sidebarStripLines(localSessionNodes(sessions), w, cw, height, topMargin, sidebarX, pal, edgeLeft)
	}

	cursorTarget, haveCursorTarget := m.sidebarCursorTarget()
	isCursor := func(kind sidebarRowKind, sessionID, windowID string) bool {
		return m.sidebarRailFocused() && haveCursorTarget &&
			cursorTarget.Kind == kind && cursorTarget.SessionID == sessionID && cursorTarget.WindowID == windowID
	}

	sessionRows := m.sidebarSessionRows(sessions)

	shown, peeking := m.sidebarShownSession(sessions)
	terminals := m.sidebarTerminals(sessions, shown)
	agents, agentsTotal := m.sidebarFilterAgents(m.sidebarAgents(sessions))
	m.sidebarSortAgents(agents)
	agents = m.sidebarKinHeads(sidebarNestAgents(m.sidebarFoldAgents(agents, time.Now())))
	m.sidebarReanchorAgents(agents)
	emptyFilter := len(agents) == 0 && agentsTotal > 0
	files := m.sidebarFileRows()

	emptyPeek := peeking && len(terminals) == 0
	nS := len(sessionRows)
	nT := len(terminals)
	nA := len(agents)
	if emptyFilter {
		nA = 1
	}
	if emptyPeek {
		nT = 1
	}
	plans := m.sidebarPlans()
	peekRows := nT
	if peeking {
		resting := m.sidebarTerminals(sessions, m.sidebarCurrentSessionID())
		if len(resting) > 0 {
			nT = len(resting)
		}
	}
	if !sidebarLayoutHas(sidebarSectionTerminals, &m.Settings) {
		nT = 0
	}
	if height < 8 {
		nA = 0
	}

	canCreate := m.SidebarCanCreateSession()
	footerCursor := func(kind sidebarRowKind) bool { return isCursor(kind, "", "") }
	footerLines, footerZones := m.sidebarFooter(variant, cw, pal, -1, -1, footerCursor)
	footerH := len(footerLines)
	if footerH >= height {
		footerLines, footerZones, footerH = nil, nil, 0
	}

	gitRows := m.gitRows()
	filesRows := len(files)
	if filesRows == 0 && m.filesSectionEnabled() {
		filesRows = 1
	}
	customRows := m.railCustomRows()
	nC := len(customRows)
	if nC == 0 && m.railCustomEnabled() {
		nC = 1
	}
	paneWin := m.railPaneWindow()
	nP := 0
	if m.railPaneEnabled() && cw >= config.SidebarPaneMinCols {
		switch {
		case paneWin == nil || paneWin.Rail != m.railPaneConfig().CommandLine():
			nP = 1
		case m.railPaneConfig().Height > 0:
			nP = m.railPaneConfig().Height
		default:
			nP = height
		}
	}
	rowsIn := [sidebarSectionCount]int{
		sidebarSectionSessions:  nS,
		sidebarSectionTerminals: nT,
		sidebarSectionAgents:    nA,
		sidebarSectionFiles:     filesRows,
		sidebarSectionGit:       len(gitRows),
		sidebarSectionCustom:    nC,
		sidebarSectionPane:      nP,
	}
	pinned := sidebarSectionCount
	if sidebarLayoutPins(plans) {
		pinned = plans[len(plans)-1].Section
	}
	plans = m.sidebarApplySplit(plans, pinned)

	agentRowH := 1
	rowH := [sidebarSectionCount]int{1, 1, agentRowH, 1, 1, 1, 1}

	planRows := make([]int, len(plans))
	planRowH := make([]int, len(plans))
	chrome := 0
	drawn := 0
	for i, p := range plans {
		if p.Spacer {
			planRows[i], planRowH[i] = 1, 1
			continue
		}
		planRows[i], planRowH[i] = rowsIn[p.Section], rowH[p.Section]
		if planRows[i] == 0 {
			continue
		}
		drawn++
		chrome++
		if p.Section == pinned {
			chrome++
		} else if m.Settings.RailRoomy && drawn > 1 {
			chrome++
		}
	}
	avail := height - footerH - chrome
	parents := agents
	childCut := false
	if nA > 0 && !emptyFilter && sidebarAgentsHaveChildren(agents) {
		for i, p := range plans {
			if p.Spacer || p.Section != sidebarSectionAgents {
				continue
			}
			demand := slices.Clone(planRows)
			demand[i] = nA + m.sidebarSubagentDemand(agents)
			room := sidebarBudgetLines(avail, plans, demand, planRowH)[i]
			plan := m.sidebarAgentChildren(agents, room)
			agents, childCut = plan.rows, plan.cut
			nA = len(agents)
			rowsIn[sidebarSectionAgents] = nA
			planRows[i] = nA
		}
	}
	budget := sidebarBudgetLines(avail, plans, planRows, planRowH)
	if peeking && !sidebarLayoutStacksAbove(plans, sidebarSectionTerminals, sidebarSectionSessions) {
		if at := slices.IndexFunc(plans, func(p sidebarSectionPlan) bool {
			return !p.Spacer && p.Section == sidebarSectionTerminals
		}); at >= 0 {
			others := 0
			for i, p := range plans {
				if !p.Spacer && p.Section != sidebarSectionTerminals {
					others += budget[i]
				}
			}
			nT = min(max(peekRows, nT), max(avail-others, budget[at]))
			planRows[at] = nT
			budget = sidebarBudgetLines(avail, plans, planRows, planRowH)
		}
	}
	shortRowH := make([]int, len(planRowH))
	copy(shortRowH, planRowH)
	var tallRowH []int
	if nA > 0 && !emptyFilter && !childCut && m.sidebarAgentsHaveNotes(agents, variant) {
		tall := make([]int, len(plans))
		copy(tall, planRowH)
		at := -1
		for i, p := range plans {
			if !p.Spacer && p.Section == sidebarSectionAgents {
				tall[i], at = sidebarAgentRowTall, i
			}
		}
		if at >= 0 {
			tallRowH = tall
			if grown := sidebarBudgetLines(avail, plans, planRows, tall); grown[at] >= nA*sidebarAgentRowTall {
				budget = grown
				rowH[sidebarSectionAgents] = sidebarAgentRowTall
			}
		}
	}
	used := 0
	for _, n := range budget {
		used += n
	}
	slack := max(avail-used, 0)
	hasDivider := pinned != sidebarSectionCount && rowsIn[pinned] > 0 && drawn > 1
	pinnedAt := -1
	for i, p := range plans {
		if !p.Spacer && p.Section == pinned {
			pinnedAt = i
		}
	}
	m.sidebarSplitGeom = sidebarSplitGeom{
		Plans: plans, Rows: planRows, RowH: shortRowH, TallH: tallRowH,
		Pinned: pinnedAt, Agents: nA, Avail: avail, Bottom: height - footerH, Valid: hasDivider,
	}

	type sectionPlace struct{ header, top, lines, y0, y1 int }
	var place [sidebarSectionCount]sectionPlace
	for s := range place {
		place[s] = sectionPlace{header: -1}
	}
	line := 0
	placed := 0
	prev := sidebarSectionCount
	for i, p := range plans {
		if p.Spacer {
			line += budget[i]
			if prev != sidebarSectionCount {
				above := place[prev]
				above.y1 = max(above.y1, line)
				place[prev] = above
			}
			continue
		}
		if planRows[i] == 0 {
			place[p.Section] = sectionPlace{header: -1, top: line, y0: line, y1: line}
			continue
		}
		y0 := line
		placed++
		if p.Section == pinned {
			line += slack
			y0 = line
			line++
		} else if m.Settings.RailRoomy && placed > 1 {
			line++
		}
		place[p.Section] = sectionPlace{header: line, top: line + 1, lines: budget[i], y0: y0, y1: line + 1 + budget[i]}
		line += 1 + budget[i]
		prev = p.Section
	}
	if drawn > 1 && pinned != sidebarSectionCount {
		for i, plan := range slices.Backward(plans) {
			if plan.Spacer || planRows[i] == 0 || plan.Section == pinned {
				continue
			}
			above := place[plan.Section]
			above.y1 = max(above.y1, place[pinned].y0)
			place[plan.Section] = above
			break
		}
	}
	for s := range m.sidebarSectionY {
		y0 := min(place[s].y0, height)
		y1 := min(place[s].y1, height)
		m.sidebarSectionY[s] = [2]int{topMargin + y0, topMargin + max(y1, y0)}
	}

	scroll := m.sidebarScrollOffsets()
	if peeking && rowsIn[sidebarSectionTerminals] > 0 {
		rowsIn[sidebarSectionTerminals] = peekRows
		peekScroll := 0
		scroll[sidebarSectionTerminals] = &peekScroll
	}
	var capRows [sidebarSectionCount]int
	for s := range capRows {
		capRows[s] = place[s].lines / rowH[s]
	}
	m.sidebarRevealFocus(sessions, sessionRows, terminals, capRows[sidebarSectionTerminals], capRows[sidebarSectionSessions])
	if m.sidebarRailFocused() && haveCursorTarget {
		if sec, idx, ok := m.sidebarCursorIndex(cursorTarget, sessionRows, terminals, agents, files); ok {
			if rows := capRows[sec]; rows > 0 {
				fold := rows
				if rows > 1 && rowsIn[sec] > rows && *scroll[sec] < rowsIn[sec]-rows {
					fold = rows - 1
				}
				if idx < *scroll[sec] {
					*scroll[sec] = idx
				} else if idx >= *scroll[sec]+fold {
					*scroll[sec] = idx - rows + 2
					if idx == rowsIn[sec]-1 || rows == 1 {
						*scroll[sec] = idx - rows + 1
					}
				}
			}
		}
	}
	rowsIn[sidebarSectionPane] = min(rowsIn[sidebarSectionPane], capRows[sidebarSectionPane])
	if !m.sidebarDrawing && m.railPaneEnabled() {
		m.railPaneView.cols, m.railPaneView.rows, m.railPaneView.rect = 0, 0, image.Rectangle{}
		if p := place[sidebarSectionPane]; p.header >= 0 && p.lines > 0 && paneWin != nil && paneWin.Rail == m.railPaneConfig().CommandLine() {
			lines := min(p.lines, height-p.top)
			if lines > 0 {
				y0 := topMargin + p.top
				m.railPaneView.cols, m.railPaneView.rows = cw, lines
				m.railPaneView.rect = image.Rect(contentX0, y0, contentX0+cw, y0+lines)
			}
		}
		m.railPaneApply()
	}
	var start, count, hidden [sidebarSectionCount]int
	for s := range rowsIn {
		start[s], count[s], hidden[s] = sidebarWindowSection(*scroll[s], rowsIn[s], capRows[s])
		*scroll[s] = start[s]
	}
	m.sidebarRecordAgentAnchor(agents, start[sidebarSectionAgents], count[sidebarSectionAgents])
	m.sidebarRecordReveal()
	focusDrawn := false
	if !peeking && !emptyPeek {
		for i := range count[sidebarSectionTerminals] {
			if terminals[start[sidebarSectionTerminals]+i].Focused {
				focusDrawn = true
				break
			}
		}
	}

	var thumb string
	for s := range rowsIn {
		top, size, ok := sidebarScrollThumb(start[s], rowsIn[s], capRows[s])
		if !ok {
			continue
		}
		if barCell == nil {
			barCell = make([]string, height)
			glyph := m.Settings.GetRailScrollThumbBeside(m.Settings.GetWindowBorderLeft())
			thumb = sidebarStyle(nil, pal.ScrollThumb).Render(glyph)
		}
		for i := top; i < top+size && place[s].top+i < height; i++ {
			barCell[place[s].top+i] = thumb
		}
	}

	var hoverRow [sidebarSectionCount]int
	for s := range hoverRow {
		hoverRow[s] = -1
	}
	footerHoverLine, footerHoverX := -1, -1
	dividerHover := false
	var headerHoverX [sidebarSectionCount]int
	for s := range headerHoverX {
		headerHoverX[s] = -1
	}
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		delta := m.SidebarHoverY - topMargin
		footerTop := height - footerH
		onHeader := -1
		for s := range place {
			if place[s].header >= 0 && delta == place[s].header {
				onHeader = s
			}
		}
		switch {
		case footerH > 0 && delta >= footerTop && delta < height:
			footerHoverLine, footerHoverX = delta-footerTop, m.SidebarHoverX-contentX0
		case onHeader >= 0:
			headerHoverX[onHeader] = m.SidebarHoverX - contentX0
		case hasDivider && delta == place[pinned].y0:
			dividerHover = true
		default:
			for s := range place {
				if place[s].header < 0 {
					continue
				}
				if d := delta - place[s].top; d >= 0 && d < count[s]*rowH[s] {
					hoverRow[s] = start[s] + d/rowH[s]
				}
			}
		}
	}
	if footerH > 0 {
		footerLines, footerZones = m.sidebarFooter(variant, cw, pal, footerHoverLine, footerHoverX, footerCursor)
	}

	nav := make([]sidebarNavRow, 0, nS+nT+nA+len(files)+2)
	recordHit := func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + max(h, 1),
			Kind:        kind,
			SessionID:   sessionID,
			WindowID:    windowID,
			WindowIndex: windowIndex,
		})
		nav = append(nav, sidebarNavRow{Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: windowIndex})
	}
	recordClick := func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + max(h, 1),
			Kind:        kind,
			SessionID:   sessionID,
			WindowID:    windowID,
			WindowIndex: windowIndex,
		})
	}
	overflowRow := func(n, indent int) string {
		more := overlay.Ellipsis() + " +" + strconv.Itoa(n)
		return compose(sidebarFit(strings.Repeat(" ", sidebarNameCol()+indent)+
			sidebarStyle(nil, pal.FgMute).Render(more), cw, nil))
	}
	recordToken := func(tk sidebarTokenSpan, sessionID string) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: contentX0 + tk.X0, X1: contentX0 + tk.X1,
			Y0: y, Y1: y + 1,
			Kind:        tk.Kind,
			SessionID:   sessionID,
			WindowID:    tk.WindowID,
			WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: tk.Kind, SessionID: sessionID, WindowID: tk.WindowID, WindowIndex: -1})
	}

	drawSessions := func() {
		add := ""
		const label = "sessions"
		if canCreate && len(m.SidebarHostIDs) == 0 {
			if tok, span, ok := sidebarHeaderAdd(sidebarRowNewSession, cw, sidebarHeaderLabelW(label, &m.Settings),
				pal, headerHoverX[sidebarSectionSessions], isCursor(sidebarRowNewSession, "", ""), &m.Settings, nil); ok {
				add = tok
				recordToken(span, "")
			}
		}
		lines = append(lines, compose(sidebarHeaderRowRuled(label, add, cw, pal, &m.Settings)))
		showCounts := false
		for _, s := range sessionRows {
			if s.WindowCount > 1 {
				showCounts = true
				break
			}
		}
		sessionIdx := m.localSessionIndexes(sessions)
		numberedHead := false
		for i := range count[sidebarSectionSessions] {
			idx := start[sidebarSectionSessions] + i
			s := sessionRows[idx]
			if s.Kind == sessiontree.KindSpace {
				lines = append(lines, compose(m.sidebarSpaceRow(s, cw, pal)))
				continue
			}
			if s.Kind == sessiontree.KindRepo {
				numberedHead = false
				st := m.railRowState(idx == hoverRow[sidebarSectionSessions], isCursor(sidebarRowRepo, s.ID, s.Space))
				recordHit(sidebarRowRepo, s.ID, s.Space, -1, 1)
				lines = append(lines, compose(st.mark(pal, m.sidebarRepoRow(s, cw, pal, st))))
				continue
			}
			if isRemoteNode(s) {
				m.drawHostRow(s, cw, variant, pal, m.railRowState(idx == hoverRow[sidebarSectionSessions], false), canCreate, showCounts,
					isCursor, recordHit, recordToken, headerHoverX[sidebarSectionSessions], compose, &lines)
				continue
			}
			dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
			st := m.railRowState(idx == hoverRow[sidebarSectionSessions], isCursor(sidebarRowSession, s.ID, ""))
			if s.GroupHead {
				numberedHead = m.Settings.SidebarShowNumbers && sessionIdx[s.ID] > 0 && sessionIdx[s.ID] <= 9
				st.FoldCursor = isCursor(sidebarRowRepo, s.Worktree.Repo, s.Space)
				recordToken(sidebarTokenSpan{Kind: sidebarRowRepo, X0: cw - sidebarFoldCells, X1: cw, WindowID: s.Space}, s.Worktree.Repo)
			}
			recordHit(sidebarRowSession, s.ID, "", -1, 1)
			lines = append(lines, compose(st.mark(pal, m.sidebarSessionRow(s, sessionIdx[s.ID], variant, cw, pal, st, dragged, showCounts, numberedHead))))
		}
		if h := hidden[sidebarSectionSessions]; h > 0 {
			lines = append(lines, overflowRow(h, m.sidebarRowIndent()))
		}
	}

	drawTerminals := func() {
		termAdd, termSpan, hasTermAdd := sidebarHeaderAdd(sidebarRowNewWindow, cw,
			sidebarHeaderLabelW("terminals", &m.Settings), pal, headerHoverX[sidebarSectionTerminals],
			isCursor(sidebarRowNewWindow, shown, ""), &m.Settings, nil)
		right := termAdd
		if peeking {
			ink := pal.Fg
			if tint := m.sessionTint(shown, m.terminalBg()); tint != nil {
				ink = tint
			}
			room := sidebarHeaderRightRoom(cw, sidebarHeaderLabelW("terminals", &m.Settings))
			if hasTermAdd {
				room -= lipgloss.Width(sidebarAddGlyph(&m.Settings)) + 1
			}
			if room >= 3 {
				name := sidebarStyle(nil, ink).Render(overlay.Truncate(printableTitle(shown), room))
				right = name
				if hasTermAdd {
					right += sidebarStyle(nil, nil).Render(" ") + termAdd
				}
			}
		}
		if hasTermAdd {
			recordToken(termSpan, shown)
		}
		lines = append(lines, compose(sidebarHeaderRowRuled("terminals", right, cw, pal, &m.Settings)))
		end := len(lines) + place[sidebarSectionTerminals].lines
		if peeking {
			defer func() {
				for len(lines) < end {
					lines = append(lines, blank)
				}
			}()
		}
		if emptyPeek {
			if count[sidebarSectionTerminals] > 0 {
				hint := "no terminals"
				lines = append(lines, compose(sidebarFit(
					sidebarStyle(nil, nil).Render(" ")+sidebarQuietDot(nil, pal, &m.Settings)+
						sidebarStyle(nil, nil).Render(" ")+
						sidebarStyle(nil, pal.FgMute).Render(overlay.Truncate(hint, sidebarNameAvail(cw, 0))), cw, nil)))
			}
			return
		}
		for i := range count[sidebarSectionTerminals] {
			idx := start[sidebarSectionTerminals] + i
			e := terminals[idx]
			st := m.railRowState(idx == hoverRow[sidebarSectionTerminals], isCursor(sidebarRowWindow, e.SessionID, e.WindowID))
			recordHit(sidebarRowWindow, e.SessionID, e.WindowID, e.WindowIndex, 1)
			lines = append(lines, compose(st.mark(pal, m.sidebarTerminalRow(e, cw, pal, st, peeking))))
		}
		if h := hidden[sidebarSectionTerminals]; h > 0 {
			lines = append(lines, overflowRow(h, 0))
		}
	}

	drawFiles := func() {
		closeTok, closeSpan, hasClose := m.sidebarFilesHeaderClose(cw, pal, headerHoverX[sidebarSectionFiles],
			isCursor(sidebarRowFileClose, "", ""))
		closeW := 0
		if hasClose {
			closeW = closeSpan.X1 - closeSpan.X0 + 1
		}
		cdTok, cdSpan, hasCd := m.sidebarFilesHeaderCd(cw-closeW, pal, headerHoverX[sidebarSectionFiles],
			isCursor(sidebarRowFileCd, "", ""))
		if hasCd {
			recordToken(cdSpan, "")
		}
		if hasClose {
			recordToken(closeSpan, "")
		}
		lines = append(lines, compose(m.sidebarFilesHeaderRow(cdTok, hasCd, closeTok, hasClose, cw, pal)))
		if len(files) == 0 {
			if count[sidebarSectionFiles] > 0 {
				lines = append(lines, compose(sidebarFilesEmptyRow(cw, pal)))
			}
			return
		}
		for i := range count[sidebarSectionFiles] {
			idx := start[sidebarSectionFiles] + i
			row := files[idx]
			st := m.railRowState(idx == hoverRow[sidebarSectionFiles], false)
			if row.Kind != 0 {
				st.Cursor = st.Cursor || isCursor(row.Kind, "", row.Key)
				recordHit(row.Kind, "", row.Key, row.Index, 1)
			}
			if m.fileRowHeld(row) {
				st.Hover = true
			}
			if row.Tree {
				rs := overlay.RowState{Cursor: st.Cursor, Focused: st.Focused, Hover: st.Hover}
				lines = append(lines, compose(pal.CursorText(pal.Mark(m.sidebarFileRow(row, cw, pal, st), rs), rs, m.fileTreeNameCol(row))))
				continue
			}
			lines = append(lines, compose(st.mark(pal, m.sidebarFileRow(row, cw, pal, st))))
		}
		if h := hidden[sidebarSectionFiles]; h > 0 {
			lines = append(lines, overflowRow(h, 0))
		}
	}

	drawGit := func() {
		lines = append(lines, compose(sidebarHeaderRowRuled("git", "", cw, pal, &m.Settings)))
		for i := range count[sidebarSectionGit] {
			idx := start[sidebarSectionGit] + i
			if idx >= len(gitRows) {
				break
			}
			st := m.railRowState(idx == hoverRow[sidebarSectionGit], false)
			lines = append(lines, compose(st.mark(pal, m.sidebarGitRow(gitRows[idx], cw, pal, st))))
		}
		if h := hidden[sidebarSectionGit]; h > 0 {
			lines = append(lines, overflowRow(h, 0))
		}
	}

	drawCustom := func() {
		lines = append(lines, compose(sidebarHeaderRowRuled(m.railCustomTitle(), "", cw, pal, &m.Settings)))
		if len(customRows) == 0 {
			if count[sidebarSectionCustom] > 0 {
				lines = append(lines, blank)
			}
			return
		}
		for i := range count[sidebarSectionCustom] {
			idx := start[sidebarSectionCustom] + i
			if idx >= len(customRows) {
				break
			}
			links := railRowLinks(customRows[idx])
			first := ""
			if limit := max(cw-1, 0); len(links) > 0 {
				textX, y := contentX0+1, topMargin+len(lines)
				for _, l := range links {
					if l.x0 >= limit {
						continue
					}
					if first == "" {
						first = l.url
					}
					m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
						X0: textX + l.x0, X1: textX + min(l.x1, limit),
						Y0: y, Y1: y + 1,
						Kind: sidebarRowCustomLink, WindowID: l.url, WindowIndex: -1,
					})
				}
				if first != "" {
					recordHit(sidebarRowCustomLink, "", first, -1, 1)
				}
			}
			st := m.railRowState(idx == hoverRow[sidebarSectionCustom], first != "" && isCursor(sidebarRowCustomLink, "", first))
			lines = append(lines, compose(st.mark(pal, m.sidebarCustomRow(customRows[idx], cw, pal, st))))
		}
		if h := hidden[sidebarSectionCustom]; h > 0 {
			lines = append(lines, overflowRow(h, 0))
		}
	}

	drawAgents := func() {
		controls, tokens := m.sidebarAgentsControls(cw, sidebarHeaderLabelW("agents", &m.Settings), pal,
			headerHoverX[sidebarSectionAgents], m.sidebarHeaderCounts(parents))
		for _, tk := range tokens {
			recordToken(tk, "")
		}
		lines = append(lines, compose(sidebarHeaderRowRuled("agents", controls, cw, pal, &m.Settings)))
		if emptyFilter {
			recordHit(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), "", -1, 1)
			lines = append(lines, compose(m.sidebarAgentsEmptyRow(agentsTotal, cw, pal,
				m.railRowState(hoverRow[sidebarSectionAgents] == 0,
					isCursor(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), "")))))
			return
		}
		for i := range count[sidebarSectionAgents] {
			idx := start[sidebarSectionAgents] + i
			e := agents[idx]
			if e.Kin != "" {
				st := m.railRowState(idx == hoverRow[sidebarSectionAgents], isCursor(sidebarRowAgentKin, e.SessionID, e.WindowID))
				recordHit(sidebarRowAgentKin, e.SessionID, e.WindowID, e.WindowIndex, rowH[sidebarSectionAgents])
				lines = append(lines, compose(st.mark(pal, m.sidebarKinRow(e, cw, pal, st, false))))
				if rowH[sidebarSectionAgents] > 1 {
					lines = append(lines, compose(st.mark(pal, m.sidebarKinRow(e, cw, pal, st, true))))
				}
				continue
			}
			if e.Head {
				st := m.railRowState(idx == hoverRow[sidebarSectionAgents], isCursor(sidebarRowSubagentToggle, e.SessionID, e.WindowID))
				recordHit(sidebarRowSubagentToggle, e.SessionID, e.WindowID, e.WindowIndex, rowH[sidebarSectionAgents])
				lines = append(lines, compose(st.mark(pal, m.sidebarChildRow(e, cw, pal, st, false))))
				if rowH[sidebarSectionAgents] > 1 {
					lines = append(lines, compose(st.mark(pal, m.sidebarChildRow(e, cw, pal, st, true))))
				}
				continue
			}
			if e.sidebarSubRow() {
				st := m.railRowState(idx == hoverRow[sidebarSectionAgents], false)
				recordClick(sidebarRowAgent, e.SessionID, e.WindowID, e.WindowIndex, rowH[sidebarSectionAgents])
				lines = append(lines, compose(st.mark(pal, m.sidebarChildRow(e, cw, pal, st, false))))
				if rowH[sidebarSectionAgents] > 1 {
					lines = append(lines, compose(st.mark(pal, m.sidebarChildRow(e, cw, pal, st, true))))
				}
				continue
			}
			if e.Fold > 0 {
				st := m.railRowState(idx == hoverRow[sidebarSectionAgents], isCursor(sidebarRowAgentFold, e.SessionID, ""))
				tall := rowH[sidebarSectionAgents] > 1
				recordHit(sidebarRowAgentFold, e.SessionID, "", -1, rowH[sidebarSectionAgents])
				lines = append(lines, compose(st.mark(pal, m.sidebarAgentFoldRow(e, cw, pal, st, false))))
				if tall {
					lines = append(lines, compose(st.mark(pal, m.sidebarAgentFoldRow(e, cw, pal, st, true))))
				}
				continue
			}
			st := m.railRowState(idx == hoverRow[sidebarSectionAgents], isCursor(sidebarRowAgent, e.SessionID, e.WindowID))
			tall := rowH[sidebarSectionAgents] > 1
			recordHit(sidebarRowAgent, e.SessionID, e.WindowID, e.WindowIndex, rowH[sidebarSectionAgents])
			row, bodyW := m.sidebarAgentRow(e, variant, cw, pal, st, tall, e.Focused && !focusDrawn)
			if e.State == "working" && bodyW > 0 {
				x0 := contentX0 + sidebarNameCol() + e.nestIndent()
				m.motion.rail = append(m.motion.rail, shimmerSpan{
					y: topMargin + len(lines), x0: x0, x1: min(x0+bodyW, contentX0+cw),
				})
			}
			lines = append(lines, compose(st.mark(pal, row)))
			if tall {
				lines = append(lines, compose(st.mark(pal, m.sidebarAgentNoteRow(e, variant, cw, pal, st))))
			}
		}
		if h := hidden[sidebarSectionAgents]; h > 0 {
			lines = append(lines, overflowRow(h, 0))
		}
	}

	drawPane := func() {
		title := m.railPaneConfig().ResolvedTitle()
		lines = append(lines, compose(sidebarHeaderRowRuled(title, "", cw, pal, &m.Settings)))
		n := count[sidebarSectionPane]
		if n <= 0 {
			return
		}
		if w := m.railPaneWindow(); m.railPaneView.rows > 0 && w != nil {
			for _, line := range m.railPaneLines(w, cw, n) {
				lines = append(lines, compose(line))
			}
			return
		}
		note, restart := m.railPaneNote()
		st := m.railRowState(hoverRow[sidebarSectionPane] == 0, false)
		if restart {
			recordHit(sidebarRowRailPaneStart, "", "", -1, 1)
		}
		row := sidebarStyle(nil, pal.FgMute).Render(" " + ansi.Truncate(note, max(cw-1, 1), "…"))
		lines = append(lines, compose(st.mark(pal, sidebarFit(row, cw, nil))))
		for range n - 1 {
			lines = append(lines, blank)
		}
	}

	draw := [sidebarSectionCount]func(){
		sidebarSectionSessions:  drawSessions,
		sidebarSectionTerminals: drawTerminals,
		sidebarSectionAgents:    drawAgents,
		sidebarSectionFiles:     drawFiles,
		sidebarSectionGit:       drawGit,
		sidebarSectionCustom:    drawCustom,
		sidebarSectionPane:      drawPane,
	}
	drawnAt := 0
	for i, p := range plans {
		if p.Spacer {
			for range budget[i] {
				lines = append(lines, blank)
			}
			continue
		}
		if planRows[i] == 0 {
			continue
		}
		drawnAt++
		if p.Section != pinned && m.Settings.RailRoomy && drawnAt > 1 {
			lines = append(lines, blank)
		}
		if p.Section == pinned {
			for range slack {
				lines = append(lines, blank)
			}
			if hasDivider {
				recordHit(sidebarRowDivider, "", "", -1, 1)
				active := dividerHover || m.sidebarSplit.Active || isCursor(sidebarRowDivider, "", "")
				lines = append(lines, compose(m.sidebarDividerRow(cw, pal, active)))
			} else {
				lines = append(lines, blank)
			}
		}
		draw[p.Section]()
	}

	for len(lines) < height-footerH {
		lines = append(lines, blank)
	}

	footerTop := topMargin + len(lines)
	for _, z := range footerZones {
		y := footerTop + z.Line
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: contentX0 + z.X0, X1: contentX0 + z.X1,
			Y0: y, Y1: y + 1,
			Kind:        z.Kind,
			WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: z.Kind, WindowIndex: -1})
	}
	for _, ln := range footerLines {
		lines = append(lines, compose(ln))
	}

	if len(lines) > height {
		lines = lines[:height]
		bottom := topMargin + height
		kept := m.SidebarHits[:0]
		for _, h := range m.SidebarHits {
			if h.Y1 <= bottom {
				kept = append(kept, h)
			}
		}
		m.SidebarHits = kept
	}

	m.sidebarPublishNav(nav, cursorTarget, haveCursorTarget)
	return lines, w
}

func (m *OS) sidebarPublishNav(nav []sidebarNavRow, target sidebarNavRow, haveTarget bool) {
	if m.sidebarCursorWantSet {
		m.sidebarCursorWantSet = false
		if !slices.ContainsFunc(nav, func(r sidebarNavRow) bool { return sidebarNavRowsEqual(r, target) }) &&
			m.SidebarCursor >= 0 && m.SidebarCursor < len(m.SidebarNav) {
			target = m.SidebarNav[m.SidebarCursor]
		}
	}
	m.SidebarNav = nav
	m.sidebarFollowSession = ""
	if m.sidebarFollowFile && !m.filesView.Loading && m.filesView.Gen == m.sidebarFollowFileGen {
		m.sidebarFollowFile = false
		if i, ok := sidebarFileRowIndex(nav, m.sidebarFollowFileName); ok {
			m.SidebarCursor = i
			return
		}
	}
	if haveTarget && isFileListRow(target.Kind) {
		target = sidebarFileFallback(nav, target)
	}
	if haveTarget {
		m.SidebarCursor = 0
		for i, r := range nav {
			if sidebarNavRowsEqual(r, target) {
				m.SidebarCursor = i
				break
			}
		}
	}
	if m.SidebarCursor >= len(nav) {
		m.SidebarCursor = max(len(nav)-1, 0)
	}
}

func sidebarFileFallback(nav []sidebarNavRow, target sidebarNavRow) sidebarNavRow {
	has := func(r sidebarNavRow) bool {
		return slices.ContainsFunc(nav, func(n sidebarNavRow) bool { return sidebarNavRowsEqual(n, r) })
	}
	if has(target) {
		return target
	}
	for key := fileTreeParentKey(target.WindowID); key != ""; key = fileTreeParentKey(key) {
		for _, r := range nav {
			if r.Kind == sidebarRowFileEntry && r.WindowID == key {
				return r
			}
		}
	}
	for _, r := range nav {
		if isFileListRow(r.Kind) {
			return r
		}
	}
	return target
}

func sidebarFileRowIndex(nav []sidebarNavRow, name string) (int, bool) {
	first := -1
	for i, r := range nav {
		if !isFileListRow(r.Kind) {
			continue
		}
		if first < 0 {
			first = i
		}
		if name != "" && r.Kind == sidebarRowFileEntry && r.WindowID == name {
			return i, true
		}
	}
	if first < 0 {
		return 0, false
	}
	return first, true
}

func (m *OS) sidebarShownSession(sessions []sessiontree.Node) (string, bool) {
	attached := m.sidebarCurrentSessionID()
	if m.SidebarPeek == "" || m.SidebarPeek == attached {
		return attached, false
	}
	for _, s := range sessions {
		if s.ID == m.SidebarPeek {
			return s.ID, true
		}
	}
	return attached, false
}

func (m *OS) sidebarTerminals(sessions []sessiontree.Node, sessionID string) []sidebarTerminalEntry {
	var node *sessiontree.Node
	for i := range sessions {
		if sessions[i].ID == sessionID {
			node = &sessions[i]
			break
		}
	}
	if node == nil {
		return nil
	}
	out := make([]sidebarTerminalEntry, 0, len(node.Children))
	for _, win := range node.Children {
		e := sidebarTerminalEntry{
			SessionID:   node.ID,
			WindowID:    win.ID,
			Title:       win.Title,
			State:       win.AgentState,
			DoneSeen:    win.DoneSeen,
			Subagents:   win.Subagents,
			Quiet:       win.Quiet,
			Focused:     win.IsCurrent,
			Host:        win.Host,
			Recording:   win.Recording,
			WindowIndex: -1,
		}
		if win.Host != "" {
			e.HostLink = win.HostLink
		}
		if win.ScreenStopped {
			e.HostLink = sidebarScreenStopped
		}
		if node.IsCurrent {
			e.WindowIndex = m.windowIndexByID(win.ID)
		}
		e.workspace = win.Workspace
		if node.Workspace > 0 && win.Workspace > 0 && win.Workspace != node.Workspace {
			if node.IsCurrent {
				e.Tag = m.workspaceTag(win.Workspace)
			} else {
				e.Tag = "w" + strconv.Itoa(m.workspaceDisplayNumber(win.Workspace))
			}
		}
		out = append(out, e)
	}
	var strip map[string]int
	if node.IsCurrent {
		strip = m.scrollStripRanks()
	}
	stripRank := func(e sidebarTerminalEntry) int {
		if r, ok := strip[e.WindowID]; ok {
			return r
		}
		return len(strip)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if ra, rb := m.workspaceRank(out[a].workspace), m.workspaceRank(out[b].workspace); ra != rb {
			return ra < rb
		}
		return stripRank(out[a]) < stripRank(out[b])
	})
	return out
}

func (m *OS) scrollStripRanks() map[string]int {
	if !m.UseScrollingLayout || !m.AutoTiling {
		return nil
	}
	ranks := make(map[string]int)
	for _, sl := range m.WorkspaceScrollingLayouts {
		if sl == nil {
			continue
		}
		n := 0
		for _, col := range sl.Columns {
			for _, id := range col.WindowIDs {
				if wid, ok := m.BSPIDToWindowID[id]; ok {
					ranks[wid] = n
					n++
				}
			}
		}
	}
	return ranks
}

func (m *OS) sidebarAgents(sessions []sessiontree.Node) []sidebarAgentEntry {
	if !sidebarLayoutHas(sidebarSectionAgents, &m.Settings) {
		return nil
	}
	return m.agentEntries(sessions)
}

func (m *OS) agentEntries(sessions []sessiontree.Node) []sidebarAgentEntry {
	if !m.agentsOn() {
		return nil
	}
	var agents []sidebarAgentEntry
	for _, s := range sessions {
		pr := ""
		if s.Worktree != nil {
			pr = s.Worktree.PR
		}
		for _, win := range s.Children {
			if win.AgentState == "" {
				continue
			}
			idx := -1
			if s.IsCurrent {
				idx = m.windowIndexByID(win.ID)
			}
			agents = append(agents, sidebarAgentEntry{
				SessionID:    s.ID,
				SessionLabel: s.Title,
				WindowID:     win.ID,
				Title:        win.Title,
				State:        win.AgentState,
				DoneSeen:     win.DoneSeen,
				StateAt:      win.StateAt,
				Harness:      win.Harness,
				Message:      win.Message,
				Reason:       win.Reason,
				AgentKind:    win.AgentKind,
				Meta:         win.Meta,
				Queued:       win.Queued,
				DraftHeld:    win.DraftHeld,
				Settled:      win.Settled,
				Quiet:        win.Quiet,
				Cwd:          win.Cwd,
				Subagents:    win.Subagents,
				Children:     win.SubagentList,
				Parent:       win.Parent,
				Program:      win.Program,
				PR:           pr,
				WindowIndex:  idx,
				Foreign:      !s.IsCurrent,
				Host:         s.Host,
				Focused:      s.IsCurrent && win.IsCurrent,
			})
		}
	}
	return agents
}

func (m *OS) sidebarCursorIndex(target sidebarNavRow, sessions []sessiontree.Node,
	terminals []sidebarTerminalEntry, agents []sidebarAgentEntry, files []fileRowSpec,
) (sidebarSection, int, bool) {
	switch target.Kind {
	case sidebarRowSession:
		for i, s := range sessions {
			if s.Kind == sessiontree.KindSession && s.ID == target.SessionID {
				return sidebarSectionSessions, i, true
			}
		}
	case sidebarRowRepo:
		for i, s := range sessions {
			if s.Kind == sessiontree.KindRepo && s.ID == target.SessionID && s.Space == target.WindowID {
				return sidebarSectionSessions, i, true
			}
			if s.GroupHead && s.Worktree.Repo == target.SessionID && s.Space == target.WindowID {
				return sidebarSectionSessions, i, true
			}
		}
	case sidebarRowHost, sidebarRowHostNew, sidebarRowHostSignIn:
		for i, s := range sessions {
			if s.Kind == sessiontree.KindHost && s.Host == target.SessionID {
				return sidebarSectionSessions, i, true
			}
		}
	case sidebarRowHostSession:
		for i, s := range sessions {
			if s.Kind == sessiontree.KindSession && s.Host == target.SessionID &&
				remoteSessionName(s) == target.WindowID {
				return sidebarSectionSessions, i, true
			}
		}
	case sidebarRowWindow:
		for i, e := range terminals {
			if e.WindowID == target.WindowID {
				return sidebarSectionTerminals, i, true
			}
		}
	case sidebarRowAgent:
		for i, e := range agents {
			if e.WindowID == target.WindowID && e.SessionID == target.SessionID {
				return sidebarSectionAgents, i, true
			}
		}
	case sidebarRowAgentFold:
		for i, e := range agents {
			if e.Fold > 0 {
				return sidebarSectionAgents, i, true
			}
		}
	case sidebarRowSubagentToggle:
		for i, e := range agents {
			if e.Head && e.WindowID == target.WindowID && e.SessionID == target.SessionID {
				return sidebarSectionAgents, i, true
			}
		}
	case sidebarRowAgentKin:
		for i, e := range agents {
			if e.Kin != "" && e.WindowID == target.WindowID && e.SessionID == target.SessionID {
				return sidebarSectionAgents, i, true
			}
		}
	case sidebarRowFileUp, sidebarRowFileBack, sidebarRowFileEntry:
		for i, row := range files {
			if !row.Note && row.Kind == target.Kind && row.Key == target.WindowID {
				return sidebarSectionFiles, i, true
			}
		}
	}
	return 0, 0, false
}

type sidebarFooterZone struct {
	Kind   sidebarRowKind
	Line   int
	X0, X1 int
}

func (m *OS) sidebarCollapseGlyph(variant int) (glyph string, ok bool) {
	left, right := m.Settings.GetRailCollapseGlyph(), m.Settings.GetRailExpandGlyph()
	collapse, expand := left, right
	if m.railEdge() == sidebarRight {
		collapse, expand = right, left
	}
	if variant == sidebarVariantGlyph {
		return expand, sidebarVariant(m.sidebarWidthFor(m.sidebarStoredWidth())) > variant
	}
	return collapse, true
}

func (m *OS) sidebarFooter(variant, cw int, pal overlay.Palette,
	hoverLine, hoverX int, isCursor func(sidebarRowKind) bool,
) ([]string, []sidebarFooterZone) {
	stepGlyph, canStep := m.sidebarCollapseGlyph(variant)
	if !canStep {
		return nil, nil
	}

	stepW := lipgloss.Width(stepGlyph)

	type placed struct {
		zone  sidebarFooterZone
		label string
	}
	facing := max(cw-1-stepW, 1)
	if m.railEdge() == sidebarRight {
		facing = 1
	}
	line := 0
	items := []placed{{sidebarFooterZone{Kind: sidebarRowCollapse, Line: line, X0: facing, X1: facing + stepW}, stepGlyph}}

	filesX := 1
	if m.railEdge() == sidebarRight {
		filesX = max(cw-1-sidebarFilesLabelW, 1)
	}
	filesEnd := filesX + sidebarFilesLabelW
	if filesEnd < facing-1 || filesX > facing+stepW {
		items = append(items, placed{
			sidebarFooterZone{Kind: sidebarRowFiles, Line: line, X0: filesX, X1: filesEnd},
			sidebarFilesLabel,
		})
	}

	cells := make([][]string, line+1)
	for i := range cells {
		cells[i] = make([]string, cw)
		for c := range cells[i] {
			cells[i][c] = " "
		}
	}
	sortx.ShortStableFunc(items, func(a, b placed) int { return a.zone.X0 - b.zone.X0 })

	filesOn := m.filesView.Show > 0 && !sidebarLayoutHas(sidebarSectionFiles, &m.Settings)
	zones := make([]sidebarFooterZone, 0, len(items))
	for _, it := range items {
		fg := pal.FgMute
		if (it.zone.Line == hoverLine && hoverX >= it.zone.X0 && hoverX < it.zone.X1) || isCursor(it.zone.Kind) {
			fg = pal.Fg
		}
		style := sidebarStyle(nil, fg)
		if it.zone.Kind == sidebarRowFiles && filesOn {
			style = sidebarStyle(nil, pal.Accent).Underline(true)
		}
		row := cells[it.zone.Line]
		row[it.zone.X0] = style.Render(it.label)
		for c := it.zone.X0 + 1; c < it.zone.X1 && c < cw; c++ {
			row[c] = ""
		}
		zones = append(zones, it.zone)
	}

	lines := make([]string, len(cells))
	for i, row := range cells {
		lines[i] = sidebarFit(strings.Join(row, ""), cw, nil)
	}
	return lines, zones
}

func (m *OS) windowIndexByID(id string) int {
	return slices.IndexFunc(m.Windows, func(w *terminal.Window) bool { return w != nil && w.ID == id })
}

const railNoteMin = 4

func (m *OS) sidebarSessionRow(node sessiontree.Node, sessionIdx, variant, cw int, pal overlay.Palette, st sidebarRowState, dragged, showCounts, numberedHead bool) string {
	rowBg := sidebarRowBg(st, pal)
	folded := node.GroupHead && m.SidebarRepoCollapsed(node.Worktree.Repo)
	if folded {
		node.AgentState, node.DoneSeen = node.GroupState, node.GroupDoneSeen
	}
	if dragged {
		rowBg = pal.Surface
	}

	tint := m.sessionTint(node.ID, m.rowGround(rowBg))
	stated := agentStateIndicator(node.AgentState) != ""
	focusTint := tint
	if folded && node.GroupCurrent && !node.IsCurrent {
		node.IsCurrent = true
		focusTint = m.sessionTint(m.sidebarCurrentSessionID(), m.rowGround(rowBg))
	}

	glyph := sidebarQuietDotTinted(dotTint(tint, pal, stated), rowBg, pal, &m.Settings)
	if stated {
		glyph = sidebarGlyph(node.AgentState, node.DoneSeen, node.Subagents, rowBg, pal, &m.Settings)
	}

	var figures [4]string
	if node.Restored && variant == sidebarVariantFull {
		figures[0] = sidebarRestoredTag
	}
	if node.Worktree != nil && node.Worktree.Gone && variant == sidebarVariantFull {
		figures[1] = sidebarWorktreeGoneTag
	} else if tag := sidebarSetupTag(node.Worktree); tag != "" && variant == sidebarVariantFull {
		figures[1] = tag
	}
	if m.Settings.SidebarShowCounts && showCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		figures[2] = strconv.Itoa(node.WindowCount)
	}
	if folded {
		figures[3] = "+" + strconv.Itoa(node.GroupCount)
	}

	fg := pal.Fg
	title := printableTitle(node.Title)
	indent := m.sidebarNodeIndent(node)
	grouped, treeMark := false, ""
	if mark, label, ok := m.sidebarWorktreeLabel(node); ok {
		treeMark, title, grouped = mark, label, true
	}
	b := ""
	if variant == sidebarVariantFull {
		switch {
		case !grouped:
			b = printableTitle(node.Branch)
		case node.Named:
			b = printableTitle(node.Worktree.Branch)
		}
	}
	note := ""
	if node.Note != "" && variant == sidebarVariantFull {
		note, b = printableTitle(node.Note), ""
	}
	s := &m.railFit
	s.tokens = append(s.tokens[:0], railToken{Whole: true})
	if b != "" {
		s.tokens[0].Cost = 1 + lipgloss.Width(b)
	}
	for _, f := range figures {
		s.tokens = append(s.tokens, railToken{Cost: sidebarFigureCost(f), Right: true})
	}
	treeW := lipgloss.Width(treeMark)
	titleW := treeW + lipgloss.Width(title)
	mark, markW := "", 0
	if m.Settings.SidebarShowNumbers && sessionIdx > 0 && sessionIdx <= 9 {
		mark = sidebarStyle(rowBg, pal.FgMute).Bold(node.IsCurrent).
			Render(strconv.Itoa(sessionIdx) + " ")
		markW = len(strconv.Itoa(sessionIdx)) + 1
	}
	nameMarked, capsW := railNameMarked(&m.Settings)
	nameMarked = nameMarked && node.IsCurrent
	if !nameMarked {
		capsW = 0
	} else {
		var took int
		treeMark, took = railTreeMarkForCaps(treeMark, &m.Settings)
		titleW -= took
		treeW -= took
	}
	foldW := 0
	if node.GroupHead {
		foldW = sidebarFoldCells
	}
	keep, avail := railRowFitInto(s.keep, titleW, railNameKeep(titleW), s.tokens, sidebarNameAvailIn(cw, 0, indent)-markW-capsW-foldW)
	s.keep = keep
	branch := ""
	if keep[0] && b != "" {
		branch = sidebarStyle(rowBg, nil).Render(" ") + sidebarStyle(rowBg, pal.FgMute).Render(b)
	}
	if room := avail - titleW - 1; note != "" && room >= railNoteMin {
		branch = sidebarStyle(rowBg, nil).Render(" ") + sidebarStyle(rowBg, pal.FgMute).Render(overlay.Truncate(note, room))
	}
	right := sidebarJoinFigures(figures[:], keep[1:], sidebarStyle(rowBg, pal.FgMute))
	shown := m.sidebarMarquee("s:"+node.ID, title, max(avail-treeW, 1), st.Cursor)
	ink := sidebarStyle(rowBg, fg).Bold(sidebarAttention(node.AgentState))
	name := mark + ink.Render(treeMark+shown) + branch
	swap := treeW > 0 && mark != "" && !numberedHead
	if swap {
		name = ink.Render(treeMark) + mark + ink.Render(shown) + branch
	}
	if nameMarked {
		if swap {
			name = ink.Render(treeMark) + mark + railCurrentName(shown, focusTint, rowBg, pal, &m.Settings) + branch
		} else {
			name = mark + ink.Render(treeMark) + railCurrentName(shown, focusTint, rowBg, pal, &m.Settings) + branch
		}
	}

	gutter := sidebarGutterTinted(node.IsCurrent, node.AgentState, focusTint, rowBg, pal, &m.Settings)
	if nameMarked {
		gutter = sidebarGutterNameMarked(node.AgentState, rowBg, pal, &m.Settings)
	}
	if tint != nil && stated && !node.IsCurrent && !sidebarAttention(node.AgentState) {
		gutter = sidebarStyle(rowBg, tint).Render(accentMark())
	}
	if node.GroupHead {
		fold := m.sidebarFoldMark(folded, st.FoldCursor, rowBg, pal)
		return sidebarComposeFoldRow(indent, gutter, glyph, name, right, fold, cw, rowBg)
	}
	return sidebarComposeGroupRow(indent, gutter, glyph, name, right, cw, rowBg)
}

func (m *OS) terminalRowTint(e sidebarTerminalEntry, rowBg color.Color) color.Color {
	tint := m.sessionTint(e.SessionID, m.rowGround(rowBg))
	accent, ok := m.WindowAccent(e.WindowID)
	if preview, previewing := m.accentPreview(AccentTargetWindow, e.WindowID); previewing {
		accent, ok = preview, true
	}
	if ok {
		tint = accent.Color()
	}
	return tint
}

func (m *OS) sidebarTerminalRow(e sidebarTerminalEntry, cw int, pal overlay.Palette, st sidebarRowState, peeked bool) string {
	rowBg := sidebarRowBg(st, pal)

	title := printableTitle(e.Title)
	if title == "" {
		title = "shell"
	}

	gutter := sidebarGutter(false, e.State, rowBg, pal, &m.Settings)
	if !peeked {
		tint := m.sessionTint(e.SessionID, m.rowGround(rowBg))
		accent, accented := m.WindowAccent(e.WindowID)
		if preview, ok := m.accentPreview(AccentTargetWindow, e.WindowID); ok {
			accent, accented = preview, true
		}
		if accented {
			tint = accent.Color()
		}
		switch {
		case e.Focused && railNameMarks(&m.Settings):
			gutter = sidebarGutterNameMarked(e.State, rowBg, pal, &m.Settings)
		case e.Focused:
			gutter = sidebarGutterTinted(true, e.State, tint, rowBg, pal, &m.Settings)
		case accented && !sidebarAttention(e.State):
			gutter = sidebarStyle(rowBg, tint).Render(accentMark())
		}
	}

	right, rightW := "", 0
	host := sidebarTerminalHostLabel(e, cw)
	switch {
	case host != "" && e.Tag != "" && sidebarNameAvail(cw, lipgloss.Width(host+" "+e.Tag)) >= sidebarHostTagFloor:
		label := host + " " + e.Tag
		right = sidebarStyle(rowBg, pal.AccentBright).Render(host) +
			sidebarStyle(rowBg, pal.FgMute).Render(" "+e.Tag)
		rightW = lipgloss.Width(label)
	case host != "":
		right = sidebarStyle(rowBg, pal.AccentBright).Render(host)
		rightW = lipgloss.Width(host)
	case e.Tag != "":
		right = sidebarStyle(rowBg, pal.FgMute).Render(e.Tag)
		rightW = lipgloss.Width(e.Tag)
	}
	if e.Recording {
		rec := sidebarStyle(rowBg, pal.Warn).Render(sidebarRecordingTag)
		recW := lipgloss.Width(sidebarRecordingTag)
		if right != "" && sidebarNameAvail(cw, recW+1+rightW) >= sidebarHostTagFloor {
			right, rightW = rec+sidebarStyle(rowBg, pal.FgMute).Render(" ")+right, recW+1+rightW
		} else {
			right, rightW = rec, recW
		}
	}

	fg := pal.FgDim
	switch {
	case peeked:
	case e.Focused:
		fg = pal.Fg
	case e.State == "done" && !e.DoneSeen:
		fg = pal.Fg
	}
	if st.lit() {
		fg = pal.Fg
	}

	nameMarked, capsW := railNameMarked(&m.Settings)
	nameMarked = nameMarked && e.Focused && !peeked
	if !nameMarked {
		capsW = 0
	}
	nameAvail := sidebarNameAvail(cw, rightW) - capsW
	quietMark := ""
	if e.Quiet {
		quietMark = sidebarQuietMark(nameAvail, lipgloss.Width(title), sidebarAgentSep())
	}
	shown := m.sidebarMarquee("t:"+e.WindowID, title, max(nameAvail-lipgloss.Width(quietMark), 1), st.lit())
	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(e.State)).Render(shown)
	if nameMarked {
		name = railCurrentName(shown, m.terminalRowTint(e, rowBg), rowBg, pal, &m.Settings)
	}
	name += sidebarStyle(rowBg, pal.FgMute).Render(quietMark)
	return sidebarComposeRow(gutter, sidebarGlyph(e.State, e.DoneSeen, e.Subagents, rowBg, pal, &m.Settings), name, right, cw, rowBg)
}

const sidebarScreenStopped = "screen stopped"

const sidebarRecordingTag = "rec"

func sidebarTerminalHostLabel(e sidebarTerminalEntry, cw int) string {
	if e.HostLink == "" {
		return e.Host
	}
	if e.Host == "" {
		return e.HostLink
	}
	full := e.Host + " " + e.HostLink
	if sidebarNameAvail(cw, lipgloss.Width(full)) >= sidebarHostTagFloor {
		return full
	}
	return e.HostLink
}

const sidebarHostTagFloor = 8

func (m *OS) workspaceTag(ws int) string {
	num := m.workspaceDisplayNumber(ws)
	if label := printableTitle(m.WorkspaceLabel(ws)); label != strconv.Itoa(num) && label != "" {
		return overlay.Truncate(label, sidebarWorkspaceTagMax)
	}
	return "w" + strconv.Itoa(num)
}

const sidebarWorkspaceTagMax = 8

func (m *OS) sidebarAgentsEmptyRow(total, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	fg := pal.FgMute
	if st.lit() {
		fg = pal.Fg
	}
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	text := "none here" + sep + strconv.Itoa(total) + " all"
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", sidebarNameCol()))+
		sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, sidebarNameAvail(cw, 0))), cw, rowBg)
}

const sidebarHarnessMax = 8

func sidebarHarnessLabel(harness string) string {
	id, _, _ := strings.Cut(printableTitle(harness), "-")
	return overlay.Truncate(strings.ToLower(id), sidebarHarnessMax)
}

const sidebarAgentRowTall = 2

func sidebarAgentName(e sidebarAgentEntry) string {
	if name := printableTitle(e.Title); name != "" {
		return name
	}
	return "shell"
}

func (m *OS) sidebarAgentNoteRow(e sidebarAgentEntry, variant, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	indent := sidebarNameCol() + 1 + e.nestIndent()
	avail := sidebarNameAvailIn(cw, 0, e.nestIndent()) - 1
	plan := m.sidebarAgentTokensFor(e, variant, true, time.Now())
	quiet := sidebarStyle(rowBg, pal.FgMute)
	note := plan.Note
	if sidebarAgentGroup(e.State, e.DoneSeen) == sidebarGroupNeedsYou {
		note = sidebarNoteKeepAsk(note, avail)
	} else {
		note = sidebarNoteKeepSubagents(sidebarNoteKeepNow(note, avail), avail)
	}
	text := m.sidebarAgentNoteText(note, quiet, avail, pal)
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent))+text, cw, rowBg)
}

const sidebarNoteAskFloor = 12

func sidebarNoteKeepAsk(tokens []sidebarAgentToken, avail int) []sidebarAgentToken {
	return sidebarNoteKeepLast(tokens, avail, "message", func(tk sidebarAgentToken) bool { return tk.Name != "need" })
}

func sidebarNoteKeepNow(tokens []sidebarAgentToken, avail int) []sidebarAgentToken {
	return sidebarNoteKeepLast(tokens, avail, "now", func(tk sidebarAgentToken) bool { return tk.Name == "harness" })
}

func sidebarNoteKeepSubagents(tokens []sidebarAgentToken, avail int) []sidebarAgentToken {
	at := slices.IndexFunc(tokens, func(tk sidebarAgentToken) bool { return tk.Name == "subagents" })
	if at < 0 {
		return tokens
	}
	sepW := lipgloss.Width(sidebarAgentSep())
	w := lipgloss.Width(tokens[at].Text)
	for _, tk := range tokens[:at] {
		w += lipgloss.Width(tk.Text) + sepW
	}
	if w <= avail {
		return tokens
	}
	out := make([]sidebarAgentToken, 0, len(tokens))
	for i, tk := range tokens {
		if i < at && tk.Name == "harness" {
			continue
		}
		out = append(out, tk)
	}
	return out
}

func sidebarNoteKeepLast(tokens []sidebarAgentToken, avail int, last string, droppable func(sidebarAgentToken) bool) []sidebarAgentToken {
	if len(tokens) < 2 || tokens[len(tokens)-1].Name != last {
		return tokens
	}
	sepW := lipgloss.Width(sidebarAgentSep())
	for {
		headW := 0
		for _, tk := range tokens[:len(tokens)-1] {
			headW += lipgloss.Width(tk.Text) + sepW
		}
		last := tokens[len(tokens)-1]
		if avail-headW >= min(lipgloss.Width(last.Text), sidebarNoteAskFloor) {
			return tokens
		}
		drop := -1
		for i := len(tokens) - 2; i >= 0; i-- {
			if droppable(tokens[i]) {
				drop = i
				break
			}
		}
		if drop < 0 {
			return tokens
		}
		tokens = append(tokens[:drop:drop], tokens[drop+1:]...)
	}
}

func (m *OS) sidebarAgentNoteText(tokens []sidebarAgentToken, quiet lipgloss.Style, avail int, pal overlay.Palette) string {
	if len(tokens) == 0 {
		return ""
	}
	sep := sidebarAgentSep()
	sepW := lipgloss.Width(sep)
	head, last := tokens, sidebarAgentToken{}
	if tail := tokens[len(tokens)-1]; sidebarNoteSentence(tail.Name) {
		head, last = tokens[:len(tokens)-1], tail
	}
	s := &m.railFit
	s.tokens = s.tokens[:0]
	for _, h := range slices.Backward(head) {
		s.tokens = append(s.tokens, railToken{Cost: lipgloss.Width(h.Text) + sepW})
	}
	keep, room := railRowFitInto(s.keep, 0, 0, s.tokens, avail+sepW)
	s.keep = keep
	room -= sepW
	var b strings.Builder
	for i, tk := range head {
		if !keep[len(head)-1-i] {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(quiet.Render(sep))
		}
		b.WriteString(m.sidebarTokenStyle(quiet, tk, pal).Render(tk.Text))
	}
	if last.Text != "" && (room >= 2 || (b.Len() == 0 && room >= 1)) {
		if cut := overlay.Truncate(last.Text, room); cut != "" && cut != overlay.Ellipsis() {
			if b.Len() > 0 {
				b.WriteString(quiet.Render(sep))
			}
			b.WriteString(m.sidebarTokenStyle(quiet, last, pal).Render(cut))
		}
	}
	return b.String()
}

func sidebarNoteSentence(name string) bool {
	switch name {
	case "message", "now", "prompt":
		return true
	}
	return false
}

func (m *OS) sidebarAgentRow(e sidebarAgentEntry, variant, cw int, pal overlay.Palette, st sidebarRowState, tall, focusMark bool) (string, int) {
	var rowBg color.Color
	fg := pal.FgDim
	if e.State == "done" && !e.DoneSeen {
		fg = pal.Fg
	}
	if st.lit() {
		rowBg = sidebarRowBg(st, pal)
		fg = pal.Fg
	}

	plan := m.sidebarAgentTokensFor(e, variant, tall, time.Now())
	nest := e.nestIndent()
	if st.lit() && plan.Right.Name == "elapsed" && plan.Right.Text == "" && variant == sidebarVariantFull {
		plan.Right.Text = agentElapsed(e.State, e.StateAt, time.Now())
	}
	name := plan.Name.Text
	if name == "" && len(plan.Prefix) == 0 && len(plan.After) == 0 {
		plan.Name = m.sidebarAgentTokenValue("name", e, variant, time.Now())
		name = plan.Name.Text
	}

	label := plan.Right.Text
	var queued []string
	if e.Queued > 0 {
		queued = m.sidebarAgentQueuedFigures(e)
	}
	mail := ""
	if n := m.agentMailUnreadFor(e.WindowID); n > 0 {
		mail = sidebarMailGlyph() + " " + strconv.Itoa(n)
	}

	nameStyle := sidebarStyle(rowBg, fg)
	timeFg := pal.FgMute
	if sidebarAttention(e.State) {
		nameStyle = nameStyle.Bold(true)
		timeFg = sidebarStateColor(e.State, e.DoneSeen, pal)
	}
	quiet := sidebarStyle(rowBg, pal.FgMute)
	sep := sidebarAgentSep()
	nameW := lipgloss.Width(name)
	quietMark := ""
	if e.Quiet {
		quietMark = sidebarQuietMark(sidebarNameAvailIn(cw, 0, nest), nameW, sep)
	}
	label, keep, nameRoom := sidebarAgentFit(&m.railFit, plan.Prefix, plan.After, nameW, label, queued, mail, sep, sidebarNameAvailIn(cw, 0, nest)-lipgloss.Width(quietMark))
	labelAt, mailAt := len(plan.Prefix)+len(plan.After), len(plan.Prefix)+len(plan.After)+1
	nameRoom = max(nameRoom, 1)
	shown := m.sidebarAgentPrefixRun(plan.Prefix, keep[:len(plan.Prefix)], quiet, pal)
	after := ""
	if len(plan.After) > 0 {
		baseFor := func(tk sidebarAgentToken) lipgloss.Style {
			if tk.Name == "state" {
				return sidebarStyle(rowBg, agentGlyphColor(displayState(e.State, e.DoneSeen, e.Subagents), pal))
			}
			return quiet
		}
		after = m.sidebarAgentRun(plan.After, keep[len(plan.Prefix):labelAt], sep, baseFor, quiet, pal)
	}
	if !keep[labelAt] {
		label = ""
	}
	if !keep[mailAt] {
		mail = ""
	}
	right := ""
	if label != "" {
		right = m.sidebarTokenStyle(sidebarStyle(rowBg, timeFg), plan.Right, pal).Render(label)
	}
	if mail != "" {
		if right != "" {
			right += sidebarStyle(rowBg, nil).Render(" ")
		}
		right += sidebarStyle(rowBg, pal.AccentBright).Render(mail)
	}
	gutter := sidebarGutter(false, e.State, rowBg, pal, &m.Settings)
	if e.Foreign && !sidebarAttention(e.State) {
		if tint := m.agentIdentityTint(e, m.rowGround(rowBg)); tint != nil {
			gutter = sidebarStyle(rowBg, tint).Render(accentMark())
		}
	}
	if focusMark {
		gutter = sidebarGutterTinted(true, e.State, m.sessionTint(e.SessionID, m.rowGround(rowBg)), rowBg, pal, &m.Settings)
	}
	body := shown +
		m.sidebarTokenStyle(nameStyle, plan.Name, pal).Render(m.sidebarMarquee("a:"+e.SessionID+"/"+e.WindowID, name, nameRoom, st.Cursor)) +
		quiet.Render(quietMark) + after
	return sidebarComposeGroupRow(nest, gutter,
		sidebarGlyph(e.State, e.DoneSeen, e.Subagents, rowBg, pal, &m.Settings), body, right, cw, rowBg), lipgloss.Width(body)
}
