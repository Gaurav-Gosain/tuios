package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
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

type keybindManager struct {
	report config.KeybindReport

	lists       [keybindTabCount]listnav.State
	filtered    []config.Binding
	filteredFor string

	armed       bool
	captured    string
	fate        config.KeyFate
	bindSection string
	bindAction  string
	bound       string
}

// OpenKeybindManager builds the report for the focused pane and shows the
// overlay.
//
// The report is built here, on the Update goroutine, and not again until the
// overlay is reopened. That is the whole of its cost: one pass over the config
// and one /proc read, at the moment a human asked for it.
func (m *OS) OpenKeybindManager() { m.OpenKeybindManagerWith("") }

// OpenKeybindManagerWith opens the overlay with the Bindings filter already
// filled in.
//
// The palette hands its own query over this way. Someone who typed "close" into
// the palette and picked the keybind row has already said what they are looking
// for, and making them type it a second time into a list of a few hundred
// bindings is the hunting the palette row exists to avoid.
func (m *OS) OpenKeybindManagerWith(query string) {
	m.ShowKeybindManager = true
	m.keybinds = keybindManager{report: m.buildKeybindReport()}
	m.keybinds.lists[KeybindTabBindings].Query.Set(query)
	m.KeybindTab = KeybindTabBindings
}

// CloseKeybindManager hides the overlay and drops the report, so a reopen reads
// the pane again rather than showing what was true last time.
func (m *OS) CloseKeybindManager() {
	m.ShowKeybindManager = false
	m.keybinds = keybindManager{}
}

func (m *OS) buildKeybindReport() config.KeybindReport {
	if m.KeybindRegistry == nil {
		return config.KeybindReport{}
	}
	facts := config.PaneFacts{HostDisambiguates: m.KeyboardEnhancementsEnabled}
	if w := m.GetFocusedWindow(); w != nil {
		facts.Command = w.ForegroundCommand()
		facts.HasForeground = w.HasForegroundProcess()
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
func (m *OS) KeybindQuery() string { return m.keybinds.lists[KeybindTabBindings].Query.String() }

func (m *OS) keybindList() *listnav.State { return &m.keybinds.lists[m.KeybindTab] }

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
	i := m.keybindList().Cursor
	if i < 0 || i >= len(rows) {
		return config.Binding{}, false
	}
	return rows[i], true
}

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
	q := strings.ToLower(strings.TrimSpace(m.keybinds.lists[KeybindTabBindings].Query.String()))
	if m.keybinds.filtered != nil && m.keybinds.filteredFor == q {
		return m.keybinds.filtered
	}
	m.keybinds.filteredFor = q
	m.keybinds.filtered = m.filterKeybindRows(q)
	return m.keybinds.filtered
}

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
		l := m.keybindList()
		l.Cursor, l.Scroll = 0, 0
		return
	}
	m.keybindList().Move(delta, count, keybindVisibleRows, m.listWraps(), nil)
}

// KeybindSelect puts the cursor on row i of the tab that shows.
func (m *OS) KeybindSelect(i int) {
	m.keybindList().Select(i, m.keybindRowCount(), keybindVisibleRows)
}

// KeybindSetTab switches tabs. Each tab keeps its own place (keybinds.lists),
// so coming back to one finds the row it was on.
func (m *OS) KeybindSetTab(tab int) {
	if tab < 0 || tab >= keybindTabCount {
		return
	}
	m.KeybindTab = tab
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
	m.keybinds.lists[KeybindTabBindings].Query.Set(q)
	m.keybindRefiltered()
}

// KeybindQueryKey applies one key press to the filter line (see lineKey).
// Left and right are not here: they change the tab.
func (m *OS) KeybindQueryKey(key, text string) {
	if changed, _ := lineKey(&m.keybinds.lists[KeybindTabBindings].Query, key, text, cleanControl); changed {
		m.keybindRefiltered()
	}
}

// KeybindQueryPaste types a paste at the filter line's caret.
func (m *OS) KeybindQueryPaste(s string) {
	if lineInsert(&m.keybinds.lists[KeybindTabBindings].Query, s, cleanControl) {
		m.keybindRefiltered()
	}
}

func (m *OS) keybindRefiltered() {
	m.keybinds.filtered = nil
	m.keybinds.lists[KeybindTabBindings].Refiltered(0)
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
	for _, existing := range target[action] {
		if config.SameKey(existing, key) {
			m.ShowNotification(key+" is already bound to "+action, "warning", 0)
			return nil
		}
	}
	target[action] = append(target[action], key)

	cmd := m.keybindApply()
	m.KeybindCapture(key)
	m.keybinds.bound = key
	m.ShowNotification("Bound "+key+" to "+action, "success", m.Settings.NotificationDuration)
	return cmd
}
