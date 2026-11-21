package app

import (
	"image"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	leftoverRightID  = "leftover-right"
	leftoverBottomID = "leftover-bottom"
)

const leftoverZ = config.ZIndexSeparators - 1

const leftoverHintMinWidth = 20

type leftoverKey struct {
	w, h         int
	x0, y0       int
	hint         string
	textX, textW int
	ascii        bool
	dot          color.Color
	text         color.Color
}

type leftoverCache struct {
	valid bool
	key   leftoverKey
	body  string
}

func (v sessionView) leftoverStrips() (right, bottom image.Rectangle) {
	if !v.on {
		return image.Rectangle{}, image.Rectangle{}
	}
	right = image.Rect(v.clip.Max.X, v.frame.Min.Y, v.frame.Max.X, v.frame.Max.Y)
	bottom = image.Rect(v.frame.Min.X, v.clip.Max.Y, v.clip.Max.X, v.frame.Max.Y)
	return right.Canon(), bottom.Canon()
}

func (m *OS) leftoverHint() string {
	who := "Another client sets the session size."
	policy := ""
	if c := m.DaemonClient; c != nil {
		name := c.SessionSizedBy()
		switch {
		case c.SessionSizedByKind() == session.ClientKindSSH:
			who = "An SSH client sets the session size."
		case c.SessionSizedByKind() == session.ClientKindWeb:
			who = "A web client sets the session size."
		case name != "" && name == c.Machine() && c.Kind == "":
			who = "Another client on this machine sets the session size."
		case name != "":
			who = "The client on " + name + " sets the session size."
		}
		policy = c.SessionWindowSize()
	}
	switch policy {
	case config.WindowSizeLatest:
		return who + " Type here to use this screen."
	case config.WindowSizeSmallest:
		return who + " Detach it, or set window_size to latest."
	}
	return who
}

func (m *OS) renderLeftover(layers []*lipgloss.Layer) []*lipgloss.Layer {
	right, bottom := m.sessionView.leftoverStrips()
	if (right.Empty() && bottom.Empty()) || m.sessionClients == 1 {
		return layers
	}
	pal := theme.GroundUI()
	hint := m.leftoverHint()
	rx, rw := m.leftoverTextSpan(right)
	bx, bw := m.leftoverTextSpan(bottom)
	rightHint, bottomHint := "", ""
	switch {
	case rw >= leftoverHintMinWidth:
		rightHint = hint
	case bw >= leftoverHintMinWidth:
		bottomHint = hint
	}
	base := leftoverKey{ascii: m.Settings.UseASCIIOnly, dot: pal.FgMute, text: pal.FgDim}
	if !right.Empty() {
		k := base
		k.w, k.h, k.x0, k.y0, k.hint = right.Dx(), right.Dy(), right.Min.X, right.Min.Y, rightHint
		k.textX, k.textW = rx, rw
		layers = append(layers, lipgloss.NewLayer(m.leftoverRight.content(k)).
			X(right.Min.X).Y(right.Min.Y).Z(leftoverZ).ID(leftoverRightID))
	}
	if !bottom.Empty() {
		k := base
		k.w, k.h, k.x0, k.y0, k.hint = bottom.Dx(), bottom.Dy(), bottom.Min.X, bottom.Min.Y, bottomHint
		k.textX, k.textW = bx, bw
		layers = append(layers, lipgloss.NewLayer(m.leftoverBottom.content(k)).
			X(bottom.Min.X).Y(bottom.Min.Y).Z(leftoverZ).ID(leftoverBottomID))
	}
	return layers
}

func (m *OS) leftoverTextSpan(strip image.Rectangle) (x, w int) {
	left, right := strip.Min.X, strip.Max.X
	rw := m.GetRenderWidth()
	cut := func(railX, railW int) {
		if railW <= 0 || railX >= right || railX+railW <= left {
			return
		}
		if railX <= left {
			left = max(left, railX+railW)
		} else {
			right = min(right, railX)
		}
	}
	cut(sidebarEdgeX(m.legacySidebarEdge(), m.GetSidebarWidth(), rw), m.GetSidebarWidth())
	opposite := sidebarRight
	if m.legacySidebarEdge() == sidebarRight {
		opposite = sidebarLeft
	}
	cut(sidebarEdgeX(opposite, m.secondarySidebarWidth(), rw), m.secondarySidebarWidth())
	if right <= left {
		return 0, 0
	}
	return left - strip.Min.X, right - left
}

func (c *leftoverCache) content(key leftoverKey) string {
	if !c.valid || c.key != key {
		c.key, c.valid = key, true
		c.body = leftoverBody(key)
	}
	return c.body
}

func leftoverBody(k leftoverKey) string {
	dot := "·"
	if k.ascii {
		dot = "."
	}
	dotStyle := lipgloss.NewStyle().Foreground(k.dot)
	textStyle := lipgloss.NewStyle().Foreground(k.text)

	var hintLines []string
	if k.hint != "" {
		wrapped := lipgloss.NewStyle().Width(k.textW - 4).Render(k.hint)
		for line := range strings.SplitSeq(wrapped, "\n") {
			hintLines = append(hintLines, strings.TrimRight(line, " "))
		}
		if len(hintLines) > k.h-2 {
			hintLines = nil
		}
	}
	top := (k.h - len(hintLines)) / 2
	pattern := func(x0, x1 int) string {
		var b strings.Builder
		for x := x0; x < x1; x++ {
			if (k.x0+x)%2 == 0 {
				b.WriteString(dot)
			} else {
				b.WriteByte(' ')
			}
		}
		return dotStyle.Render(b.String())
	}

	var out strings.Builder
	for y := range k.h {
		if y > 0 {
			out.WriteByte('\n')
		}
		i := y - top
		switch {
		case len(hintLines) > 0 && i >= -1 && i <= len(hintLines):
			text := ""
			if i >= 0 && i < len(hintLines) {
				text = hintLines[i]
			}
			out.WriteString(pattern(0, k.textX+1))
			out.WriteString(" ")
			out.WriteString(textStyle.Render(text))
			pad := k.textW - 2 - lipgloss.Width(text)
			out.WriteString(strings.Repeat(" ", max(pad, 0)))
			out.WriteString(pattern(k.textX+k.textW, k.w))
		default:
			out.WriteString(pattern(0, k.w))
		}
	}
	return out.String()
}
