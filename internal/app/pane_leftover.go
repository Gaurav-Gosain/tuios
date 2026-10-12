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

// The leftover: the part of this client's pane area that a smaller session
// does not reach (see pane_view.go). tmux fills it with a dot in a dim colour
// (its fill-character option) so it reads as unused rather than as a pane
// that failed to draw. tuios does the same, with a dot on every other column
// so it stays quiet, and adds one line that says which client sets the size
// and what to do about it. The pattern alone says "this space is not in use"
// and the line says why, which the pattern cannot.

// leftoverLayerID names the leftover layers. Each strip has its own id.
const (
	leftoverRightID  = "leftover-right"
	leftoverBottomID = "leftover-bottom"
)

// leftoverZ is below every pane-independent layer, so the PiP view, the
// rail, the dock and every overlay draw over the leftover. Panes never reach
// it: they are clipped to the view's pane area.
const leftoverZ = config.ZIndexSeparators - 1

// leftoverHintMinWidth is the narrowest strip the hint is written in. A
// narrower one shows the pattern alone.
const leftoverHintMinWidth = 20

// leftoverKey is everything a strip's content is built from.
type leftoverKey struct {
	w, h   int
	x0, y0 int // the strip's screen origin, which the dot phase follows
	hint   string
	// textX and textW are the columns of the strip the hint is written in:
	// the whole strip, less any rail drawn over it.
	textX, textW int
	ascii        bool
	dot          color.Color
	text         color.Color
}

// leftoverCache keeps one strip's content across frames: the strips change
// only when a size, the hint or the theme does.
type leftoverCache struct {
	valid bool
	key   leftoverKey
	body  string
}

// leftoverStrips are the two parts of the leftover on the screen: the
// columns right of the session's panes, the full height of the pane area,
// and the rows under them, as wide as the panes.
func (v sessionView) leftoverStrips() (right, bottom image.Rectangle) {
	if !v.on {
		return image.Rectangle{}, image.Rectangle{}
	}
	right = image.Rect(v.clip.Max.X, v.frame.Min.Y, v.frame.Max.X, v.frame.Max.Y)
	bottom = image.Rect(v.frame.Min.X, v.clip.Max.Y, v.clip.Max.X, v.frame.Max.Y)
	return right.Canon(), bottom.Canon()
}

// leftoverHint is the line the leftover carries: which client sets the
// session's size, and what makes this client's screen the size instead.
//
// A client that a server runs (SSH, web) is named by its kind, since the
// machine its hello names is the server's. A terminal on this client's own
// machine is "another client on this machine", so the line never names the
// reader's machine as if it were someone else's. With no one client to name,
// as under smallest when one client sets the width and another the height,
// it says "another client".
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

// renderLeftover draws the leftover, as up to two layers. It appends nothing
// when the session fills this client's pane area.
func (m *OS) renderLeftover(layers []*lipgloss.Layer) []*lipgloss.Layer {
	right, bottom := m.sessionView.leftoverStrips()
	if (right.Empty() && bottom.Empty()) || m.sessionClients == 1 {
		return layers
	}
	pal := theme.GroundUI()
	hint := m.leftoverHint()
	// The hint goes in the right strip when the part of it no rail covers is
	// wide enough, else in the bottom one.
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

// leftoverTextSpan is the part of a strip no rail covers, as the column it
// starts at inside the strip and its width. A rail opened over the panes for
// the keyboard is out of the reserve, so it can sit on the strip, and the
// hint under it would be cut. The rails are at the screen's edges, so what
// is left is one run of columns.
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

// content is the strip's text for key, built again only when key changed.
func (c *leftoverCache) content(key leftoverKey) string {
	if !c.valid || c.key != key {
		c.key, c.valid = key, true
		c.body = leftoverBody(key)
	}
	return c.body
}

// leftoverBody builds a strip: a dot on every other screen column, and the
// hint, wrapped to the strip and set in the middle of it with a clear cell
// around it.
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
	// The hint's block: a blank row above and below, and two blank columns
	// each side, so the dots do not run into the words.
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
