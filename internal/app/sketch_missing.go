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

// Every Mermaid block tuios draws in a client goes through loom: a TML
// <mermaid>, and a fenced Mermaid block of the agents thread and of quick
// look, which cockpit_blocks.go draws as a TML document of one <mermaid>. A
// type this build does not draw goes to the sketch plugin's program through
// the hooks of renderSurface, off the frame path: the block shows a pending
// note, and the drawing comes back as a sketchDrawnMsg that draws the blocks
// again. A picture of the thread (cockpit_mermaid_pic.go) asks the same
// program for SVG through MermaidPicture, and keeps the text drawing until
// the picture comes, or when it cannot come.
//
// Without the plugin a block shows its source under a note, and the person
// hears once per type in this client session what to run. loom tells of each
// source once, when it parses it, so nothing here runs per frame.

// missingDiagramMsg is a diagram type a surface could not draw.
type missingDiagramMsg struct{ typ string }

// renderSurface renders s for this client. Every loom.Surface.Render in this
// package goes through it, so a missing diagram type, and a drawing of the
// sketch plugin that comes in, reach this client's program as messages, and
// the work is done in Update. The hooks are the surface's own, so another
// client's surfaces never call this client's program.
func (m *OS) renderSurface(s *loom.Surface, w, h int) *loom.Buffer {
	hooks := tmldraw.Hooks{External: drawWithSketchPlugin}
	if p := m.frameRate.program; p != nil {
		hooks.Missing = func(typ string) { go p.Send(missingDiagramMsg{typ: typ}) }
		hooks.Drawn = func() { go p.Send(sketchDrawnMsg{}) }
	}
	return tmldraw.Render(s, w, h, hooks)
}

// sketchDrawnMsg is a drawing of the sketch plugin that came in.
type sketchDrawnMsg struct{}

// SketchProgram, when set, is the program of the sketch plugin, or why there
// is none: ErrSketchNoProgram when the plugin is on and has no program, any
// other error when the plugin does not run here. The binary sets it.
var SketchProgram func() (string, error)

// ErrSketchNoProgram is the sketch plugin on, with no program in its folder:
// a folder or a GitHub install that was not built.
var ErrSketchNoProgram = errors.New("the sketch plugin has no program")

// errSketchNotInstalled is the sketch plugin not installed, or not on.
var errSketchNotInstalled = errors.New("the sketch plugin is not installed")

// sketchFailed says a drawing of the sketch plugin failed since the last
// retry. Opening the agents view or quick look then asks again, once, so a
// plugin installed or built while tuios runs draws the diagram.
var sketchFailed atomic.Bool

// sketchProgram is SketchProgram, or errSketchNotInstalled when it is unset.
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

// drawWithSketchPlugin runs the sketch plugin's program for one diagram and
// reads its cells. The error is the line over the diagram's source.
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

// needsSketchPlugin reports whether src is of a type this build does not
// draw.
func needsSketchPlugin(src string) bool {
	_, known := sketch.Detect(src)
	return known && !sketch.Supports(src)
}

// redrawDiagrams draws the rich blocks and the TML surfaces again, which
// take the drawings from loom's cache.
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

// retrySketch asks the sketch plugin again for the diagrams it could not
// draw, when one failed since the last retry. The agents view and quick look
// call it when they open, so it never runs per frame.
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

// noteMissingDiagram tells the person about typ, once in this session.
func (m *OS) noteMissingDiagram(typ string) {
	if m.diagramsTold[typ] {
		return
	}
	if m.diagramsTold == nil {
		m.diagramsTold = map[string]bool{}
	}
	m.diagramsTold[typ] = true
	// Short enough for the dock, so the command is not cut.
	// With the plugin's program, the surface draws the diagram, so there is
	// nothing to tell.
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
