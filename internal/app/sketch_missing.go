package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"tuios.dev/loom"
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
	text := "Run tuios plugins install sketch to draw this diagram."
	if sketchPluginInstalled(m.UserConfig) {
		text = "tuios mermaid draws this diagram with the sketch plugin."
	}
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
