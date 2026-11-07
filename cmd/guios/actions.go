package main

import (
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/plugins/updater"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// run runs one of the app's actions, from a menu, a key or the palette,
// on the main thread.
func (a *App) run(id string, args map[string]string) {
	s := a.session(a.selected)
	switch id {
	case "settings.open":
		a.openSettings("")
	case "cli.install":
		a.installCLI()
	case "app.quitAll":
		a.quitAndEnd()
	case "session.new":
		switch dir := args["dir"]; {
		case dir != "":
			a.newSessionIn(a.selectedHost(), dir)
		default:
			a.newSession(args["project"])
		}
	case "session.newHome":
		if h := a.selectedHost(); h.remote() {
			a.newSessionIn(h, "~")
		} else {
			a.newSessionIn(h, proto.Home())
		}
	case "session.newWorktree":
		a.newWorktreeSession()
	case "project.open":
		a.openProject()
	case "host.add":
		a.openSettings("hosts")
	case "palette.open":
		a.openPalette()
	case "conversation.resume":
		a.openConversations()
	case "session.attention":
		a.jumpToAttention()
	case "agents.toggle":
		a.toggleCockpit()
	case "agents.new":
		// On the board, with a card selected, the agent starts on the
		// card, as n did.
		if b := a.ck.tabs.board; a.ck.open && a.ck.view == viewBoard && b != nil {
			if t, ok := b.card(b.sel); ok {
				a.newAgentFor(t)
				return
			}
		}
		a.newAgent()
	case "session.next":
		a.selectOffset(1)
	case "session.previous":
		a.selectOffset(-1)
	case "session.rename":
		a.startRename(a.selected)
	case "pane.restart":
		if s != nil {
			a.restartSession(a.focusOf(s))
		}
	case "pane.close":
		a.closeFocused()
	case "session.close":
		a.closeSession(a.selected, true)
	case "find.open":
		// In the viewer, its file's find bar, or its diff's.
		if v := a.viewOf(s); a.side.focused && v != nil {
			if v.kind == sideFiles && v.file != nil && v.file.st != nil {
				v.file.st.Find("")
				break
			}
			if f := a.side.changes.focus; v.kind == sideChanges && f != nil && f.d != nil {
				f.d.Find("")
				break
			}
		}
		a.openFind()
	case "find.next":
		a.findStep(false)
	case "find.previous":
		a.findStep(true)
	case "pane.new":
		// As the TUI's new window: the focused pane splits on the side
		// the layout picks.
		if s == nil {
			a.run("session.new", args)
			return
		}
		a.splitPane(s, "", "")
	case "pane.split.right":
		if s == nil {
			a.run("session.new", args)
			return
		}
		a.splitPane(s, "", "right")
	case "pane.split.down":
		a.splitPane(s, "", "down")
	case "pane.browser":
		a.openBrowser(args["url"])
	case "browser.address":
		a.focusAddress()
	case "pane.zoom":
		a.zoomPane(s, "")
	case "pane.next":
		a.cyclePane(1)
	case "pane.previous":
		a.cyclePane(-1)
	case "pane.left", "pane.right", "pane.up", "pane.down":
		a.focusDirection(id[len("pane."):])
	case "layout.bsp", "layout.master", "layout.scrolling", "layout.floating":
		a.arrange(s, "mode", func(p *proto.ArrangeParams) { p.Mode = layoutModes[id] })
	case "layout.next":
		a.arrange(s, "next", nil)
	case "layout.tiling":
		a.arrange(s, "tiling", nil)
	case "layout.rotate":
		a.arrange(s, "rotate", nil)
	case "layout.equalize":
		a.arrange(s, "equalize", nil)
	case "layout.float":
		a.arrange(s, "float", nil)
	case "layout.master.position", "layout.master.add", "layout.master.remove", "layout.master.swap":
		a.arrange(s, strings.TrimPrefix(id, "layout."), nil)
	case "layout.master.focus":
		if s != nil && s.LayoutMode == "master-stack" && len(s.Panes) > 0 {
			a.setFocus(s, paneOrder(s)[0])
			a.focusTerminal()
		}
	case "layout.scroll.width", "layout.scroll.max", "layout.scroll.consume", "layout.scroll.expel", "layout.scroll.moveLeft", "layout.scroll.moveRight":
		a.arrange(s, strings.TrimPrefix(id, "layout."), nil)
	case "layout.snap.left", "layout.snap.right", "layout.snap.full", "layout.snap.unsnap":
		if s != nil && s.LayoutMode != "floating" {
			a.toast(locales.LayoutSnapNeedsFloating())
			break
		}
		a.arrange(s, "snap", func(p *proto.ArrangeParams) { p.Mode = strings.TrimPrefix(id, "layout.snap.") })
	case "workspace.next":
		a.stepWorkspace(s, 1)
	case "workspace.previous":
		a.stepWorkspace(s, -1)
	case "workspace.new":
		a.newWorkspace(s)
	case "workspace.1", "workspace.2", "workspace.3", "workspace.4", "workspace.5", "workspace.6", "workspace.7", "workspace.8", "workspace.9":
		a.showWorkspace(s, int(id[len(id)-1]-'0'))
	case "pane.workspace.1", "pane.workspace.2", "pane.workspace.3", "pane.workspace.4", "pane.workspace.5", "pane.workspace.6", "pane.workspace.7", "pane.workspace.8", "pane.workspace.9":
		a.movePaneTo(s, "", int(id[len(id)-1]-'0'))
	case "sidebar.toggle":
		a.toggleSidebar()
	case "changes.toggle":
		a.toggleSide(sideChanges)
	case "files.toggle":
		a.toggleSide(sideFiles)
	case "rightSidebar.toggle":
		a.toggleRight()
	case "text.bigger":
		a.zoom(1, false)
	case "text.smaller":
		a.zoom(-1, false)
	case "text.reset":
		a.zoom(0, true)
	case quickAction:
		a.toggleQuick()
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// item is a menu item running an action, with the user's shortcut.
func (a *App) item(id string) *mygo.MenuItem {
	act, _ := actions.Find(id)
	return &mygo.MenuItem{
		Label:       actionTitle(id),
		Accelerator: a.accelerator(act),
		Click: func(*mygo.MenuItem, *mygo.Window) {
			// Closing is the window's in front: Settings closes itself.
			if (id == "pane.close" || id == "session.close") && a.setWin != nil && mygo.FocusedWindow() == a.setWin {
				a.setWin.Close()
				return
			}
			if fastActions[id] && a.win != nil {
				// GTK runs the item on the main thread: the action goes
				// at once, not through the main loop's queue, which
				// held it about 11 ms past the key's frame.
				a.run(id, nil)
				a.win.Invalidate()
				return
			}
			a.update(func() { a.run(id, nil) })
		},
	}
}

// fastActions are the actions whose first frame the person waits for:
// the sidebar and the workspaces. Their menu items run them at once.
var fastActions = func() map[string]bool {
	m := map[string]bool{"sidebar.toggle": true, "workspace.next": true, "workspace.previous": true}
	for n := 1; n <= 9; n++ {
		m["workspace."+itoa(n)] = true
	}
	return m
}()

// actionTitles are the actions' titles in the language the app shows;
// an Action's Title stays English, for `kero actions`.
var actionTitles = map[string]func() string{
	"settings.open":           locales.ActionSettings,
	"cli.install":             locales.ActionInstallCli,
	"app.quitAll":             locales.ActionQuitAll,
	"session.new":             locales.ActionNewSession,
	"session.newHome":         locales.ActionNewSessionHome,
	"session.newWorktree":     locales.ActionNewSessionWorktree,
	"project.open":            locales.ActionOpenProject,
	"host.add":                locales.ActionAttachMachine,
	"palette.open":            locales.ActionGoToSession,
	"conversation.resume":     locales.ActionResumeConversation,
	"session.attention":       locales.ActionJumpToAttention,
	"agents.toggle":           locales.ActionAgents,
	"agents.new":              locales.ActionNewAgent,
	"session.next":            locales.ActionNextSession,
	"session.previous":        locales.ActionPreviousSession,
	"session.rename":          locales.ActionRenameSession,
	"pane.restart":            locales.ActionRestartPane,
	"pane.close":              locales.ActionClose,
	"session.close":           locales.ActionCloseSession,
	"find.open":               locales.ActionFind,
	"find.next":               locales.ActionFindNext,
	"find.previous":           locales.ActionFindPrevious,
	"pane.split.right":        locales.ActionSplitRight,
	"pane.split.down":         locales.ActionSplitDown,
	"pane.browser":            locales.ActionNewBrowser,
	"browser.address":         locales.ActionOpenLocation,
	"pane.zoom":               locales.ActionZoomPane,
	"pane.next":               locales.ActionNextPane,
	"pane.previous":           locales.ActionPreviousPane,
	"pane.left":               locales.ActionPaneLeft,
	"pane.right":              locales.ActionPaneRight,
	"pane.up":                 locales.ActionPaneAbove,
	"pane.down":               locales.ActionPaneBelow,
	"sidebar.toggle":          locales.ActionToggleSidebar,
	"changes.toggle":          locales.ActionToggleChanges,
	"files.toggle":            locales.ActionToggleFiles,
	"rightSidebar.toggle":     locales.ActionToggleRightSidebar,
	"text.bigger":             locales.ActionBigger,
	"text.smaller":            locales.ActionSmaller,
	"text.reset":              locales.ActionActualSize,
	quickAction:               locales.ActionQuickTerminal,
	"workspace.next":          locales.ActionNextWorkspace,
	"layout.bsp":              locales.ActionLayoutBsp,
	"layout.master":           locales.ActionLayoutMaster,
	"layout.scrolling":        locales.ActionLayoutScrolling,
	"layout.floating":         locales.ActionLayoutFloating,
	"layout.next":             locales.ActionLayoutNext,
	"layout.tiling":           locales.ActionLayoutTiling,
	"layout.rotate":           locales.ActionLayoutRotate,
	"layout.equalize":         locales.ActionLayoutEqualize,
	"layout.float":            locales.ActionLayoutFloat,
	"layout.master.position":  locales.ActionLayoutMasterPosition,
	"layout.master.add":       locales.ActionLayoutMasterAdd,
	"layout.master.remove":    locales.ActionLayoutMasterRemove,
	"layout.master.swap":      locales.ActionLayoutMasterSwap,
	"layout.master.focus":     locales.ActionLayoutMasterFocus,
	"layout.scroll.width":     locales.ActionLayoutScrollWidth,
	"layout.scroll.max":       locales.ActionLayoutScrollMax,
	"layout.scroll.consume":   locales.ActionLayoutScrollConsume,
	"layout.scroll.expel":     locales.ActionLayoutScrollExpel,
	"layout.scroll.moveLeft":  locales.ActionLayoutScrollMoveLeft,
	"layout.scroll.moveRight": locales.ActionLayoutScrollMoveRight,
	"layout.snap.left":        locales.ActionLayoutSnapLeft,
	"layout.snap.right":       locales.ActionLayoutSnapRight,
	"layout.snap.full":        locales.ActionLayoutSnapFull,
	"layout.snap.unsnap":      locales.ActionLayoutSnapUnsnap,
	"workspace.previous":      locales.ActionPreviousWorkspace,
	"workspace.new":           locales.WorkspaceNew,
}

func init() {
	for n := 1; n <= 9; n++ {
		actionTitles["workspace."+itoa(n)] = func() string { return locales.ActionGoToWorkspace(itoa(n)) }
		actionTitles["pane.workspace."+itoa(n)] = func() string { return locales.ActionMovePaneToWorkspace(itoa(n)) }
	}
}

// actionTitle is an action's title as the menus, the palette and
// Settings show it.
func actionTitle(id string) string {
	if t, ok := actionTitles[id]; ok {
		return t()
	}
	act, _ := actions.Find(id)
	return act.Title
}

// accelerator is an action's shortcut in the menus: none while the user
// types a new one in Settings, and none of a global one, which the system
// takes before the menus.
func (a *App) accelerator(act actions.Action) string {
	if a.ss.recording != "" || act.Global {
		return ""
	}
	return actions.Keys(act, a.set.Shortcuts)
}

// items returns the items of a menu's actions, in their order, with
// separators where the groups change.
func (a *App) items(menu string, groups ...[]string) []*mygo.MenuItem {
	var out []*mygo.MenuItem
	for i, g := range groups {
		if i > 0 {
			out = append(out, mygo.Separator())
		}
		for _, id := range g {
			out = append(out, a.item(id))
		}
	}
	return out
}

func (a *App) menu() *mygo.Menu {
	appMenu := []*mygo.MenuItem{{Role: mygo.RoleAbout}, updater.MenuItem(), mygo.Separator(), a.item("settings.open"), a.item("cli.install"),
		mygo.Separator(), {Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
		{Label: locales.MenuQuitApp(mygo.App.Name()), Accelerator: quitKeys, ToolTip: locales.MenuQuitTip(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		a.item("app.quitAll")}
	// The app menu is macOS's: MyGo leaves it out elsewhere, with its items'
	// shortcuts, so they go in a menu of Kero's there, without macOS's own.
	kero := &mygo.MenuItem{Role: mygo.RoleAppMenu, Submenu: appMenu}
	if runtime.GOOS != "darwin" {
		kero = &mygo.MenuItem{Label: "guios", Submenu: []*mygo.MenuItem{a.item("settings.open"), a.item("cli.install"), mygo.Separator(),
			{Label: locales.MenuQuit(), Accelerator: quitKeys, ToolTip: locales.MenuQuitTip(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
			a.item("app.quitAll")}}
	}
	view := append(a.items("View", []string{"sidebar.toggle", "rightSidebar.toggle", "changes.toggle", "files.toggle"}, []string{"text.bigger", "text.smaller", "text.reset"}, []string{quickAction}),
		mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleToggleFullScreen})
	if inspectorWanted {
		view = append(view, &mygo.MenuItem{Role: mygo.RoleToggleDevTools})
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		kero,
		{Label: locales.MenuSession(), Submenu: a.items("Session",
			[]string{"session.new", "session.newHome", "session.newWorktree", "project.open", "host.add"},
			[]string{"palette.open", "conversation.resume", "session.attention", "agents.toggle", "agents.new", "session.next", "session.previous"},
			[]string{"session.rename", "pane.restart"},
			[]string{"pane.close", "session.close"})},
		// The Edit menu's own items, then finding.
		{Role: mygo.RoleEditMenu, Submenu: append([]*mygo.MenuItem{{Role: mygo.RoleUndo}, {Role: mygo.RoleRedo}, mygo.Separator(),
			{Role: mygo.RoleCut}, {Role: mygo.RoleCopy}, {Role: mygo.RolePaste}, {Role: mygo.RoleSelectAll}, mygo.Separator()},
			a.items("Edit", []string{"find.open", "find.next", "find.previous"})...)},
		{Label: locales.MenuPane(), Submenu: a.items("Pane",
			[]string{"pane.new", "pane.split.right", "pane.split.down"},
			[]string{"pane.browser", "browser.address"},
			[]string{"pane.zoom"},
			[]string{"pane.next", "pane.previous", "pane.left", "pane.right", "pane.up", "pane.down"})},
		{Label: locales.MenuLayout(), Submenu: a.layoutMenu()},
		{Label: locales.MenuWorkspace(), Submenu: a.workspaceMenu()},
		{Label: locales.MenuView(), Submenu: view},
		{Role: mygo.RoleWindowMenu},
	})
}

// layoutModes are the layout actions' modes, as the client names them.
var layoutModes = map[string]string{
	"layout.bsp": "bsp", "layout.master": "master-stack", "layout.scrolling": "scrolling", "layout.floating": "floating",
}

// layoutMenu is the Layout menu: the four layouts, then what each one
// can change, in submenus by layout.
func (a *App) layoutMenu() []*mygo.MenuItem {
	items := a.items("Layout",
		[]string{"layout.bsp", "layout.master", "layout.scrolling", "layout.floating", "layout.next", "layout.tiling"},
		[]string{"layout.rotate", "layout.equalize", "layout.float"})
	return append(items, mygo.Separator(),
		&mygo.MenuItem{Label: locales.MenuLayoutMaster(), Submenu: a.items("Layout",
			[]string{"layout.master.position", "layout.master.add", "layout.master.remove", "layout.master.swap", "layout.master.focus"})},
		&mygo.MenuItem{Label: locales.MenuLayoutScrolling(), Submenu: a.items("Layout",
			[]string{"layout.scroll.width", "layout.scroll.max", "layout.scroll.consume", "layout.scroll.expel", "layout.scroll.moveLeft", "layout.scroll.moveRight"})},
		&mygo.MenuItem{Label: locales.MenuLayoutFloating(), Submenu: a.items("Layout",
			[]string{"layout.snap.left", "layout.snap.right", "layout.snap.full", "layout.snap.unsnap"})})
}

// workspaceMenu is the Workspace menu: next, previous and new, each
// workspace, and moving the pane to one.
func (a *App) workspaceMenu() []*mygo.MenuItem {
	var show, move []string
	for n := 1; n <= 9; n++ {
		show = append(show, "workspace."+itoa(n))
		move = append(move, "pane.workspace."+itoa(n))
	}
	items := a.items("Workspace", []string{"workspace.next", "workspace.previous", "workspace.new"}, show)
	return append(items, mygo.Separator(), &mygo.MenuItem{Label: locales.MenuMovePaneToWorkspace(), Submenu: a.items("Workspace", move)})
}

// keys is an action's shortcut as the menus show it, "" when it has none.
func (a *App) keys(id string) string {
	act, _ := actions.Find(id)
	return actions.Glyphs(actions.Keys(act, a.set.Shortcuts))
}

// tip is a button's tooltip: what it does, with its action's shortcut.
func (a *App) tip(title, id string) string {
	if k := a.keys(id); k != "" {
		return title + " (" + k + ")"
	}
	return title
}

// menuShortcut shows an action's shortcut by a context menu's item.
func (a *App) menuShortcut(it *ui.MenuItem, id string) *ui.MenuItem {
	act, _ := actions.Find(id)
	parts := strings.Split(actions.Keys(act, a.set.Shortcuts), "+")
	var mods ui.Modifiers
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "cmdorctrl", "commandorcontrol":
			mods |= ui.Cmd
		case "cmd", "command", "super", "meta", "win":
			mods |= ui.Super
		case "ctrl", "control":
			mods |= ui.Ctrl
		case "alt", "option":
			mods |= ui.Alt
		case "shift":
			mods |= ui.Shift
		}
	}
	for k, name := range keyNames {
		if strings.EqualFold(name, parts[len(parts)-1]) {
			return it.Shortcut(mods, k)
		}
	}
	return it
}

// quitKeys quits the app: Control+Q is the terminal's on Windows and Linux.
var quitKeys = func() string {
	if runtime.GOOS == "darwin" {
		return "CmdOrCtrl+Q"
	}
	return "Ctrl+Shift+Q"
}()
