package theme

import (
	"image/color"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// With theme = "none", tuios draws on the terminal's own colours, and the
// dialogs' neutral ramp follows the terminal's background: a terminal themed
// in kitty or ghostty gets dialogs in the same greys, with no theme file. An
// empty theme keeps the constant ramp, as it always did. Only a client that
// is alone in its process tells this package its ground. A server process
// serves several terminals at once, and its clients keep the constant ramp.

// matchHost is set while the theme is "none".
var matchHost atomic.Bool

// hostGround is a terminal's background and the client's verdict on it.
type hostGround struct {
	bg    color.Color
	light bool
}

// hostInForce is the ground the chrome follows: the local client's, while the
// theme is "none", else nil.
func hostInForce() *hostGround {
	if !matchHost.Load() {
		return nil
	}
	return host.Load()
}

// host is the ground the local client reported, or nil. It is read on every
// UI call through the memo key, so it is an atomic pointer: a new report is a
// new pointer, and a new key.
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

// sameColor reports whether a and b are the same colour.
func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// hostDarkChrome is the dialog ramp on a dark terminal ground: the ground as
// the canvas, and the surface one constant ramp step above it, so a dialog is
// raised off the screen by the same spacing the constant ramp has. The other
// steps and the ink tiers derive from the surface.
func hostDarkChrome(ground color.Color) Chrome {
	c := Chrome{Canvas: ground, Surface: overlay.Lighter(ground, chromeRamp.canvas)}
	c.deriveRamp()
	return c
}

// schemeLight is the local terminal's verdict on its background: light or
// dark. It picks appearance.theme_light or appearance.theme when both are set.
var schemeLight atomic.Bool

// SetSchemeLight records whether the local terminal is light, and reports
// whether that changed. Only the local client calls it, for the reason
// SetHostGround gives.
func SetSchemeLight(light bool) bool {
	return schemeLight.Swap(light) != light
}

// SchemeLight reports whether the local terminal said it is light.
func SchemeLight() bool { return schemeLight.Load() }
