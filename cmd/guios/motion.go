package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// Motion: how the chrome moves. Every animation here eases out, takes 120
// to 200 ms and never overshoots. MyGo runs them on the frame clock and
// asks for frames only while something moves, so a window at rest wakes
// no more than before. They follow the desktop's reduced-motion setting
// (ui.Preferences.ReduceMotion): MyGo's transitions then fade in place or
// change at once.

// easeOut starts fast and lands softly, without overshoot: about
// CSS's cubic-bezier(0.2, 0.9, 0.3, 1).
var easeOut = ui.CubicBezier(0.2, 0.9, 0.3, 1)

var (
	// motionQuick is for what only fades: a backdrop, a toast leaving.
	motionQuick = ui.Animation{Duration: 120 * time.Millisecond, Ease: easeOut}
	// motionOut is for what leaves at once: a card the keys close.
	motionOut = ui.Animation{Duration: 80 * time.Millisecond, Ease: easeOut}
	// motionBase is for what comes and goes: a dialog, a toast, a pane.
	motionBase = ui.Animation{Duration: 160 * time.Millisecond, Ease: easeOut}
	// motionLayout is for what moves the layout: a split, a pane closing,
	// zoom, the sidebars.
	motionLayout = ui.Animation{Duration: 200 * time.Millisecond, Ease: easeOut}
)

// enterKeys are the keyframes of a dialog appearing: 0 to 1 over
// motionQuick, eased out.
var enterKeys = []ui.Keyframe{{At: 0, Value: 0}, {At: motionQuick.Duration, Value: 1, Ease: easeOut}}

// dialogMotion animates a dialog built by ui.DialogBase and sets its
// backdrop's colour, scrim. The backdrop's colour fades in, and the panel
// rises 4 DIPs as it fades in, over 120 ms. Opacity on the backdrop would
// draw the window's whole overlay again in each frame of the motion, so
// only its colour changes, and the panel fades as one cached bitmap
// (Cache) while it moves. As it closes, the backdrop fades out with the
// panel on it. keepOverlay must run in every frame of the window, so that
// the closed dialog's parent is still built for its exit.
func dialogMotion(backdrop, panel ui.Element, scrim ui.Color) {
	backdrop.Transition(ui.ElementTransition{Animation: &motionQuick, Exit: &ui.Motion{Opacity: 0}})
	k := panel.Keyframes("in", enterKeys)
	backdrop.Background(scrim.Alpha(k))
	if k < 1 {
		panel.Cache().Opacity(k).Translate(0, -4*(1-k))
	}
}

// keepOverlay keeps the window's overlay layer built in every frame, empty
// or not. A dialog's backdrop is built there, and its exit transition plays
// only while its parent is still built.
func keepOverlay(c *ui.Context) { ui.Overlay(c, func() {}) }

// scaleMotion makes every animation of motion.go k times as long: the e2e
// build's GUIOS_MOTION_SCALE slows them down to capture frames on the way.
func scaleMotion(k float64) {
	for _, a := range []*ui.Animation{&motionQuick, &motionOut, &motionBase, &motionLayout, &sidebarMotion} {
		a.Duration = time.Duration(float64(a.Duration) * k)
	}
	enterKeys = []ui.Keyframe{{At: 0, Value: 0}, {At: motionQuick.Duration, Value: 1, Ease: easeOut}}
}
