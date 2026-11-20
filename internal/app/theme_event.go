package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

// The theme this client reports to the daemon, for the theme event that
// tuios subscribe delivers (see session/theme_event.go).
//
// It watches the same answer the after-theme-change hook watches (see
// theme_hook.go), and differs from it in one way: it reports previews. While
// the theme picker or the theme editor is open, each theme it shows is
// reported with preview set, so a program that follows the theme can repaint
// as the person browses. When the picker closes, the theme in force is
// reported once with preview clear: the pick, or the theme from before a
// cancel. Opening the picker on the theme in force reports nothing, and
// closing it with no other theme shown reports nothing either.
//
// Like the hook, the first frame and the terminal's own colours arriving
// after it only record what is in force: neither is a change.

// themeEventState is the theme this client last reported, or recorded.
type themeEventState struct {
	id, report  string
	preview     bool
	seen        bool
	hostPending bool
}

// noteThemeEvent reports the theme to the daemon when it changed since the
// last report. Called once per frame, it costs a few string comparisons when
// nothing changed: paneReportNow is memoised.
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
		// Nothing changed, or the picker opened on the theme in force.
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
		// The rule the mode 2031 notice uses, as the hook does.
		p.Light = vt.IsLightBackground(r.bg)
	}
	if err := m.DaemonClient.ReportTheme(p); err != nil {
		m.LogWarn("Theme: could not report the theme to the daemon: %v", err)
	}
}
