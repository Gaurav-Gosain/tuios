package main

import (
	_ "embed"

	"github.com/Gaurav-Gosain/sip"
)

//go:embed strip_touch.js
var stripTouchJS string

func installStripTouch(cfg *sip.Config) {
	cfg.ExtraJS += stripTouchJS
}
