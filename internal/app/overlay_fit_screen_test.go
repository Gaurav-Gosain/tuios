package app_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// fitScreens are the sizes the panels have to survive: a tall narrow
// terminal, a short wide one, the narrowest viewport worth supporting, and a
// normal terminal as a control, plus the accent picker's wide layout at its
// floor, just over it, and on a wide screen too short to keep everything.
var fitScreens = []struct {
	name string
	w, h int
}{
	{"tall-narrow", 51, 37},
	{"short-wide", 90, 20},
	{"very-narrow", 30, 24},
	{"very-short", 100, 12},
	{"desktop", 120, 40},
	{"wide-picker-floor", 73, 30},
	{"wide-picker", 74, 20},
	{"wide-picker-short", 100, 14},
}

// palette opens a command palette row by its name, as a person does.
func palette(name string) func(f *apptest.Fixture) {
	return func(f *apptest.Fixture) {
		f.Action("command_palette")
		f.Type(name)
		f.Keys("enter")
	}
}

// panelDoor is one panel and how a person opens it. A door that steps
// through the panel's states lists them in steps; the panel is checked after
// opening and after each step.
type panelDoor struct {
	name    string
	prepare func(t *testing.T)
	panes   int
	open    func(f *apptest.Fixture)
	steps   []func(f *apptest.Fixture)
}

func repeat(n int, step func(f *apptest.Fixture)) []func(f *apptest.Fixture) {
	out := make([]func(f *apptest.Fixture), n)
	for i := range out {
		out[i] = step
	}
	return out
}

func keys(names ...string) func(f *apptest.Fixture) {
	return func(f *apptest.Fixture) { f.Keys(names...) }
}

func typed(text string) func(f *apptest.Fixture) {
	return func(f *apptest.Fixture) { f.Type(text) }
}

func panelDoors() []panelDoor {
	return []panelDoor{
		// Help, every section, then its search.
		{name: "help", open: func(f *apptest.Fixture) { f.Action("toggle_help") },
			steps: append(repeat(14, keys("right")), keys("/"), typed("window"))},
		{name: "command palette", open: func(f *apptest.Fixture) { f.Action("command_palette") },
			steps: []func(*apptest.Fixture){typed("zzzz-no-match")}},
		// Settings, every tab, then a long free-text value being edited.
		{name: "settings", open: func(f *apptest.Fixture) { f.Action("open_settings") },
			steps: repeat(14, keys("tab"))},
		{name: "settings editing", open: func(f *apptest.Fixture) {
			f.Action("open_settings")
			f.Keys("/")
			f.Type("Preferred shell")
			f.Keys("enter", "enter")
		}, steps: []func(*apptest.Fixture){typed(strings.Repeat("/very/long/path", 8))}},
		// The keybind manager, every tab, a recorded key, and a filter that
		// matches nothing.
		{name: "keybind manager", open: func(f *apptest.Fixture) { f.Action("prefix_keybinds") },
			steps: append(repeat(6, keys("tab")), typed("zzzz-no-match"))},
		{name: "theme picker", open: palette("Theme picker")},
		{name: "accent picker", panes: 1, open: func(f *apptest.Fixture) { f.Action("set_accent") }},
		// Outside a daemon session the switcher is the informational panel.
		{name: "session switcher", open: func(f *apptest.Fixture) { f.Action("prefix_session_switcher") }},
		{name: "layout picker", open: palette("Load layout")},
		{name: "layout save", open: palette("Save layout"),
			steps: []func(*apptest.Fixture){typed(strings.Repeat("name", 30))}},
		{name: "all windows", panes: 2, open: func(f *apptest.Fixture) {
			f.Push(func(st *session.SessionState) {
				st.Windows[0].CustomName = "a-window-title-far-too-long-for-a-narrow-screen"
			})
			palette("All windows")(f)
		}},
		{name: "logs", open: func(f *apptest.Fixture) { f.Action("toggle_logs") }},
		{name: "cache stats", open: func(f *apptest.Fixture) { f.Action("toggle_cache_stats") }},
		// The tape manager, then naming a tape with a long name, then asking
		// to delete one.
		{name: "tape manager", prepare: longTapeFiles, open: func(f *apptest.Fixture) { f.Action("toggle_tape_manager") },
			steps: []func(*apptest.Fixture){keys("r"), typed(strings.Repeat("name", 30)), keys("esc"), keys("d")}},
		// With no pane open the screen shows the welcome panel.
		{name: "welcome", panes: -1, open: func(*apptest.Fixture) {}},
	}
}

// longTapeFiles puts a tape with a long name where the tape manager looks.
func longTapeFiles(t *testing.T) {
	tapeFiles(t)
	dir := tapeDir(t)
	writeFile(t, dir+"/a-tape-file-name-that-is-much-too-long-to-fit.tape", "Type \"x\"\n")
}

// knownCut lists the panels the test finds cut today, by screen and door.
// Each is a bug: the case runs and must fail, so a fix that makes it pass
// has to take it off this list.
var knownCut = map[string]bool{}

// TestPanelsFitTheScreen opens every panel at each screen size, as a person
// opens it, and checks the panel's frame is whole on screen: four corners and
// both edges on every row. A panel wider or taller than the screen has its
// right side or its bottom cut off, where it cannot be read or scrolled to,
// and a row padded with a control character (a tab is one byte lipgloss
// measures as one cell and a terminal advances to its next stop) moves that
// row's right edge off the frame's column.
//
// Panels are drawn framed (appearance.frame_panels), so the frame is there to
// read; the fitting is the same with the frame off.
func TestPanelsFitTheScreen(t *testing.T) {
	for _, sc := range fitScreens {
		for _, d := range panelDoors() {
			name := sc.name + "/" + d.name
			t.Run(name, func(t *testing.T) {
				if d.prepare != nil {
					d.prepare(t)
				}
				if knownCut[name] {
					probe := &cutProbe{TB: t}
					runPanelDoor(probe, sc.w, sc.h, d)
					if !probe.failed {
						t.Errorf("%s is on knownCut but every frame is whole now: take it off the list", name)
					}
					return
				}
				runPanelDoor(t, sc.w, sc.h, d)
			})
		}
	}
}

// cutProbe records a failure instead of failing, for a knownCut case.
type cutProbe struct {
	testing.TB
	failed bool
}

func (p *cutProbe) Errorf(string, ...any) { p.failed = true }

func runPanelDoor(t testing.TB, w, h int, d panelDoor) {
	t.Helper()
	panes := 1
	switch {
	case d.panes < 0:
		panes = 0
	case d.panes > 0:
		panes = d.panes
	}
	f := apptest.New(t,
		apptest.Panes(panes),
		apptest.Size(w, h),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }),
	)
	d.open(f)
	checkPanel(t, f, "open")
	for i, step := range d.steps {
		step(f)
		checkPanel(t, f, "step "+itoa(i+1))
	}
}

func checkPanel(t testing.TB, f *apptest.Fixture, when string) {
	t.Helper()
	whole, cut := f.Panels()
	for _, why := range cut {
		t.Errorf("%s: %s\nscreen:\n%s", when, why, f.Text())
	}
	if len(whole) == 0 && len(cut) == 0 {
		t.Errorf("%s: no panel on screen\nscreen:\n%s", when, f.Text())
	}
}
