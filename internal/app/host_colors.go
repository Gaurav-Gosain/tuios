package app

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	tint "github.com/lrstanley/bubbletint/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type hostColors struct {
	fg, bg               color.Color
	ansi                 [16]color.Color
	light                bool
	fgHex, bgHex, palHex string
	gen                  uint64
	settlePending        bool
}

const (
	hostLightAbove = 140
	hostDarkBelow  = 110
)

func hostLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(b>>8)
}

func (m *OS) seedHostColors(caps *HostCapabilities) {
	if caps == nil {
		return
	}
	if caps.HasBg {
		m.setHostBg(unpackColor(caps.Bg))
	}
	if caps.HasFg {
		m.setHostFg(unpackColor(caps.Fg))
	}
	for i := range 16 {
		if caps.ANSIMask&(1<<uint(i)) != 0 {
			m.setHostSlot(i, unpackColor(caps.ANSI[i]))
		}
	}
	m.followSchemeTheme()
}

func (m *OS) schemeTheme() string {
	if m.UserConfig == nil {
		return ""
	}
	a := m.UserConfig.Appearance
	if a.ThemeLight != "" && theme.SchemeLight() {
		return a.ThemeLight
	}
	return a.Theme
}

func (m *OS) followSchemeTheme() {
	if m.UserConfig == nil || m.UserConfig.Appearance.ThemeLight == "" ||
		m.Client != ClientLocal || m.LearnMode || m.ShowThemePicker || m.themeEditor.open ||
		config.FlagTheme() != "" {
		return
	}
	name := m.schemeTheme()
	if name == m.schemeApplied {
		return
	}
	m.schemeApplied = name
	m.applyTheme(name)
}

func unpackColor(packed uint32) color.Color {
	return color.RGBA{R: uint8(packed >> 16), G: uint8(packed >> 8), B: uint8(packed), A: 0xff}
}

func (m *OS) setHostBg(c color.Color) bool {
	if isNilColor(c) {
		return false
	}
	c = solidColor(c)
	hex := colorHex(c)
	h := &m.host
	if h.bgHex == hex {
		return false
	}
	lum := hostLuminance(c)
	switch {
	case h.bg == nil:
		h.light = theme.GroundIsLight(c)
	case h.light && lum < hostDarkBelow:
		h.light = false
	case !h.light && lum > hostLightAbove:
		h.light = true
	}
	h.bg, h.bgHex = c, hex
	h.gen++
	if m.Client == ClientLocal && !m.LearnMode {
		theme.SetHostGround(c, h.light)
		theme.SetSchemeLight(h.light)
	}
	return true
}

func (m *OS) setHostFg(c color.Color) bool {
	if isNilColor(c) {
		return false
	}
	c = solidColor(c)
	hex := colorHex(c)
	h := &m.host
	if h.fgHex == hex {
		return false
	}
	h.fg, h.fgHex = c, hex
	h.gen++
	return true
}

func (m *OS) setHostSlot(i int, c color.Color) bool {
	if i < 0 || i > 15 || isNilColor(c) {
		return false
	}
	c = solidColor(c)
	h := &m.host
	if h.ansi[i] != nil && colorHex(h.ansi[i]) == colorHex(c) {
		return false
	}
	h.ansi[i] = c
	var b strings.Builder
	for j, slot := range h.ansi {
		if j > 0 {
			b.WriteByte(',')
		}
		b.WriteString(colorHex(slot))
	}
	h.palHex = b.String()
	h.gen++
	return true
}

var hostPaletteQuery = func() string {
	var b strings.Builder
	for i := range 16 {
		fmt.Fprintf(&b, "\x1b]4;%d;?\x1b\\", i)
	}
	return b.String()
}()

const (
	hostSchemeReportsOn  = "\x1b[?2031h"
	hostSchemeReportsOff = "\x1b[?2031l"
)

func (m *OS) asksHostColors() bool {
	return m.Client != ClientUnknown && !m.LearnMode
}

func (m *OS) followsHostScheme() bool {
	return m.Client == ClientLocal && !m.LearnMode
}

func (m *OS) hostColorQueries() tea.Cmd {
	if !m.asksHostColors() {
		return nil
	}
	var cmds []tea.Cmd
	caps := m.hostCaps()
	if !caps.HasBg || !caps.HasFg || caps.ANSIMask != 0xffff {
		cmds = append(cmds, hostColorRequests()...)
	}
	if m.followsHostScheme() {
		cmds = append(cmds, tea.Raw(hostSchemeReportsOn))
	}
	if m.IsDaemonSession && m.host.gen > 0 && !m.host.settlePending {
		m.host.settlePending = true
		cmds = append(cmds, func() tea.Msg { return hostColorsSettledMsg{} })
	}
	return tea.Batch(cmds...)
}

func hostColorRequests() []tea.Cmd {
	return []tea.Cmd{tea.RequestBackgroundColor, tea.RequestForegroundColor, tea.Raw(hostPaletteQuery)}
}

type hostColorsSettledMsg struct{}

const hostColorsSettle = 15 * time.Millisecond

func (m *OS) handleHostColorMsg(msg tea.Msg) (tea.Cmd, bool) {
	changed := false
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		changed = m.setHostBg(msg.Color)
	case tea.ForegroundColorMsg:
		changed = m.setHostFg(msg.Color)
	case uv.UnknownOscEvent:
		idx, c, ok := parseHostSlotReply(string(msg))
		if !ok {
			return nil, false
		}
		changed = m.setHostSlot(idx, c)
	case uv.LightColorSchemeEvent, uv.DarkColorSchemeEvent:
		if !m.asksHostColors() {
			return nil, true
		}
		return tea.Batch(hostColorRequests()...), true
	case hostColorsSettledMsg:
		m.host.settlePending = false
		m.hostColorsChanged()
		return nil, true
	default:
		return nil, false
	}
	if !changed || m.host.settlePending {
		return nil, true
	}
	m.host.settlePending = true
	return tea.Tick(hostColorsSettle, func(time.Time) tea.Msg { return hostColorsSettledMsg{} }), true
}

func parseHostSlotReply(s string) (int, color.Color, bool) {
	m := oscColorReply().FindStringSubmatch(s)
	if m == nil || !strings.HasPrefix(m[1], "4;") {
		return 0, nil, false
	}
	idx, err := strconv.Atoi(m[2])
	if err != nil || idx < 0 || idx > 15 {
		return 0, nil, false
	}
	return idx, unpackColor(packOSCColor(m[3], m[4], m[5])), true
}

func (m *OS) hostColorsChanged() {
	m.followSchemeTheme()
	m.MarkAllDirty()
	m.syncReportColors()
	m.SyncStateToDaemon()
}

func (m *OS) railGround() color.Color {
	if t := theme.Current(); t != nil {
		return theme.AsColor(t.Bg)
	}
	if m.host.bg != nil {
		return m.host.bg
	}
	return theme.RailGround()
}

func (m *OS) terminalBg() color.Color {
	if theme.Current() == nil && m.host.bg != nil {
		return m.host.bg
	}
	return theme.TerminalBg()
}

func (m *OS) groundUI() overlay.Palette {
	if theme.Current() == nil && m.host.bg != nil {
		return theme.GroundUIOn(m.host.bg, m.host.light)
	}
	return theme.GroundUI()
}

func (m *OS) railRule() color.Color {
	return theme.RailRuleOn(m.railGround())
}

type paneReport struct {
	fg, bg               color.Color
	pal                  *[16]color.Color
	key                  string
	bgHex, fgHex, palHex string
}

type paneReportMemo struct {
	groundKey string
	themed    bool
	tint      *tint.Tint
	gen       uint64
	valid     bool
	r         paneReport
}

func themeReportPalette(t *tint.Tint) (*[16]color.Color, string) {
	var pal [16]color.Color
	var b strings.Builder
	for i, c := range theme.ANSIOrder(t) {
		if i > 0 {
			b.WriteByte(',')
		}
		pal[i] = theme.AsColor(c)
		b.WriteString(colorHex(pal[i]))
	}
	return &pal, b.String()
}

func (m *OS) paneReportNow() *paneReport {
	g := m.paneGround()
	themed := theme.IsEnabled()
	cur := theme.Current()
	memo := &m.paneReport
	if memo.valid && memo.groundKey == g.key && memo.themed == themed && memo.tint == cur && memo.gen == m.host.gen {
		return &memo.r
	}
	memo.groundKey, memo.themed, memo.tint, memo.gen, memo.valid = g.key, themed, cur, m.host.gen, true

	var r paneReport
	if g.on() {
		r.bg, r.fg = g.bg, g.fg
	}
	if !themed {
		if r.bg == nil {
			r.bg = m.host.bg
		}
		if r.fg == nil {
			r.fg = m.host.fg
		}
		if m.host.palHex != "" {
			pal := m.host.ansi
			r.pal = &pal
			r.palHex = m.host.palHex
		}
	} else if cur != nil {
		if r.bg == nil {
			r.bg = theme.AsColor(cur.Bg)
		}
		if r.fg == nil {
			r.fg = theme.AsColor(cur.Fg)
		}
		r.pal, r.palHex = themeReportPalette(cur)
	}
	r.bgHex, r.fgHex = colorHex(r.bg), colorHex(r.fg)
	if r.bgHex != "" || r.fgHex != "" || r.palHex != "" {
		r.key = r.bgHex + "\x00" + r.fgHex + "\x00" + r.palHex
	}
	memo.r = r
	return &memo.r
}
