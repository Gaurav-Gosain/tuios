package main

import (
	"time"

	"github.com/egoist/mygo/ui"
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
	// motionBase is for what comes and goes: a dialog, a toast, a pane.
	motionBase = ui.Animation{Duration: 160 * time.Millisecond, Ease: easeOut}
	// motionLayout is for what moves the layout: a split, a pane closing,
	// zoom, the sidebars.
	motionLayout = ui.Animation{Duration: 200 * time.Millisecond, Ease: easeOut}
)

// enterKeys are the keyframes of something appearing: 0 to 1, eased out.
var enterKeys = []ui.Keyframe{{At: 0, Value: 0}, {At: motionBase.Duration, Value: 1, Ease: easeOut}}

// dialogMotion animates a dialog built by ui.DialogBase: the backdrop
// fades in, and the panel rises a little and grows to its size as it
// fades in. As it closes, the backdrop fades out with the panel on it.
// keepOverlay must run in every frame of the window, so that the closed
// dialog's parent is still built for its exit.
func dialogMotion(backdrop, panel ui.Element) {
	backdrop.Transition(ui.ElementTransition{
		Animation: &motionQuick,
		Colors:    true,
		Enter:     &ui.Motion{Opacity: 0},
		Exit:      &ui.Motion{Opacity: 0},
	})
	k := panel.Keyframes("in", enterKeys)
	if k < 1 {
		panel.Opacity(k).Translate(0, -8*(1-k)).Scale(0.97+0.03*k, 0.97+0.03*k)
	}
}

// keepOverlay keeps the window's overlay layer built in every frame, empty
// or not. A dialog's backdrop is built there, and its exit transition plays
// only while its parent is still built.
func keepOverlay(c *ui.Context) { ui.Overlay(c, func() {}) }

// scaleMotion makes every animation of motion.go k times as long: the e2e
// build's GUIOS_MOTION_SCALE slows them down to capture frames on the way.
func scaleMotion(k float64) {
	for _, a := range []*ui.Animation{&motionQuick, &motionBase, &motionLayout, &sidebarMotion} {
		a.Duration = time.Duration(float64(a.Duration) * k)
	}
	enterKeys = []ui.Keyframe{{At: 0, Value: 0}, {At: motionBase.Duration, Value: 1, Ease: easeOut}}
}
