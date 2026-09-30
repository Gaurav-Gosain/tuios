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
	if m.Client != ClientSSH || m.SixelPassthrough == nil || m.hostCaps().SixelPinned {
		return true
	}
	sixel := slices.Contains([]int(da), 4)
	m.hostCaps().SixelGraphics = sixel
	m.SixelPassthrough.SetHostSixel(sixel)
	m.LogInfo("SSH client DA1 %v: sixel=%v", []int(da), sixel)
	// The daemon's emulator answers the panes' DA1, and it knew this client
	// only from its hello, which carried the guess.
	if client := m.DaemonClient; client != nil {
		kitty := m.hostCaps().KittyGraphics
		go func() { _ = client.ReportGraphics(sixel, kitty) }()
	}
	return true
}
