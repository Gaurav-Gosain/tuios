package main

import (
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// view opens the document full screen until the person quits or the
// document closes. closed says a close action ended it, with value.
func view(doc *tml.Node, depth loom.Depth) (value any, closed bool, err error) {
	// esc quits, as it closes a popup, so the document reads its placement
	// as one.
	s, err := loom.New(doc, &loom.Options{Palette: loom.DefaultPalette(depth), Placement: "popup"})
	if err != nil {
		return nil, false, err
	}
	t := uv.DefaultTerminal()
	scr := t.Screen()
	w, h, err := t.GetSize()
	if err != nil {
		return nil, false, err
	}
	scr.Resize(w, h)
	scr.EnterAltScreen()
	scr.HideCursor()
	scr.SetMouseMode(uv.MouseModeClick)
	if err := t.Start(); err != nil {
		return nil, false, err
	}
	defer func() { _ = t.Stop() }()

	draw := func() error {
		paint(scr, s.Render(w, h))
		scr.Render()
		return scr.Flush()
	}
	if err := draw(); err != nil {
		return nil, false, err
	}
	// A document that shows ago() changes with the clock.
	var tick <-chan time.Time
	if s.NeedsClock() {
		tk := time.NewTicker(time.Minute)
		defer tk.Stop()
		tick = tk.C
	}

	for {
		var res loom.Result
		select {
		case <-tick:
			s.Tick()
		case ev := <-t.Events():
			switch ev := ev.(type) {
			case uv.WindowSizeEvent:
				w, h = ev.Width, ev.Height
				scr.Resize(w, h)
				s.Invalidate()
			case uv.KeyPressEvent:
				key := ev.String()
				if key == "ctrl+c" {
					return nil, false, nil
				}
				res, err = s.Key(key)
				if err != nil {
					return nil, false, err
				}
				if (key == "q" || key == "esc") && len(res.Changed) == 0 && !res.View && len(res.Effects) == 0 {
					return nil, false, nil
				}
			case uv.MouseEvent:
				m, ok := mouse(ev)
				if !ok {
					continue
				}
				res, err = s.Mouse(m)
				if err != nil {
					return nil, false, err
				}
			default:
				continue
			}
		}
		for _, e := range res.Effects {
			switch e.Action {
			case "close":
				if e.HasValue {
					return e.Value, true, nil
				}
				return nil, true, nil
			case "copy":
				if text, ok := e.Value.(string); ok {
					_, _ = scr.WriteString(ansi.SetSystemClipboard(text))
				}
			}
			// emit, open, pane and link need a host that owns the document,
			// such as tuios. Here they do nothing.
		}
		if err := draw(); err != nil {
			return nil, false, err
		}
	}
}

// mouse turns a terminal mouse event into a loom one.
func mouse(ev uv.MouseEvent) (loom.Mouse, bool) {
	m := ev.Mouse()
	out := loom.Mouse{X: m.X, Y: m.Y}
	switch m.Button {
	case uv.MouseLeft:
		out.Button = loom.MouseLeft
	case uv.MouseMiddle:
		out.Button = loom.MouseMiddle
	case uv.MouseRight:
		out.Button = loom.MouseRight
	case uv.MouseWheelUp:
		out.Button = loom.WheelUp
	case uv.MouseWheelDown:
		out.Button = loom.WheelDown
	default:
		if _, ok := ev.(uv.MouseMotionEvent); !ok {
			return out, false
		}
	}
	switch ev.(type) {
	case uv.MouseReleaseEvent:
		out.Action = loom.Release
	case uv.MouseMotionEvent:
		out.Action = loom.Motion
	}
	return out, true
}

// paint copies a loom buffer onto the screen.
func paint(scr *uv.TerminalScreen, b *loom.Buffer) {
	for y := 0; y < b.Height(); y++ {
		for x := 0; x < b.Width(); x++ {
			c := b.Cell(x, y)
			if c.Width == 0 {
				continue
			}
			var attrs uint8
			if c.Bold {
				attrs |= uv.AttrBold
			}
			if c.Faint {
				attrs |= uv.AttrFaint
			}
			if c.Italic {
				attrs |= uv.AttrItalic
			}
			if c.Strike {
				attrs |= uv.AttrStrikethrough
			}
			if c.Reverse {
				attrs |= uv.AttrReverse
			}
			st := uv.Style{Fg: c.Fg, Bg: c.Bg, Attrs: attrs}
			if c.Underline {
				st.Underline = uv.UnderlineSingle
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			scr.SetCell(x, y, &uv.Cell{Content: content, Width: max(c.Width, 1), Style: st})
		}
	}
}
