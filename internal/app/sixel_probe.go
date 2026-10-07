package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// An SSH client's terminal cannot be probed before the session starts: the
// program owns the channel's input from the first byte, and the server only
// has the TERM and environment the client sent, which is where the passive
// guess in internal/server/ssh_caps.go comes from. TERM=xterm-256color is sent
// by terminals with sixel and by many more without it, so the guess is weak.
//
// Once the program runs, its own input reader can take the answer. The client
// is asked for DA1, the same question the local startup probe asks, and the
// reply decides sixel for this session. A local terminal was probed before the
// program started and a browser is known to draw sixel, so only SSH asks.
//
// A local terminal can answer the startup probe too late: the probe gives up
// after probeTimeout, and the reply reaches the program's input reader
// instead. Dropped, it left a terminal with sixel taken for one without, so
// its images were drawn as glyphs. The probe records that it gave up
// (HostCapabilities.DA1Late), and the first reply after that is taken here.

// sixelProbe asks an SSH client's terminal for its device attributes.
func (m *OS) sixelProbe() tea.Cmd {
	if m.Client != ClientSSH || m.SixelPassthrough == nil || m.hostCaps().SixelPinned {
		return nil
	}
	return tea.Raw(ansi.RequestPrimaryDeviceAttributes)
}

// handleSixelProbe takes the DA1 reply. It reports whether msg was one.
func (m *OS) handleSixelProbe(msg tea.Msg) bool {
	da, ok := msg.(uv.PrimaryDeviceAttributesEvent)
	if !ok {
		return false
	}
	late := m.Client != ClientSSH && m.hostCaps().DA1Late
	if (m.Client != ClientSSH && !late) || m.SixelPassthrough == nil || m.hostCaps().SixelPinned {
		return true
	}
	if late {
		m.hostCaps().DA1Late = false
	}
	sixel := slices.Contains([]int(da), 4)
	m.hostCaps().SixelGraphics = sixel
	m.SixelPassthrough.SetHostSixel(sixel)
	m.LogInfo("client DA1 %v (late=%v): sixel=%v", []int(da), late, sixel)
	// The daemon's emulator answers the panes' DA1, and it knew this client
	// only from its hello, which carried the guess.
	if client := m.DaemonClient; client != nil {
		kitty := m.hostCaps().KittyGraphics
		symbols := drawsSymbols(m.Settings.ImageSymbols, m.hostCaps())
		go func() { _ = client.ReportGraphics(sixel, kitty, symbols) }()
	}
	return true
}
