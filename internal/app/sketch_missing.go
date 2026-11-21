package app

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/sketchplugin"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
	"tuios.dev/loom"
	sketch "tuios.dev/sketch/core"
	sketchtext "tuios.dev/sketch/text"
)

type missingDiagramMsg struct{ typ string }

func (m *OS) renderSurface(s *loom.Surface, w, h int) *loom.Buffer {
	hooks := tmldraw.Hooks{External: drawWithSketchPlugin}
	if p := m.frameRate.program; p != nil {
		hooks.Missing = func(typ string) { go p.Send(missingDiagramMsg{typ: typ}) }
		hooks.Drawn = func() { go p.Send(sketchDrawnMsg{}) }
	}
	return tmldraw.Render(s, w, h, hooks)
}

type sketchDrawnMsg struct{}

// SketchProgram, when set, is the program of the sketch plugin, or why there
// is none: ErrSketchNoProgram when the plugin is on and has no program, any
// other error when the plugin does not run here. The binary sets it.
var SketchProgram func() (string, error)

// ErrSketchNoProgram is the sketch plugin on, with no program in its folder:
// a folder or a GitHub install that was not built.
var ErrSketchNoProgram = errors.New("the sketch plugin has no program")

var errSketchNotInstalled = errors.New("the sketch plugin is not installed")

var sketchFailed atomic.Bool

func sketchProgram() (string, error) {
	if SketchProgram == nil {
		return "", errSketchNotInstalled
	}
	prog, err := SketchProgram()
	if err == nil && prog == "" {
		err = errSketchNotInstalled
	}
	return prog, err
}

func drawWithSketchPlugin(src string, cols int) ([][]sketchtext.Cell, bool, error) {
	prog, err := sketchProgram()
	if err != nil {
		sketchFailed.Store(true)
		if errors.Is(err, ErrSketchNoProgram) {
			return nil, false, errors.New("the sketch plugin has no program. Run tuios plugins build tuios.sketch")
		}
		typ, _ := sketch.Detect(src)
		return nil, false, errfmt.Errorf("the sketch plugin draws %s diagrams", typ)
	}
	rows, cut, err := sketchplugin.Cells(prog, src, cols)
	if err != nil {
		sketchFailed.Store(true)
		return nil, false, errfmt.Errorf("the sketch plugin could not draw it: %w", err)
	}
	return rows, cut, nil
}

func needsSketchPlugin(src string) bool {
	_, known := sketch.Detect(src)
	return known && !sketch.Supports(src)
}

func (m *OS) redrawDiagrams() {
	m.blockGen++
	m.blocksChanged()
	for id, v := range m.tml.views {
		if v.surface != nil {
			v.surface.Invalidate()
			m.markTMLDirty(id)
		}
	}
	m.MarkAllDirty()
}

func (m *OS) retrySketch() {
	if !sketchFailed.Swap(false) {
		return
	}
	loom.RetryExternal()
	for k, p := range m.mermaidPics {
		if p.ti.err != "" && needsSketchPlugin(k.src) {
			delete(m.mermaidPics, k)
		}
	}
	m.redrawDiagrams()
}

func (m *OS) noteMissingDiagram(typ string) {
	if m.diagramsTold[typ] {
		return
	}
	if m.diagramsTold == nil {
		m.diagramsTold = map[string]bool{}
	}
	m.diagramsTold[typ] = true
	text := "Run tuios plugins install sketch to draw this diagram."
	switch _, err := sketchProgram(); {
	case err == nil:
		return
	case errors.Is(err, ErrSketchNoProgram):
		text = "Run tuios plugins build tuios.sketch to draw this diagram."
	}
	m.ShowNotification(text, "info", max(m.Settings.NotificationDuration, 8*time.Second))
	m.MarkAllDirty()
}
