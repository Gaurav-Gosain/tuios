package main

import (
	_ "embed"
	"fmt"

	"github.com/Gaurav-Gosain/sip"
)

// stripTouchJS lets a horizontal finger drag scroll the scrolling layout's
// strip on the worker engine renderer. See strip_touch.js.
//
//go:embed strip_touch.js
var stripTouchJS string

// installStripTouch adds the strip's finger drag to the page. cells is
// appearance.niri_scroll_cells, how far one wheel step moves the strip, so a
// drag moves it about as far as the finger went. reverse is
// appearance.niri_reverse_scroll, which tuios applies to the steps and the
// page undoes, so the strip follows the finger.
func installStripTouch(cfg *sip.Config, cells int, reverse bool) {
	if cells < 1 {
		cells = 1
	}
	cfg.ExtraJS += fmt.Sprintf("window.tuiosStripTouch = { cells: %d, reverse: %t };\n", cells, reverse) + stripTouchJS
}
