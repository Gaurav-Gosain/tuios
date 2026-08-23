package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The keybind manager's tabs. Four surfaces over one analysis: what is bound,
// where tuios argues with itself, where tuios argues with the pane's program,
// and a place to press a key and be told all three about it.
const (
	KeybindTabBindings = iota
	KeybindTabConflicts
	KeybindTabGuests
	KeybindTabRecord
	keybindTabCount
)

// KeybindTabNames are the tab labels, indexed by the constants above.
var KeybindTabNames = []string{"Bindings", "Conflicts", "Guests", "Record"}

// keybindManager is the overlay's state. It is a field rather than a set of
// bools on OS because the recorder alone needs five of them, and a recorder
// half-mixed into the model is how the arm flag ends up surviving a close.
type keybindManager struct {
	// report is the analysis, built once when the overlay opens. Rebuilding it
	// per frame would re-read the pane's foreground process on the render path,
	// which is the one thing this must not do.
	report config.KeybindReport

	query    string
	selected int
	scroll   int
	// filtered memoises FilteredKeybindRows against filteredFor, the query it
	// was computed for. Invalidated by setting filtered to nil, which is what
	// every path that changes the query or the report does.
	filtered    []config.Binding
	filteredFor string

	// armed is whether the next key press is data rather than a command. It is
	// one-shot: capturing a key disarms it, so there is always a key that means
	// "stop capturing" and it is the next one.
	armed bool
	// captured is the last key recorded, and fate is what tuios does with it.
	captured string
	fate     config.KeyFate
	// bindSection and bindAction are what a captured key would be bound to.
	//
	// The target is chosen before the key is pressed, not after. Recording a key
	// and then asking which of two hundred actions to attach it to needs a
	// second picker and gives the recorder two jobs; arming from a selected row
	// on the Bindings tab means the answer is already on screen. Arming from the
	// Record tab leaves them empty, which is the inspect-only case: press a key,
	// find out what it does, bind nothing.
	bindSection string
	bindAction  string
	// bound is the key most recently written to the config, so the recorder can
	// say the write happened rather than looking unchanged.
	bound string
}

// OpenKeybindManager builds the report for the focused pane and shows the
// overlay.
//
// The report is built here, on the Update goroutine, and not again until the
// overlay is reopened. That is the whole of its cost: one pass over the config
// and one /proc read, at the moment a human asked for it.
func (m *OS) OpenKeybindManager() {
	m.ShowKeybindManager = true
	m.keybinds = keybindManager{report: m.buildKeybindReport()}
	m.KeybindTab = KeybindTabBindings
}

// CloseKeybindManager hides the overlay and drops the report, so a reopen reads
// the pane again rather than showing what was true last time.
func (m *OS) CloseKeybindManager() {
	m.ShowKeybindManager = false
	m.keybinds = keybindManager{}
}

// buildKeybindReport gathers what tuios can observe about the focused pane and
// hands it to the analysis.
//
// Every field is optional and an unavailable one is left zero rather than
// guessed: PaneFacts is written so that a report built with nothing known says
// nothing about the pane instead of saying the pane is empty.
func (m *OS) buildKeybindReport() config.KeybindReport {
	if m.KeybindRegistry == nil {
		return config.KeybindReport{}
	}
	facts := config.PaneFacts{HostDisambiguates: m.KeyboardEnhancementsEnabled}
	if w := m.GetFocusedWindow(); w != nil {
		facts.Command = w.ForegroundCommand()
		facts.HasForeground = w.HasForegroundProcess()
		// A daemon-backed pane has no local PTY to ask, but the daemon's own
		// poll already shipped the same observation on the wire
		// (WindowState.ForegroundCmd, empty at a shell prompt). Reading it
		// keeps the observed tier alive for attached, SSH and web clients,
		// at most one poll interval stale.
		if facts.Command == "" && w.ForegroundCmd != "" {
			facts.Command = w.ForegroundCmd
			facts.HasForeground = true
		}
		if w.Terminal != nil {
			facts.AltScreen = w.Terminal.IsAltScreen()
			facts.GuestKittyFlags = w.Terminal.KittyKeyboardFlags()
		}
	}
	return m.KeybindRegistry.Report(facts)
}

// KeybindReport exposes the open overlay's analysis, for the renderer.
func (m *OS) KeybindReport() config.KeybindReport { return m.keybinds.report }

// KeybindQuery is the current filter text.
func (m *OS) KeybindQuery() string { return m.keybinds.query }

// KeybindSelected is the selected row in the active tab's list.
func (m *OS) KeybindSelected() int { return m.keybinds.selected }

// KeybindArmed is whether the recorder is waiting for a key.
func (m *OS) KeybindArmed() bool { return m.keybinds.armed }

// KeybindCaptured is the last recorded key and what tuios does with it.
func (m *OS) KeybindCaptured() (string, config.KeyFate) {
	return m.keybinds.captured, m.keybinds.fate
}

// KeybindBindTarget is the action a captured key would be bound to, and the
// section it would be written into. Both empty means inspect-only.
func (m *OS) KeybindBindTarget() (section, action string) {
	return m.keybinds.bindSection, m.keybinds.bindAction
}

// KeybindBound is the key most recently written to the config.
func (m *OS) KeybindBound() string { return m.keybinds.bound }

// KeybindSelectedBinding is the binding under the cursor on the Bindings tab,
// and whether there is one.
func (m *OS) KeybindSelectedBinding() (config.Binding, bool) {
	if m.KeybindTab != KeybindTabBindings {
		return config.Binding{}, false
	}
	rows := m.FilteredKeybindRows()
	i := m.keybinds.selected
	if i < 0 || i >= len(rows) {
		return config.Binding{}, false
	}
	return rows[i], true
}

// keybindRowCount is how many rows the active tab has after filtering.
func (m *OS) keybindRowCount() int {
	switch m.KeybindTab {
	case KeybindTabBindings:
		return len(m.FilteredKeybindRows())
	case KeybindTabConflicts:
		return len(m.keybinds.report.Collisions)
	case KeybindTabGuests:
		return len(m.keybinds.report.GuestClashes)
	}
	return 0
}

// FilteredKeybindRows is the Bindings tab's list: every binding, filtered by
// the query.
//
// Memoised against the query, because the renderer asks for this once to count
// the rows and again for every row it draws. Recomputed per call it was a fuzzy
// sweep over a few hundred candidates a dozen times a frame, for a list that
// only changes when a keystroke changes the query.
func (m *OS) FilteredKeybindRows() []config.Binding {
	q := strings.ToLower(strings.TrimSpace(m.keybinds.query))
	if m.keybinds.filtered != nil && m.keybinds.filteredFor == q {
		return m.keybinds.filtered
	}
	m.keybinds.filteredFor = q
	m.keybinds.filtered = m.filterKeybindRows(q)
	return m.keybinds.filtered
}

// filterKeybindRows does the matching.
//
// The query runs against the chord, the action, its description and the scope
// joined into one candidate, so "close" finds the action and "ctrl+b" finds the
// chord without the user having to know which field they are searching. Fuzzy
// rather than substring, so it behaves like the palette and the theme picker.
func (m *OS) filterKeybindRows(q string) []config.Binding {
	all := m.keybinds.report.Bindings
	if q == "" {
		return all
	}
	hay := make([]string, len(all))
	for i, b := range all {
		hay[i] = b.Press + " " + b.Action + " " + b.Desc + " " + b.Scope
	}
	hits := fuzzy.Filter(q, hay)
	out := make([]config.Binding, 0, len(hits))
	for _, h := range hits {
		// Hit.Index is the candidate's position in the input, so the binding it
		// came from is a direct lookup rather than a search back through hay.
		if h.Index >= 0 && h.Index < len(all) {
			out = append(out, all[h.Index])
		}
	}
	return out
}

// KeybindMove steps the selection within the active tab.
func (m *OS) KeybindMove(delta int) {
	count := m.keybindRowCount()
	if count == 0 {
		m.keybinds.selected, m.keybinds.scroll = 0, 0
		return
	}
	m.keybinds.selected = clampInt(m.keybinds.selected+delta, 0, count-1)
}

// KeybindSetTab switches tabs, resetting the list position: the tabs list
// different things, so carrying a row index across them lands somewhere
// arbitrary.
func (m *OS) KeybindSetTab(tab int) {
	if tab < 0 || tab >= keybindTabCount {
		return
	}
	m.KeybindTab = tab
	m.keybinds.selected = 0
	m.keybinds.scroll = 0
	// Leaving the Record tab disarms: an armed recorder that survived a tab
	// switch would eat the next keystroke somewhere it is not being shown.
	if tab != KeybindTabRecord {
		m.keybinds.armed = false
	}
}

// KeybindStepTab moves to the next or previous tab, wrapping.
func (m *OS) KeybindStepTab(delta int) {
	m.KeybindSetTab(((m.KeybindTab+delta)%keybindTabCount + keybindTabCount) % keybindTabCount)
}

// KeybindSetQuery replaces the filter text and puts the selection back at the
// top, since the row it pointed at is probably gone.
func (m *OS) KeybindSetQuery(q string) {
	m.keybinds.query = q
	m.keybinds.filtered = nil
	m.keybinds.selected = 0
	m.keybinds.scroll = 0
}

// KeybindArm makes the next key press data rather than a command, with no bind
// target: press a key, find out what it does, change nothing.
//
// One-shot by design. An arm that stayed armed would need a key to mean "stop",
// and every key that could mean it is a key the recorder is supposed to be able
// to record. Disarming on capture means the escape is always the very next
// press, whatever it is.
func (m *OS) KeybindArm() { m.armKeybind("", "") }

// KeybindArmFor arms the recorder with a binding target, so the captured key
// can be written to that action.
func (m *OS) KeybindArmFor(section, action string) { m.armKeybind(section, action) }

func (m *OS) armKeybind(section, action string) {
	m.KeybindTab = KeybindTabRecord
	m.keybinds.armed = true
	m.keybinds.bindSection = section
	m.keybinds.bindAction = action
	m.keybinds.bound = ""
}

// KeybindCapture records a pressed key and works out everything tuios does with
// it. It disarms, so the key after a capture is a command again.
func (m *OS) KeybindCapture(key string) {
	m.keybinds.armed = false
	m.keybinds.captured = key
	if m.KeybindRegistry == nil {
		return
	}
	m.keybinds.fate = m.KeybindRegistry.Fate(key, m.keybinds.report.Pane)
}

// KeybindCommitBinding writes the captured key to the armed target and
// persists it.
//
// The write goes out through persistSettings, which renders the file on this
// goroutine and returns a command that does the write, so the Update loop is
// never waiting on a disk.
func (m *OS) KeybindCommitBinding() tea.Cmd {
	key := m.keybinds.captured
	section, action := m.keybinds.bindSection, m.keybinds.bindAction
	if key == "" || action == "" || m.UserConfig == nil || m.KeybindRegistry == nil {
		return nil
	}
	target := m.UserConfig.Keybindings.SectionFor(section)
	if target == nil {
		return nil
	}
	// Appended rather than replacing, so recording a second key for an action
	// adds an alternative instead of silently dropping the one that was there.
	for _, existing := range target[action] {
		if strings.EqualFold(existing, key) {
			m.ShowNotification(key+" is already bound to "+action, "info", 0)
			return nil
		}
	}
	target[action] = append(target[action], key)

	// Reloaded and re-reported before the file write is even started, so the
	// overlay shows the consequence of the binding (including any conflict it
	// just created) rather than the state before it.
	m.KeybindRegistry.Reload(m.UserConfig)
	m.keybinds.report = m.buildKeybindReport()
	m.keybinds.filtered = nil
	m.KeybindCapture(key)
	m.keybinds.bound = key
	m.ShowNotification("Bound "+key+" to "+action, "success", config.NotificationDuration)
	return m.persistSettings()
}
