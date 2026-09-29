package app

import tea "charm.land/bubbletea/v2"

// kittyLegacyFormKey reports whether the kitty keyboard protocol sends code in
// its legacy form (CSI <n> ; <mods> ~, or CSI 1 ; <mods> <letter>) rather than
// as CSI u. These are the arrows, the navigation block and F1 to F12.
func kittyLegacyFormKey(code rune) bool {
	switch code {
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyBegin,
		tea.KeyHome, tea.KeyEnd, tea.KeyInsert, tea.KeyDelete,
		tea.KeyPgUp, tea.KeyPgDown:
		return true
	}
	return code >= tea.KeyF1 && code <= tea.KeyF12
}

// fixKittyLegacyMods puts Super back on a key the host sent in the kitty
// protocol's legacy form.
//
// Under the kitty protocol the modifier parameter of every key uses the kitty
// bits: 8 is Super and 32 is Meta. Bubble Tea's decoder reads the legacy forms
// with the xterm bits instead, where 8 is Meta, so Cmd+F12 arrived as
// meta+f12 and a super+f12 binding could never match. CSI u keys are decoded
// with the kitty bits already and are left alone, as is every key from a host
// that has not granted the protocol, where 8 really is xterm's Meta.
func fixKittyLegacyMods(k tea.Key, hostKitty bool) tea.Key {
	if !hostKitty || k.Mod&(tea.ModMeta|tea.ModSuper) == 0 || !kittyLegacyFormKey(k.Code) {
		return k
	}
	meta, super := k.Mod&tea.ModMeta != 0, k.Mod&tea.ModSuper != 0
	k.Mod &^= tea.ModMeta | tea.ModSuper
	if meta {
		k.Mod |= tea.ModSuper
	}
	if super {
		k.Mod |= tea.ModMeta
	}
	return k
}

// fixHostKeyMods applies fixKittyLegacyMods to a key event from the host
// terminal. Every other message is returned unchanged. Keys that tuios makes
// itself (send-keys, tapes) do not come through here, since they carry the
// modifiers they were given.
func (m *OS) fixHostKeyMods(msg tea.Msg) tea.Msg {
	switch k := msg.(type) {
	case tea.KeyPressMsg:
		return tea.KeyPressMsg(fixKittyTextOnlyKey(fixKittyLegacyMods(tea.Key(k), m.KeyboardEnhancementsEnabled)))
	case tea.KeyReleaseMsg:
		return tea.KeyReleaseMsg(fixKittyTextOnlyKey(fixKittyLegacyMods(tea.Key(k), m.KeyboardEnhancementsEnabled)))
	}
	return msg
}

// fixKittyTextOnlyKey turns a kitty text event with no key back into the text
// it carries.
//
// With report-all-keys and associated text on, the kitty protocol sends text
// that no key produced (an input method commit, composed text) as key number
// 0: CSI 0 ; mods ; codepoints u. The decoder looks 0 up in its legacy table,
// where it is NUL, so the text arrived as ctrl+space and a pane got a NUL or
// CSI 32;5u instead of the characters typed. tuios asks for that mode while it
// reads keys itself and while a hold key or a pane needs every key, so this is
// how IME text reaches it then.
//
// A real Ctrl+Space carries no text, or a space, so any other text on a
// ctrl+space can only have come from key 0.
func fixKittyTextOnlyKey(k tea.Key) tea.Key {
	if k.Code != tea.KeySpace || k.Mod&tea.ModCtrl == 0 || k.Text == "" || k.Text == " " {
		return k
	}
	runes := []rune(k.Text)
	if len(runes) == 1 {
		k.Code = runes[0]
	} else {
		k.Code = tea.KeyExtended
	}
	k.Mod &^= tea.ModCtrl
	return k
}
