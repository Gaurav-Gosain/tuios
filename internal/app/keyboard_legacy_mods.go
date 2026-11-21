package app

import tea "charm.land/bubbletea/v2"

func kittyLegacyFormKey(code rune) bool {
	switch code {
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyBegin,
		tea.KeyHome, tea.KeyEnd, tea.KeyInsert, tea.KeyDelete,
		tea.KeyPgUp, tea.KeyPgDown:
		return true
	}
	return code >= tea.KeyF1 && code <= tea.KeyF12
}

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

func (m *OS) fixHostKeyMods(msg tea.Msg) tea.Msg {
	switch k := msg.(type) {
	case tea.KeyPressMsg:
		return tea.KeyPressMsg(fixKittyTextOnlyKey(fixKittyLegacyMods(tea.Key(k), m.KeyboardEnhancementsEnabled)))
	case tea.KeyReleaseMsg:
		return tea.KeyReleaseMsg(fixKittyTextOnlyKey(fixKittyLegacyMods(tea.Key(k), m.KeyboardEnhancementsEnabled)))
	}
	return msg
}

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
