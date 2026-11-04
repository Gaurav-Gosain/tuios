package main

import "github.com/Gaurav-Gosain/mygo/ui"

// toasts shows the window's toasts (DESIGN.md section 6): raised cards in
// the text's color with an edge and the overlay shadow, at the bottom
// center, 48 DIPs up so that they clear the hint bars, and moves them: a
// toast rises and fades in, those above make room, and one that goes
// sinks and fades out. Call it once in each window's view.
func toasts(c *ui.Context) {
	pal := paletteFor(c)
	fill := pal.popover
	if pal.dark {
		fill = pal.popover.Mix(pal.text, 0.06)
	}
	w, _ := c.Size()
	move := ui.ElementTransition{
		Animation: &motionBase,
		Position:  true,
		Enter:     &ui.Motion{Y: 14, Opacity: 0, Scale: 0.98},
		Exit:      &ui.Motion{Y: 8, Opacity: 0, Scale: 0.98},
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, list []ui.Toast) {
		viewport.Bottom(48).AlignItems(ui.Center).Gap(sp2)
		for _, t := range list {
			p := ui.ToastBase(c, t)
			box := p.Root.Row().AlignItems(ui.Center).Gap(sp3).Padding(10, sp4).Radius(rCard).
				MaxWidth(min(dialogW, w-2*sp4)).Background(fill).TextColor(pal.text).Border(1, pal.kbdBorder).
				Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.12)).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22))
			box.Transition(move)
			box.Children(func() {
				ui.Column(c).Shrink(1).MinWidth(0).Gap(2).Children(func() {
					title := ui.Text(c, t.Title).FontSize(fsBody).LineHeight(1.4).MaxLines(3)
					if t.Description != "" {
						title.FontWeight(wMedium)
						ui.Text(c, t.Description).FontSize(fsSmall).TextColor(pal.textMuted).LineHeight(1.4).MaxLines(3)
					}
				})
				if t.Action == "" {
					return
				}
				b := p.ActionButton().Height(hControlSm).Padding(0, 10).Radius(rControl).Shrink(0).TextColor(pal.accent).FontSize(fsBody).FontWeight(wMedium)
				if b.Hovered() {
					b.Background(pal.rowHover)
				}
				b.Children(func() { ui.Text(c, t.Action).SingleLine() })
			})
		}
	})
}
