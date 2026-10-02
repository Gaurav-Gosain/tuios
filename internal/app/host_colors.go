package app

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The host terminal's own colours.
//
// A program that picks a light or a dark palette asks the terminal for its
// background with OSC 11, and some ask for the default text colour (OSC 10)
// and the sixteen ANSI colours (OSC 4) too. Inside tuios the terminal that
// answers is a pane's emulator, and with no tuios theme and no pane background
// it used to answer with its own defaults: a black background and a white
// foreground, whatever the terminal around tuios really was. Codex, Claude
// Code and helix then drew their dark palettes on a light terminal.
//
// So each client asks its own terminal. The local client's startup probe
// already does (capabilities.go), and every client asks again through Bubble
// Tea when the probe had nothing, which is the case for the SSH and browser
// clients, whose terminals the probe never saw. The answers are held here,
// per client, because one server process serves clients on different
// terminals. They are what panes are told while no theme is set (see
// paneReportNow), and the background is the ground the rail and the dock are
// measured against (railGround, groundUI), so a light terminal with no theme
// gets the light chrome ramp a light theme gets.
//
// A terminal that answers nothing (mosh answers no colour queries) leaves all
// of it unset, and everything behaves as it did before: panes get the
// emulator's defaults and the chrome its constant dark ramp.
//
// The local client also follows the terminal's light and dark switch. It turns
// on mode 2031, and a terminal that supports it (ghostty, kitty, contour and
// others) then reports every change of the system appearance with DSR 997.
// tuios answers each report by asking for the colours again, since the new
// scheme's background is what matters and the report only says light or dark.
// The mode is only turned on for a local client, because only the local
// client turns it off again when it exits (terminal.ResetTerminal): an SSH or
// browser client that went away with the mode on would leave its terminal
// sending reports into whatever runs there next.

// hostColors is what this client's terminal said about its own colours. A nil
// colour is one it did not answer for.
type hostColors struct {
	fg, bg color.Color
	ansi   [16]color.Color
	// light is the verdict on bg, held with hysteresis so a background near
	// the middle does not flip the chrome between ramps. Meaningful only while
	// bg is set.
	light bool
	// fgHex, bgHex and palHex spell the colours the way the daemon is sent
	// them; palHex is empty until some slot is known.
	fgHex, bgHex, palHex string
	// gen counts changes, so a memo keyed on it notices one.
	gen uint64
	// settlePending is set while a settle message is on its way. See
	// hostColorsSettledMsg.
	settlePending bool
}

// The hysteresis band for the light verdict, in 8-bit luminance. A dark
// verdict turns light only above hostLightAbove, and a light one turns dark
// only below hostDarkBelow. The numbers are Gemini CLI's, which follows the
// terminal's appearance the same way.
const (
	hostLightAbove = 140
	hostDarkBelow  = 110
)

// hostLuminance is a colour's luminance on 0 to 255, weighted the way the eye
// weighs the three channels.
func hostLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(b>>8)
}

// seedHostColors takes what the startup probe learned. It is called once, from
// NewOS, before anything is drawn, so nothing needs repainting.
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
}

// unpackColor turns a 0xRRGGBB word into a colour.
func unpackColor(packed uint32) color.Color {
	return color.RGBA{R: uint8(packed >> 16), G: uint8(packed >> 8), B: uint8(packed), A: 0xff}
}

// setHostBg records the host's background and reports whether it changed.
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
		// The first answer has no verdict to hold, so it is judged the way a
		// theme's ground is: by which ink reads better on it.
		h.light = theme.GroundIsLight(c)
	case h.light && lum < hostDarkBelow:
		h.light = false
	case !h.light && lum > hostLightAbove:
		h.light = true
	}
	h.bg, h.bgHex = c, hex
	h.gen++
	return true
}

// setHostFg records the host's default foreground and reports whether it
// changed.
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

// setHostSlot records one of the host's sixteen and reports whether it
// changed.
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

// hostPaletteQuery asks for the sixteen in one write.
var hostPaletteQuery = func() string {
	var b strings.Builder
	for i := range 16 {
		fmt.Fprintf(&b, "\x1b]4;%d;?\x1b\\", i)
	}
	return b.String()
}()

// Mode 2031 on and off: the terminal reports light and dark switches while it
// is on.
const (
	hostSchemeReportsOn  = "\x1b[?2031h"
	hostSchemeReportsOff = "\x1b[?2031l"
)

// asksHostColors reports whether this client talks to a terminal of its own.
// A model built without a kind (the tests, the benchmarks) and the browser
// tour's fake terminal do not.
func (m *OS) asksHostColors() bool {
	return m.Client != ClientUnknown && !m.LearnMode
}

// followsHostScheme reports whether this client turns mode 2031 on. Only the
// local client does; see the comment at the top of the file.
func (m *OS) followsHostScheme() bool {
	return m.Client == ClientLocal && !m.LearnMode
}

// hostColorQueries is what Init sends: the colour questions the startup probe
// left unanswered, and mode 2031 for a client that follows the scheme.
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
	// What the startup probe already learned goes to the daemon now, rather
	// than with the first push some input happens to cause: a program started
	// in a pane before the person touches a key asks the daemon's emulator,
	// and it has to be told by then.
	if m.IsDaemonSession && m.host.gen > 0 && !m.host.settlePending {
		m.host.settlePending = true
		cmds = append(cmds, func() tea.Msg { return hostColorsSettledMsg{} })
	}
	return tea.Batch(cmds...)
}

// hostColorRequests asks for the background, the foreground and the sixteen.
func hostColorRequests() []tea.Cmd {
	return []tea.Cmd{tea.RequestBackgroundColor, tea.RequestForegroundColor, tea.Raw(hostPaletteQuery)}
}

// hostColorsSettledMsg lands a batch of host colour answers. A terminal
// answers the seventeen questions as seventeen replies, and repainting and
// pushing state after each would be seventeen of both; the first answer that
// changes something schedules this, and the rest ride along.
type hostColorsSettledMsg struct{}

// hostColorsSettle is how long the answers after the first are given to
// arrive. They come back in one burst, so a few milliseconds is plenty.
const hostColorsSettle = 15 * time.Millisecond

// handleHostColorMsg takes the host colour messages. It reports whether msg
// was one, and the command to run.
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
		// The report says light or dark and nothing else. The colours of the
		// new scheme are what panes and the chrome need, so ask for them.
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

// parseHostSlotReply reads an OSC 4 answer for one of the sixteen.
func parseHostSlotReply(s string) (int, color.Color, bool) {
	m := oscColorReply.FindStringSubmatch(s)
	if m == nil || !strings.HasPrefix(m[1], "4;") {
		return 0, nil, false
	}
	idx, err := strconv.Atoi(m[2])
	if err != nil || idx < 0 || idx > 15 {
		return 0, nil, false
	}
	return idx, unpackColor(packOSCColor(m[3], m[4], m[5])), true
}

// hostColorsChanged lands a change of the host's colours: the chrome is
// rebuilt on the new ground, and panes and the daemon are told.
func (m *OS) hostColorsChanged() {
	m.MarkAllDirty()
	m.syncReportColors()
	m.SyncStateToDaemon()
}

// railGround is what the rail and the dock are drawn on: the theme's
// background when a theme is on, else the host's own when it said, else the
// chrome ramp's canvas, which every constant ink was picked against.
func (m *OS) railGround() color.Color {
	if t := theme.Current(); t != nil {
		return theme.AsColor(t.Bg)
	}
	if m.host.bg != nil {
		return m.host.bg
	}
	return theme.RailGround()
}

// terminalBg is theme.TerminalBg on this client's terminal: the theme's
// background when a theme is on, else the host's own when it said, else the
// black theme.TerminalBg assumes. Everything that measures an ink against the
// ground under a pane or a rail row asks here, so a light terminal with no
// theme is not measured as black.
func (m *OS) terminalBg() color.Color {
	if theme.Current() == nil && m.host.bg != nil {
		return m.host.bg
	}
	return theme.TerminalBg()
}

// groundUI is theme.GroundUI on this client's ground.
func (m *OS) groundUI() overlay.Palette {
	if theme.Current() == nil && m.host.bg != nil {
		return theme.GroundUIOn(m.host.bg, m.host.light)
	}
	return theme.GroundUI()
}

// railRule is theme.RailRule on this client's ground.
func (m *OS) railRule() color.Color {
	return theme.RailRuleOn(m.railGround())
}

// paneReport is what a pane's emulator answers colour questions with, and
// the key that names it. A nil colour or a nil palette keeps the emulator's
// own answer.
type paneReport struct {
	fg, bg color.Color
	pal    *[16]color.Color
	key    string
	// bgHex, fgHex and palHex are the same for the daemon.
	bgHex, fgHex, palHex string
}

// paneReportMemo keeps the answer and what it was worked out from, so asking
// on every frame costs comparisons.
type paneReportMemo struct {
	groundKey string
	themed    bool
	gen       uint64
	valid     bool
	r         paneReport
}

// paneReportNow is what panes are told they are drawn on.
//
// A painted pane background is the answer for the ground, as it always was.
// With no theme the host's own colours fill in whatever the paint does not
// say: the background when nothing is painted, the text colour when a colour
// of the user's is painted with no theme to take ink from, and the sixteen.
// With a theme and nothing painted the emulator answers with the theme's
// colours, which is what it is given as its defaults.
func (m *OS) paneReportNow() *paneReport {
	g := m.paneGround()
	themed := theme.IsEnabled()
	memo := &m.paneReport
	if memo.valid && memo.groundKey == g.key && memo.themed == themed && memo.gen == m.host.gen {
		return &memo.r
	}
	memo.groundKey, memo.themed, memo.gen, memo.valid = g.key, themed, m.host.gen, true

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
	}
	r.bgHex, r.fgHex = colorHex(r.bg), colorHex(r.fg)
	if r.bgHex != "" || r.fgHex != "" || r.palHex != "" {
		r.key = r.bgHex + "\x00" + r.fgHex + "\x00" + r.palHex
	}
	memo.r = r
	return &memo.r
}
