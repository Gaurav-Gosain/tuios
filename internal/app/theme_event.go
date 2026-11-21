package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

type themeEventState struct {
	id, report  string
	preview     bool
	seen        bool
	hostPending bool
}

func (m *OS) noteThemeEvent() {
	if m.DaemonClient == nil || !m.IsDaemonSession {
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
	preview := m.themePreviewing()
	st := &m.themeEvent
	same := st.seen && st.id == id && st.report == report
	if same && (st.preview == preview || preview) {
		return
	}
	quiet := !st.seen || (st.hostPending && st.id == id && !preview)
	st.id, st.report, st.preview, st.seen = id, report, preview, true
	st.hostPending = id == themeNone && r.bgHex == ""
	if quiet {
		return
	}
	p := session.ClientThemePayload{Theme: id, Background: r.bgHex, Preview: preview}
	if r.bg != nil {
		p.Light = vt.IsLightBackground(r.bg)
	}
	if err := m.DaemonClient.ReportTheme(p); err != nil {
		m.LogWarn("Theme: could not report the theme to the daemon: %v", err)
	}
}
