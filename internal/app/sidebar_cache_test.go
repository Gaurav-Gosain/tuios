package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// BenchmarkSidebarPanelLinesCached measures the steady-state cost of composing
// the rail when nothing changed: the common case, a pane printing output while
// the sidebar sits still. It must not rebuild or restyle.
func BenchmarkSidebarPanelLinesCached(b *testing.B) {
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.Global.SidebarEnabled = false }()

	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.sidebarPanelLines() // prime the cache

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.sidebarPanelLines()
	}
}

// BenchmarkSidebarPanelCached is the same steady state as
// BenchmarkSidebarPanelLinesCached, but through the call the compositor
// actually makes. renderSidebar needs the rail as one string, and joining the
// rows there rebuilt the whole rail on every composed frame, including the
// frames the row cache had just declared unchanged.
func BenchmarkSidebarPanelCached(b *testing.B) {
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.Global.SidebarEnabled = false }()

	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.sidebarPanel() // prime the cache

	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanel()
	}
}
