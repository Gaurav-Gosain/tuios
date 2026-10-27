package learn

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// Commands lists the names RunCommand accepts, for the README and for a test
// that keeps the two in step.
var Commands = []string{
	"action", "agent", "cascade", "celebrate", "closeWindow", "layout", "mode",
	"newWindow", "notify", "reset", "set", "tape", "theme", "themeJSON", "tiling", "type", "workspace",
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
		m.setTheme(arg(0))
	case "themeJSON":
		// A theme a look carries, as the JSON of a theme file. It is checked
		// as tuios look apply checks it, registered in this tab only as
		// look-<id>, and selected. A theme that fails the check does nothing.
		id := config.LookAssetPrefix + strings.TrimPrefix(arg(0), config.LookAssetPrefix)
		if err := theme.RegisterLookTheme(id, []byte(arg(1))); err == nil {
			m.setTheme(id)
		}
	case "set":
		// Any option tuios set-config takes, by the same path, such as
		// appearance.glyphs or appearance.background. It changes this tab's
		// copy of the config and nothing on disk. A docs page uses it to
		// start a demo in the look the page is about.
		// Only an option that changes how the screen looks goes through. See
		// pageSettable for the ones refused.
		path, ok := pageSettable(arg(0))
		switch {
		case !ok:
		case config.CheckLookItems(path, arg(1)) != nil:
			// A list item a look leaves out, such as a custom dock cell.
		case path == "appearance.theme":
			// The theme command's path, which also recolours the panes.
			m.setTheme(arg(1))
		case path == "startup.tiled":
			// A tab has no new session to start, so tiling changes now.
			if on, err := strconv.ParseBool(arg(1)); err == nil && on != o.AutoTiling {
				o.ToggleAutoTiling()
			}
		case path == "startup.layout":
			// Only a scheme tuios look apply takes. The layout command
			// cycles three times on a name it does not know, which ends
			// where it started but flashes three notifications.
			if slices.Contains(config.LayoutModes, arg(1)) {
				m.RunCommand("layout", arg(1))
			}
		case config.IsLookFileKey(path):
			// The dock's lists and its tabs table: the dock reads them from
			// the config on each frame.
			if err := config.SetLookValue(o.UserConfig, path, arg(1)); err == nil {
				o.MarkAllDirty()
			}
		default:
			if err := o.SetConfig(path, arg(1)); err == nil {
				o.MarkAllDirty()
			}
		}
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
// on, window mode, the starting theme, the shipped keys, the rail closed, and
// no overlay, beam or picture-in-picture.
func (m *Model) reset() {
	o := m.OS
	o.HideShownScratch()
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
	if o.ShowInbox {
		o.CloseInbox()
	}
	if o.ShowAgentMail {
		o.CloseAgentMail()
	}
	o.ClosePresetPicker()
	o.CloseGotoPicker()
	o.CloseHints()
	o.SetSpotlight(false)
	o.UnpinPiP()
	if o.SidebarActive() {
		o.ExitSidebarFocus()
		o.ToggleSidebar()
	}
	o.PrefixActive = false
	o.WorkspacePrefixActive = false
	o.TilingPrefixActive = false
	o.MinimizePrefixActive = false
	o.LayoutPrefixActive = false
	o.DebugPrefixActive = false
	o.TapePrefixActive = false
	o.TuiosPrefixActive = false
	o.Menu = ""
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
	// A lesson can switch to another preset. The next one starts on the
	// shipped keys.
	_ = o.LearnApplyPreset(config.PresetTuios)
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

// pageSections are the config sections whose options only change how the
// screen looks or how input moves through it.
//
// The rest name a host program, a file or folder, the daemon, or startup:
// tape.autorun, screenshot.directory, launcher.gui_command,
// notifications.agent.command, daemon.*. None of them work in a browser tab,
// where os/exec and the file system fail, but a page has no reason to reach
// them, and a section added later stays shut until it is listed here.
var pageSections = map[string]bool{
	"appearance": true, "dock": true, "sidebar": true, "scrollbar": true,
	"selection": true, "screensaver": true, "spotlight": true, "pip": true,
	"hints": true, "panes": true, "scratch": true, "workspaces": true,
}

// pageSettable reports the registry path the page's set command may change
// for path, or false. It takes the spellings OS.SetConfig takes: the full
// path, or the bare name of an appearance option. Every look key goes
// through as well, so the gallery's Try it shows what tuios look apply sets.
// An unknown path is refused, so it does nothing.
func pageSettable(path string) (string, bool) {
	if config.IsLookKey(path) {
		return path, true
	}
	if path == "animations" {
		path = "appearance.animations_enabled"
	}
	opt, ok := config.LookupOption(path)
	if !ok && !strings.Contains(path, ".") {
		path = "appearance." + path
		opt, ok = config.LookupOption(path)
	}
	if !ok || !pageSections[opt.Section] || config.ReachesHost(path) {
		return "", false
	}
	return path, true
}

// setTheme switches the theme and records it in the tab's config. A set call
// re-applies that config, so without the record the next set would put back
// the theme the config held.
func (m *Model) setTheme(name string) {
	if err := m.OS.SetTheme(name); err == nil && m.OS.UserConfig != nil {
		_ = config.SetOptionValue(m.OS.UserConfig, "appearance.theme", name)
	}
}
