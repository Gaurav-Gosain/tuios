package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The dock as an ordered list of named components.
//
// Every segment the bar draws is a name in one of three lists, and a name the
// user can move, drop, or put their own command beside. What that buys, beyond
// the customisation, is a single place where the bar's membership is decided:
// the layout pass and the draw pass read the same plan, which is what the
// triple-computed layout in this package kept failing to do.
//
// A built-in keeps whatever condition it always had, so naming a component
// makes it eligible to draw rather than certain to. That is what lets the
// default lists reproduce the bar byte for byte.

// dockPlan is the resolved arrangement for one frame: which components are on
// which side, in which order.
type dockPlan struct {
	Left   []string
	Center []string
	Right  []string
	placed map[string]bool
}

// Has reports whether a component is placed anywhere.
func (p dockPlan) Has(name string) bool { return p.placed[name] }

// buildDockPlan resolves the config's lists, dropping names that no built-in
// and no [dock.custom] table defines. A dropped name is reported through
// ConfigWarnings at load; here it just does not draw.
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
			} else if !config.IsDockBuiltin(name) {
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
	// A component with a fixed side that was listed elsewhere is appended to the
	// side it actually draws on, so it never reserves width on one side while
	// drawing on another. The config warning says so at load.
	plan.Center = append(plan.Center, misplaced["center"]...)
	plan.Right = append(plan.Right, misplaced["right"]...)
	return plan
}

// ensureDockPlan fills in the plan for a model that has not built one.
//
// Every dock path goes through this, because a model that was assembled without
// running Init (a test fixture, an embed host, a frame drawn before the engine
// started) still has a bar to draw, and a bar with no plan is a bar with no
// components. Absent config resolves to the default arrangement, which is the
// same answer buildDockPlan gives a config file with no [dock] table.
func (m *OS) ensureDockPlan() {
	if m.dockPlan.placed == nil {
		m.dockPlan = buildDockPlan(m.UserConfig)
	}
}

// dockRefreshableComponents is the set the engine schedules: the three
// built-ins that move on their own, plus every custom cell the plan placed.
//
// Everything else on the bar is drawn from model state on the frames that were
// happening anyway, so it has nothing to schedule and costs nothing to hold.
func dockRefreshableComponents(cfg *config.UserConfig, plan dockPlan, showClock bool) []*dockComponent {
	var comps []*dockComponent

	// Scheduled only when it will actually draw. The clock sits in the default
	// list now, so an "or" here woke a component every minute for every session
	// that had left show_clock off. The meters below read the same way for the
	// same reason: the list says where, the switch says whether. Its cadence
	// comes from the format, so 15:04 wakes once a minute rather than sixty
	// times a second.
	if plan.Has(config.DockComponentClock) && showClock {
		format := cfg.Dock.DockClockFormat()
		comps = append(comps, &dockComponent{
			Name:    config.DockComponentClock,
			Builtin: true,
			Refresh: config.DockRefresh{
				Kind:     config.DockRefreshInterval,
				Interval: config.DockClockInterval(format),
			},
		})
	}
	// The meters keep their own switches: the lists say where they go, show_cpu
	// and show_ram still say whether they are on.
	if plan.Has(config.DockComponentCPU) && config.ShowCPU {
		comps = append(comps, &dockComponent{
			Name:    config.DockComponentCPU,
			Builtin: true,
			Refresh: config.DockRefresh{Kind: config.DockRefreshInterval, Interval: dockMeterInterval},
		})
	}
	if plan.Has(config.DockComponentRAM) && config.ShowRAM {
		comps = append(comps, &dockComponent{
			Name:    config.DockComponentRAM,
			Builtin: true,
			Refresh: config.DockRefresh{Kind: config.DockRefreshInterval, Interval: dockMeterInterval},
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
				// The warning was raised at load; a component whose contract
				// does not parse falls back to the safest one there is.
				refresh = config.DockRefresh{Kind: config.DockRefreshOnce}
			}
			width := spec.MaxWidth
			if width <= 0 || width > config.DockCustomMaxWidthLimit {
				width = config.DockCustomDefaultMaxWidth
			}
			comps = append(comps, &dockComponent{
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

// dockMeterInterval is how often the CPU and RAM readouts are re-measured. The
// RAM figure already self-throttled to this, and the CPU graph has one bar per
// sample, so a faster cadence only moved the graph off the screen sooner.
const dockMeterInterval = 2 * time.Second

// dockCustomHit is where a custom cell was drawn, recorded by the renderer as
// it draws for the reason every other dock rect is: a handler that worked the
// columns out again is a second implementation that can disagree with the frame
// the user clicked on.
type dockCustomHit struct {
	X0, X1, Y int
	Name      string
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

// dockTruncateCell caps a cell at maxWidth cells, measured as rendered rather
// than in bytes so a component emitting wide runes or colour is cut where it
// looks cut.
func dockTruncateCell(text string, maxWidth int) string {
	if text == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = config.DockCustomDefaultMaxWidth
	}
	if lipgloss.Width(text) <= maxWidth {
		return text
	}
	return overlay.Truncate(text, maxWidth)
}

// dockCustomCell is one custom component drawn as a bar cell: the component's
// own text, with a column either side, on the Panel step the rest of the dock's
// quiet chrome rests on.
//
// The ground matters more than it looks. A component may emit SGR, including a
// reset, and a reset on a transparent background punches a hole through the bar
// that the overlay tests exist to forbid. Rendering the cell over Panel means
// the worst a component can do to its neighbours is nothing.
func (m *OS) dockCustomCell(name string) string {
	// The clock keeps its own switch, the way the meters do: the list says
	// where it goes, show_clock still says whether it is on. Gated here rather
	// than per side, so a clock moved to the left or the centre obeys the same
	// switch without the caller having to remember.
	if name == config.DockComponentClock && (!config.ShowClock || config.HideClock) {
		return ""
	}
	text := m.dockEngine.Text(name)
	if name == config.DockComponentClock && text == "" {
		// The clock is a built-in that carries a value, so it draws through the
		// same cell as a user's component does. It falls back to the wall clock
		// for the first frame, before the engine has filled it.
		text = m.DockClockText()
	}
	if text == "" {
		return ""
	}
	pal := theme.UI()
	return lipgloss.NewStyle().
		Background(pal.Panel).
		Foreground(theme.Readable(pal.FgDim, pal.Panel)).
		Render(" " + text + " ")
}

// dockCustomCells renders the custom components on one side, in list order,
// skipping the ones with nothing to say.
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

// dockCellComponent reports whether a component draws as a bar cell from a
// value the engine holds, rather than from model state the renderer reads.
func dockCellComponent(name string) bool {
	return strings.HasPrefix(name, config.DockCustomPrefix) || name == config.DockComponentClock
}

// dockCustomWidth is the room a side's custom cells want.
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
	Source   string `json:"source"` // builtin | custom
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
				info.Text = c.text
				info.LastExit = c.lastExit
				info.LastErr = c.lastErr
				info.Stopped = c.stopped
				if c.Refresh.Interval > 0 {
					info.Interval = c.Refresh.Interval.String()
				}
				if len(c.Refresh.Events) > 0 {
					info.Events = strings.Join(c.Refresh.Events, ",")
				}
				if !c.lastRun.IsZero() {
					info.LastRun = c.lastRun.UTC().Format(time.RFC3339)
				}
			}
			// A built-in drawn from model state is visible whenever its own
			// condition holds, which is not knowable here; a refreshable one is
			// visible exactly when it has text.
			info.Visible = info.Refresh == "render" || info.Text != ""
			out = append(out, info)
		}
	}
	return out
}

// renderDockRightCells draws the right block's cells in plan order and reports
// where each custom one landed inside the block.
//
// The degradation order is the design's, and it is not arbitrary. The meters
// are readouts nobody can act on, so they already yielded their columns to the
// minimized entries; a custom cell yields before either, because it is the one
// thing on the bar tuios cannot reason about the value of. What is left when
// nothing fits is the same thing that was left before components existed.
func (m *OS) renderDockRightCells(room int, meterStyle lipgloss.Style) (string, []dockCustomHit) {
	// One pass over the plan builds the segments in draw order. The two meters
	// are one segment, emitted where the first of them is named, so a plan of
	// ["cpu", "custom/x"] draws the readouts and then the cell.
	type segment struct {
		text   string
		width  int
		name   string
		custom bool // records a hit rectangle, and yields first when room runs out
		meters bool // stands for whatever is left of the CPU and RAM readouts
	}

	meters := m.dockMeterParts()
	var segs []segment
	placedMeters := false
	for _, name := range m.dockPlan.Right {
		switch name {
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
				custom: strings.HasPrefix(name, config.DockCustomPrefix),
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
			if seg.custom {
				hits = append(hits, dockCustomHit{X0: x, X1: x + width, Name: seg.name})
			}
			b.WriteString(text)
			x += width
		}
		if x <= room {
			return b.String(), hits
		}

		// A custom cell yields before a meter does, because it is the one thing
		// on the bar tuios cannot reason about the value of. Then the CPU graph
		// before the RAM figure: a clipped graph reads as noise where a clipped
		// figure still reads as a figure.
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
		// Nothing left to give up. Whatever remains is drawn and the bar's own
		// backstop truncates it.
		return b.String(), hits
	}
}

// lastSegmentMatching is the index of the last of n segments matching pred, or -1.
func lastSegmentMatching(n int, pred func(int) bool) int {
	for i := n - 1; i >= 0; i-- {
		if pred(i) {
			return i
		}
	}
	return -1
}
