package app

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
// No sequence tuios can send undoes either, so on such a host the image
// protocols are turned off and a pane's picture is drawn as block glyphs,
// which the host treats as the text they are. TUIOS_KITTY_GRAPHICS=1 and
// TUIOS_SIXEL_GRAPHICS=1 still turn them back on, for an embedder whose
// xterm.js draws images some other way.

// cellBoundImageHosts are the XTVERSION names of terminals that store an image
// in their cells. xterm.js answers for every terminal built on it: VS Code,
// Netcatty, Tabby, Hyper and the rest.
var cellBoundImageHosts = map[string]bool{
	"xterm.js": true,
}

// hostBindsImagesToCells reports whether the terminal that answered this probe
// stores images in its cells.
func hostBindsImagesToCells(response string) bool {
	name, _, ok := parseHostIdentity(response)
	return ok && cellBoundImageHosts[name]
}

// dropCellBoundGraphics turns off every image protocol of a host that stores
// images in its cells. Sixel is pinned off, so a late DA1 reply that lists it
// does not turn it back on.
func dropCellBoundGraphics(caps *HostCapabilities, response string) {
	if !hostBindsImagesToCells(response) {
		return
	}
	caps.KittyGraphics = false
	caps.KittyFileTransfer = false
	caps.KittyAnimation = false
	caps.KittyPlaceholders = false
	caps.SixelGraphics = false
	caps.SixelPinned = true
}
