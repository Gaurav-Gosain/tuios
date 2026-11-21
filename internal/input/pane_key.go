package input

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

func paneKeyBytes(host tea.KeyPressMsg, w *terminal.Window, o *app.OS, focused bool) (raw []byte, released bool) {
	flags, modifyOtherKeys := 0, 0
	if w.Terminal != nil {
		flags = w.Terminal.KittyKeyboardFlags()
		modifyOtherKeys = w.Terminal.ModifyOtherKeys()
	}
	key := vtKeyFromBubbletea(host)
	if encoded := vt.EncodePaneKey(key, flags, modifyOtherKeys); encoded != "" {
		raw = []byte(encoded)
	}
	if len(raw) == 0 && legacyDropsKey(host) {
		return nil, false
	}
	if len(raw) == 0 {
		appCursorKeys := w.Terminal != nil && w.Terminal.ApplicationCursorKeys()
		raw = getRawKeyBytesWithMode(host, appCursorKeys)
	}
	if len(raw) == 0 || flags&ansi.KittyReportEventTypes == 0 {
		return raw, false
	}
	if focused && o.HostReportsReleases() {
		return raw, false
	}
	release := vt.EncodeKeyReleaseCSIu(key, flags)
	if release == "" {
		return raw, false
	}
	return append(raw, release...), true
}

func legacyDropsKey(k tea.KeyPressMsg) bool {
	if k.Mod&(tea.ModSuper|tea.ModHyper) == 0 {
		return false
	}
	return k.Code == tea.KeySpace || (k.Code > 0x20 && k.Code != 0x7f && k.Code <= unicode.MaxRune)
}
