package terminal

import "tuios.dev/vt"

// A guest that asks for the terminal's name (XTVERSION) learns it runs in
// tuios. Both binaries link this package, the daemon included, which is where
// the panes answer. cmd/tuios adds the release version.
func init() {
	vt.SetTerminalName("tuios")
	// A guest that draws a page from image patches asks for this. A multiplexer
	// cannot pass for the terminal under it, so it says what the passthrough
	// draws: images stacked by z, at pixel offsets, unscaled (nativeScale in
	// internal/app), with an id sent again at a new place and no delete first.
	vt.AddTermcapBoolean("kitty-graphics-patches")
}
