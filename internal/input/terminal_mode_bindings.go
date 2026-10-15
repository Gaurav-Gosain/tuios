package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const (
	terminalScrollUp       = "terminal_scroll_up"
	terminalScrollDown     = "terminal_scroll_down"
	terminalScrollPageUp   = "terminal_scroll_page_up"
	terminalScrollPageDown = "terminal_scroll_page_down"
	terminalPasteHost      = "terminal_paste_host"
)

// handleTerminalScrollBinding runs one of terminal mode's line or page scroll
// actions. It reports false when action is not a scroll action, or when the
// pane owns the input because it is on the alternate screen or uses mouse
// tracking. The caller can then forward the original key or mouse press.
func handleTerminalScrollBinding(action string, o *app.OS, focused *terminal.Window) bool {
	if !terminalScrollAction(action) || focused == nil || !keyScrollsPane(focused) {
		return false
	}

	o.NoteAction(action)
	if o.MultiCopy.Has(focused.ID) {
		for _, w := range o.MultiCopyWindows() {
			scrollTerminalPane(w, action)
		}
		return true
	}
	scrollTerminalPane(focused, action)
	return true
}

func terminalScrollAction(action string) bool {
	switch action {
	case terminalScrollUp, terminalScrollDown, terminalScrollPageUp, terminalScrollPageDown:
		return true
	}
	return false
}

func scrollTerminalPane(win *terminal.Window, action string) {
	up := action == terminalScrollUp || action == terminalScrollPageUp
	lines := 1
	if action == terminalScrollPageUp || action == terminalScrollPageDown {
		lines = max(1, win.ContentHeight())
	}

	if up {
		if !win.InCopyMode() && win.ScrollbackLen() > 0 {
			win.EnterCopyModeImplicit()
		}
		scrollCopyModeUpBy(win, lines)
		return
	}
	if win.InCopyMode() {
		scrollCopyModeDownBy(win, lines)
		leaveCopyModeAtBottom(win)
	}
}

// handleTerminalMouseBinding gives the middle button the same terminal-mode
// binding lookup as a key. "middle" is Bubble Tea's event spelling and is
// deliberately the only mouse button admitted here: "left" and "right" are
// already keyboard key names, so looking every button up in the keyboard table
// would make an arrow binding also capture a click.
//
// A bare middle press over a program that tracks the mouse (vim with
// mouse=a, lazygit, a nested tmux) goes to that program, as it did before
// the binding existed. A binding with a modifier, such as ctrl+middle, runs
// over such a program too.
func handleTerminalMouseBinding(msg tea.MouseClickMsg, o *app.OS, clicked int) (*app.OS, tea.Cmd, bool) {
	if msg.Button == tea.MouseMiddle {
		// A new press starts over. A release that never came must not make
		// this press's release disappear.
		o.MiddleBindingPressed = false
	}
	if o.Mode != app.TerminalMode || msg.Button != tea.MouseMiddle ||
		o.KeybindRegistry == nil || clicked < 0 || clicked >= len(o.Windows) {
		return o, nil, false
	}
	win := o.Windows[clicked]
	if _, _, inContent := win.ScreenToTerminal(msg.X, msg.Y); !inContent {
		return o, nil, false
	}
	if msg.Mod == 0 && win.Terminal != nil && win.Terminal.HasMouseMode() {
		return o, nil, false
	}
	action := o.KeybindRegistry.GetTerminalModeAction(msg.String())
	if !terminalMouseActionCanRun(action, win) {
		return o, nil, false
	}

	o.MiddleBindingPressed = true
	o.FocusWindowFromClick(clicked, msg.X, msg.Y)
	if terminalScrollAction(action) {
		handleTerminalScrollBinding(action, o, win)
		return o, nil, true
	}
	if action == terminalPasteHost {
		o.NoteAction(action)
		return o, o.RequestPaste(), true
	}
	next, cmd, handled := dispatchAction(action, tea.KeyPressMsg{}, o)
	return next, cmd, handled
}

// terminalMouseBindingConsumesRelease reports whether a binding took the
// middle press this release ends, and clears the mark. It is decided at the
// press, not by where the release lands: a release outside the pane's content
// still belongs to the binding, and a release over the content after a press
// the binding did not take still goes to the program.
func terminalMouseBindingConsumesRelease(msg tea.MouseReleaseMsg, o *app.OS) bool {
	if msg.Button != tea.MouseMiddle || !o.MiddleBindingPressed {
		return false
	}
	o.MiddleBindingPressed = false
	return true
}

func terminalMouseActionCanRun(action string, win *terminal.Window) bool {
	switch {
	case action == terminalPasteHost:
		return true
	case terminalScrollAction(action):
		return keyScrollsPane(win)
	default:
		return GetDispatcher().HasAction(action)
	}
}
