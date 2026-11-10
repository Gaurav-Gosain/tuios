package main

import (
	"math"
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/dockengine"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The rail's sections from the TUI's config, appearance.sidebar.sections:
// which of them the rail shows, and in what order. guios maps the TUI's
// sections onto its own:
//
//	agents               the Agents and Needs you rows
//	sessions, terminals  the project groups and the machines
//	files                Files
//	custom               the rows a command prints ([appearance.sidebar.custom])
//	pane                 a program's terminal ([appearance.sidebar.pane])
//
// git and spacer are left out: guios has no git section (the branch is in
// the session row), and its rail scrolls, so shares and spacers have nothing
// to divide. A config that names no layout of its own, the shipped one
// included, keeps guios's order, so the rail of a person who never set the
// key does not change under them.

// railDefaultOrder is guios's own order.
var railDefaultOrder = []string{"agents", "sessions", "files"}

// railOrder is the sections the rail shows, in order, for the config's
// appearance.sidebar.sections.
func railOrder(src string) []string {
	if strings.TrimSpace(src) == "" || src == config.SidebarDefaultSections {
		return railDefaultOrder
	}
	var out []string
	for _, e := range config.ParseSidebarSections(src) {
		name := e.Name
		switch name {
		case "terminals":
			name = "sessions"
		case "git", config.SidebarSectionSpacer:
			continue
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// railSections draws the sections of the rail between the search field and
// the ended sessions, in the config's order.
func (a *App) railSections(c *ui.Context, pal *palette) {
	order := railOrder(a.set.RailSections)
	a.syncRailCustom(slices.Contains(order, config.SidebarSectionCustom))
	n := 0  // the rows shown, for ⌘1–9 (Ctrl+1–9 elsewhere)
	gi := 0 // the groups shown, the first of which sits higher
	for _, name := range order {
		switch name {
		case "agents":
			a.agentsRow(c, pal)
			a.needsSection(c, pal)
		case "sessions":
			// This machine's projects first, unlabeled; then each host's,
			// under its name.
			if local := a.local(); local.loaded {
				for _, g := range a.hostGroups(local) {
					a.groupView(c, pal, g, gi, &n)
					gi++
				}
				if len(a.st.Sessions) == 0 && local.conn == connReady && len(a.hosts) == 1 {
					ui.Text(c, locales.SidebarNoSessions()).FontSize(fsSmall).TextColor(pal.textFaint).Padding(8, 10).MaxLines(2)
				}
			}
			// Then the machines this machine's daemon links, each with its
			// sessions under it.
			a.machinesSection(c, pal, &gi, &n)
		case "files":
			a.filesRow(c, pal)
		case config.SidebarSectionCustom:
			a.railCustomView(c, pal)
		case config.SidebarSectionPane:
			a.railPaneView(c, pal)
		}
	}
}

// railSectionHead is a section's heading, as the minimized group's.
func railSectionHead(c *ui.Context, pal *palette, key, title string) {
	ui.Row(c.Key(key)).Height(hControl).Margin(14, 0, 2, 0).Padding(0, 10, 0, rowInset).AlignItems(ui.Center).Children(func() {
		oneLine(c, title, fsSmall, wMedium).TextColor(pal.textMuted)
	})
}

// railCustom is the custom section's: the dock engine that runs its command
// in this process, as the TUI's runs it in its client, and what it was
// built for.
type railCustom struct {
	eng     *dockengine.Engine
	stop    chan struct{}
	command string
	refresh string
	ctx     dockengine.RailContext
}

// syncRailCustom starts, replaces or stops the engine as the config and the
// section's place in the rail say, and hands it the focused pane.
func (a *App) syncRailCustom(shown bool) {
	rc := &a.railCustom
	cfg := a.set.RailCustom
	want := shown && cfg.HasCommand()
	if rc.eng != nil && (!want || rc.command != cfg.Command || rc.refresh != cfg.Refresh) {
		rc.eng.Stop()
		close(rc.stop)
		*rc = railCustom{}
	}
	if !want {
		return
	}
	if rc.eng == nil {
		refresh, err := config.ParseSidebarCustomRefresh(cfg.Refresh)
		if err != nil {
			return
		}
		eng := dockengine.New([]*dockengine.Component{{
			Name: dockengine.RailComponent, Command: cfg.Command, Refresh: refresh, MultiLine: true, Coalesce: true,
		}}, railFolder)
		stop := make(chan struct{})
		*rc = railCustom{eng: eng, stop: stop, command: cfg.Command, refresh: cfg.Refresh}
		go func() {
			for {
				select {
				case <-stop:
					return
				case u := <-eng.Updates():
					a.update(func() {
						if a.railCustom.eng != eng {
							return
						}
						if changed, _ := eng.ApplyUpdate(u); changed && a.win != nil {
							a.win.Invalidate()
						}
					})
				}
			}
		}()
		eng.SetRailContext(a.railContext())
		eng.Start()
	}
	if ctx := a.railContext(); ctx != rc.ctx {
		focus := ctx.PaneID != rc.ctx.PaneID
		rc.ctx = ctx
		rc.eng.SetRailContext(ctx)
		if focus {
			// The rows are about the focused pane: a new one runs the
			// command again, as the TUI's focus event does.
			rc.eng.Rerun(dockengine.RailComponent)
		}
	}
}

// railFolder is the folder the command is told (TUIOS_ACTIVE_PANE_CWD): the
// focused pane's, when it is a folder of this machine.
func railFolder(ctx dockengine.RailContext) string {
	if ctx.Remote || ctx.PaneDir == "" {
		return ""
	}
	if st, err := os.Stat(ctx.PaneDir); err == nil && st.IsDir() {
		return ctx.PaneDir
	}
	return ""
}

// railCols is how many cells of the rail's text a row has, for the
// commands the sections run.
func (a *App) railCols() int {
	w := a.prefs.SidebarWidth - 2*10 - rowInset - 10
	return max(int(w/float32(math.Ceil(fsSmall*0.6))), 1)
}

// railContext is what a run of the custom command is told: the focused
// pane, and the section's width in cells.
func (a *App) railContext() dockengine.RailContext {
	ctx := dockengine.RailContext{Height: 8}
	if a.sidebarShown() {
		ctx.Width = a.railCols()
	}
	s := a.session(a.selected)
	if s == nil {
		return ctx
	}
	ctx.PaneID = a.focusOf(s)
	ctx.Remote = a.hostOf(s.ID).remote()
	if p := s.PaneByID(ctx.PaneID); p != nil {
		ctx.PaneDir = p.WorkDir()
	}
	return ctx
}

// railCustomView is the custom section: its title and the command's rows,
// in the rail's ink, colour dropped and cut to the row.
func (a *App) railCustomView(c *ui.Context, pal *palette) {
	cfg := a.set.RailCustom
	if !cfg.HasCommand() {
		return
	}
	railSectionHead(c, pal, "rail-custom-head", cfg.ResolvedTitle())
	text := a.railCustom.eng.Text(dockengine.RailComponent)
	if text == "" {
		return
	}
	ui.Column(c.Key("rail-custom")).Padding(0, 10, 0, rowInset).Margin(0, 0, 10, 0).Gap(2).Children(func() {
		for i, line := range strings.Split(text, "\n") {
			ui.Text(c.Key(i), ansi.Strip(line)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
}

// railPaneRows is the pane section's height in lines: its config's, or 8.
func (a *App) railPaneRows() int {
	if h := a.set.RailPane.Height; h > 0 {
		return min(h, 60)
	}
	return 8
}

// railPaneView is the pane section: the session's rail pane, which the daemon
// starts for the command line the config names, drawn at the section's
// height. A TUI on the session with the same line shows the same pane.
func (a *App) railPaneView(c *ui.Context, pal *palette) {
	cfg := a.set.RailPane
	line := cfg.CommandLine()
	s := a.session(a.selected)
	if line == "" || s == nil {
		return
	}
	railSectionHead(c, pal, "rail-pane-head", cfg.ResolvedTitle())
	rows := a.railPaneRows()
	h := float32(math.Ceil(float64(a.set.FontSize*a.set.LineHeight*1.32)))*float32(rows) + 12
	box := ui.Box(c.Key("rail-pane")).Height(h).Margin(0, 0, 0, rowInset).Radius(rRow).Clip().Background(pal.control)
	box.Children(func() {
		switch {
		case s.RailPane != nil && s.RailKey == line:
			a.railPaneAsked[s.ID], a.railPaneSeen[s.ID] = line, line
			e := a.terms[s.RailPane.ID]
			if e == nil {
				a.ensureTerm(s.RailPane.ID)
				e = a.terms[s.RailPane.ID]
			}
			if e != nil && e.term != nil {
				terminal.View(c.Key("term"), e.term).Fill()
			}
		case a.railPaneAsked[s.ID] == line:
			// Asked, and the push has not come, or the program stopped:
			// it was shown, and is gone.
			if a.railPaneSeen[s.ID] == line {
				a.railPaneStopped(c, pal, s, line, rows)
			}
		default:
			a.startRailPane(s, line, rows)
		}
	})
}

// startRailPane asks the daemon for the session's rail pane.
func (a *App) startRailPane(s *proto.Session, line string, rows int) {
	sid := s.ID
	a.railPaneAsked[sid] = line
	dir := ""
	if !a.hostOf(sid).remote() {
		dir, _ = os.UserHomeDir()
	}
	a.call(sid, "rail.pane", proto.RailPaneParams{Session: sid, Command: line, Cols: a.railCols(), Rows: rows, Dir: dir}, nil)
}

// railPaneStopped is the section once its program has ended: a line that
// says so, and a button that starts it again.
func (a *App) railPaneStopped(c *ui.Context, pal *palette, s *proto.Session, line string, rows int) {
	ui.Row(c).Fill().Center().Gap(6).Children(func() {
		ui.Text(c, locales.RailPaneStopped()).FontSize(fsSmall).TextColor(pal.textFaint)
		b := ui.ButtonBase(c.Key("again")).Padding(2, 6).Radius(rControl).Label(locales.RailPaneStartAgain()).Cursor(ui.CursorPointer)
		if b.Hovered() {
			b.Background(pal.rowHover)
		}
		if b.Clicked() {
			delete(a.railPaneSeen, s.ID)
			a.startRailPane(s, line, rows)
		}
		b.Children(func() {
			ui.Text(c, locales.RailPaneStartAgain()).FontSize(fsSmall).TextColor(pal.accentText)
		})
	})
}
