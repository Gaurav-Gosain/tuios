package theme

import (
	"image/color"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

var matchHost atomic.Bool

type hostGround struct {
	bg    color.Color
	light bool
}

func hostInForce() *hostGround {
	if !matchHost.Load() {
		return nil
	}
	return host.Load()
}

var host atomic.Pointer[hostGround]

// SetHostGround records the local terminal's background for the chrome to
// use while no theme is set. light is the client's verdict on bg, so a
// verdict held with hysteresis stays held here. A nil bg forgets it.
func SetHostGround(bg color.Color, light bool) {
	if bg == nil {
		host.Store(nil)
		return
	}
	if h := host.Load(); h != nil && h.light == light && sameColor(h.bg, bg) {
		return
	}
	host.Store(&hostGround{bg: bg, light: light})
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func hostDarkChrome(ground color.Color) Chrome {
	c := Chrome{Canvas: ground, Surface: overlay.Lighter(ground, chromeRamp.canvas)}
	c.deriveRamp()
	return c
}

var schemeLight atomic.Bool

// SetSchemeLight records whether the local terminal is light, and reports
// whether that changed. Only the local client calls it, for the reason
// SetHostGround gives.
func SetSchemeLight(light bool) bool {
	return schemeLight.Swap(light) != light
}

// SchemeLight reports whether the local terminal said it is light.
func SchemeLight() bool { return schemeLight.Load() }
