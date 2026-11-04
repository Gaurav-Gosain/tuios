package main

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// run runs one of the app's actions, from a menu, the palette or
// `kero do`, on the main thread.
func (a *App) run(id string, args map[string]string) {
	if !a.fromMay(id, args) {
		return
	}
	s := a.session(a.selected)
	switch id {
	case "settings.open":
		a.openSettings("")
	case "cli.install":
		a.installCLI()
	case "app.quitAll":
		a.quitAndEnd()
	case "session.new":
		// A directory kero do names is of the host it runs on.
		switch dir := args["dir"]; {
		case dir != "":
			a.newSessionIn(a.actionHost(), dir)
		case a.from != nil:
			a.newSessionOn(a.from, args["project"])
		default:
			a.newSession(args["project"])
		}
	case "session.newHome":
		if h := a.actionHost(); h.remote() {
			a.newSessionIn(h, "~")
		} else {
			a.newSessionIn(h, proto.Home())
		}
	case "session.newWorktree":
		a.newWorktreeSession()
	case "project.open":
		a.openProject()
	case "palette.open":
		a.openPalette()
	case "conversation.resume":
		a.openConversations()
	case "session.attention":
		a.jumpToAttention()
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
			a.update(func() { a.run(id, nil) })
		},
	}
}

// actionTitles are the actions' titles in the language the app shows;
// an Action's Title stays English, for `kero actions`.
var actionTitles = map[string]func() string{
	"settings.open":       locales.ActionSettings,
	"cli.install":         locales.ActionInstallCli,
	"app.quitAll":         locales.ActionQuitAll,
	"session.new":         locales.ActionNewSession,
	"session.newHome":     locales.ActionNewSessionHome,
	"session.newWorktree": locales.ActionNewSessionWorktree,
	"project.open":        locales.ActionOpenProject,
	"palette.open":        locales.ActionGoToSession,
	"conversation.resume": locales.ActionResumeConversation,
	"session.attention":   locales.ActionJumpToAttention,
	"session.next":        locales.ActionNextSession,
	"session.previous":    locales.ActionPreviousSession,
	"session.rename":      locales.ActionRenameSession,
	"pane.restart":        locales.ActionRestartPane,
	"pane.close":          locales.ActionClose,
	"session.close":       locales.ActionCloseSession,
	"find.open":           locales.ActionFind,
	"find.next":           locales.ActionFindNext,
	"find.previous":       locales.ActionFindPrevious,
	"pane.split.right":    locales.ActionSplitRight,
	"pane.split.down":     locales.ActionSplitDown,
	"pane.browser":        locales.ActionNewBrowser,
	"browser.address":     locales.ActionOpenLocation,
	"pane.zoom":           locales.ActionZoomPane,
	"pane.next":           locales.ActionNextPane,
	"pane.previous":       locales.ActionPreviousPane,
	"pane.left":           locales.ActionPaneLeft,
	"pane.right":          locales.ActionPaneRight,
	"pane.up":             locales.ActionPaneAbove,
	"pane.down":           locales.ActionPaneBelow,
	"sidebar.toggle":      locales.ActionToggleSidebar,
	"changes.toggle":      locales.ActionToggleChanges,
	"files.toggle":        locales.ActionToggleFiles,
	"rightSidebar.toggle": locales.ActionToggleRightSidebar,
	"text.bigger":         locales.ActionBigger,
	"text.smaller":        locales.ActionSmaller,
	"text.reset":          locales.ActionActualSize,
	quickAction:           locales.ActionQuickTerminal,
	"workspace.next":      locales.ActionNextWorkspace,
	"workspace.previous":  locales.ActionPreviousWorkspace,
	"workspace.new":       locales.WorkspaceNew,
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
		mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleToggleFullScreen}, &mygo.MenuItem{Role: mygo.RoleToggleDevTools})
	return mygo.NewMenu([]*mygo.MenuItem{
		kero,
		{Label: locales.MenuSession(), Submenu: a.items("Session",
			[]string{"session.new", "session.newHome", "session.newWorktree", "project.open"},
			[]string{"palette.open", "conversation.resume", "session.attention", "session.next", "session.previous"},
			[]string{"session.rename", "pane.restart"},
			[]string{"pane.close", "session.close"})},
		// The Edit menu's own items, then finding.
		{Role: mygo.RoleEditMenu, Submenu: append([]*mygo.MenuItem{{Role: mygo.RoleUndo}, {Role: mygo.RoleRedo}, mygo.Separator(),
			{Role: mygo.RoleCut}, {Role: mygo.RoleCopy}, {Role: mygo.RolePaste}, {Role: mygo.RoleSelectAll}, mygo.Separator()},
			a.items("Edit", []string{"find.open", "find.next", "find.previous"})...)},
		{Label: locales.MenuPane(), Submenu: a.items("Pane",
			[]string{"pane.split.right", "pane.split.down"},
			[]string{"pane.browser", "browser.address"},
			[]string{"pane.zoom"},
			[]string{"pane.next", "pane.previous", "pane.left", "pane.right", "pane.up", "pane.down"})},
		{Label: locales.MenuWorkspace(), Submenu: a.workspaceMenu()},
		{Label: locales.MenuView(), Submenu: view},
		{Role: mygo.RoleWindowMenu},
	})
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

// fromMay reports whether an action another machine's program asked for
// (kero do, a.from) may run: one that acts on a session acts on one of
// that host's alone, never on this machine's, which a local agent's may;
// one of the app, as Settings or the sidebar, may.
func (a *App) fromMay(id string, args map[string]string) bool {
	h := a.from
	if h == nil || !h.remote() {
		return true
	}
	switch {
	case id == "session.new" || id == "session.newHome":
		p := args["project"]
		return p == "" || a.hostOf(p) == h
	case strings.HasPrefix(id, "pane.") || id == "changes.toggle" || id == "files.toggle" || id == "rightSidebar.toggle":
		return a.session(a.selected) != nil && a.selectedHost() == h
	case id == quickAction:
		return false // this machine's screen is no other's to drop on
	}
	return true
}
