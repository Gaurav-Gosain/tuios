package main

import "github.com/egoist/mygo/ui"

// toasts shows the window's toasts at its bottom, in the theme's inverse
// colors, as MyGo's own look does, and moves them: a toast rises and fades
// in, those above make room, and one that goes sinks and fades out. Call
// it once in each window's view.
func toasts(c *ui.Context) {
	th := c.Theme()
	fill, text := th.Inverse, th.InverseText
	if fill == ui.Transparent {
		fill = th.Text
	}
	if text == ui.Transparent {
		text = th.Background
	}
	w, _ := c.Size()
	move := ui.ElementTransition{
		Animation: &motionBase,
		Position:  true,
		Enter:     &ui.Motion{Y: 14, Opacity: 0, Scale: 0.98},
		Exit:      &ui.Motion{Y: 8, Opacity: 0, Scale: 0.98},
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, list []ui.Toast) {
		viewport.Bottom(th.Space(6)).AlignItems(ui.Center).Gap(th.Space(2))
		for _, t := range list {
			p := ui.ToastBase(c, t)
			box := p.Root.Row().AlignItems(ui.Center).Gap(th.Space(4)).Padding(th.Space(2.5), th.Space(4)).Radius(th.Space(2)).
				MaxWidth(w-th.Space(12)).Background(fill).TextColor(text).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.25))
			box.Transition(move)
			box.Children(func() {
				if t.Description == "" {
					ui.Text(c, t.Title)
				} else {
					ui.Column(c).Shrink(1).Gap(th.Space(0.5)).Children(func() {
						ui.Text(c, t.Title).FontWeight(600)
						ui.Text(c, t.Description).TextColor(text.Alpha(0.75))
					})
				}
				if t.Action == "" {
					return
				}
				b := p.ActionButton().Padding(th.Space(1), th.Space(2.5)).Radius(th.Radius).TextColor(th.Accent.Mix(text, 0.35)).FontWeight(600)
				if b.Hovered() {
					b.Background(text.Alpha(0.12))
				}
				b.Children(func() { ui.Text(c, t.Action) })
			})
		}
	})
}
