package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

type effectPreviewFrameMsg struct{ gen int }

type effectPreview struct {
	capture      [][]tfx.InputCell
	canvasWidth  int
	canvasHeight int
	width        int
	height       int

	engine  *tfx.Engine
	effect  tfx.Effect
	running string
	frame   string

	gen       int
	ticking   bool
	resized   bool
	capturing bool
}

func (m *OS) effectPickerItems() []string {
	all := config.ScreensaverEffects
	q := strings.ToLower(strings.TrimSpace(m.effectList.Query.String()))
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

// OpenEffectPicker shows the searchable effect picker and starts its preview.
func (m *OS) OpenEffectPicker() tea.Cmd {
	m.effectList.Reset()
	m.EffectPickerOriginal = m.screensaverConfig().EffectName()
	for i, id := range m.effectPickerItems() {
		if id == m.EffectPickerOriginal {
			m.effectList.Cursor = i
			break
		}
	}

	m.captureEffectPreview()
	m.ShowEffectPicker = true

	if m.ShowSettings {
		so := m.overlayOffset("settings")
		m.setOverlayOffset("effectpicker", so[0]+10, so[1]+3)
	}

	m.effectPreview.gen++
	m.effectPreview.ticking = false
	m.buildEffectPreview()
	return m.effectPreviewTick()
}

func (m *OS) captureEffectPreview() {
	p := &m.effectPreview
	p.capture, p.canvasWidth, p.canvasHeight = nil, 0, 0
	p.resized = false
	width, height := m.GetRenderWidth(), m.GetRenderHeight()
	if width < 8 || height < 4 {
		return
	}
	p.capturing = true
	grid := m.composedGrid(0, 0, width, height)
	p.capturing = false
	if grid == nil {
		return
	}
	p.capture = screensaverCells(grid)
	p.canvasWidth, p.canvasHeight = grid.Cols, grid.Rows
	p.width, p.height = width, height
}

func (m *OS) buildEffectPreview() {
	p := &m.effectPreview
	p.engine, p.effect, p.running, p.frame = nil, nil, "", ""
	if p.capture == nil {
		return
	}
	items := m.effectPickerItems()
	if m.effectList.Cursor < 0 || m.effectList.Cursor >= len(items) {
		return
	}
	name, effect, fill := screensaverEffect(items[m.effectList.Cursor])
	if effect == nil {
		return
	}
	engine, ok := screensaverBuild(p.capture, p.canvasWidth, p.canvasHeight, effect, fill, &m.Settings)
	if !ok {
		return
	}
	p.engine, p.effect, p.running = engine, effect, name
	p.frame = engine.Frame()
	m.renderSkipped = false
}

func (m *OS) effectPreviewTick() tea.Cmd {
	p := &m.effectPreview
	if p.engine == nil || p.ticking {
		return nil
	}
	p.ticking = true
	return effectPreviewFrameCmd(p.gen, &m.Settings)
}

func effectPreviewFrameCmd(gen int, s *config.Settings) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(screensaverRate(s)), func(time.Time) tea.Msg {
		return effectPreviewFrameMsg{gen: gen}
	})
}

func (m *OS) handleEffectPreviewFrame(msg effectPreviewFrameMsg) tea.Cmd {
	p := &m.effectPreview
	if msg.gen != p.gen {
		return nil
	}
	p.ticking = false
	if !m.ShowEffectPicker {
		return nil
	}
	if p.engine == nil {
		return nil
	}
	if m.GetRenderWidth() != p.width || m.GetRenderHeight() != p.height {
		m.stopEffectPreview()
		p.resized = true
		m.MarkAllDirty()
		m.renderSkipped = false
		return nil
	}
	if !p.effect.Advance(p.engine) {
		m.buildEffectPreview()
	} else {
		p.frame = p.engine.Frame()
	}
	m.renderSkipped = false
	return m.effectPreviewTick()
}

func (m *OS) stopEffectPreview() {
	p := &m.effectPreview
	p.engine, p.effect = nil, nil
	p.frame, p.running = "", ""
	p.capture = nil
	p.canvasWidth, p.canvasHeight = 0, 0
}

func (m *OS) effectPickerPreview() tea.Cmd {
	m.buildEffectPreview()
	return m.effectPreviewTick()
}

func effectPicker() *listPicker {
	return &listPicker{
		kind:  "effectpicker",
		state: func(m *OS) *listnav.State { return &m.effectList },
		count: func(m *OS) int { return len(m.effectPickerItems()) },
		visible: func(m *OS) int {
			_, visible, _ := m.effectPickerLayout()
			return visible
		},
		accept:     func(m *OS) tea.Cmd { return m.EffectPickerApplySelection() },
		moved:      func(m *OS) tea.Cmd { return m.effectPickerPreview() },
		refiltered: func(m *OS) tea.Cmd { return m.effectPickerPreview() },
		close: func(m *OS) tea.Cmd {
			m.CancelEffectPicker()
			return nil
		},
		clearKey: true,
	}
}

// CloseEffectPicker hides the picker and takes the animation off the screen.
func (m *OS) CloseEffectPicker() {
	m.ShowEffectPicker = false
	m.effectList.Reset()
	m.stopEffectPreview()
	m.effectPreview.resized = false
	m.MarkAllDirty()
	m.renderSkipped = false
}

// CancelEffectPicker closes without changing the setting. Used for Esc and for
// a click outside the panel.
//
// Unlike the theme and glyph pickers it has nothing to restore. Those two
// preview by applying the value, so cancel has to put the old one back; this
// one previews by running the animation, and the setting is not written until
// Enter. The contract a user sees is the same either way: Esc leaves the row
// where it was.
func (m *OS) CancelEffectPicker() {
	m.CloseEffectPicker()
}

// EffectPickerApplySelection commits the selected effect, persists it, and
// closes.
func (m *OS) EffectPickerApplySelection() tea.Cmd {
	items := m.effectPickerItems()
	if m.effectList.Cursor < 0 || m.effectList.Cursor >= len(items) {
		m.ShowNotification("No effect matches "+m.effectList.Query.String(),
			"info", m.Settings.NotificationDuration)
		return nil
	}
	m.setOption("screensaver.effect", items[m.effectList.Cursor])
	save := m.persistSettings()
	m.CloseEffectPicker()
	return save
}

var effectOpenings = map[string]effectOpening{
	"binarypath":      {frames: 801},
	"blackhole":       {frames: 848},
	"bouncyballs":     {frames: 1043},
	"bubbles":         {frames: 1411},
	"burn":            {frames: 754},
	"crumble":         {frames: 544},
	"decrypt":         {frames: 1014},
	"errorcorrect":    {frames: 454},
	"expand":          {frames: 88},
	"fireworks":       {frames: 1164},
	"highlight":       {frames: 0, keepsScreen: true},
	"laseretch":       {frames: 1649},
	"matrix":          {frames: 1548},
	"middleout":       {frames: 99},
	"orbittingvolley": {frames: 1043},
	"overflow":        {frames: 152},
	"pour":            {frames: 807},
	"print":           {frames: 2087},
	"rain":            {frames: 539},
	"randomsequence":  {frames: 156},
	"rings":           {frames: 1373},
	"scattered":       {frames: 136},
	"slice":           {frames: 121},
	"slide":           {frames: 113},
	"smoke":           {frames: 154},
	"spotlights":      {frames: 641},
	"spray":           {frames: 366},
	"swarm":           {frames: 2778},
	"sweep":           {frames: 177},
	"synthgrid":       {frames: 530},
	"thunderstorm":    {frames: 0},
	"tuffbaby":        {frames: 505},
	"unstable":        {frames: 291},
	"vhstape":         {frames: 702},
	"waves":           {frames: 410},
	"wipe":            {frames: 54},
}

type effectOpening struct {
	frames      int
	keepsScreen bool
}

type effectOpeningBand int

const (
	effectOpeningUnknown effectOpeningBand = iota
	effectOpeningNone
	effectOpeningShort
	effectOpeningMedium
	effectOpeningLong
)

const (
	effectMediumOpening = 5.5
	effectSlowOpening   = 10.0
)

func effectOpeningBandOf(name string, s *config.Settings) effectOpeningBand {
	opening, ok := effectOpenings[name]
	if !ok {
		return effectOpeningUnknown
	}
	if opening.keepsScreen {
		return effectOpeningNone
	}
	switch seconds := float64(opening.frames) / float64(screensaverRate(s)); {
	case seconds < effectMediumOpening:
		return effectOpeningShort
	case seconds < effectSlowOpening:
		return effectOpeningMedium
	default:
		return effectOpeningLong
	}
}
