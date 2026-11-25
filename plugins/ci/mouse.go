package main

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The mouse acts on what the frame draws. The view wraps each part a click
// or the wheel can reach in two zero-width markers, and the frame is
// scanned for them after it is drawn: what a marker encloses is where the
// part is on the screen. So a zone cannot drift from the text it covers,
// and a part that a scroll or a cut leaves out of view has no zone.

// zone is what a part of the frame does under the mouse.
type zone struct {
	// hover names the part, so the view can light it up under the pointer.
	hover string
	// key runs as if pressed on a click, with the same checks as the key.
	key string
	// click runs on a left press when key is empty.
	click func(mouseMsg) Cmd
	// wheel scrolls the part, by notches: up is negative.
	wheel func(int)
	// drag follows the pointer from a press until the release.
	drag func(mouseMsg)
	// area names a part that other code needs the place of.
	area string
}

// rect is where a zone is: on row y, from cell x0 up to x1.
type rect struct{ x0, x1, y, zone int }

// mouseMsg is a mouse report: the cell, 0-based, and what happened.
type mouseMsg struct {
	x, y int
	// button is 0 for the left button, 1 the middle, 2 the right, and 3
	// for none, as in a motion with no button down.
	button  int
	wheel   int // -1 up, 1 down, 0 no wheel.
	release bool
	motion  bool
	shift   bool
}

// blurMsg says the pane lost the focus.
type blurMsg struct{}

// addZone keeps z for this frame and gives its number.
func (m *model) addZone(z zone) int {
	m.zones = append(m.zones, z)
	return len(m.zones) - 1
}

// inZone wraps s in the markers of zone n.
func inZone(n int, s string) string {
	id := strconv.Itoa(n)
	return "\x1b[" + id + "{" + s + "\x1b[" + id + "}"
}

// zoned wraps s in the markers of a new zone z.
func (m *model) zoned(z zone, s string) string { return inZone(m.addZone(z), s) }

// zoneMark is a marker: CSI, the zone number, and { at the start or } at
// the end. No terminal reads such a sequence, and none reaches one.
var zoneMark = regexp.MustCompile("\x1b\\[([0-9]+)([{}])")

// place finds the zones in a frame and takes their markers out.
func place(frame string) (string, []rect) {
	lines := strings.Split(frame, "\n")
	var rects []rect
	for y, line := range lines {
		marks := zoneMark.FindAllStringSubmatchIndex(line, -1)
		if marks == nil {
			continue
		}
		var sb strings.Builder
		open := map[int]int{}
		col, last := 0, 0
		for _, mk := range marks {
			seg := line[last:mk[0]]
			sb.WriteString(seg)
			col += ansi.StringWidth(seg)
			last = mk[1]
			n, _ := strconv.Atoi(line[mk[2]:mk[3]])
			if line[mk[4]] == '{' {
				open[n] = col
				continue
			}
			if x0, ok := open[n]; ok {
				delete(open, n)
				if col > x0 {
					rects = append(rects, rect{x0: x0, x1: col, y: y, zone: n})
				}
			}
		}
		seg := line[last:]
		sb.WriteString(seg)
		col += ansi.StringWidth(seg)
		// A zone that a cut left open runs to the end of the line.
		for n, x0 := range open {
			if col > x0 {
				rects = append(rects, rect{x0: x0, x1: col, y: y, zone: n})
			}
		}
		lines[y] = sb.String()
	}
	return strings.Join(lines, "\n"), rects
}

// frame draws the screen and places its zones.
func (m *model) frame() string {
	m.zones = m.zones[:0]
	out, rects := place(m.render())
	m.rects = rects
	m.drawn = m.zones
	m.zones = nil
	return out
}

// hit is the zone at the cell that ok accepts, the smallest when zones
// nest: a row in a panel wins over the panel.
func (m *model) hit(x, y int, ok func(zone) bool) (zone, bool) {
	best, bestW := -1, 0
	for _, r := range m.rects {
		if r.y != y || x < r.x0 || x >= r.x1 || r.zone >= len(m.drawn) || !ok(m.drawn[r.zone]) {
			continue
		}
		if w := r.x1 - r.x0; best < 0 || w <= bestW {
			best, bestW = r.zone, w
		}
	}
	if best < 0 {
		return zone{}, false
	}
	return m.drawn[best], true
}

// areaTop is the first row of the area, or -1.
func (m *model) areaTop(area string) int {
	top := -1
	for _, r := range m.rects {
		if r.zone < len(m.drawn) && m.drawn[r.zone].area == area && (top < 0 || r.y < top) {
			top = r.y
		}
	}
	return top
}

// parseMouse reads an SGR mouse report: the parameters after CSI <, and
// the final byte, M for a press or a motion and m for a release.
func parseMouse(params string, final byte) (mouseMsg, bool) {
	f := strings.Split(params, ";")
	if len(f) != 3 {
		return mouseMsg{}, false
	}
	var n [3]int
	for i, s := range f {
		v, err := strconv.Atoi(s)
		if err != nil {
			return mouseMsg{}, false
		}
		n[i] = v
	}
	b := n[0]
	ev := mouseMsg{x: n[1] - 1, y: n[2] - 1, button: b & 3, release: final == 'm', motion: b&32 != 0, shift: b&4 != 0}
	if b&64 != 0 {
		switch b & 3 {
		case 0:
			ev.wheel = -1
		case 1:
			ev.wheel = 1
		default:
			return mouseMsg{}, false
		}
		ev.button = 3
	}
	return ev, true
}

func hasHover(z zone) bool { return z.hover != "" }
func hasWheel(z zone) bool { return z.wheel != nil }
func canClick(z zone) bool { return z.key != "" || z.click != nil || z.drag != nil }

// mouse handles a mouse report. A press acts at once. Motion only lights
// up what is under the pointer, or moves a drag.
func (m *model) mouse(ev mouseMsg) (*model, Cmd) {
	switch {
	case ev.wheel != 0:
		if m.picker != nil {
			p := m.picker
			p.at = min(max(0, p.at+ev.wheel), max(0, len(p.shown)-1))
			return m, nil
		}
		if z, ok := m.hit(ev.x, ev.y, hasWheel); ok {
			z.wheel(ev.wheel)
			return m, nil
		}
	case ev.release:
		// The end of a drag: draw the divider without its hint.
		m.dragging = nil
		return m, nil
	case ev.motion:
		if m.dragging != nil && ev.button != 3 {
			m.dragging(ev)
			return m, nil
		}
		hover := ""
		if z, ok := m.hit(ev.x, ev.y, hasHover); ok {
			hover = z.hover
		}
		if hover == m.hover {
			// Nothing changed under the pointer: no frame to draw.
			m.still = true
			return m, nil
		}
		m.hover = hover
		return m, nil
	case ev.button == 0:
		return m.press(ev)
	}
	m.still = true
	return m, nil
}

func (m *model) press(ev mouseMsg) (*model, Cmd) {
	if m.help {
		m.help = false
		return m, nil
	}
	z, ok := m.hit(ev.x, ev.y, canClick)
	if m.picker != nil {
		inside := false
		for _, r := range m.rects {
			if r.y == ev.y && ev.x >= r.x0 && ev.x < r.x1 && r.zone < len(m.drawn) && m.drawn[r.zone].area == "picker" {
				inside = true
			}
		}
		if !inside {
			// A click outside the menu closes it, as esc does.
			return m.pickerKey(keyMsg{name: "esc"})
		}
	}
	if !ok {
		m.disarm()
		return m, nil
	}
	// A click ends the typing of a search, as enter does, so the key of a
	// button is not typed into the query.
	m.search.editing = false
	if z.key != "" {
		return m.key_(keyMsg{name: z.key})
	}
	m.disarm()
	if z.drag != nil {
		m.dragging = z.drag
	}
	if z.click != nil {
		return m, z.click(ev)
	}
	return m, nil
}

// disarm drops an action that waits for its second press: any other
// click is not that press.
func (m *model) disarm() {
	if m.confirm != "" {
		m.confirm, m.toast = "", ""
	}
}

// selectBuild selects the build with key k, as j and k do, but keeps the
// list where the wheel left it.
func (m *model) selectBuild(k string) Cmd {
	if k == m.key {
		return nil
	}
	m.key, m.jobPinned = k, false
	m.setJob(0)
	return m.want()
}

// selectJob selects the job, as h and l do.
func (m *model) selectJob(id int64) Cmd {
	m.jobPinned = true
	m.setJob(id)
	return m.fetchLog()
}

// showFailure shows failure i in the log, as e does.
func (m *model) showFailure(i int) {
	if i < 0 || i >= len(m.found) {
		return
	}
	m.failAt, m.follow = i, false
	m.top = max(0, m.found[i].first-2)
	m.clampLog()
}

// The wheel moves three rows a notch.
const wheelRows = 3

func (m *model) wheelList(d int) {
	m.listScrolled = true
	m.listTop = max(0, m.listTop+d*wheelRows)
}

func (m *model) wheelSteps(d int) {
	m.stepsScrolled = true
	m.stepsTop = max(0, m.stepsTop+d*wheelRows)
}

func (m *model) wheelLog(d int) { m.scroll(d * wheelRows) }

// dragSplit sets the height of the jobs from the row of the pointer: the
// divider follows it.
func (m *model) dragSplit(ev mouseMsg) {
	if top := m.areaTop("jobs"); top >= 0 {
		m.jobsWant = max(1, ev.y-top)
		m.clampLog()
	}
}
