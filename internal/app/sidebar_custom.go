package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The rail's custom section: the rows a command of the user's printed.
//
// The command is a dock component in everything but where it draws. It is
// scheduled by the dock engine, with the dock's refresh grammar, debounce,
// sanitiser, timeout and failure rule, because a second scheduler for the
// same kind of subprocess would be a second set of ways for it to go wrong.
// Three things differ. Two are flags on the component: every line is a row
// (MultiLine), and an event that lands mid-run keeps one re-run (Coalesce).
// The third is the run's environment, which carries the focused pane and
// the section's size. The model hands those to the engine under its lock,
// because runs start on the engine's goroutines and may not read the model.

// railCustomComponent is the engine name of the section's command. It is not
// under the dock's custom/ prefix, so it is never drawn as a bar cell, and
// tuios refresh-dock rail/custom reaches it.
const railCustomComponent = "rail/custom"
const railSecondaryComponent = "rail/secondary/custom"

// railCustomState is the model's side of the section.
type railCustomState struct {
	// on records whether the engine was built with the section's command,
	// so the per-message sync can tell a layout change from a frame.
	on bool
	// runnable records whether the config gives the section a command and a
	// refresh the rail honours. Both change only on a config reload, which
	// rebuilds the engine, so it is worked out there and not per message.
	runnable bool
	// gen counts the updates that changed the rows. The render cache folds
	// it, so new output redraws the rail and an unchanged value does not.
	gen uint64
	// ctx is what the engine was last told. Compared before the engine's
	// lock is taken, so a message that moved nothing takes no lock.
	ctx railContext
}

// railCustomEnabled reports whether the layout names the section. The
// section draws its title from this alone. Naming it with no command set
// draws the title over an empty section, so a person who placed it sees
// where it went.
func (m *OS) railCustomEnabled() bool {
	return sidebarLayoutHas(sidebarSectionCustom, &m.Settings)
}

// railCustomConfig is the section's table, or the zero table for a model
// with no config loaded.
func (m *OS) railCustomConfig() config.SidebarCustomConfig {
	if m.UserConfig == nil {
		return config.SidebarCustomConfig{}
	}
	sb := m.UserConfig.Appearance.Sidebar
	if m.Settings.SidebarPosition == "left" && sb.Left != nil && sb.Left.Custom != nil {
		return *sb.Left.Custom
	}
	if m.Settings.SidebarPosition == "right" && sb.Right != nil && sb.Right.Custom != nil {
		return *sb.Right.Custom
	}
	return sb.Custom
}

// railCustomTitle is the section's heading.
func (m *OS) railCustomTitle() string {
	if m.sidebarDrawing {
		return m.secondaryRailCustomConfig().ResolvedTitle()
	}
	return m.railCustomConfig().ResolvedTitle()
}

func (m *OS) secondaryRailCustomConfig() config.SidebarCustomConfig {
	if m.UserConfig == nil {
		return config.SidebarCustomConfig{}
	}
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	if cfg != nil && cfg.Custom != nil {
		return *cfg.Custom
	}
	return config.SidebarCustomConfig{}
}

// railCustomRows is what the section draws, one row per line the command
// printed. Empty after a failure, a timeout or a silent run: the engine
// blanks a failed component's text, so the rows can never be an earlier
// run's.
func (m *OS) railCustomRows() []string {
	component := railCustomComponent
	if m.sidebarDrawing {
		component = railSecondaryComponent
	}
	text := m.dockEngine.Text(component)
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// sidebarCustomRow draws one row: the command's own text, colour included,
// cut to the rail's columns and closed with a reset so a colour the command
// left open cannot run into the padding or the row below.
func (m *OS) sidebarCustomRow(text string, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	body := overlay.Truncate(strings.TrimRight(text, " "), max(cw-1, 0)) + "\x1b[0m"
	return sidebarFit(sidebarStyle(rowBg, nil).Render(" ")+body, cw, rowBg)
}

// railCustomRunnable reports whether the config gives the section something
// to run: a command, and a refresh the rail honours. It parses the refresh,
// so it is asked when the engine is built and its answer kept in
// railCustom.runnable.
func (m *OS) railCustomRunnable() bool {
	custom := m.railCustomConfig()
	if !custom.HasCommand() {
		return false
	}
	_, err := config.ParseSidebarCustomRefresh(custom.Refresh)
	return err == nil
}

// railCustomWanted reports whether the engine should hold the section's
// command: the config gives it one and the layout names the section. The
// flag first, because it is asked once per message and most clients set no
// command, so they never take the layout's mutex here.
func (m *OS) railCustomWanted() bool {
	return m.railCustom.runnable && m.railCustomEnabled()
}

// railCustomComponent is the engine component for the section, or nil when
// the section runs nothing. Built beside the dock's components in
// InitDockComponents, so a config reload rebuilds it with them.
func (m *OS) secondaryRailCustomComponent() *dockComponent {
	custom := m.secondaryRailCustomConfig()
	if !custom.HasCommand() || m.secondarySidebarWidth() == 0 || m.UserConfig == nil {
		return nil
	}
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	sections := config.SidebarDefaultSections
	if cfg != nil && cfg.Sections != "" {
		sections = cfg.Sections
	}
	if !config.SidebarCustomPlaced(sections) {
		return nil
	}
	refresh, err := config.ParseSidebarCustomRefresh(custom.Refresh)
	if err != nil {
		return nil
	}
	return &dockComponent{Name: railSecondaryComponent, Command: custom.Command, Refresh: refresh, MultiLine: true, Coalesce: true}
}

func (m *OS) railCustomComponent() *dockComponent {
	if !m.railCustomWanted() {
		return nil
	}
	custom := m.railCustomConfig()
	refresh, _ := config.ParseSidebarCustomRefresh(custom.Refresh)
	return &dockComponent{
		Name:      railCustomComponent,
		Command:   custom.Command,
		Refresh:   refresh,
		MultiLine: true,
		Coalesce:  true,
	}
}

// railContextNow is what a run started now would be told. Width is the
// columns a row may use: the rail's width less the edge rule and the one
// column inset every row has. Height is the most rows the section's share
// can give it, before the rail's chrome and the other sections' claims: a
// ceiling, as shares are, and the docs say so. A rail collapsed to the glyph
// strip, hidden or turned off draws no rows, so both are zero there, and the
// engine runs nothing while the width is zero (runOnce).
//
// It runs once per message, so it only copies what the model holds. The
// folder is left for the run to resolve (railContext.folder).
func (m *OS) railContextNow() railContext {
	ctx := railContext{}
	if w := m.GetFocusedWindow(); w != nil {
		ctx.PaneID = w.ID
		ctx.Remote = m.AttachedHost != ""
		switch {
		case ctx.Remote, w.Cwd != "" && w.Host == "":
			ctx.PaneDir = w.Cwd
		case w.Host == "":
			ctx.PanePgid = w.ShellPgid
		default:
			// A pane on another machine in a session on this one: nothing
			// here can say where it is, so the run is told the home folder.
		}
	}
	w := m.GetSidebarWidth()
	if w <= 0 || sidebarVariant(w) == sidebarVariantGlyph {
		return ctx
	}
	ctx.Width = max(w-2, 0)
	lines := max(m.ViewUsableHeight()-1, 0)
	for _, p := range sidebarLayoutPlans(&m.Settings) {
		if !p.Spacer && p.Section == sidebarSectionCustom && p.Share > 0 {
			lines = lines * p.Share / 100
		}
	}
	ctx.Height = max(lines, 1)
	return ctx
}

// folder is TUIOS_ACTIVE_PANE_CWD: the folder the command keys would start a
// command for the focused pane in, so a script written for one works for the
// other. Empty with no pane focused. It reads the shell's working directory
// and stats the folder, so it is called when a run starts, on the run's
// goroutine, and never on the update goroutine.
func (ctx railContext) folder() string {
	switch {
	case ctx.PaneID == "":
		return ""
	case ctx.Remote:
		return remoteFolder(ctx.PaneDir)
	}
	raw := ctx.PaneDir
	if raw == "" && ctx.PanePgid != 0 {
		raw, _ = terminal.ShellCWD(ctx.PanePgid)
	}
	return localFolder(raw)
}

// RailCustomSyncCmd keeps the engine in step with the model. It runs once
// per message from Update, after the handler, the way GitSyncCmd does, so
// every path that moves the focus, resizes the rail or edits the layout is
// covered by one comparison here rather than by a hook in each.
//
// Two things can have moved. Whether the section should run at all changes
// when an edit to the layout gains or loses the section; then the engine is
// rebuilt, which is what a dock component gets on a config reload too. The
// per-run context is the other, and syncRailContext hands it over.
func (m *OS) secondaryRailContextNow() railContext {
	ctx := m.railContextNow()
	w := m.secondarySidebarWidth()
	if w <= 0 || sidebarVariant(w) == sidebarVariantGlyph {
		ctx.Width, ctx.Height = 0, 0
		return ctx
	}
	ctx.Width = max(w-2, 0)
	lines := max(m.ViewUsableHeight()-1, 0)
	if m.UserConfig != nil {
		cfg := m.UserConfig.Appearance.Sidebar.Left
		if m.legacySidebarEdge() == sidebarLeft {
			cfg = m.UserConfig.Appearance.Sidebar.Right
		}
		sections := config.SidebarDefaultSections
		if cfg != nil && cfg.Sections != "" {
			sections = cfg.Sections
		}
		plans, _ := sidebarLayoutFor(sections)
		for _, p := range plans {
			if !p.Spacer && p.Section == sidebarSectionCustom && p.Share > 0 {
				lines = lines * p.Share / 100
			}
		}
	}
	ctx.Height = max(lines, 1)
	return ctx
}

func (m *OS) RailCustomSyncCmd() tea.Cmd {
	if m.dockEngine == nil {
		// No engine means no dock either: the client is shutting down or was
		// built without one, and rebuilding here would start one by accident.
		return nil
	}
	var cmd tea.Cmd
	if want := m.railCustomWanted(); want != m.railCustom.on || (m.secondaryRailCustomComponent() != nil) != m.railCustomSecond.on {
		cmd = m.ReloadDockComponents(nil)
	}
	m.syncRailContext()
	return cmd
}

// syncRailContext pushes the per-run context to the engine, under its lock,
// when it differs from the last push, which on an idle client is never.
//
// The per-message sync calls it, and so does NotifyDockEvent before it wakes
// the engine. A run reads the context when it starts, so the event that moved
// the focus must not wake a run before the new pane is handed over. The sync
// after the handler would usually win that race, but only because the
// debounce outlasts the handler.
//
// A rail that opens asks for one run. The engine skips every run while the
// width is zero, so the rows are the last ones from before the rail shut, or
// none when it was shut from the start, and a once or event section would
// keep them until its next trigger.
func (m *OS) syncRailContext() {
	if m.railCustom.on {
		if ctx := m.railContextNow(); ctx != m.railCustom.ctx {
			opened := m.railCustom.ctx.Width <= 0 && ctx.Width > 0
			m.railCustom.ctx = ctx
			m.dockEngine.SetRailContext(ctx)
			if opened {
				m.dockEngine.Rerun(railCustomComponent)
			}
		}
	}
	if m.railCustomSecond.on {
		if ctx := m.secondaryRailContextNow(); ctx != m.railCustomSecond.ctx {
			opened := m.railCustomSecond.ctx.Width <= 0 && ctx.Width > 0
			m.railCustomSecond.ctx = ctx
			m.dockEngine.SetSecondaryRailContext(ctx)
			if opened {
				m.dockEngine.Rerun(railSecondaryComponent)
			}
		}
	}
}

// railCustomInfo is the section's row in list-dock-components. From the
// engine when the command is loaded; from the config otherwise, so a refused
// refresh or a missing command is reported with its reason rather than by
// the row's absence.
func (m *OS) railCustomInfo() DockComponentInfo {
	custom := m.railCustomConfig()
	info := DockComponentInfo{
		Name:    railCustomComponent,
		Side:    "rail",
		Source:  "rail",
		Refresh: "once",
		Command: custom.Command,
	}
	if c, ok := m.dockEngine.Component(railCustomComponent); ok {
		info.Refresh = c.Refresh.Kind.String()
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
		info.Visible = info.Text != ""
		return info
	}
	info.Stopped = true
	_, refreshErr := config.ParseSidebarCustomRefresh(custom.Refresh)
	switch {
	case !custom.HasCommand():
		info.LastErr = "no command is set in [appearance.sidebar.custom], so the section draws its title over nothing"
	case refreshErr != nil:
		info.Refresh = "refused"
		info.LastErr = refreshErr.Error()
	case !m.railCustomEnabled():
		info.LastErr = "the layout in [appearance.sidebar] sections does not place custom, so the command does not run"
	}
	return info
}
