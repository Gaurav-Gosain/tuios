package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func handleFocusSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterSidebarFocus()
	return o, nil
}

func handleToggleFocusSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarFocused {
		o.ExitSidebarFocus()
		return o, nil
	}
	o.EnterSidebarFocus()
	return o, nil
}

// HandleSidebarKey routes a keypress while the rail owns the keyboard. It looks
// the key up in the [keybindings] sidebar section and mutates the OS through the
// same methods the mouse handlers call, so keyboard and mouse can never diverge.
// Keys with no rail binding are swallowed, not passed to the pane underneath:
// the whole point of the scope is that pane bindings do not fire here.
func HandleSidebarKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarFocusOnSecondary() {
		var result *app.OS
		var cmd tea.Cmd
		o.WithSecondarySidebar(func() { result, cmd = handleSidebarKey(msg, o) })
		return result, cmd
	}
	return handleSidebarKey(msg, o)
}

func handleSidebarKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if o.KeybindRegistry == nil {
		o.ExitSidebarFocus()
		return o, nil
	}
	if o.QuickLookPromptOpen() {
		quickLookPromptKey(msg, o)
		return o, nil
	}
	if cm, grid := o.QuickLookCopy(); cm != nil {
		if !cm.Implicit {
			return handleQuickLookCopyKey(msg, o, cm, grid)
		}
		o.QuickLookCopyEnd()
		if msg.String() == "esc" {
			return o, nil
		}
	}
	if act := tableAction(o, msg, o.KeybindRegistry, config.TableSidebarFiles); act == sidebarActFileQuickLook &&
		(o.QuickLookOpen() || o.SidebarCursorOnListing()) {
		noteRailAction(o, act)
		return o, o.SidebarQuickLook()
	}
	if o.QuickLookInside() {
		act := tableAction(o, msg, o.KeybindRegistry, config.TableQuickLookInside)
		if act != "" {
			noteRailAction(o, act)
		}
		return o, quickLookInsideAction(o, act)
	}
	if key == "esc" && o.QuickLookOpen() {
		o.CloseQuickLook()
		return o, nil
	}
	if o.QuickLookOpen() {
		handled := false
		act := tableAction(o, msg, o.KeybindRegistry, config.TableQuickLook)
		switch act {
		case "seek_down", "half_down":
			handled = o.QuickLookHalfPage(true)
		case "seek_up", "half_up":
			handled = o.QuickLookHalfPage(false)
		case "go_inside":
			handled = o.QuickLookGoInside()
		case "full_size":
			o.QuickLookFullSize()
			handled = true
		case "filmstrip":
			o.QuickLookFilmstrip()
			handled = true
		case "next_view":
			noteRailAction(o, act)
			return o, o.QuickLookNextView()
		}
		if handled {
			noteRailAction(o, act)
			return o, nil
		}
	}

	if o.SidebarCursorOnFile() {
		if act := tableAction(o, msg, o.KeybindRegistry, config.TableSidebarFiles); act != "" {
			noteRailAction(o, act)
			if o.LearnBlocksAction(act) {
				return o, nil
			}
			return o, handleSidebarFileAction(act, o)
		}
	}

	if o.SidebarCursorOnAgent() {
		if act := tableAction(o, msg, o.KeybindRegistry, config.TableSidebarAgents); act != "" {
			if cmd, handled := o.SidebarAgentAction(act); handled {
				noteRailAction(o, act)
				return o, cmd
			}
		}
	}

	action := tableAction(o, msg, o.KeybindRegistry, config.TableSidebar)
	if action == "" {
		if m, cmd, ok := handleGlobalBinds(msg, o); ok {
			return m, cmd
		}

		if isReservedTerminalChord(msg) {
			if act := lookupAction(o, msg, o.KeybindRegistry.GetAction); config.TerminalSafeAction(act) {
				if m, cmd, ok := dispatchAction(act, msg, o); ok {
					return m, cmd
				}
			}
		}

		focus := tableAction(o, msg, o.KeybindRegistry, config.TableTerminalMode)
		if dir, ok := terminalFocusDirection(focus); ok && o.LeaveRailToward(dir) {
			return o, nil
		}

		if key == "esc" {
			o.ExitSidebarFocus()
		}
		return o, nil
	}

	if after, ok := strings.CutPrefix(action, sidebarActJumpPrefix); ok {
		if n, err := strconv.Atoi(after); err == nil {
			noteRailAction(o, action)
			o.SidebarJumpToSession(n)
		}
		return o, nil
	}

	handled := true

	switch action {
	case sidebarActCursorDown:
		o.SidebarCursorMove(1)
	case sidebarActCursorUp:
		o.SidebarCursorMove(-1)
	case sidebarActFirst:
		o.SidebarCursorFirst()
	case sidebarActLast:
		o.SidebarCursorLast()
	case sidebarActExpand:
		o.SidebarCursorExpand()
	case sidebarActCollapse:
		o.SidebarCursorCollapse()
	case sidebarActActivate:
		if o.SidebarActivateCursor() {
			o.ExitSidebarFocus()
		}
	case sidebarActReorderDown:
		o.SidebarReorderCursor(1)
	case sidebarActReorderUp:
		o.SidebarReorderCursor(-1)
	case sidebarActSection:
		o.SidebarCycleSection()
	case sidebarActSwitchEdge:
		o.FocusOtherSidebar()
	case sidebarActPalette:
		noteRailAction(o, action)
		return o, o.OpenCommandPalette()
	case sidebarActFileSearch:
		noteRailAction(o, action)
		return o, o.OpenFileSearch(true)
	case sidebarActAgentFilter:
		o.SidebarCycleAgentsFilter()
	case sidebarActAgentSort:
		o.SidebarCycleAgentsSort()
	case sidebarActMail:
		noteRailAction(o, action)
		return o, o.SidebarOpenMail()
	case sidebarActNarrow:
		if o.SidebarCursorOnDivider() {
			o.SidebarSplitStep(-1)
		} else {
			o.SidebarSetCollapsed(true)
		}
	case sidebarActWiden:
		if o.SidebarCursorOnDivider() {
			o.SidebarSplitStep(1)
		} else {
			o.SidebarSetCollapsed(false)
		}
	case sidebarActKill:
		o.SidebarOpenCursorMenu(true)
	case sidebarActMenu:
		o.SidebarOpenCursorMenu(false)
	case sidebarActHelp:
		o.OpenHelpAtCategory(app.HelpCategorySidebar)
	case sidebarActRename:
		o.SidebarRenameCursor()
	case sidebarActAccent:
		o.SidebarAccentCursor()
	case sidebarActNewSession:
		o.SidebarNewSessionHere()
	case sidebarActNewWindow:
		o.SidebarNewWindow("")
		o.ExitSidebarFocus()
	case sidebarActExit:
		o.ExitSidebarFocus()
	default:
		handled = false
	}
	if handled {
		noteRailAction(o, action)
	}
	return o, o.TakeSidebarCmd()
}

func noteRailAction(o *app.OS, action string) {
	if action == actionMenuOpened {
		return
	}
	o.NoteAction(action)
	if o.OnAction != nil {
		o.OnAction(action)
	}
}

func makeSidebarReorderHandler(delta int) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.SidebarReorderCursor(delta)
		return o, o.TakeSidebarCmd()
	}
}

func makeSidebarFileHandler(action string) ActionHandler {
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		return o, handleSidebarFileAction(action, o)
	}
}

func handleSidebarFileAction(action string, o *app.OS) tea.Cmd {
	switch action {
	case sidebarActFileOpen:
		return o.SidebarFileOpen()
	case sidebarActFileEdit:
		return o.SidebarFileEdit()
	case sidebarActFileCopyPath:
		return o.SidebarFileCopyPath()
	case sidebarActFileCreate:
		o.SidebarFileCreate()
	case sidebarActFileRename:
		o.SidebarFileRename()
	case sidebarActFileDelete:
		o.SidebarFileDelete(false)
	case sidebarActFileDeleteAll:
		o.SidebarFileDelete(true)
	case sidebarActFileCopy:
		o.SidebarFileCopy()
	case sidebarActFileCut:
		o.SidebarFileCut()
	case sidebarActFilePaste:
		return o.SidebarFilePaste()
	case sidebarActFileSend:
		return o.SidebarFileSend()
	case sidebarActFileInsertPath:
		o.SidebarFileInsert()
	case sidebarActFileQuickLook:
		return o.SidebarQuickLook()
	case sidebarActFileOpenSystem:
		return o.SidebarFileOpenSystem()
	}
	return nil
}

func quickLookInsideAction(o *app.OS, act string) tea.Cmd {
	switch act {
	case "line_down":
		o.QuickLookLine(true)
	case "line_up":
		o.QuickLookLine(false)
	case "half_down":
		o.QuickLookHalfPage(true)
	case "half_up":
		o.QuickLookHalfPage(false)
	case "start":
		o.QuickLookEdge(false)
	case "end":
		o.QuickLookEdge(true)
	case "full_size":
		o.QuickLookFullSize()
	case "go_back":
		o.QuickLookGoBack()
	case "find":
		o.QuickLookFind()
	case "find_next":
		o.QuickLookFindMove(true)
	case "find_prev":
		o.QuickLookFindMove(false)
	case "go_to":
		o.QuickLookGoTo()
	case "wrap":
		o.QuickLookWrap()
	case "filmstrip":
		o.QuickLookFilmstrip()
	case "open_details":
		o.QuickLookOpenDetails()
	case "column_left":
		o.QuickLookColumn(false)
	case "column_right":
		o.QuickLookColumn(true)
	case "next_view":
		return o.QuickLookNextView()
	case "copy_mode":
		o.QuickLookCopyStart()
	case "open_editor":
		return o.QuickLookOpenEditor()
	}
	return nil
}

func quickLookPromptKey(msg tea.KeyPressMsg, o *app.OS) {
	key := msg.String()
	switch {
	case key == "esc":
		o.QuickLookPromptCancel()
	case key == "enter":
		o.QuickLookPromptEnter()
	case msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) != 0:
		o.QuickLookPromptKey(key, "")
	default:
		o.QuickLookPromptKey(key, msg.Text)
	}
}

func handleFreeQuickLookKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.QuickLookPromptOpen() {
		quickLookPromptKey(msg, o)
		return o, nil
	}
	if cm, grid := o.QuickLookCopy(); cm != nil {
		if !cm.Implicit {
			return handleQuickLookCopyKey(msg, o, cm, grid)
		}
		o.QuickLookCopyEnd()
		if msg.String() == "esc" {
			return o, nil
		}
	}
	if tableAction(o, msg, o.KeybindRegistry, config.TableSidebarFiles) == sidebarActFileQuickLook {
		o.CloseQuickLook()
		return o, nil
	}
	act := tableAction(o, msg, o.KeybindRegistry, config.TableQuickLookInside)
	if act == "go_back" || msg.String() == "esc" {
		o.CloseQuickLook()
		return o, nil
	}
	if !o.QuickLookInside() {
		if tableAction(o, msg, o.KeybindRegistry, config.TableQuickLook) == "full_size" {
			o.QuickLookFullSize()
		}
		return o, nil
	}
	return o, quickLookInsideAction(o, act)
}
