package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"tuios.dev/loom"
	sketchtext "tuios.dev/sketch/text"
)

// A TML <mermaid> of a type tuios does not draw shows its source under a
// note (loom/mermaid.go), and the person hears once per type in this client
// session what to do. loom tells of each source once, when it parses it, so
// nothing here runs per frame.

// missingDiagramMsg is a diagram type a surface could not draw.
type missingDiagramMsg struct{ typ string }

// bindMissingDiagram routes loom's report to the program as a message, so
// the notice is made in Update and not where the surface draws.
func bindMissingDiagram(p *tea.Program) {
	loom.MissingDiagram = func(typ string) {
		go p.Send(missingDiagramMsg{typ: typ})
	}
	// With the sketch plugin, a surface draws the diagram with the plugin's
	// program, once per source and width (loom/mermaid.go).
	loom.ExternalDiagram = drawWithSketchPlugin
	loom.ExternalDrawn = func() { go p.Send(sketchDrawnMsg{}) }
}

// sketchDrawnMsg is a drawing of the sketch plugin that came in.
type sketchDrawnMsg struct{}

// SketchProgram, when set, is the program of the sketch plugin, "" when it
// is not installed or does not run. The binary sets it.
var SketchProgram func() string

// sketchDrawTimeout bounds one run of the plugin's program.
const sketchDrawTimeout = 20 * time.Second

// drawWithSketchPlugin runs the sketch plugin's program for one diagram and
// reads its cells.
func drawWithSketchPlugin(src string, cols int) ([][]sketchtext.Cell, bool, error) {
	prog := ""
	if SketchProgram != nil {
		prog = SketchProgram()
	}
	if prog == "" {
		return nil, false, errors.New("the sketch plugin is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), sketchDrawTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, "-f", "cells", "-cols", strconv.Itoa(cols), "-") //nolint:gosec // the program of the plugin the person installed
	cmd.Stdin = strings.NewReader(src)
	data, err := cmd.Output()
	if err != nil {
		return nil, false, err
	}
	if len(data) > sketchMaxCells {
		return nil, false, errors.New("the drawing is too large")
	}
	var got struct {
		Rows [][][3]int `json:"rows"`
		Cut  bool       `json:"cut"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		return nil, false, fmt.Errorf("the sketch plugin gave no cells: %w", err)
	}
	rows := make([][]sketchtext.Cell, len(got.Rows))
	for y, row := range got.Rows {
		rows[y] = make([]sketchtext.Cell, len(row))
		for x, c := range row {
			r := rune(c[0])
			if r != 0 && (r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
				r = ' '
			}
			rows[y][x] = sketchtext.Cell{Rune: r, Role: sketchtext.Role(c[1]), Bg: sketchtext.Role(c[2])} //nolint:gosec // a role is a small number
		}
	}
	return rows, got.Cut, nil
}

// sketchMaxCells bounds what the plugin's program may answer, in bytes.
const sketchMaxCells = 16 << 20

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
	// With the plugin, the surface draws the diagram, so there is nothing
	// to tell.
	if sketchPluginInstalled(m.UserConfig) {
		return
	}
	text := "Run tuios plugins install sketch to draw this diagram."
	m.ShowNotification(text, "info", max(m.Settings.NotificationDuration, 8*time.Second))
	m.MarkAllDirty()
}

// sketchPluginInstalled reports whether the sketch plugin runs here. It reads
// the plugin files, once per type in a session.
func sketchPluginInstalled(cfg *config.UserConfig) bool {
	path, err := config.GetConfigPath()
	if err != nil || cfg == nil {
		return false
	}
	e := herdrplugin.Find(herdrplugin.Discover(herdrplugin.DefaultDirs(path, cfg.Plugins.Dirs), cfg.Plugins.Enabled), "tuios.sketch")
	return e != nil && e.Runnable()
}
