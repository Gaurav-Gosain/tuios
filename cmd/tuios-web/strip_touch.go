package main

import (
	_ "embed"

	"github.com/Gaurav-Gosain/sip"
)

// stripTouchJS lets a horizontal finger drag scroll the scrolling layout's
// strip on the worker engine renderer. See strip_touch.js.
//
//go:embed strip_touch.js
var stripTouchJS string

// installStripTouch adds the strip's finger drag to the page. The page needs
// no setting: it reports the cells the finger crossed, and tuios decides what
// they do.
func installStripTouch(cfg *sip.Config) {
	cfg.ExtraJS += stripTouchJS
}
