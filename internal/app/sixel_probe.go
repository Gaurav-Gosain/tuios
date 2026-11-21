package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func (m *OS) sixelProbe() tea.Cmd {
	if m.SixelPassthrough == nil || m.hostCaps().SixelPinned {
		return nil
	}
	if m.Client != ClientSSH && !m.hostCaps().DA1Late {
		return nil
	}
	return tea.Raw(xtversionQuery + ansi.RequestPrimaryDeviceAttributes)
}

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
	if client := m.DaemonClient; client != nil {
		kitty := m.hostCaps().KittyGraphics
		symbols := drawsSymbols(m.Settings.ImageSymbols, m.hostCaps())
		go func() { _ = client.ReportGraphics(sixel, kitty, symbols) }()
	}
	return true
}
