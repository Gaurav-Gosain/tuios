package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleFilesViewInput handles a key while the Files view is on top. It owns
// every key but the leader: a key meant for the view must not reach a pane
// or the rail under it. The keys are [keybindings.files]. Quick look beside
// the list takes its own keys first, as it does beside the rail: tab goes
// into the file, and inside the file its scope takes every key.
func handleFilesViewInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.KeybindRegistry == nil {
		o.CloseFilesView()
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
	if o.FilesViewPrompt() {
		return o, filesPromptKey(msg, o)
	}
	act := tableAction(o, msg, o.KeybindRegistry, config.TableFiles)
	if act == config.ActionFilesQuickLook {
		o.NoteAction(act)
		return o, o.FilesToggleLook()
	}
	if o.QuickLookInside() {
		in := tableAction(o, msg, o.KeybindRegistry, config.TableQuickLookInside)
		if in != "" {
			o.NoteAction(in)
		}
		return o, quickLookInsideAction(o, in)
	}
	if o.QuickLookOpen() {
		if cmd, ok := filesLookKey(msg, o); ok {
			return o, cmd
		}
	}
	if msg.String() == "esc" {
		o.FilesEscape()
		return o, nil
	}
	if act == "" {
		return o, nil
	}
	o.NoteAction(act)
	return o, filesAction(act, o)
}

// filesLookKey runs a key of quick look's own table while the preview is
// beside the list, the way the rail does. ok is false for a key the preview
// does not take, which then goes to the view.
func filesLookKey(msg tea.KeyPressMsg, o *app.OS) (tea.Cmd, bool) {
	act := tableAction(o, msg, o.KeybindRegistry, config.TableQuickLook)
	handled := false
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
		o.NoteAction(act)
		return o.QuickLookNextView(), true
	case "zoom_in", "zoom_out", "zoom_fit":
		o.QuickLookZoom(act)
		handled = true
	}
	if handled {
		o.NoteAction(act)
	}
	return nil, handled
}

// filesPromptKey handles a key while a line is typed in the view: the
// filter or the go to line. Every printable key is text.
func filesPromptKey(msg tea.KeyPressMsg, o *app.OS) tea.Cmd {
	switch msg.String() {
	case "enter":
		return o.FilesPromptSubmit()
	case "esc":
		o.FilesPromptCancel()
	case "backspace", "ctrl+h":
		o.FilesPromptBackspace()
	case "space":
		o.FilesPromptType(" ")
	default:
		if msg.Text != "" {
			o.FilesPromptType(msg.Text)
		}
	}
	return nil
}

// filesAction runs one action of [keybindings.files]. The switch is the
// app's, so a click on a footer hint runs the same code as its key.
func filesAction(act string, o *app.OS) tea.Cmd { return o.FilesAction(act) }
