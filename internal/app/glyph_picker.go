package app

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The glyph picker is the theme picker's opposite number. A theme is a name
// from an open set and so is a glyph set, so both are searched rather than
// cycled, and both preview live: the theme picker shows you the colour, this
// shows you the shape.
//
// What it shows that a cycler could not is the difference between what a set
// says and what draws. A set states only the roles it changes, and a role whose
// glyph is the wrong width for its slot is dropped back to the default with
// nothing on screen to say so, because the alternative is a window control the
// pointer no longer lands on. list-glyphs answers that in two columns for an
// agent; the same answer belongs on screen.

// glyphSample is one set's preview: the glyphs a row draws, and what the set
// asked for that did not survive.
type glyphSample struct {
	// Frame is the shape strip: a corner of the border, the window controls and
	// the rail's marks, which is the four surfaces a set can change.
	Frame string
	// Named is how many roles the set states.
	Named int
	// Dropped are the roles the set named that are not what draws, in the set's
	// own spelling. A role lands here when its glyph was the wrong width for
	// the slot, which is the failure that is otherwise invisible.
	Dropped []string
	// ASCII says the frame is 7-bit throughout, so the set is offerable to a
	// terminal that can draw nothing else.
	ASCII bool
}

// glyphPickerItems returns the set ids offered, filtered by the current query.
func (m *OS) glyphPickerItems() []string {
	all := theme.AvailableGlyphSets()
	q := strings.ToLower(strings.TrimSpace(m.glyphList.Query.String()))
	if q == "" {
		return all
	}
	hits := fuzzy.Filter(q, all)
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Text
	}
	return out
}

// OpenGlyphPicker shows the searchable glyph-set picker, remembering the
// current set so cancel can restore it.
func (m *OS) OpenGlyphPicker() {
	// A set written a moment ago resolves on this call rather than on the next
	// restart, which is what makes authoring one worth doing.
	theme.ReloadGlyphSets()
	m.ShowGlyphPicker = true
	m.glyphList.Reset()
	m.GlyphPickerOriginal = theme.ActiveGlyphSetID()

	// Sampled once here rather than per frame: each sample borrows the active
	// selection to read what the set draws, and doing that while composing a
	// frame would be churning global state under the renderer.
	m.buildGlyphSamples()

	for i, id := range m.glyphPickerItems() {
		if id == m.GlyphPickerOriginal {
			m.glyphList.Cursor = i
			break
		}
	}

	// Cascade down-right of the settings panel it was opened from, so both are
	// visible and can be dragged as separate panels.
	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("glyphpicker", so[0]+10, so[1]+3)
	}
}

// buildGlyphSamples fills the preview for every set on offer.
func (m *OS) buildGlyphSamples() {
	sets := theme.AvailableGlyphSets()
	m.GlyphPickerSamples = make(map[string]glyphSample, len(sets))
	for _, id := range sets {
		m.GlyphPickerSamples[id] = glyphSampleFor(id, &m.Settings)
	}
}

// glyphSampleFor builds one set's preview from what it would draw.
func glyphSampleFor(id string, s *config.Settings) glyphSample {
	drawn := s.GlyphsForSet(id)

	// The strip reads left to right as border, window controls, rail marks: a
	// corner and two cells of edge, the three buttons, then the focus mark, a
	// resting bullet and the attention mark.
	var b strings.Builder
	b.WriteString(drawn["border.top_left"])
	b.WriteString(strings.Repeat(orGlyph(drawn["border.top"], "-"), 2))
	b.WriteString(drawn["border.top_right"])
	b.WriteString("  ")
	b.WriteString(drawn["minimize"])
	b.WriteString(drawn["maximize"])
	b.WriteString(drawn["close"])
	b.WriteString("  ")
	b.WriteString(drawn["focus"])
	b.WriteString(drawn["bullet"])
	b.WriteString(drawn["attention"])

	sample := glyphSample{Frame: b.String(), ASCII: true}
	for _, glyph := range drawn {
		if !isASCII(glyph) {
			sample.ASCII = false
			break
		}
	}

	// What the set says. Counted off the resolved set, which is what survived
	// loading: a role whose glyph was the wrong width is not here any more.
	set := theme.ResolveGlyphSet(id)
	for _, glyph := range theme.GlyphSetRoles(set) {
		if glyph != "" {
			sample.Named++
		}
	}
	if set.Border != nil {
		for _, glyph := range theme.GlyphSetBorderRoles(set.Border) {
			if glyph != "" {
				sample.Named++
			}
		}
	}

	// What it asked for and did not get. It cannot be found by comparing the
	// set against what draws, because the drop already happened: the loader
	// takes the role out of the set and writes a line about it, and that line
	// is the only record.
	sample.Dropped = theme.GlyphSetDroppedRoles(id)
	sort.Strings(sample.Dropped)
	return sample
}

// orGlyph is fallback when a role resolves to nothing to draw.
func orGlyph(glyph, fallback string) string {
	if glyph == "" {
		return fallback
	}
	return glyph
}

// isASCII reports whether every rune is 7-bit.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}

// CloseGlyphPicker hides the picker without changing the applied set.
func (m *OS) CloseGlyphPicker() {
	m.ShowGlyphPicker = false
	m.glyphList.Reset()
}

// CancelGlyphPicker restores the set that was active when the picker opened and
// closes it. Used for Esc, so a live preview does not stick. It persists only
// when a preview actually changed the set, so a no-op cancel does not rewrite
// config.toml over the set the file already named.
func (m *OS) CancelGlyphPicker() tea.Cmd {
	if !m.previewsLive() {
		// Nothing was applied, so there is nothing to restore.
		m.CloseGlyphPicker()
		return nil
	}
	current := theme.ActiveGlyphSetID()
	m.applyGlyphSet(m.GlyphPickerOriginal)
	var save tea.Cmd
	if current != m.GlyphPickerOriginal {
		save = m.persistSettings()
	}
	m.CloseGlyphPicker()
	return save
}

// applyGlyphSet previews a set: the config records it and the globals the
// renderer reads are pushed from there, which is the funnel a set selected any
// other way goes through.
func (m *OS) applyGlyphSet(id string) {
	m.setOption("appearance.glyphs", id)
}

// glyphPickerPreview applies the set under the cursor, when previews are
// live, after a move or a refilter.
func (m *OS) glyphPickerPreview() tea.Cmd {
	items := m.glyphPickerItems()
	if c := m.glyphList.Cursor; c >= 0 && c < len(items) && m.previewsLive() {
		m.applyGlyphSet(items[c])
	}
	return nil
}

// GlyphPickerMove moves the selection by delta and previews the new row.
func (m *OS) GlyphPickerMove(delta int) { glyphPicker().move(m, delta) }

// glyphPicker is the glyph-set picker's list behaviour. See list_picker.go.
func glyphPicker() *listPicker {
	return &listPicker{
		kind:  "glyphpicker",
		state: func(m *OS) *listnav.State { return &m.glyphList },
		count: func(m *OS) int { return len(m.glyphPickerItems()) },
		visible: func(m *OS) int {
			_, visible, _ := m.glyphPickerLayout()
			return visible
		},
		accept:     func(m *OS) tea.Cmd { return m.GlyphPickerApplySelection() },
		moved:      func(m *OS) tea.Cmd { return m.glyphPickerPreview() },
		refiltered: func(m *OS) tea.Cmd { return m.glyphPickerPreview() },
		close:      func(m *OS) tea.Cmd { return m.CancelGlyphPicker() },
		clearKey:   true,
	}
}

// GlyphPickerApplySelection commits the selected set, persists it, and closes.
func (m *OS) GlyphPickerApplySelection() tea.Cmd {
	items := m.glyphPickerItems()
	if m.glyphList.Cursor < 0 || m.glyphList.Cursor >= len(items) {
		// Nothing to commit, so nothing is closed. Closing here would leave the
		// preview from the last query that did match on screen, unpersisted,
		// with the picker gone and no Esc left to press to get back to it.
		// Staying up keeps both the query and the escape route that reverts.
		m.ShowNotification("No glyph set matches "+m.glyphList.Query.String(),
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.applyGlyphSet(items[m.glyphList.Cursor])
	save := m.persistSettings()
	m.CloseGlyphPicker()
	return save
}
