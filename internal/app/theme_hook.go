package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

// The after-theme-change hook.
//
// A theme reaches this client by several paths: the picker, the settings
// page, the light and dark pair, the Omarchy watcher, tuios theme reload, and
// with no theme the terminal's own colours. Each path ends in a frame, and
// every frame works out what the panes are told they are drawn on (see
// paneReportNow). So the hook watches that one answer instead of each path:
// when the theme id or the colours panes report differ from what the hook
// last saw, it fires. Those are also the changes that send a pane with mode
// 2031 on its colour scheme notice.
//
// The fact is the client's: each client has its own theme, and the command
// runs on the machine the person sits at, as after-attach does. Only a local
// client runs it. A tuios ssh or tuios-web process serves many people, and a
// hook there would run on the server for a theme the server's user never
// sees. Two local clients each run it for their own theme.
//
// The picker and the theme editor show each theme on the way to a pick. The
// hook waits while either is open and fires once for the theme in force when
// it closes, and not at all when the pick is cancelled. The first frame only
// records the theme: starting tuios is not a theme change. With no theme, the
// terminal's colours arrive a moment after the first frame, and learning them
// is not a change either.

// themeHookState is what the hook last saw.
type themeHookState struct {
	id, report string
	seen       bool
	// hostPending is set while no theme is in force and the terminal has not
	// said what its background is yet.
	hostPending bool
}

// noteThemeChange fires after-theme-change when the theme colours changed
// since the last frame that could fire it. Called once per frame, it costs two
// string comparisons when nothing changed: paneReportNow is memoised, and its
// key is the same string from frame to frame.
func (m *OS) noteThemeChange() {
	if m.Client != ClientLocal || m.themePreviewing() {
		return
	}
	r := m.paneReportNow()
	id := theme.CurrentThemeID()
	if id == "" || !theme.IsEnabled() {
		id = themeNone
	}
	// With no theme only the background counts. The terminal's text colour
	// and sixteen can arrive after its background, and they are not a new
	// theme.
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
		// The rule the mode 2031 notice uses, so the hook and the panes
		// agree on what is light.
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
