package app

import (
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/pkg/tfx"
)

// The screen saver animates a snapshot of the screen after a spell with no
// input, and stops on the first key or pointer movement.
//
// It costs nothing at idle. Two rules make that true, and both matter:
//
//   - Arming is one deferred timer, never a standing tick. Input records the
//     time and starts a timer only if none is in flight; when that timer fires
//     early it re-arms for the remainder. So at most one timer exists, and it
//     fires at most once per idle delay however hard the keyboard is used.
//   - While running, the saver drives its own frames through screensaverFrame,
//     the way autoScrollTick does. It adds no term to tickNeedsWork and none to
//     needsRender, so the maintenance tick's idle path is untouched, byte for
//     byte, whether the saver is armed or not.

// screensaverArmMsg is the deferred idle timer firing. It is not proof that
// the session is idle: the timer may have been armed before later input, which
// is why the handler re-checks the elapsed time.
type screensaverArmMsg struct{}

// screensaverFrameMsg asks the running saver for its next frame.
type screensaverFrameMsg struct{}

// screensaverState is everything the saver needs. The zero value is a saver
// that is off and unarmed.
type screensaverState struct {
	// active is true while the animation is on screen.
	active bool
	// armed is true while a deferred start timer is in flight.
	armed bool
	// lastInput is when input last arrived. The armed timer compares against
	// it rather than assuming it fired at the right moment.
	lastInput time.Time

	engine *tfx.Engine
	effect tfx.Effect
	// name is the running effect, for the hint line.
	name string
	// frame is the last rendered frame, held so a repaint that is not a new
	// animation frame does not have to step the effect.
	frame string
	// width and height are the canvas the effect was built for. A resize
	// rebuilds rather than stretching.
	width  int
	height int
	// capture is the converted screen the effects are built over. It is kept
	// so an effect that runs to an end can be replaced without composing and
	// re-capturing a screen that is no longer showing.
	capture [][]tfx.InputCell
}

// ScreensaverActive reports whether the saver is on screen. Other packages ask
// so they can leave the input alone.
func (m *OS) ScreensaverActive() bool { return m.screensaver.active }

// screensaverConfig is the [screensaver] section this client holds.
func (m *OS) screensaverConfig() config.ScreensaverConfig {
	if m.UserConfig == nil {
		return config.ScreensaverConfig{}
	}
	return m.UserConfig.Screensaver
}

// armScreensaver starts the single deferred timer, unless the saver is off,
// already running, or a timer is already in flight.
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
	return screensaverArmCmd(screensaverIdleDelay(cfg))
}

// screensaverIdleDelay is the quiet time before the saver starts.
func screensaverIdleDelay(cfg config.ScreensaverConfig) time.Duration {
	return time.Duration(cfg.IdleDelayMinutes()) * time.Minute
}

// screensaverArmCmd waits out a delay and then asks whether to start.
func screensaverArmCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return screensaverArmMsg{} })
}

// screensaverFrameCmd schedules the next animation frame.
func screensaverFrameCmd() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(config.NormalFPS), func(time.Time) tea.Msg {
		return screensaverFrameMsg{}
	})
}

// handleScreensaverArm runs when the deferred timer fires. It starts the saver
// only if the session really has been quiet for the whole delay; otherwise it
// re-arms for whatever is left, which is what keeps one timer serving any
// amount of input.
func (m *OS) handleScreensaverArm() tea.Cmd {
	m.screensaver.armed = false
	cfg := m.screensaverConfig()
	if !cfg.IsEnabled() || m.screensaver.active {
		return nil
	}
	delay := screensaverIdleDelay(cfg)
	if idle := time.Since(m.screensaver.lastInput); idle < delay {
		m.screensaver.armed = true
		return screensaverArmCmd(delay - idle)
	}
	if !m.screensaverMayStart(cfg) {
		// Something is working. Wait another full delay and ask again rather
		// than polling, so a long build costs one timer every few minutes.
		m.screensaver.armed = true
		return screensaverArmCmd(delay)
	}
	if !m.startScreensaver(cfg) {
		m.screensaver.armed = true
		return screensaverArmCmd(delay)
	}
	return screensaverFrameCmd()
}

// screensaverMayStart reports whether it is polite to cover the screen now.
//
// A saver that hides a running build is a bug, so a pane with a foreground
// process or an agent that is working or waiting on the user holds it off
// unless the setting says otherwise.
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

// startScreensaver captures the screen and builds the effect over it. It
// reports whether the saver actually started.
func (m *OS) startScreensaver(cfg config.ScreensaverConfig) bool {
	width, height := m.GetRenderWidth(), m.GetRenderHeight()
	if width < 8 || height < 4 {
		return false
	}
	grid := m.composedGrid(0, 0, width, height)
	if grid == nil {
		return false
	}
	name, effect := screensaverEffect(cfg.EffectName())
	if effect == nil {
		return false
	}

	capture := screensaverCells(grid)
	engine, ok := screensaverBuild(capture, width, height, effect)
	if !ok {
		return false
	}

	m.screensaver.capture = capture
	m.screensaver.engine = engine
	m.screensaver.effect = effect
	m.screensaver.name = name
	m.screensaver.width = width
	m.screensaver.height = height
	m.screensaver.active = true
	m.screensaver.frame = engine.Frame()
	m.renderSkipped = false
	return true
}

// screensaverBuild puts an effect over a captured screen.
//
// The colour policy is the whole reason a screen saver reads as one: every
// character resolves back to the colour it was captured with, so the screen
// reassembles as itself rather than in the effect's own palette.
func screensaverBuild(capture [][]tfx.InputCell, width, height int, effect tfx.Effect) (*tfx.Engine, bool) {
	terminal := tfx.NewTerminalFromCells(capture, tfx.TerminalConfig{
		Width:                 width,
		Height:                height,
		ExistingColorHandling: tfx.DynamicExistingColors,
	})
	engine := tfx.NewEngine(terminal, tfx.NewRng(rand.Uint64()))
	if err := effect.Build(engine); err != nil {
		return nil, false
	}
	return engine, true
}

// screensaverEffect resolves a configured name to a fresh effect. The random
// setting picks a different one each time the saver starts.
func screensaverEffect(name string) (string, tfx.Effect) {
	if name == config.ScreensaverRandomEffect {
		names := tfx.Names()
		if len(names) == 0 {
			return "", nil
		}
		name = names[rand.IntN(len(names))]
	}
	d, ok := tfx.Lookup(name)
	if !ok {
		return "", nil
	}
	return d.Name, d.New()
}

// screensaverCells turns a captured grid into engine input.
//
// A glyph wider than one cell is dropped. The engine places one character per
// column, so keeping a two-cell glyph would push the rest of its row one
// column right and the whole capture would shear. A missing emoji in a screen
// that is dissolving anyway is the cheaper loss.
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

// handleScreensaverFrame advances the animation by one frame. An effect that
// finished restarts, so the saver keeps going until someone dismisses it.
func (m *OS) handleScreensaverFrame() tea.Cmd {
	if !m.screensaver.active {
		// A stale frame message from a saver that was already dismissed. Drop
		// it without re-arming, or the timer would outlive the animation.
		return nil
	}
	// A resize while the saver runs invalidates the capture it was built from.
	// Rebuilding needs a screen to capture, and the screen is the animation, so
	// the honest answer is to stop and let the idle timer bring it back.
	if m.GetRenderWidth() != m.screensaver.width || m.GetRenderHeight() != m.screensaver.height {
		m.stopScreensaver()
		return m.armScreensaver()
	}
	if !m.screensaver.effect.Advance(m.screensaver.engine) {
		if !m.restartScreensaverEffect() {
			m.stopScreensaver()
			return m.armScreensaver()
		}
	} else {
		m.screensaver.frame = m.screensaver.engine.Frame()
	}
	m.renderSkipped = false
	return screensaverFrameCmd()
}

// restartScreensaverEffect builds a fresh effect over the same capture, so an
// effect that runs to an end loops instead of freezing.
func (m *OS) restartScreensaverEffect() bool {
	cfg := m.screensaverConfig()
	name, effect := screensaverEffect(cfg.EffectName())
	if effect == nil {
		return false
	}
	// A fresh terminal, because the finished one has every character resolved
	// and its scenes played out.
	if m.screensaver.capture == nil {
		return false
	}
	engine, ok := screensaverBuild(m.screensaver.capture, m.screensaver.width, m.screensaver.height, effect)
	if !ok {
		return false
	}
	m.screensaver.engine = engine
	m.screensaver.effect = effect
	m.screensaver.name = name
	m.screensaver.frame = engine.Frame()
	return true
}

// dismissScreensaver takes the animation off the screen and re-arms the idle
// timer. It is called from the input path, which then drops the event that
// triggered it.
func (m *OS) dismissScreensaver() tea.Cmd {
	m.stopScreensaver()
	m.screensaver.lastInput = time.Now()
	m.MarkAllDirty()
	m.renderSkipped = false
	return m.armScreensaver()
}

// stopScreensaver drops the animation and everything it held.
func (m *OS) stopScreensaver() {
	m.screensaver.active = false
	m.screensaver.engine = nil
	m.screensaver.effect = nil
	m.screensaver.frame = ""
	m.screensaver.capture = nil
}
