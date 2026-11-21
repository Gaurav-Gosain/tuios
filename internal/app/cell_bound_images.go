package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

var cellBoundImageHosts = map[string]bool{
	"xterm.js": true,
}

func isCellBoundImageHost(version string) bool {
	name, _, ok := parseVersionName(version)
	return ok && cellBoundImageHosts[name]
}

func hostBindsImagesToCells(response string, getenv func(string) string) bool {
	if name, _, ok := parseHostIdentity(response); ok {
		return cellBoundImageHosts[name]
	}
	return getenv("TERM_PROGRAM") == "vscode"
}

func dropCellBoundGraphics(caps *HostCapabilities, response string, getenv func(string) string) {
	if !hostBindsImagesToCells(response, getenv) {
		return
	}
	caps.CellBoundImages = true
	caps.KittyGraphics = false
	caps.KittyFileTransfer = false
	caps.KittyAnimation = false
	caps.KittyPlaceholders = false
	caps.SixelGraphics = false
	caps.SixelPinned = true
}

func (m *OS) handleHostVersion(msg tea.Msg) bool {
	v, ok := msg.(tea.TerminalVersionMsg)
	if !ok {
		return false
	}
	caps := m.hostCaps()
	if caps.CellBoundImages || !isCellBoundImageHost(v.Name) {
		return true
	}
	caps.CellBoundImages = true
	if !caps.KittyPinned {
		caps.KittyGraphics = false
		caps.KittyFileTransfer = false
		caps.KittyAnimation = false
		caps.KittyPlaceholders = false
		m.KittyPassthrough.disableForHost()
	}
	if !caps.SixelPinned {
		caps.SixelGraphics = false
		caps.SixelPinned = true
	}
	if sp := m.SixelPassthrough; sp != nil {
		sp.SetHostSixel(caps.SixelGraphics)
	}
	m.LogInfo("host %q stores images in its cells: kitty=%v sixel=%v", v.Name, caps.KittyGraphics, caps.SixelGraphics)
	m.refreshKittyPlaceholderMode()
	terminal.SetGraphicsCapabilities(
		m.KittyPassthrough != nil && m.KittyPassthrough.IsEnabled(),
		m.SixelPassthrough != nil && m.SixelPassthrough.IsEnabled(),
		caps.KittyAnimation,
	)
	if client := m.DaemonClient; client != nil {
		sixel, kitty, symbols := caps.SixelGraphics, caps.KittyGraphics, drawsSymbols(m.Settings.ImageSymbols, caps)
		go func() { _ = client.ReportGraphics(sixel, kitty, symbols) }()
	}
	return true
}

func (kp *KittyPassthrough) disableForHost() {
	if kp == nil {
		return
	}
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.enabled = false
}
