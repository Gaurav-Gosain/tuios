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

type glyphSample struct {
	Frame   string
	Named   int
	Dropped []string
	ASCII   bool
}

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
	theme.ReloadGlyphSets()
	m.ShowGlyphPicker = true
	m.glyphList.Reset()
	m.GlyphPickerOriginal = theme.ActiveGlyphSetID()

	m.buildGlyphSamples()

	for i, id := range m.glyphPickerItems() {
		if id == m.GlyphPickerOriginal {
			m.glyphList.Cursor = i
			break
		}
	}

	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("glyphpicker", so[0]+10, so[1]+3)
	}
}

func (m *OS) buildGlyphSamples() {
	sets := theme.AvailableGlyphSets()
	m.GlyphPickerSamples = make(map[string]glyphSample, len(sets))
	for _, id := range sets {
		m.GlyphPickerSamples[id] = glyphSampleFor(id, &m.Settings)
	}
}

func glyphSampleFor(id string, s *config.Settings) glyphSample {
	drawn := s.GlyphsForSet(id)

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

	sample.Dropped = theme.GlyphSetDroppedRoles(id)
	sort.Strings(sample.Dropped)
	return sample
}

func orGlyph(glyph, fallback string) string {
	if glyph == "" {
		return fallback
	}
	return glyph
}

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

func (m *OS) applyGlyphSet(id string) {
	m.setOption("appearance.glyphs", id)
}

func (m *OS) glyphPickerPreview() tea.Cmd {
	items := m.glyphPickerItems()
	if c := m.glyphList.Cursor; c >= 0 && c < len(items) && m.previewsLive() {
		m.applyGlyphSet(items[c])
	}
	return nil
}

// GlyphPickerMove moves the selection by delta and previews the new row.
func (m *OS) GlyphPickerMove(delta int) { glyphPicker().move(m, delta) }

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
		m.ShowNotification("No glyph set matches "+m.glyphList.Query.String(),
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.applyGlyphSet(items[m.glyphList.Cursor])
	save := m.persistSettings()
	m.CloseGlyphPicker()
	return save
}
