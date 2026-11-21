package app

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dockengine"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type dockPlan struct {
	Left   []string
	Center []string
	Right  []string
	placed map[string]bool
}

func (p dockPlan) Has(name string) bool { return p.placed[name] }

func buildDockPlan(cfg *config.UserConfig) dockPlan {
	plan := dockPlan{placed: map[string]bool{}}
	if cfg == nil {
		cfg = &config.UserConfig{}
	}
	misplaced := map[string][]string{}
	keep := func(side string, list []string) []string {
		out := make([]string, 0, len(list))
		for _, name := range list {
			name = strings.TrimSpace(name)
			if name == "" || plan.placed[name] {
				continue
			}
			if custom, ok := strings.CutPrefix(name, config.DockCustomPrefix); ok {
				spec, defined := cfg.Dock.Custom[custom]
				if !defined || strings.TrimSpace(spec.Command) == "" {
					continue
				}
			} else if !config.IsDockBuiltin(name) && !strings.HasPrefix(name, config.DockPluginPrefix) {
				continue
			}
			plan.placed[name] = true
			if fixed := config.DockFixedSide(name); fixed != "" && fixed != side {
				misplaced[fixed] = append(misplaced[fixed], name)
				continue
			}
			out = append(out, name)
		}
		return out
	}
	plan.Left = keep("left", cfg.Dock.DockList("left"))
	plan.Center = keep("center", cfg.Dock.DockList("center"))
	plan.Right = keep("right", cfg.Dock.DockList("right"))
	plan.Center = append(plan.Center, misplaced["center"]...)
	plan.Right = append(plan.Right, misplaced["right"]...)
	return plan
}

func (m *OS) ensureDockPlan() {
	if m.dockPlan.placed == nil {
		m.rebuildDockPlan()
	}
}

func dockRefreshableComponents(cfg *config.UserConfig, plan dockPlan, showClock bool, s *config.Settings) []*dockengine.Component {
	var comps []*dockengine.Component

	if plan.Has(config.DockComponentClock) && showClock {
		format := cfg.Dock.DockClockFormat(s)
		comps = append(comps, &dockengine.Component{
			Name:    config.DockComponentClock,
			Builtin: true,
			Refresh: config.DockRefresh{
				Kind:     config.DockRefreshInterval,
				Interval: config.DockClockInterval(format),
			},
		})
	}
	if plan.Has(config.DockComponentCPU) && s.ShowCPU {
		comps = append(comps, &dockengine.Component{
			Name:    config.DockComponentCPU,
			Builtin: true,
			Refresh: config.DockRefresh{Kind: config.DockRefreshInterval, Interval: time.Duration(s.DockMeterIntervalMS) * time.Millisecond},
		})
	}
	if plan.Has(config.DockComponentRAM) && s.ShowRAM {
		comps = append(comps, &dockengine.Component{
			Name:    config.DockComponentRAM,
			Builtin: true,
			Refresh: config.DockRefresh{Kind: config.DockRefreshInterval, Interval: time.Duration(s.DockMeterIntervalMS) * time.Millisecond},
		})
	}

	for _, side := range [][]string{plan.Left, plan.Center, plan.Right} {
		for _, name := range side {
			key, ok := strings.CutPrefix(name, config.DockCustomPrefix)
			if !ok {
				continue
			}
			spec := cfg.Dock.Custom[key]
			refresh, err := config.ParseDockRefresh(spec.Refresh)
			if err != nil {
				refresh = config.DockRefresh{Kind: config.DockRefreshOnce}
			}
			width := spec.MaxWidth
			if width <= 0 || width > config.DockCustomMaxWidthLimit {
				width = config.DockCustomDefaultMaxWidth
			}
			comps = append(comps, &dockengine.Component{
				Name:     name,
				Command:  spec.Command,
				OnClick:  spec.OnClick,
				MaxWidth: width,
				Refresh:  refresh,
			})
		}
	}
	return comps
}

type dockCustomHit struct {
	X0, X1, Y int
	Name      string
	Button    int
}

// DockCustomComponentAt returns the custom component covering the absolute cell
// (x, y), or "".
func (m *OS) DockCustomComponentAt(x, y int) string {
	for _, h := range m.dockCustomHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			return h.Name
		}
	}
	return ""
}

func (m *OS) dockCustomCell(name string) string {
	if name == config.DockComponentClock && (!m.Settings.ShowClock || m.Settings.HideClock) {
		return ""
	}
	if name == config.DockComponentTransfers {
		return m.transfersDockCell()
	}
	if strings.HasPrefix(name, config.DockPluginPrefix) {
		return m.pluginCellDrawn(name)
	}
	text := m.dockEngine.Text(name)
	if name == config.DockComponentClock && text == "" {
		text = m.DockClockText()
	}
	if name == config.DockComponentSession {
		text = m.dockSessionText()
	}
	if text == "" {
		return ""
	}
	return renderDockCell(text, m.dockCellStyle(name), m.groundUI())
}

func renderDockCell(text string, st config.DockCellStyle, pal overlay.Palette) string {
	bg := pal.Panel
	if c := theme.ConfigColorOn(st.Bg, &pal); c != nil {
		bg = c
	}
	fg := theme.Readable(pal.FgDim, bg)
	if c := theme.ConfigColorOn(st.Fg, &pal); c != nil {
		fg = c
	}
	body := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(st.Bold).Render(" " + text + " ")
	if st.CapLeft == "" && st.CapRight == "" {
		return body
	}
	if overlay.IsNoColor(bg) {
		return strings.Repeat(" ", lipgloss.Width(st.CapLeft)) + body + strings.Repeat(" ", lipgloss.Width(st.CapRight))
	}
	caps := lipgloss.NewStyle().Foreground(bg)
	if st.CapLeft != "" {
		body = caps.Render(st.CapLeft) + body
	}
	if st.CapRight != "" {
		body += caps.Render(st.CapRight)
	}
	return body
}

func (m *OS) dockCellStyle(name string) config.DockCellStyle {
	if m.UserConfig == nil {
		return config.DockCellStyle{}
	}
	if name == config.DockComponentSession {
		return m.UserConfig.Dock.Session.DockCellStyle
	}
	if key, ok := strings.CutPrefix(name, config.DockCustomPrefix); ok {
		return m.UserConfig.Dock.Custom[key].DockCellStyle
	}
	return config.DockCellStyle{}
}

func (m *OS) dockSessionText() string {
	format := ""
	if m.UserConfig != nil {
		format = m.UserConfig.Dock.Session.Format
	}
	pane := 0
	if w := m.GetFocusedWindow(); w != nil {
		pane = m.workspacePosition(w)
	}
	return config.FormatDockSession(format, m.SessionName, m.workspaceDisplayNumber(m.dockWorkspace()), pane)
}

func (m *OS) dockCustomCells(names []string) (cells []string, widths []int, keys []string) {
	for _, name := range names {
		if !dockCellComponent(name) {
			continue
		}
		cell := m.dockCustomCell(name)
		if cell == "" {
			continue
		}
		cells = append(cells, cell)
		widths = append(widths, lipgloss.Width(cell))
		keys = append(keys, name)
	}
	return cells, widths, keys
}

func dockCellComponent(name string) bool {
	return strings.HasPrefix(name, config.DockCustomPrefix) || strings.HasPrefix(name, config.DockPluginPrefix) || name == config.DockComponentClock ||
		name == config.DockComponentTransfers || name == config.DockComponentSession
}

func dockCellClickable(name string) bool {
	return strings.HasPrefix(name, config.DockCustomPrefix) || strings.HasPrefix(name, config.DockPluginPrefix) || name == config.DockComponentTransfers
}

func (m *OS) dockCustomWidth(names []string) int {
	_, widths, _ := m.dockCustomCells(names)
	total := 0
	for _, w := range widths {
		total += w
	}
	return total
}

// DockComponentInfo is one row of list-dock-components: what the component is,
// how it refreshes, and what it last did. The last two fields are the whole
// debugging story for a component that is not drawing, and the reason a broken
// component is quiet on the bar without being invisible to the person who wrote
// it.
type DockComponentInfo struct {
	Name     string `json:"name"`
	Side     string `json:"side"`
	Source   string `json:"source"` // builtin | custom | rail
	Refresh  string `json:"refresh"`
	Interval string `json:"interval,omitempty"`
	Events   string `json:"events,omitempty"`
	Command  string `json:"command,omitempty"`
	OnClick  string `json:"on_click,omitempty"`
	MaxWidth int    `json:"max_width,omitempty"`
	Text     string `json:"text"`
	Visible  bool   `json:"visible"`
	LastExit int    `json:"last_exit"`
	LastRun  string `json:"last_run,omitempty"`
	LastErr  string `json:"last_error,omitempty"`
	Stopped  bool   `json:"stopped"`
	// Off names the setting that keeps a placed built-in off the bar, such as
	// "show_cpu = false". Empty when nothing does.
	Off string `json:"off,omitempty"`
}

// DockComponentsData is the listing shaped for the wire: plain maps rather than
// structs, because the daemon protocol encodes with gob and gob cannot encode a
// concrete type inside a map[string]any without it being registered first. The
// window listing is the same shape for the same reason.
//
// This cost a debugging round: the encode error was discarded at the send site,
// so the result was never written and the caller saw only a ten second timeout.
func (m *OS) DockComponentsData() []map[string]any {
	infos := m.DockComponents()
	out := make([]map[string]any, 0, len(infos))
	for _, c := range infos {
		out = append(out, map[string]any{
			"name":       c.Name,
			"side":       c.Side,
			"source":     c.Source,
			"refresh":    c.Refresh,
			"interval":   c.Interval,
			"events":     c.Events,
			"command":    c.Command,
			"on_click":   c.OnClick,
			"max_width":  c.MaxWidth,
			"text":       c.Text,
			"visible":    c.Visible,
			"last_exit":  c.LastExit,
			"last_run":   c.LastRun,
			"last_error": c.LastErr,
			"stopped":    c.Stopped,
			"off":        c.Off,
		})
	}
	return out
}

// DockComponents describes every component the dock has placed, in draw order.
// This is what an agent reads to find out what the bar is made of and why a
// cell it just wrote is not on it.
func (m *OS) DockComponents() []DockComponentInfo {
	m.ensureDockPlan()
	plan := m.dockPlan
	sides := []struct {
		name  string
		names []string
	}{
		{"left", plan.Left}, {"center", plan.Center}, {"right", plan.Right},
	}

	out := make([]DockComponentInfo, 0, len(plan.placed))
	for _, side := range sides {
		for _, name := range side.names {
			info := DockComponentInfo{
				Name:    name,
				Side:    side.name,
				Source:  "builtin",
				Refresh: "render",
			}
			if strings.HasPrefix(name, config.DockCustomPrefix) {
				info.Source = "custom"
			}
			if c, ok := m.dockEngine.Component(name); ok {
				info.Refresh = c.Refresh.Kind.String()
				info.Command = c.Command
				info.OnClick = c.OnClick
				info.MaxWidth = c.MaxWidth
				info.Text = c.Output()
				info.LastExit = c.LastExit()
				info.LastErr = c.LastErr()
				info.Stopped = c.Stopped()
				if c.Refresh.Interval > 0 {
					info.Interval = c.Refresh.Interval.String()
				}
				if len(c.Refresh.Events) > 0 {
					info.Events = strings.Join(c.Refresh.Events, ",")
				}
				if !c.LastRun().IsZero() {
					info.LastRun = c.LastRun().UTC().Format(time.RFC3339)
				}
			}
			info.Visible = info.Refresh == "render" || info.Text != ""
			if off := m.dockSwitchedOff(name); off != "" {
				info.Visible = false
				info.Off = off
			}
			out = append(out, info)
		}
	}
	if m.railCustomEnabled() || m.railCustomConfig().HasCommand() {
		out = append(out, m.railCustomInfo())
	}
	return out
}

func (m *OS) renderDockRightCells(room int, meterStyle lipgloss.Style) (string, []dockCustomHit) {
	type segment struct {
		text    string
		width   int
		name    string
		custom  bool
		hit     bool
		meters  bool
		buttons bool
	}

	meters := m.dockMeterParts()
	var segs []segment
	placedMeters := false
	fit := dockButtonsFit{shown: len(m.dockButtonRow())}
	for _, name := range m.dockPlan.Right {
		switch name {
		case config.DockComponentButtons:
			segs = append(segs, segment{buttons: true})
		case config.DockComponentCPU, config.DockComponentRAM:
			if placedMeters || len(meters) == 0 {
				continue
			}
			placedMeters = true
			segs = append(segs, segment{meters: true})
		default:
			if !dockCellComponent(name) {
				continue
			}
			cell := m.dockCustomCell(name)
			if cell == "" {
				continue
			}
			segs = append(segs, segment{
				text:   cell,
				width:  lipgloss.Width(cell),
				name:   name,
				custom: strings.HasPrefix(name, config.DockCustomPrefix) || strings.HasPrefix(name, config.DockPluginPrefix),
				hit:    dockCellClickable(name),
			})
		}
	}

	for {
		var b strings.Builder
		var hits []dockCustomHit
		x := 0
		for _, seg := range segs {
			text, width := seg.text, seg.width
			if seg.meters {
				text = meterStyle.Render(strings.Join(meters, " "))
				width = lipgloss.Width(text)
			}
			if seg.buttons {
				var bh []dockCustomHit
				text, bh = m.renderDockButtons(fit)
				width = lipgloss.Width(text)
				for _, h := range bh {
					h.X0, h.X1 = h.X0+x, h.X1+x
					hits = append(hits, h)
				}
			}
			if seg.hit {
				hits = append(hits, dockCustomHit{X0: x, X1: x + width, Name: seg.name})
			}
			b.WriteString(text)
			x += width
		}
		if x <= room {
			return b.String(), hits
		}

		if i := lastSegmentMatching(len(segs), func(i int) bool { return segs[i].custom }); i >= 0 {
			segs = append(segs[:i], segs[i+1:]...)
			continue
		}
		if len(meters) > 1 {
			meters = meters[1:]
			continue
		}
		if len(meters) == 1 {
			meters = nil
			if i := lastSegmentMatching(len(segs), func(i int) bool { return segs[i].meters }); i >= 0 {
				segs = append(segs[:i], segs[i+1:]...)
			}
			continue
		}
		if i := lastSegmentMatching(len(segs), func(i int) bool { return segs[i].buttons }); i >= 0 {
			switch {
			case !fit.compact:
				fit.compact = true
			case fit.shown > 0:
				fit.shown--
			default:
				segs = append(segs[:i], segs[i+1:]...)
				m.dockButtonsShown = 0
			}
			continue
		}
		return b.String(), hits
	}
}

func lastSegmentMatching(n int, pred func(int) bool) int {
	for i := n - 1; i >= 0; i-- {
		if pred(i) {
			return i
		}
	}
	return -1
}

func (m *OS) dockSwitchedOff(name string) string {
	switch name {
	case config.DockComponentCPU:
		if !m.Settings.ShowCPU {
			return "show_cpu = false"
		}
	case config.DockComponentRAM:
		if !m.Settings.ShowRAM {
			return "show_ram = false"
		}
	case config.DockComponentClock:
		if m.Settings.HideClock {
			return "hide_clock = true"
		}
		if !m.Settings.ShowClock {
			return "show_clock = false"
		}
	}
	return ""
}
