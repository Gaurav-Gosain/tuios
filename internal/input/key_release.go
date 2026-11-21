package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

func forwardKeyReleaseToFocused(msg tea.KeyReleaseMsg, o *app.OS) bool {
	pressedIn, pressed := o.TakePaneKeyDown(msg.Code)
	if !pressed {
		return false
	}
	window := o.GetFocusedWindow()
	if o.SidebarSessionFocused() && !o.AnyOverlayOpen() {
		window = o.SidebarSessionWindow()
	} else if o.Mode != app.TerminalMode {
		return false
	}
	if window == nil || window.Terminal == nil || window.ID != pressedIn {
		return false
	}
	flags := window.Terminal.KittyKeyboardFlags()
	if isModifierKeyPress(tea.KeyPressMsg(msg.Key())) && flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		return false
	}
	encoded := vt.EncodeKeyReleaseCSIu(vtKeyFromBubbletea(tea.KeyPressMsg(msg.Key())), flags)
	if encoded == "" {
		return false
	}
	return window.SendInput([]byte(encoded)) == nil
}
