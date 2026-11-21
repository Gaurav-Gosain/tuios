package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/dockengine"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	railCustomComponent    = dockengine.RailComponent
	railSecondaryComponent = dockengine.RailSecondaryComponent
)

type railCustomState struct {
	on       bool
	runnable bool
	gen      uint64
	ctx      dockengine.RailContext
}

func (m *OS) railCustomEnabled() bool {
	return sidebarLayoutHas(sidebarSectionCustom, &m.Settings)
}

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
	if sb.Position == "hidden" {
		return config.SidebarCustomConfig{}
	}
	return sb.Custom
}

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

func (m *OS) sidebarCustomRow(text string, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	text = strings.TrimRight(text, " ")
	var body string
	if strings.Contains(text, "\x1b]8;") {
		body = ansi.Truncate(text, max(cw-1, 0), overlay.Ellipsis())
	} else {
		body = overlay.Truncate(text, max(cw-1, 0))
	}
	body += "\x1b[0m"
	return sidebarFit(sidebarStyle(rowBg, nil).Render(" ")+body, cw, rowBg)
}

type railRowLink struct {
	url    string
	x0, x1 int
}

func railRowLinks(text string) []railRowLink {
	if !strings.Contains(text, "\x1b]8;") {
		return nil
	}
	var links []railRowLink
	col, start, open := 0, 0, ""
	state := byte(ansi.NormalState)
	for text != "" {
		seq, width, n, next := ansi.DecodeSequence(text, state, nil)
		state = next
		text = text[n:]
		if body, ok := strings.CutPrefix(seq, "\x1b]8;"); ok {
			body = strings.TrimSuffix(strings.TrimSuffix(body, "\x1b\\"), "\x07")
			_, url, _ := strings.Cut(body, ";")
			if open != "" && col > start {
				links = append(links, railRowLink{url: open, x0: start, x1: col})
			}
			open, start = url, col
			continue
		}
		col += width
	}
	if open != "" && col > start {
		links = append(links, railRowLink{url: open, x0: start, x1: col})
	}
	return links
}

func (m *OS) railCustomRunnable() bool {
	custom := m.railCustomConfig()
	if !custom.HasCommand() {
		return false
	}
	_, err := config.ParseSidebarCustomRefresh(custom.Refresh)
	return err == nil
}

func (m *OS) railCustomWanted() bool {
	return m.railCustom.runnable && m.railCustomEnabled() && !m.sidebarSessionVisible(m.legacySidebarEdge())
}

func (m *OS) secondaryRailCustomComponent() *dockengine.Component {
	custom := m.secondaryRailCustomConfig()
	if !custom.HasCommand() || m.secondarySidebarWidth() == 0 || m.UserConfig == nil {
		return nil
	}
	other := sidebarLeft
	if m.legacySidebarEdge() == sidebarLeft {
		other = sidebarRight
	}
	if m.sidebarSessionVisible(other) {
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
	return &dockengine.Component{Name: railSecondaryComponent, Command: custom.Command, Refresh: refresh, MultiLine: true, Coalesce: true}
}

func (m *OS) railCustomComponent() *dockengine.Component {
	if !m.railCustomWanted() {
		return nil
	}
	custom := m.railCustomConfig()
	refresh, _ := config.ParseSidebarCustomRefresh(custom.Refresh)
	return &dockengine.Component{
		Name:      railCustomComponent,
		Command:   custom.Command,
		Refresh:   refresh,
		MultiLine: true,
		Coalesce:  true,
	}
}

func (m *OS) railContextNow() dockengine.RailContext {
	ctx := dockengine.RailContext{}
	if w := m.GetFocusedWindow(); w != nil {
		ctx.PaneID = w.ID
		ctx.Remote = m.AttachedHost != ""
		switch {
		case ctx.Remote, w.Cwd != "" && w.Host == "":
			ctx.PaneDir = w.Cwd
		case w.Host == "":
			ctx.PanePgid = w.ShellPgid
		default:
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

func railFolder(ctx dockengine.RailContext) string {
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

func (m *OS) secondaryRailContextNow() dockengine.RailContext {
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
		return nil
	}
	var cmd tea.Cmd
	if want := m.railCustomWanted(); want != m.railCustom.on || (m.secondaryRailCustomComponent() != nil) != m.railCustomSecond.on {
		cmd = m.ReloadDockComponents(nil)
	}
	m.syncRailContext()
	return cmd
}

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
