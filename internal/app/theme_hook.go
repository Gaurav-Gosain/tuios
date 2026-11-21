package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

type themeHookState struct {
	id, report  string
	seen        bool
	hostPending bool
}

func (m *OS) noteThemeChange() {
	if m.Client != ClientLocal || m.themePreviewing() {
		return
	}
	r := m.paneReportNow()
	id := theme.CurrentThemeID()
	if id == "" || !theme.IsEnabled() {
		id = themeNone
	}
	report := r.key
	if id == themeNone {
		report = r.bgHex
	}
	st := &m.themeHook
	if st.seen && st.id == id && st.report == report {
		return
	}
	quiet := !st.seen || (st.hostPending && st.id == id)
	st.id, st.report, st.seen = id, report, true
	st.hostPending = id == themeNone && r.bgHex == ""
	if quiet {
		return
	}
	light := ""
	if r.bg != nil {
		light = "0"
		if vt.IsLightBackground(r.bg) {
			light = "1"
		}
	}
	m.FireHookContext(hooks.AfterThemeChange, hooks.Context{
		Theme:      id,
		ThemeBg:    r.bgHex,
		ThemeLight: light,
	})
}
