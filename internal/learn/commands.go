package learn

import (
	"maps"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// Commands lists the names RunCommand accepts, for the README and for a test
// that keeps the two in step.
var Commands = []string{
	"action", "agent", "cascade", "celebrate", "closeWindow", "layout", "mode",
	"newWindow", "notify", "reset", "tape", "theme", "tiling", "type", "workspace",
}

// RunCommand lets the page set the scene for a step or play a step for the
// person. It runs on the program's goroutine. An unknown command or a bad
// argument does nothing, since the page cannot do anything useful with an
// error. See cmd/tuios-wasm/README.md for each command.
func (m *Model) RunCommand(name string, args ...string) tea.Cmd {
	o := m.OS
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch name {
	case "notify":
		level := arg(1)
		if level == "" {
			level = "info"
		}
		o.ShowNotification(arg(0), level, 2*o.Settings.NotificationDuration)
	case "newWindow":
		var command []string
		if p := arg(1); p != "" {
			command = args[1:]
		}
		o.AddWindow(arg(0), command...)
		o.MarkAllDirty()
	case "closeWindow":
		id := arg(0)
		if id == "" {
			id = o.GetFocusedWindowID()
		}
		_ = o.CloseWindow(id)
	case "tiling":
		switch arg(0) {
		case "on":
			if !o.AutoTiling {
				_ = o.EnableTiling()
			}
		case "off":
			if o.AutoTiling {
				_ = o.DisableTiling()
			}
		default:
			o.ToggleAutoTiling()
		}
	case "layout":
		for range 3 {
			if o.LayoutModeName() == arg(0) {
				break
			}
			o.ToggleLayoutMode()
		}
	case "cascade":
		m.cascade()
	case "workspace":
		if n, err := strconv.Atoi(arg(0)); err == nil {
			_ = o.SwitchWorkspace(n)
		}
	case "mode":
		_ = o.SetMode(arg(0))
	case "theme":
		_ = o.SetTheme(arg(0))
	case "type":
		// Types into the focused pane, for a step that plays itself.
		if id := o.GetFocusedWindowID(); id != "" {
			_ = o.SendToWindow(id, []byte(arg(0)))
		}
	case "action":
		// Any registry action, through the same dispatcher a key reaches, so
		// the page sees the same action event a key press would produce.
		d := input.GetDispatcher()
		if d.HasAction(arg(0)) {
			next, cmd := d.Dispatch(arg(0), tea.KeyPressMsg{}, o)
			m.OS = next
			return cmd
		}
	case "agent":
		// Sets the focused pane's agent state, for a step about the rail that
		// does not want to wait for the fake agent.
		_ = o.ReportAgentState(app.AgentReport{
			WindowID: o.GetFocusedWindowID(), State: arg(0), Message: arg(1), Kind: arg(2),
			Harness: "claude-code",
		})
	case "tape":
		m.tapeName = arg(1)
		cmd, err := o.PlayTape(arg(1), arg(0))
		if err != nil {
			o.ShowNotification(err.Error(), "warning", o.Settings.NotificationDuration)
		}
		return cmd
	case "celebrate":
		return m.celebrate(args)
	case "reset":
		m.reset()
	}
	return nil
}

// reset puts the tour back where it started: no windows, workspace 1, tiling
// on, window mode, the starting theme, and no overlay open.
func (m *Model) reset() {
	o := m.OS
	for i := len(o.Windows) - 1; i >= 0; i-- {
		o.DeleteWindow(i)
	}
	o.ShowHelp = false
	o.ShowCommandPalette = false
	o.ShowLauncher = false
	o.ShowSettings = false
	o.ShowThemePicker = false
	o.ShowKeybindManager = false
	o.ShowWorkspaceSwitcher = false
	o.ShowLayoutPicker = false
	o.ShowScrollbackBrowser = false
	o.ShowLogs = false
	if o.ShowQuitMenu {
		o.CloseQuitMenu()
	}
	o.PrefixActive = false
	o.WorkspacePrefixActive = false
	o.TilingPrefixActive = false
	o.MinimizePrefixActive = false
	o.LayoutPrefixActive = false
	o.DebugPrefixActive = false
	o.TapePrefixActive = false
	_ = o.SwitchWorkspace(1)
	if o.LayoutModeName() != config.LayoutModeBSP {
		m.RunCommand("layout", config.LayoutModeBSP)
	}
	if !o.AutoTiling {
		_ = o.EnableTiling()
	}
	_ = o.SetMode("window")
	if m.theme != "" && m.theme != take(o).Theme {
		_ = o.SetTheme(m.theme)
	}
	o.MarkAllDirty()
}

// cascade turns tiling off and slides the windows on the current workspace
// into an overlapping diagonal, back to front in their stacking order, so the
// focused window ends on top at the bottom right. It sets the scene for a
// tiling step: turning tiling off alone leaves every window where the tiler
// put it, and turning it on again would then change nothing on screen.
func (m *Model) cascade() {
	o := m.OS
	if o.AutoTiling {
		_ = o.DisableTiling()
	}
	var wins []*terminal.Window
	for _, w := range o.Windows {
		if w != nil && w.Workspace == o.CurrentWorkspace && !w.Minimized {
			wins = append(wins, w)
		}
	}
	if len(wins) == 0 {
		return
	}
	sort.SliceStable(wins, func(i, j int) bool { return wins[i].Z < wins[j].Z })

	left, top := o.GetLeftMargin(), o.GetTopMargin()
	areaW, areaH := o.GetContentWidth(), o.GetUsableHeight()
	width, height := areaW*3/5, areaH*3/5
	// Spread the windows over the free space, with a small margin, so each
	// one shows its title bar and a corner of the window under it.
	stepX, stepY := 0, 0
	if n := len(wins) - 1; n > 0 {
		stepX = (areaW - width - 4) / n
		stepY = (areaH - height - 2) / n
	}
	dur := o.Settings.GetAnimationDuration()
	for i, w := range wins {
		o.CancelSnapAnimation(w)
		x, y := left+2+i*stepX, top+1+i*stepY
		if anim := ui.NewSnapAnimation(w, x, y, width, height, dur); anim != nil {
			o.Animations = append(o.Animations, anim)
		} else {
			w.X, w.Y = x, y
			w.Resize(width, height)
		}
		w.InvalidateCache()
	}
	o.MarkAllDirty()
}

// celebrate runs the in-app confetti burst. Each argument is one of:
//
//	"big"    the larger finale burst
//	"still"  a static sparkle, for a page that honours prefers-reduced-motion
//	"x,y"    burst from that screen cell
//	anything else is a window id to burst from
//
// With no cell and no window it bursts from the focused pane.
func (m *Model) celebrate(args []string) tea.Cmd {
	var opts app.CelebrateOptions
	windowID := ""
	cellX, cellY, haveCell := 0, 0, false
	for _, a := range args {
		switch a {
		case "":
		case "big":
			opts.Big = true
		case "still":
			opts.Still = true
		default:
			if xs, ys, ok := strings.Cut(a, ","); ok {
				x, errX := strconv.Atoi(strings.TrimSpace(xs))
				y, errY := strconv.Atoi(strings.TrimSpace(ys))
				if errX == nil && errY == nil {
					cellX, cellY, haveCell = x, y, true
					continue
				}
			}
			windowID = a
		}
	}
	switch {
	case haveCell:
		return m.OS.CelebrateAt(cellX, cellY, opts)
	case windowID != "":
		return m.OS.CelebrateWindow(windowID, opts)
	default:
		return m.OS.Celebrate(opts)
	}
}

// LauncherApps is what the launcher offers in the tour: the fake shell's
// programs, which a guest pty runs when a pane asks for them by path.
func LauncherApps() []applist.Entry {
	var out []applist.Entry
	for _, p := range webshell.Programs() {
		out = append(out, applist.Entry{
			Name:   p.Name,
			Path:   "/usr/bin/" + p.Name,
			Dir:    "/usr/bin",
			Detail: p.Summary,
		})
	}
	return out
}

// Actions maps every registry action to its description, so the page can
// explain a key that did something other than the step wanted.
func Actions() map[string]string {
	out := make(map[string]string, len(config.ActionDescriptions))
	maps.Copy(out, config.ActionDescriptions)
	return out
}
