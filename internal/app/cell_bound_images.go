package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Hosts that store an image in their cells.
//
// tuios draws a pane's image on the host by placing it, moving the placement
// when the pane scrolls or moves, and deleting it when the pane clears. That
// needs a host that keeps an image apart from the text: kitty's placements,
// which a later placement with the same ids replaces, or a sixel that the text
// printed over it erases.
//
// xterm.js's image addon does neither. It writes every image, kitty or sixel,
// into the cells under it, the way it draws a sixel. A second placement with
// the same image and placement ids is a second picture beside the first, so
// each move leaves the rows the new one does not cover behind. Text, ECH, EL
// and ED do not take an image out of a cell, and deleting the image leaves its
// cells drawing a grey checker, the addon's placeholder for an image it no
// longer holds. Issue 567 is that trail and that checker, in Netcatty.
//
// No cheaper sequence undoes either: with the placeholder on, delete then
// place and a fresh id per placement both leave the checker behind. Only an
// ED 2 and a repaint of the whole screen is clean. So on such a host the image
// protocols are turned off and a pane's picture is drawn as block glyphs,
// which the host treats as the text they are. TUIOS_KITTY_GRAPHICS=1 and
// TUIOS_SIXEL_GRAPHICS=1 still turn them back on, for an embedder whose
// xterm.js draws images some other way.
//
// The host is recognised three ways:
//   - its XTVERSION answer inside the startup probe (dropCellBoundGraphics);
//   - TERM_PROGRAM=vscode, for a VS Code build that does not answer XTVERSION;
//   - its XTVERSION answer after the probe, read as a tea.TerminalVersionMsg
//     (handleHostVersion). That covers a local terminal that answered too
//     late for the probe, and an SSH client, which sixelProbe asks.

// cellBoundImageHosts are the XTVERSION names of terminals that store an image
// in their cells. xterm.js answers for every terminal built on it: VS Code,
// Netcatty, Tabby, Hyper and the rest.
var cellBoundImageHosts = map[string]bool{
	"xterm.js": true,
}

// isCellBoundImageHost reports whether an XTVERSION answer's text names a
// terminal that stores images in its cells.
func isCellBoundImageHost(version string) bool {
	name, _, ok := parseVersionName(version)
	return ok && cellBoundImageHosts[name]
}

// hostBindsImagesToCells reports whether the local terminal that answered this
// probe stores images in its cells, by its XTVERSION answer or, failing that,
// by TERM_PROGRAM.
func hostBindsImagesToCells(response string, getenv func(string) string) bool {
	if name, _, ok := parseHostIdentity(response); ok {
		return cellBoundImageHosts[name]
	}
	return getenv("TERM_PROGRAM") == "vscode"
}

// dropCellBoundGraphics turns off every image protocol of a host that stores
// images in its cells. Sixel is pinned off, so a late DA1 reply that lists it
// does not turn it back on.
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

// handleHostVersion takes an XTVERSION answer that came after the startup
// probe. It reports whether msg was one.
//
// A host that stores images in its cells has its graphics turned off here, the
// way dropCellBoundGraphics does in the probe, unless an environment override
// pinned them. The answer comes before any image can have been drawn: it is
// the reply to a question asked at startup, and DA1, asked after it, is what
// would have turned sixel on.
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

// disableForHost stops forwarding kitty graphics, for a host found after
// startup to be unable to show them. Nothing has been placed yet, so there is
// nothing to take down.
func (kp *KittyPassthrough) disableForHost() {
	if kp == nil {
		return
	}
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.enabled = false
}
