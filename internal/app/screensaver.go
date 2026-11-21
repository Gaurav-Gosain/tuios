package app

import (
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	tfx "github.com/Gaurav-Gosain/tuiffects"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"tuios.dev/shot"
)

type screensaverArmMsg struct{}

type screensaverState struct {
	active    bool
	armed     bool
	lastInput time.Time

	player *framesSaver

	engine       *tfx.Engine
	effect       tfx.Effect
	name         string
	frame        string
	width        int
	height       int
	canvasWidth  int
	canvasHeight int
	capture      [][]tfx.InputCell
}

func (m *OS) screensaverConfig() config.ScreensaverConfig {
	if m.UserConfig == nil {
		return config.ScreensaverConfig{}
	}
	return m.UserConfig.Screensaver
}

func (m *OS) armScreensaver() tea.Cmd {
	if m.screensaver.active || m.screensaver.armed {
		return nil
	}
	cfg := m.screensaverConfig()
	if !cfg.IsEnabled() {
		return nil
	}
	if m.screensaver.lastInput.IsZero() {
		m.screensaver.lastInput = time.Now()
	}
	m.screensaver.armed = true
	return m.screensaverArmCmd(screensaverIdleDelay(cfg))
}

func screensaverIdleDelay(cfg config.ScreensaverConfig) time.Duration {
	return time.Duration(cfg.IdleDelayMinutes()) * time.Minute
}

func (m *OS) screensaverArmCmd(d time.Duration) tea.Cmd {
	return m.tickUntilStop(d, func(time.Time) tea.Msg { return screensaverArmMsg{} })
}

const screensaverFPS = 60

func screensaverRate(s *config.Settings) int {
	if s.NormalFPS <= 0 || s.NormalFPS > screensaverFPS {
		return screensaverFPS
	}
	return s.NormalFPS
}

func (m *OS) handleScreensaverArm() tea.Cmd {
	m.screensaver.armed = false
	cfg := m.screensaverConfig()
	if !cfg.IsEnabled() || m.screensaver.active {
		return nil
	}
	delay := screensaverIdleDelay(cfg)
	if idle := time.Since(m.screensaver.lastInput); idle < delay {
		m.screensaver.armed = true
		return m.screensaverArmCmd(delay - idle)
	}
	if !m.screensaverMayStart(cfg) {
		m.screensaver.armed = true
		return m.screensaverArmCmd(delay)
	}
	if !m.startScreensaver(cfg) {
		m.screensaver.armed = true
		return m.screensaverArmCmd(delay)
	}
	return m.motionCmd()
}

// StartScreensaverNow runs the saver because the user asked for it.
//
// It skips the busy check that the idle timer respects. That check exists so a
// saver does not cover a build nobody asked it to hide; someone pressing the
// key has asked, and refusing them because a pane is busy would be the program
// arguing with a direct instruction.
func (m *OS) StartScreensaverNow() tea.Cmd {
	if m.screensaver.active {
		return nil
	}
	if !m.startScreensaver(m.screensaverConfig()) {
		m.ShowNotification("The screen saver could not start here.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	return m.motionCmd()
}

func (m *OS) screensaverMayStart(cfg config.ScreensaverConfig) bool {
	if cfg.RunsWhileBusy() {
		return true
	}
	if m.anyForegroundProcess() {
		return false
	}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		switch session.AgentState(w.AgentState) {
		case session.AgentStateWorking, session.AgentStateNeedsInput:
			return false
		}
	}
	return true
}

func (m *OS) startScreensaver(cfg config.ScreensaverConfig) bool {
	width, height := m.GetRenderWidth(), m.GetRenderHeight()
	if width < 8 || height < 4 {
		return false
	}
	if name := cfg.EffectName(); strings.HasPrefix(name, config.ScreensaverPluginPrefix) {
		return m.startFramesSaver(name, width, height)
	}
	grid := m.composedGrid(0, 0, width, height)
	if grid == nil {
		return false
	}
	name, effect, fill := screensaverEffect(cfg.EffectName())
	if effect == nil {
		return false
	}

	capture := screensaverCells(grid)
	engine, ok := screensaverBuild(capture, grid.Cols, grid.Rows, effect, fill, &m.Settings)
	if !ok {
		return false
	}

	m.screensaver.capture = capture
	m.screensaver.engine = engine
	m.screensaver.effect = effect
	m.screensaver.name = name
	m.screensaver.width = width
	m.screensaver.height = height
	m.screensaver.canvasWidth = grid.Cols
	m.screensaver.canvasHeight = grid.Rows
	m.screensaver.active = true
	m.screensaver.frame = engine.Frame()
	m.renderSkipped = false
	return true
}

func screensaverBuild(capture [][]tfx.InputCell, width, height int, effect tfx.Effect, fill bool, s *config.Settings) (*tfx.Engine, bool) {
	terminal := tfx.NewTerminalFromCells(screensaverFit(capture, height), tfx.TerminalConfig{
		Width:                 width,
		Height:                height,
		MakeFillCharacters:    fill,
		AnchorText:            tfx.AnchorSW,
		ExistingColorHandling: tfx.DynamicExistingColors,
	})
	engine := tfx.NewEngine(terminal, tfx.NewRng(rand.Uint64()))
	engine.Clock = tfx.NewVirtualClock(screensaverRate(s))
	if err := effect.Build(engine); err != nil {
		return nil, false
	}
	return engine, true
}

func screensaverFit(capture [][]tfx.InputCell, height int) [][]tfx.InputCell {
	if height < 1 || len(capture) == height {
		return capture
	}
	if len(capture) > height {
		return capture[:height]
	}
	fitted := make([][]tfx.InputCell, height)
	copy(fitted, capture)
	return fitted
}

func screensaverEffect(name string) (string, tfx.Effect, bool) {
	if name == config.ScreensaverRandomEffect {
		names := tfx.Names()
		if len(names) == 0 {
			return "", nil, false
		}
		name = names[rand.IntN(len(names))]
	}
	d, ok := tfx.Lookup(name)
	if !ok {
		return "", nil, false
	}
	return d.Name, d.New(), d.NeedsFillCharacters
}

func screensaverCells(g *shot.Grid) [][]tfx.InputCell {
	cells := make([][]tfx.InputCell, g.Rows)
	for y := range g.Cells {
		row := make([]tfx.InputCell, g.Cols)
		for x, c := range g.Cells[y] {
			if x >= g.Cols || c.Width > 1 {
				continue
			}
			row[x] = tfx.InputCell{
				Symbol: c.Cluster,
				Fg:     tfx.RGB(c.FG.R, c.FG.G, c.FG.B),
				HasFg:  true,
				Bold:   c.Bold,
			}
			if !c.BGDefault {
				row[x].Bg = tfx.RGB(c.BG.R, c.BG.G, c.BG.B)
				row[x].HasBg = true
			}
		}
		cells[y] = row
	}
	return cells
}

func (m *OS) stepScreensaver(time.Time) (bool, tea.Cmd) {
	if m.GetRenderWidth() != m.screensaver.width || m.GetRenderHeight() != m.screensaver.height {
		m.stopScreensaver()
		return true, m.armScreensaver()
	}
	if p := m.screensaver.player; p != nil {
		p.at = (p.at + 1) % len(p.frames)
		m.screensaver.frame = p.show(p.at)
		return true, nil
	}
	if !m.screensaver.effect.Advance(m.screensaver.engine) {
		if !m.restartScreensaverEffect() {
			m.stopScreensaver()
			return true, m.armScreensaver()
		}
	} else {
		m.screensaver.frame = m.screensaver.engine.Frame()
	}
	return true, nil
}

func (m *OS) saverFrameEvery() time.Duration {
	if p := m.screensaver.player; p != nil {
		return p.delays[p.at]
	}
	return time.Second / time.Duration(screensaverRate(&m.Settings))
}

func (m *OS) restartScreensaverEffect() bool {
	cfg := m.screensaverConfig()
	name, effect, fill := screensaverEffect(cfg.EffectName())
	if effect == nil {
		return false
	}
	if m.screensaver.capture == nil {
		return false
	}
	engine, ok := screensaverBuild(m.screensaver.capture, m.screensaver.canvasWidth, m.screensaver.canvasHeight, effect, fill, &m.Settings)
	if !ok {
		return false
	}
	m.screensaver.engine = engine
	m.screensaver.effect = effect
	m.screensaver.name = name
	m.screensaver.frame = engine.Frame()
	return true
}

func (m *OS) dismissScreensaver() tea.Cmd {
	m.stopScreensaver()
	m.screensaver.lastInput = time.Now()
	m.MarkAllDirty()
	m.renderSkipped = false
	return m.armScreensaver()
}

func (m *OS) stopScreensaver() {
	m.screensaver.active = false
	m.screensaver.player = nil
	m.screensaver.engine = nil
	m.screensaver.effect = nil
	m.screensaver.frame = ""
	m.screensaver.capture = nil
	m.screensaver.canvasWidth = 0
	m.screensaver.canvasHeight = 0
}

// ScreensaverActive reports whether the screen saver is on screen.
func (m *OS) ScreensaverActive() bool { return m.screensaver.active }
