package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// colors are the inks and surfaces of the frames. With the palette of the
// terminal they are shades between its background and its colours, so
// they follow the tuios theme. Without it they are the 16 colours and the
// terminal's own background.
type colors struct {
	known bool
	// SGR parameters of the backgrounds.
	surface, raised, chip, redTint, accentTint string
	// Under the pointer: a row, a chip, a chip that is on, a failure.
	hoverRow, hot, accentHot, redHot string
	// SGR parameters of the inks.
	text, dim, faint, line, accent, green, red, yellow, other, doneGreen, doneRed string
	// Ghosts: the part of a bar that is only expected.
	ghostAccent, ghostLine, band, ghostGreen, ghostRed string
	acc, acc2, bgc, acDim                              rgb
}

func newColors(p palette) colors {
	if !p.known {
		return colors{text: "39", dim: "90", faint: "90", line: "90", accent: "34", green: "32", red: "31",
			yellow: "33", other: "35", doneGreen: "32", doneRed: "31", ghostAccent: "90", ghostLine: "90", band: "37",
			ghostGreen: "90", ghostRed: "90"}
	}
	bg, fg := p.bg, p.fg
	acc := p.ansi[4]
	return colors{
		known:       true,
		surface:     bg.mix(fg, .045).bg(),
		raised:      bg.mix(fg, .10).bg(),
		chip:        bg.mix(fg, .14).bg(),
		redTint:     bg.mix(p.ansi[1], .14).bg(),
		accentTint:  bg.mix(acc, .22).bg(),
		hoverRow:    bg.mix(fg, .075).bg(),
		hot:         bg.mix(fg, .22).bg(),
		accentHot:   bg.mix(acc, .34).bg(),
		redHot:      bg.mix(p.ansi[1], .26).bg(),
		text:        fg.fg(),
		dim:         fg.mix(bg, .42).fg(),
		faint:       fg.mix(bg, .62).fg(),
		line:        fg.mix(bg, .82).fg(),
		accent:      acc.fg(),
		green:       p.ansi[2].fg(),
		red:         p.ansi[1].fg(),
		yellow:      p.ansi[3].fg(),
		other:       p.ansi[5].fg(),
		doneGreen:   p.ansi[2].mix(bg, .35).fg(),
		doneRed:     p.ansi[1].mix(bg, .2).fg(),
		ghostAccent: bg.mix(acc, .32).fg(),
		ghostLine:   bg.mix(fg, .10).fg(),
		band:        bg.mix(fg, .22).fg(),
		ghostGreen:  bg.mix(p.ansi[2], .30).fg(),
		ghostRed:    bg.mix(p.ansi[1], .30).fg(),
		acc:         acc,
		acc2:        p.ansi[5],
		bgc:         bg,
		acDim:       acc.mix(bg, .55),
	}
}

// ink colours text and gives the colour back to the text colour, so a
// surface under it stays.
func ink(code, s string) string {
	if s == "" {
		return ""
	}
	return "\x1b[" + code + "m" + s + "\x1b[39m"
}

func bold(s string) string { return "\x1b[1m" + s + "\x1b[22m" }

// on puts s on the background code, then gives back the background back.
func on(code, back, s string) string {
	if code == "" {
		return s
	}
	end := "\x1b[49m"
	if back != "" {
		end = "\x1b[" + back + "m"
	}
	return "\x1b[" + code + "m" + s + end
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *model) mark(s string) string {
	c := m.pal
	switch s {
	case "running":
		return ink(c.accent, spinner[int(m.now().Unix())%len(spinner)])
	case "queued":
		return ink(c.faint, "○")
	case "pending":
		return ink(c.faint, "◌")
	case "success":
		return ink(c.green, "✓")
	case "failure":
		return ink(c.red, "✗")
	case "cancelled":
		return ink(c.faint, "–")
	case "skipped":
		return ink(c.faint, "⊘")
	case "approval":
		return ink(c.yellow, "◷")
	}
	return ink(c.faint, "·")
}

// stateInk is the colour of a state: the accent only for what runs.
func (m *model) stateInk(s string) string {
	switch s {
	case "running", "queued":
		return m.pal.accent
	case "success":
		return m.pal.green
	case "failure":
		return m.pal.red
	}
	return m.pal.dim
}

// dur is a time such as 45s, 3m 20s or 1h 5m.
func dur(sec float64) string {
	s := int(math.Round(max(0, sec)))
	switch {
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, s/60%60)
}

// ago is a time such as 3m ago or 2h ago.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d ago"
}

func itoa(n int) string { return strconv.Itoa(n) }

// fit cuts or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// spread2 puts left and right on one line of w cells, the right end kept.
func spread2(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	if rw >= w {
		return fit(right, w)
	}
	return fit(left, w-rw-1) + " " + right
}

// chip is text on a filled chip, with the surface under it back after it.
func (m *model) chip(text, bg, back string) string {
	if !m.pal.known {
		return "[" + text + "]"
	}
	if bg == "" {
		bg = m.pal.chip
	}
	return on(bg, back, " "+text+" ")
}

// label is a section label: uppercase and dim.
func (m *model) label(name string) string { return ink(m.pal.faint, bold(name)) }

// button is a chip that a click runs as the key, lit up under the pointer.
// id names it, as one key can have a button in two places.
func (m *model) button(id, key, text, bg, back string) string {
	if m.hover == id {
		if bg != "" && bg == m.pal.accentTint {
			bg = m.pal.accentHot
		} else {
			bg = m.pal.hot
		}
	}
	return m.zoned(zone{hover: id, key: key}, m.chip(text, bg, back))
}

// anchor is text that a click runs as the key, underlined under the
// pointer.
func (m *model) anchor(id, key, text string) string {
	if m.hover == id {
		text = "\x1b[4m" + text + "\x1b[24m"
	}
	return m.zoned(zone{hover: id, key: key}, text)
}

// lit marks a row of a panel for surface: \x00 when it is selected, \x01
// when it is under the pointer.
func (m *model) lit(line string, selected bool, id string) string {
	switch {
	case selected:
		return "\x00" + line
	case id != "" && m.hover == id:
		return "\x01" + line
	}
	return line
}

// area wraps each line of a panel in zone z.
func (m *model) area(z zone, lines []string) []string {
	n := m.addZone(z)
	for i := range lines {
		lines[i] = inZone(n, lines[i])
	}
	return lines
}

// surface draws lines on a filled surface of w cells, with a margin of two
// cells inside. A line that starts with \x00 is the selected row: it has
// the raised background and a bar in the accent at the left edge. A line
// that starts with \x01 is the row under the pointer, on a lighter shade.
func (m *model) surface(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	bg := m.pal.surface
	for i := range h {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		if rest, ok := strings.CutPrefix(line, "\x00"); ok {
			row := on(m.pal.raised, bg, ink(m.pal.accent, "▌")+" "+fit(rest, w-3)+" ")
			out = append(out, on(bg, "", row))
			continue
		}
		if rest, ok := strings.CutPrefix(line, "\x01"); ok {
			out = append(out, on(bg, "", on(m.pal.hoverRow, bg, "  "+fit(rest, w-4)+"  ")))
			continue
		}
		out = append(out, on(bg, "", "  "+fit(line, w-4)+"  "))
	}
	return out
}

func beside(gap int, cols ...[]string) []string {
	n := 0
	for _, c := range cols {
		n = max(n, len(c))
	}
	out := make([]string, n)
	for i := range n {
		for k, c := range cols {
			if k > 0 {
				out[i] += strings.Repeat(" ", gap)
			}
			if i < len(c) {
				out[i] += c[i]
			}
		}
	}
	return out
}

type layout struct {
	listW, rightW, body int
	buildH, jobsH, lowH int
	stepsW, logW        int
}

const headerRows, footerRows = 6, 2

func (m *model) layout() layout {
	l := layout{body: max(0, m.height-headerRows-footerRows)}
	inner := m.width - 2
	if m.width >= 100 {
		l.listW = min(46, max(32, inner*28/100))
	}
	l.rightW = inner - l.listW
	if l.listW > 0 {
		l.rightW--
	}
	if m.tall {
		l.lowH, l.logW = l.body, l.rightW
		return l
	}
	l.buildH = 8
	rows := 1
	if b, ok := m.selected(); ok {
		rows = len(m.timeline(b, m.fc(b)))
		if slices.ContainsFunc(m.jobsOfBuild(b), func(r jobRow) bool { return r.job.State == "skipped" }) {
			rows++
		}
	}
	// The jobs take the rows they need, and leave the steps and the log at
	// least eight.
	l.jobsH = min(rows+4, max(5, l.body-l.buildH-2-8))
	if m.jobsWant > 0 {
		// The height a drag of the divider set, with room for the log.
		l.jobsH = max(4, min(m.jobsWant, l.body-l.buildH-2-6))
	}
	l.lowH = max(0, l.body-l.buildH-l.jobsH-2)
	need := 24
	if j, ok := m.selectedJob(); ok {
		for _, s := range j.Steps {
			need = max(need, ansi.StringWidth(s.Name)+14)
		}
	}
	l.stepsW = min(max(28, l.rightW*2/5), need)
	l.logW = l.rightW - l.stepsW - 1
	return l
}

// logRows is how many log lines are in view.
func (m *model) logRows() int {
	n := m.layout().lowH - 4 - m.failureRows()
	return max(1, n)
}

func (m *model) fc(b Build) forecast {
	f, _ := m.estimate(b)
	return f
}

func (m *model) windowTitle() string {
	title := "CI"
	if m.repo != "" {
		title += " " + m.repo
		if b, ok := m.selected(); ok && active(b.State) {
			text, _ := m.headline(b, m.fc(b))
			title += ": " + ansi.Strip(text)
		}
	}
	return title
}

func (m *model) render() string {
	if m.width < 30 || m.height < 10 {
		return "The pane is too small."
	}
	if m.picker != nil {
		// The menu of repositories is a box over the app, which dims.
		p := m.picker
		m.picker = nil
		base := strings.Split(m.render(), "\n")
		m.picker = p
		return strings.Join(m.overlay(base), "\n")
	}
	l := m.layout()
	var body []string
	switch {
	case m.help:
		// A click anywhere closes the help.
		body = m.surface(m.helpLines(), m.width-2, l.body)
	default:
		var right []string
		gap := []string{""}
		b, ok := m.selected()
		switch {
		case m.tall:
			right = m.logPanel(l.logW, l.lowH)
		case ok:
			fc, like := m.estimate(b)
			right = append(right, m.surface(m.buildLines(b, fc, like, l.rightW-4), l.rightW, l.buildH)...)
			right = append(right, gap...)
			right = append(right, m.area(zone{area: "jobs", wheel: m.wheelJobs}, m.surface(m.jobLines(b, fc, l.rightW-4), l.rightW, l.jobsH))...)
			right = append(right, m.divider(l.rightW))
			right = append(right, beside(1, m.stepsPanel(l.stepsW, l.lowH), m.logPanel(l.logW, l.lowH))...)
		default:
			msg := "No builds yet."
			switch {
			case m.err != "":
				msg = ""
			case m.updated.IsZero():
				msg = "Loading the builds."
			case len(m.all) > 0:
				msg = "No build matches the filters."
			}
			right = m.surface([]string{"", ink(m.pal.dim, msg)}, l.rightW, l.body)
		}
		for i := range right {
			if ansi.StringWidth(right[i]) == 0 {
				right[i] = strings.Repeat(" ", l.rightW)
			}
		}
		if l.listW > 0 {
			body = beside(1, m.listPanel(l.listW, l.body), right)
		} else {
			body = right
		}
	}
	for len(body) < l.body {
		body = append(body, "")
	}
	lines := append(m.header(), body[:l.body]...)
	lines = append(lines, "", m.footer())
	for i := range lines {
		lines[i] = fit(" "+lines[i], m.width)
	}
	return strings.Join(lines, "\n")
}

// divider is the row between the jobs and the log. A drag on it moves it,
// and so sets the height of the jobs. Under the pointer it shows a line.
func (m *model) divider(w int) string {
	c := m.pal
	line := strings.Repeat(" ", w)
	if m.hover == "divider" || m.dragging != nil {
		hint := " drag to resize "
		side := max(0, w-len(hint)) / 2
		line = ink(c.line, strings.Repeat("─", side)) + ink(c.faint, hint) + ink(c.line, strings.Repeat("─", max(0, w-side-len(hint))))
	}
	return m.zoned(zone{hover: "divider", drag: m.dragSplit}, line)
}

func (m *model) header() []string {
	c := m.pal
	w := m.width - 2
	owner, name, _ := strings.Cut(m.repo, "/")
	// The wordmark: the repository name in the pixel font, in a fade from
	// the accent to the next colour of the theme.
	scale := 2
	if _, n := wordmark(name, 2); n > w/2 {
		scale = 1
	}
	rows, mw := wordmark(name, scale)
	for i := range rows {
		rows[i] = m.fadeText(rows[i])
	}
	var right string
	if m.err != "" {
		right = ink(c.red, m.err)
	} else if !m.updated.IsZero() {
		right = ink(c.faint, "updated "+m.updated.Format("15:04:05"))
	}
	info := []string{
		ink(c.faint, bold("GITHUB ACTIONS")),
		ink(c.dim, owner+" / ") + ink(c.text, bold(name)),
		right,
	}
	chart := m.historyChart(20)
	for i := range rows {
		left := rows[i] + "   " + info[i]
		r := ""
		if i < 2 {
			r = chart[i]
		} else {
			r = ink(c.faint, "last builds")
		}
		rows[i] = spread2(left, r, w)
	}
	_ = mw
	running, failed := 0, 0
	for _, b := range m.all {
		if active(b.State) {
			running++
		}
		if b.State == "failure" {
			failed++
		}
	}
	chips := m.chip(bold(itoa(len(m.list)))+" builds", "", "")
	if running > 0 {
		chips += " " + m.chip(ink(c.accent, bold(itoa(running))+" running"), c.accentTint, "")
	}
	if failed > 0 {
		chips += " " + m.chip(ink(c.red, bold(itoa(failed))+" failed"), c.redTint, "")
	}
	main := m.defaultBranch
	if main == "" {
		main = "main"
	}
	mainOn := m.f.branch != "" && m.f.branch == m.defaultBranch
	branch := "this branch"
	if m.f.branch != "" && !mainOn {
		branch = m.f.branch
	}
	filter := func(on bool, key, word string) string {
		box := ink(c.faint, "□")
		bg := ""
		if on {
			box, bg = ink(c.accent, "■"), c.accentTint
		}
		return m.button("head:"+key, key, ink(c.accent, key)+" "+box+" "+word, bg, "")
	}
	filters := ink(c.faint, "filter ") + filter(mainOn, "m", main) + " " + filter(m.f.branch != "" && !mainOn, "f", branch) + " " +
		filter(m.f.activeOnly, "a", "running") + " " + filter(m.f.showSkipped, "s", "skipped")
	keys := m.button("head:r", "r", ink(c.accent, "r")+" "+ink(c.dim, "refresh"), c.raised, "") + " " + m.button("head:?", "?", ink(c.accent, "?")+" "+ink(c.dim, "keys"), c.raised, "")
	return []string{"", rows[0], rows[1], rows[2], spread2(chips+"   "+filters, keys, w), ""}
}

// fadeText colours each cell of s in a fade from the accent to the next
// colour of the theme.
func (m *model) fadeText(s string) string {
	c := m.pal
	if !c.known {
		return ink(c.accent, s)
	}
	r := []rune(s)
	var sb strings.Builder
	for i, ch := range r {
		if ch == ' ' {
			sb.WriteRune(ch)
			continue
		}
		t := float64(i) / float64(max(1, len(r)-1))
		sb.WriteString(ink(c.acc.mix(c.acc2, t).fg(), string(ch)))
	}
	return sb.String()
}

// historyChart is the last builds of the repository as a chart two rows
// high, oldest at the left, one bar for each build with a gap, as tall as
// the build was long and in the muted colour of how it ended.
func (m *model) historyChart(most int) [2]string {
	var done []Build
	for _, b := range m.all {
		if b.Seconds > 0 && b.State != "skipped" {
			done = append(done, b)
		}
		if len(done) == most {
			break
		}
	}
	top := 0.0
	for _, b := range done {
		top = max(top, b.Seconds)
	}
	levels := []rune(" ▁▂▃▄▅▆▇█")
	var hi, lo strings.Builder
	for i, b := range slices.Backward(done) {
		n := 1
		if top > 0 {
			n = max(1, int(b.Seconds/top*16+.5))
		}
		code := m.resultInk(b)
		upper, lower := max(0, n-8), min(8, n)
		hi.WriteString(ink(code, string(levels[upper])))
		lo.WriteString(ink(code, string(levels[lower])))
		if i > 0 {
			hi.WriteString(" ")
			lo.WriteString(" ")
		}
	}
	return [2]string{hi.String(), lo.String()}
}

// resultInk is the muted colour of how a build ended.
func (m *model) resultInk(b Build) string {
	switch b.State {
	case "failure":
		return m.pal.doneRed
	case "success":
		return m.pal.doneGreen
	}
	return m.pal.faint
}

func (m *model) footer() string {
	c := m.pal
	if m.toast != "" {
		if m.confirm != "" {
			// A click on the question is the second press.
			return m.zoned(zone{key: m.confirm}, ink(c.accent, m.toast))
		}
		return ink(c.accent, m.toast)
	}
	// The keys with one key are buttons: a click on the chip or its word
	// runs it.
	keys := [][2]string{{"j k", "build"}, {"h l", "job"}, {"/", "search"}, {"R", "run again"}, {"o", "open"},
		{"G", "repository"}, {"q", "quit"}}
	var parts []string
	for _, k := range keys {
		if strings.Contains(k[0], " ") {
			parts = append(parts, m.chip(ink(c.text, bold(k[0])), c.raised, "")+" "+ink(c.dim, k[1]))
			continue
		}
		id := "foot:" + k[0]
		word := ink(c.dim, k[1])
		if m.hover == id {
			word = ink(c.text, k[1])
		}
		parts = append(parts, m.zoned(zone{hover: id, key: k[0]}, m.button(id, k[0], ink(c.text, bold(k[0])), c.raised, "")+" "+word))
	}
	pos := ""
	if b, ok := m.selected(); ok {
		// The status line: the jobs of the build by how they ended.
		n := map[string]int{}
		for _, r := range m.jobsOfBuild(b) {
			n[r.job.State]++
		}
		var parts []string
		for _, p := range []struct{ st, word, code string }{
			{"failure", "failing", c.red}, {"running", "running", c.accent}, {"queued", "waiting", c.dim},
			{"approval", "waiting for approval", c.yellow},
			{"success", "successful", c.green}, {"skipped", "skipped", c.faint}} {
			if n[p.st] > 0 {
				parts = append(parts, ink(p.code, itoa(n[p.st])+" "+p.word))
			}
		}
		pos = strings.Join(parts, ink(c.faint, ", "))
	}
	if i := slices.IndexFunc(m.list, func(b Build) bool { return b.Key == m.key }); i >= 0 {
		pos += ink(c.faint, "   "+itoa(i+1)+" / "+itoa(len(m.list)))
	}
	return spread2(strings.Join(parts, "  "), pos, m.width-2)
}

func (m *model) helpLines() []string {
	rows := [][2]string{
		{"j  k", "Next or previous build"},
		{"h  l  tab", "Previous or next job"},
		{"n  p", "Next or previous step: show where it ends in the log"},
		{"e  E", "Next or previous failure"},
		{"c", "Copy the link of the job"},
		{"o  O", "Open the job, or its pull request, on GitHub"},
		{"R", "Run the failed jobs again (press it twice)"},
		{"ctrl+r", "Run all the jobs again (press it twice)"},
		{"/", "Search the log, ctrl+n and ctrl+p step, esc closes"},
		{"#", "Show or hide the line numbers of the log"},
		{"m", "Show only the builds of the default branch"},
		{"f", "Show only the branch of this build"},
		{"a", "Show only the builds that run or wait"},
		{"s", "Show the builds in which every run was skipped"},
		{"z", "Give the whole height to the log"},
		{"pgup  pgdown", "Scroll the log"},
		{"home  end", "Go to the top of the log, or follow its end"},
		{"r  space", "Read the builds now"},
		{"G", "Watch another repository"},
		{"click", "Select a build, a job or a step, or run a button"},
		{"wheel", "Scroll the builds, the steps or the log under the pointer"},
		{"drag", "Drag the row above the log to resize the jobs"},
		{"?  esc", "Close this help"},
		{"q", "Quit"},
	}
	out := []string{"", m.label("KEYS"), ""}
	for _, r := range rows {
		out = append(out, ink(m.pal.accent, fit(r[0], 16))+r[1])
	}
	return out
}

// listPanel is the builds, newest first. The builds of other branches than
// the default one are set in, with the branch in another colour.
func (m *model) listPanel(w, h int) []string {
	c := m.pal
	inner := w - 4
	var lines []string
	selStart, selEnd := 0, 0
	for _, b := range m.list {
		onMain := b.Branch == m.defaultBranch && b.IsBuild
		indent := ""
		branch := ink(c.other, b.Branch)
		title := ink(c.text, b.Title)
		if onMain {
			branch = ink(c.accent, b.Branch)
		} else {
			indent = "  "
			title = ink(c.dim, b.Title)
		}
		sha := b.SHA[:min(7, len(b.SHA))]
		start := len(lines)
		key := b.Key
		id := "build:" + key
		n := m.addZone(zone{hover: id, click: func(mouseMsg) Cmd { return m.selectBuild(key) }})
		row := []string{
			spread2(indent+m.mark(b.State)+" "+title, ink(c.faint, ago(m.now().Sub(b.Started))), inner),
			spread2(indent+"  "+branch+ink(c.faint, " · "+sha), m.rowTime(b), inner),
		}
		if active(b.State) {
			fc := m.fc(b)
			row = append(row, indent+"  "+m.bar(b, fc, inner-2-len(indent), false))
		}
		if b.Key == m.key {
			selStart, selEnd = start, start+len(row)
		}
		for i := range row {
			row[i] = m.lit(inZone(n, row[i]), b.Key == m.key, id)
		}
		lines = append(lines, row...)
		lines = append(lines, "")
	}
	head := []string{"", spread2(m.label("BUILDS")+" "+m.chip(itoa(len(m.list)), "", c.surface), ink(c.faint, "newest first"), inner), ""}
	rows := h - len(head)
	// Keep the selected build in view, unless the wheel moved the list.
	start := 0
	switch {
	case m.listScrolled:
		start = min(m.listTop, max(0, len(lines)-rows))
	case selEnd > rows:
		start = min(selStart, selEnd-rows)
	}
	m.listTop = start
	if start > 0 && start < len(lines) {
		lines = lines[start:]
	}
	return m.area(zone{wheel: m.wheelList}, m.surface(append(head, lines...), w, h))
}

// rowTime is the time in the list: how long a finished build took, and for
// a running one about how long it has left.
func (m *model) rowTime(b Build) string {
	c := m.pal
	if !active(b.State) {
		if b.Seconds > 0 {
			return ink(c.dim, dur(b.Seconds))
		}
		return ""
	}
	if fc := m.fc(b); fc.known && !fc.late {
		return ink(c.accent, "~"+dur(rounded(max(5, fc.total-fc.elapsed)))+" left")
	}
	return ink(c.accent, dur(m.now().Sub(b.Started).Seconds()))
}

// headline is the answer the window is opened for, and its ink.
func (m *model) headline(b Build, fc forecast) (string, string) {
	c := m.pal
	switch b.State {
	case "queued":
		return "Waits for a runner", c.accent
	case "approval":
		return "Waits for a maintainer to approve it", c.yellow
	case "running":
		switch {
		case !fc.known:
			return "Running for " + dur(fc.elapsed), c.accent
		case fc.late:
			return "Taking longer than usual, " + dur(rounded(fc.over)) + " over", c.yellow
		}
		text := "About " + dur(rounded(max(5, fc.total-fc.elapsed))) + " left"
		if b.Failing {
			return "A job failed. " + text, c.red
		}
		return text, c.accent
	case "success":
		return "Passed in " + dur(b.Seconds), c.green
	case "failure":
		return "Failed after " + dur(b.Seconds), c.red
	case "cancelled":
		return "Cancelled after " + dur(b.Seconds), c.dim
	case "skipped":
		return "Skipped", c.dim
	}
	return "Ended after " + dur(b.Seconds), c.dim
}

// before is the build that the default branch had before b: the newest
// finished one that started earlier.
func (m *model) before(b Build) (Build, bool) {
	for _, o := range m.all {
		if o.Key != b.Key && o.IsBuild && o.Branch == m.defaultBranch && !active(o.State) && o.State != "skipped" && o.Started.Before(b.Started) {
			return o, true
		}
	}
	return Build{}, false
}

func (m *model) buildLines(b Build, fc forecast, like []Build, w int) []string {
	c := m.pal
	text, hue := m.headline(b, fc)
	sha := b.SHA[:min(7, len(b.SHA))]
	where := b.Branch
	if b.Branch != m.defaultBranch {
		where += " · not on " + m.defaultBranch
	}
	word := b.State
	if b.Failing {
		word = "failing"
	}
	meta := strings.Join(slices.DeleteFunc([]string{b.Event, b.Actor, strings.Join(b.Workflows, ", "), "started " + ago(m.now().Sub(b.Started))}, func(s string) bool { return s == "" }), " · ")
	var note string
	switch {
	case active(b.State) && fc.known:
		note = ink(c.text, dur(fc.elapsed)+" so far") + ink(c.dim, " · usually "+dur(rounded(fc.total)))
		if lo, hi := dur(rounded(fc.low)), dur(rounded(fc.high)); lo != hi {
			note += ink(c.dim, " · between "+lo+" and "+hi)
		}
		note += ink(c.faint, ", going by the jobs of the last "+itoa(fc.samples)+" builds")
	case active(b.State):
		note = ink(c.text, dur(fc.elapsed)+" so far") + ink(c.dim, " · fewer than 3 earlier builds passed, so no estimate yet")
	case fc.known && fc.samples > 0:
		note = ink(c.dim, "usually "+dur(rounded(fc.total)))
		if lo, hi := dur(rounded(fc.low)), dur(rounded(fc.high)); lo != hi {
			note += ink(c.dim, ", between "+lo+" and "+hi)
		}
		if d := b.Seconds - fc.total; b.State == "success" && math.Abs(d) >= 5 && math.Abs(d) >= .15*fc.total {
			word := " slower"
			if d < 0 {
				word, d = " faster", -d
			}
			note += ink(c.dim, " · this one "+dur(rounded(d))+word)
		}
	}
	prev := ink(c.faint, "before, "+m.defaultBranch+"  ")
	if o, ok := m.before(b); ok {
		prev += m.mark(o.State) + " " + ink(c.text, o.Title) + ink(c.faint, " "+o.SHA[:min(7, len(o.SHA))]) +
			ink(c.dim, " "+strings.ToLower(ansi.Strip(m.headlineWord(o)))+" · "+ago(m.now().Sub(o.Started)))
	} else {
		prev += ink(c.dim, "no earlier build")
	}
	spark := m.sparkline(like)
	if spark != "" {
		spark += ink(c.faint, " earlier builds")
	}
	barW := max(10, w-ansi.StringWidth(spark)-3)
	return []string{
		"",
		spread2(m.label("BUILD")+ink(c.faint, "  "+where+" · "+sha)+m.pill(b), ink(m.stateInk(b.State), word), w),
		spread2(m.mark(b.State)+" "+ink(c.text, bold(b.Title)), ink(hue, bold(text)), w),
		ink(c.dim, "  "+meta),
		m.bar(b, fc, barW, true) + "   " + spark,
		note,
		prev,
	}
}

// pill is the state, the number and the title of the pull request of the
// build, when it has one.
func (m *model) pill(b Build) string {
	c := m.pal
	pr, ok := m.pulls[b.PR]
	if b.PR == 0 || !ok {
		return ""
	}
	word, code, bg := "Open", c.green, c.raised
	switch pr.State {
	case "merged":
		word, code = "Merged", c.other
	case "closed":
		word, code = "Closed", c.red
	}
	// A click on the pill opens the pull request, as O does.
	if m.hover == "pill" {
		bg = m.pal.hot
	}
	if pr.State == "" {
		word = "#" + itoa(pr.Number)
		return "   " + m.zoned(zone{hover: "pill", key: "O"}, m.chip(ink(c.dim, word), bg, c.surface))
	}
	title := ink(c.text, "#"+itoa(pr.Number)+" ") + ink(c.dim, pr.Title)
	if m.hover == "pill" {
		title = "\x1b[4m" + title + "\x1b[24m"
	}
	return "   " + m.zoned(zone{hover: "pill", key: "O"}, m.chip(ink(code, bold(word)), bg, c.surface)+title)
}

func (m *model) headlineWord(o Build) string {
	text, _ := m.headline(o, forecast{})
	return text
}

// bar is the time gone as a thin line on a scale to the slow end of the
// earlier builds. The gone part fades in towards now. A tick marks where
// a build like this usually ends, and a lighter band how far apart the
// earlier ones ended.
func (m *model) bar(b Build, fc forecast, w int, tick bool) string {
	c := m.pal
	elapsed := fc.elapsed
	if !active(b.State) {
		elapsed = b.Seconds
	}
	scale := max(elapsed, fc.high, fc.total) * 1.04
	glyph := "▄"
	if !tick {
		glyph = "━"
	}
	if scale <= 0 || !fc.known {
		if active(b.State) {
			return ink(c.ghostLine, strings.Repeat(glyph, w))
		}
		return ink(m.resultInk(b), strings.Repeat(glyph, w))
	}
	fillTo := int(elapsed / scale * float64(w))
	tk := int(fc.total / scale * float64(w))
	var sb strings.Builder
	for i := range w {
		t := (float64(i) + .5) / float64(w) * scale
		switch {
		case tick && i == tk:
			sb.WriteString(ink(c.text, "▐"))
		case i < fillTo:
			sb.WriteString(m.fade(b, i, fillTo, glyph))
		case t >= fc.low && t <= fc.high:
			sb.WriteString(ink(c.band, glyph))
		default:
			sb.WriteString(ink(c.ghostLine, glyph))
		}
	}
	return sb.String()
}

// fade is the colour of cell i of n of the gone part: from a soft shade of
// the state's colour to the colour itself at now.
func (m *model) fade(b Build, i, n int, glyph string) string {
	c := m.pal
	code := m.stateInk(b.State)
	if b.Failing {
		code = c.red
	}
	if !c.known || !active(b.State) {
		if !active(b.State) {
			code = m.resultInk(b)
		}
		return ink(code, glyph)
	}
	t := .35 + .65*float64(i+1)/float64(max(1, n))
	return ink(c.acDim.mix(c.acc, t).fg(), glyph)
}

// sparkline is the builds, oldest first, one bar each with a gap, as tall
// as the build was long, in the muted colour of how it ended.
func (m *model) sparkline(like []Build) string {
	if len(like) == 0 {
		return ""
	}
	like = like[:min(12, len(like))]
	// The bars span from the shortest build to the longest, so builds of
	// close lengths still read apart.
	lo, hi := math.Inf(1), 0.0
	for _, b := range like {
		lo, hi = min(lo, b.Seconds), max(hi, b.Seconds)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var parts []string
	for _, b := range slices.Backward(like) {
		n := 4
		if hi > lo {
			n = 1 + int((b.Seconds-lo)/(hi-lo)*float64(len(levels)-2)+.5)
		}
		parts = append(parts, ink(m.resultInk(b), string(levels[n])))
	}
	return strings.Join(parts, " ")
}

// timeline is the rows of the jobs: the jobs, then the jobs the earlier
// builds say come. The skipped jobs share one row.
func (m *model) timeline(b Build, fc forecast) []jobRow {
	var rows []jobRow
	byKey := map[string]jobForecast{}
	for _, f := range fc.jobs {
		byKey[f.key] = f
	}
	for _, r := range m.jobsOfBuild(b) {
		if r.job.State == "skipped" {
			continue
		}
		r.fc = byKey[r.key]
		rows = append(rows, r)
	}
	if active(b.State) {
		for _, f := range fc.jobs {
			if f.expected {
				// The key joins the workflow and the name with a slash, and
				// either can hold one, so the names come from the jobs.
				wf, name, _ := strings.Cut(f.key, "/")
				if n, ok := m.names[f.key]; ok {
					wf, name = n[0], n[1]
				}
				rows = append(rows, jobRow{key: f.key, job: Job{Name: name, Workflow: wf, State: "pending"}, fc: f})
			}
		}
	}
	return rows
}

// beforeJobs are the times of the jobs of the build before, by key.
func (m *model) beforeJobs(b Build) map[string]float64 {
	o, ok := m.before(b)
	if !ok {
		return nil
	}
	out := map[string]float64{}
	for _, r := range m.jobsOfBuild(o) {
		if !r.job.Started.IsZero() && !r.job.Completed.IsZero() {
			out[r.key] = r.job.Completed.Sub(r.job.Started).Seconds()
		}
	}
	return out
}

func (m *model) jobLines(b Build, fc forecast, w int) []string {
	c := m.pal
	rows := m.timeline(b, fc)
	var skipped []string
	for _, r := range m.jobsOfBuild(b) {
		if r.job.State == "skipped" {
			skipped = append(skipped, r.job.Name)
		}
	}
	prev := m.beforeJobs(b)
	nameW := min(30, max(14, w/4))
	durW, prevW := 17, 21
	if prev == nil {
		prevW = 0
	}
	tw := max(8, w-nameW-durW-prevW-4)
	elapsed := fc.elapsed
	if !active(b.State) {
		elapsed = b.Seconds
	}
	end := elapsed
	off := func(t time.Time) float64 {
		if t.IsZero() {
			return -1
		}
		return max(0, t.Sub(b.Started).Seconds())
	}
	for _, r := range rows {
		end = max(end, off(r.job.Completed), r.fc.high, r.fc.end)
	}
	scale := max(1, end*1.03)
	cell := func(t float64) int { return min(tw-1, max(0, int(t/scale*float64(tw)))) }
	running, coming, passed, failed := 0, 0, 0, 0
	var lines []string
	for _, r := range rows {
		s, e := off(r.job.Started), off(r.job.Completed)
		st := r.job.State
		switch st {
		case "running":
			running++
			e = elapsed
		case "pending":
			coming++
			s, e = r.fc.start, r.fc.end
		case "queued":
			s, e = r.fc.start, r.fc.end
		case "success":
			passed++
		case "failure":
			failed++
		}
		cells := make([]string, tw)
		for i := range cells {
			cells[i] = " "
		}
		paint := func(from, to float64, glyph, code string) {
			if from < 0 || to < from {
				return
			}
			for i := cell(from); i <= cell(to); i++ {
				cells[i] = ink(code, glyph)
			}
		}
		// Half blocks: done in the muted colour of the result, running in
		// the accent, and a ghost where the job is only expected.
		switch st {
		case "pending", "queued":
			paint(s, e, "▄", c.ghostAccent)
			paint(e, r.fc.high, "╌", c.ghostLine)
		case "running":
			paint(s, e, "▄", c.accent)
			paint(e, r.fc.end, "▄", c.ghostAccent)
			paint(r.fc.end, r.fc.high, "╌", c.band)
		case "failure":
			paint(s, max(s, e), "▄", c.doneRed)
		default:
			paint(s, max(s, e), "▄", c.doneGreen)
		}
		if at := cell(elapsed); active(b.State) && cells[at] == " " {
			cells[at] = ink(c.dim, "│")
		}
		time := ""
		switch {
		case st == "running" && r.fc.typical > 0:
			time = ink(c.accent, dur(elapsed-s)) + ink(c.dim, " / ~"+dur(r.fc.typical))
		case st == "running":
			time = ink(c.accent, dur(elapsed-s))
		case st == "pending" || st == "queued":
			if r.fc.typical > 0 {
				time = ink(c.faint, "~"+dur(r.fc.typical))
			} else {
				time = ink(c.faint, "waits")
			}
		case s >= 0 && e >= s:
			time = ink(c.dim, dur(e-s))
		}
		pcol := ""
		if prevW > 0 {
			// The time the job took then, and in words what changed:
			// broke, slower or faster, with the difference.
			if p, ok := prev[r.key]; ok {
				pcol = ink(c.faint, dur(p))
				switch cur := e - s; {
				case st == "failure":
					pcol += ink(c.red, " · broke")
				case done(st) && cur-p > 5 && cur-p > .15*p:
					pcol += ink(c.yellow, " · "+dur(rounded(cur-p))+" slower")
				case done(st) && p-cur > 5 && p-cur > .15*p:
					pcol += ink(c.green, " · "+dur(rounded(p-cur))+" faster")
				}
			}
		}
		name := ink(c.text, r.job.Name)
		if st == "pending" {
			name = ink(c.dim, r.job.Name)
		}
		if len(b.Workflows) > 1 && r.job.Workflow != "" {
			name += ink(c.faint, " "+r.job.Workflow)
		}
		line := m.mark(st) + " " + fit(name, nameW) + " " + strings.Join(cells, "") + " " + lpad(time, durW) + lpad(pcol, prevW)
		if id := r.job.ID; id != 0 {
			// A click on the row, its bar too, selects the job.
			hover := "job:" + strconv.FormatInt(id, 10)
			line = m.lit(m.zoned(zone{hover: hover, click: func(mouseMsg) Cmd { return m.selectJob(id) }}, line), id == m.jobID, hover)
		}
		lines = append(lines, line)
	}
	if len(skipped) > 0 {
		lines = append(lines, m.mark("skipped")+" "+ink(c.faint, "skipped: "+strings.Join(skipped, ", ")))
	}
	if len(lines) == 0 {
		lines = append(lines, ink(c.dim, "No jobs yet."))
	}
	var note []string
	for _, p := range []struct {
		n    int
		word string
	}{{running, "running"}, {coming, "to come"}, {failed, "failed"}, {passed, "passed"}} {
		if p.n > 0 {
			note = append(note, itoa(p.n)+" "+p.word)
		}
	}
	prevHead := ""
	if prevW > 0 {
		prevHead = lpad(ink(c.faint, "before, "+m.defaultBranch), prevW)
	}
	// The axis: a label each minute or more, and now.
	axis := make([]rune, tw)
	for i := range axis {
		axis[i] = ' '
	}
	step := 60.0
	for scale/step > float64(tw)/6 {
		step *= 2
	}
	nowAt := -100
	if active(b.State) {
		nowAt = cell(elapsed)
	}
	for t := 0.0; t < scale; t += step {
		label := "0"
		if t > 0 {
			label = dur(t)
			label = strings.TrimSuffix(label, " 0s")
		}
		at := cell(t)
		// A label that would touch now gives way to it.
		if at+len(label) <= tw && (at+len(label) < nowAt-1 || at > nowAt+6) {
			copy(axis[at:], []rune(label))
		}
	}
	axisText := ink(c.faint, string(axis))
	if active(b.State) {
		at := cell(elapsed)
		if at+5 <= tw {
			axisText = ink(c.faint, string(axis[:at])) + m.chip(ink(c.text, bold("now")), c.raised, c.surface) + ink(c.faint, string(axis[at+5:]))
		}
	}
	head := []string{
		"",
		spread2(m.label("JOBS")+ink(c.faint, "  "+strings.Join(note, ", ")), prevHead, w),
		strings.Repeat(" ", 2+nameW+1) + axisText,
	}
	all := append(head, lines...)
	// Keep the selected job in view, unless the wheel moved the jobs. The
	// head stays at the top either way.
	h := m.layout().jobsH
	over := len(all) - h
	start := 0
	switch {
	case m.jobsScrolled:
		start = min(m.jobsTop, max(0, over))
	case over > 0:
		sel := slices.IndexFunc(rows, func(r jobRow) bool { return r.job.ID == m.jobID })
		start = min(over, max(0, sel-(h-len(head)-1)))
	}
	m.jobsTop = start
	if start > 0 {
		all = append(head, lines[start:]...)
	}
	return all
}

// lpad right-aligns s in w cells.
func lpad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw >= w {
		return ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-sw) + s
}

func (m *model) stepsPanel(w, h int) []string {
	c := m.pal
	j, ok := m.selectedJob()
	if !ok {
		return m.surface([]string{"", m.label("STEPS")}, w, h)
	}
	inner := w - 4
	lines := []string{"", spread2(m.label("STEPS")+ink(c.faint, "  "+j.Name), m.chip(itoa(len(j.Steps)), "", c.surface), inner), ""}
	sel := -1
	for i, s := range j.Steps {
		right := ""
		switch {
		case !s.Started.IsZero() && !s.Completed.IsZero():
			right = ink(c.dim, dur(s.Completed.Sub(s.Started).Seconds()))
		case s.State == "running" && !s.Started.IsZero():
			right = ink(c.accent, dur(m.now().Sub(s.Started).Seconds()))
		}
		name := ink(c.text, s.Name)
		switch s.State {
		case "failure":
			name = ink(c.red, s.Name)
		case "queued", "skipped", "pending":
			name = ink(c.faint, s.Name)
		case "running":
			name = ink(c.accent, s.Name)
		}
		line := spread2(m.mark(s.State)+" "+name, right, inner-1)
		hover := ""
		if i < len(m.starts) && m.starts[i] >= 0 {
			// A step that wrote to the log: a click shows where it ends,
			// as n and p do.
			hover = "step:" + itoa(i)
			line = m.zoned(zone{hover: hover, click: func(mouseMsg) Cmd { m.showStep(i); return nil }}, line)
		}
		picked := i == m.step || (m.step < 0 && (s.State == "running" || (s.State == "failure" && sel < 0)))
		if picked {
			sel = i
		}
		lines = append(lines, m.lit(line, picked, hover))
	}
	// Keep the selected step in view, unless the wheel moved the steps.
	k, over := 0, len(lines)-h
	switch {
	case m.stepsScrolled:
		k = min(m.stepsTop, max(0, over), max(0, len(lines)-3))
	case over > 0 && sel >= 0:
		k = min(over, max(0, sel-(h-6)/2), len(lines)-3)
	}
	m.stepsTop = k
	if k > 0 {
		lines = append(lines[:3], lines[3+k:]...)
	}
	return m.area(zone{wheel: m.wheelSteps}, m.surface(lines, w, h))
}

// failureRows is how many rows the failures take above the log.
func (m *model) failureRows() int {
	if len(m.found) == 0 || m.search.query != "" {
		return 0
	}
	return min(3, len(m.found)) + 2
}

func (m *model) logPanel(w, h int) []string {
	c := m.pal
	inner := w - 4
	j, ok := m.selectedJob()
	note := ""
	if ok {
		note = j.Name
		if m.step >= 0 && m.step < len(j.Steps) {
			note += " › " + j.Steps[m.step].Name
		} else {
			for _, s := range j.Steps {
				if s.State == "running" || s.State == "failure" {
					note += " › " + s.Name
					break
				}
			}
		}
		if n := len(m.found); n > 0 {
			word := " failures"
			if n == 1 {
				word = " failure"
			}
			note = j.Name + " · " + itoa(n) + word + strings.TrimPrefix(note, j.Name)
		}
	}
	count := ""
	if len(m.log) > 0 {
		count = ink(c.faint, itoa(len(m.log))+" lines")
		if m.logCut {
			count = ink(c.faint, "the last "+itoa(maxLogBytes>>20)+" MB, "+itoa(len(m.log))+" lines")
		}
	}
	action := func(key, word string) string {
		return m.button("log:"+key, key, ink(c.accent, key)+" "+ink(c.dim, word), c.raised, c.surface)
	}
	// The second row: the search while there is one, else the actions.
	var second string
	if s := m.search; s.editing || s.query != "" {
		found := ink(c.faint, "no match")
		if len(s.matches) > 0 {
			found = ink(c.text, itoa(s.at+1)) + ink(c.dim, " of "+itoa(len(s.matches))+" matches")
		}
		cursor := ""
		if s.editing {
			cursor = "▏"
		}
		field := m.chip(ink(c.accent, "/")+" "+ink(c.text, fit(s.query+cursor, 24)), c.raised, c.surface)
		if !s.editing {
			// A click on the field types a new search.
			field = m.button("log:/", "/", ink(c.accent, "/")+" "+ink(c.text, fit(s.query, 24)), c.raised, c.surface)
		}
		second = field + "  " + found
		if hint := ink(c.faint, "ctrl+n ctrl+p step · esc closes"); ansi.StringWidth(second)+ansi.StringWidth(hint)+2 <= inner {
			second = spread2(second, hint, inner)
		}
	} else {
		acts := action("c", "copy link")
		if b, ok := m.selected(); ok && (b.State == "failure" || b.State == "cancelled") {
			acts += " " + action("R", "run again")
		}
		if len(m.found) > 0 {
			acts += " " + action("e", "next failure")
		}
		acts += " " + action("/", "search") + " " + action("z", "tall log")
		if !m.follow {
			acts += " " + action("end", "follow")
		}
		second = acts
	}
	// The name of the job is a link to it on GitHub, as o is.
	title := m.label("LOG")
	if ok {
		name, rest := j.Name, strings.TrimPrefix(note, j.Name)
		title += "  " + m.anchor("log:job", "o", ink(c.dim, name)) + ink(c.faint, rest)
	}
	lines := []string{"", spread2(title, count, inner), second}
	if len(m.found) > 0 && m.search.query == "" {
		lines = append(lines, on(c.redTint, c.surface, fit(" "+ink(c.red, bold("✗ "+itoa(len(m.found))+" in "+m.failedStepName(j))), inner)))
		for i, f := range m.found {
			if i == 3 {
				break
			}
			row := ink(c.text, f.title)
			if f.location != "" {
				row = ink(c.red, f.location) + "  " + row
			}
			if f.detail != "" {
				row += ink(c.dim, " · "+f.detail)
			}
			// A click on a failure shows it in the log, as e does.
			hover, tint := "failure:"+itoa(i), c.redTint
			if m.hover == hover {
				tint = c.redHot
			}
			lines = append(lines, m.zoned(zone{hover: hover, click: func(mouseMsg) Cmd { m.showFailure(i); return nil }}, on(tint, c.surface, fit(" "+row, inner))))
		}
		lines = append(lines, "")
	}
	logArea := zone{wheel: m.wheelLog}
	if len(m.log) == 0 {
		lines = append(lines, m.emptyLog(j, ok, inner, h-len(lines))...)
		return m.area(logArea, m.surface(lines, w, h))
	}
	rows := max(1, h-len(lines))
	top := min(max(0, m.top), max(0, len(m.log)-rows))
	if m.follow {
		top = max(0, len(m.log)-rows)
	}
	gutter := 0
	if m.numbers {
		gutter = len(itoa(len(m.log))) + 1
	}
	textW := inner - 3 - gutter
	matchSet := map[int]bool{}
	for _, i := range m.search.matches {
		matchSet[i] = true
	}
	current := -1
	if len(m.search.matches) > 0 {
		current = m.search.matches[m.search.at]
	}
	// The scrollbar: a thumb as long as the share of the log in view.
	thumb, thumbAt := rows, 0
	if len(m.log) > rows {
		thumb = max(1, rows*rows/len(m.log))
		thumbAt = (rows - thumb) * top / max(1, len(m.log)-rows)
	}
	for k := 0; k < rows; k++ {
		i := top + k
		bar := ink(c.ghostLine, "│")
		if len(m.log) > rows && k >= thumbAt && k < thumbAt+thumb {
			bar = ink(c.faint, "┃")
		}
		if i >= len(m.log) {
			lines = append(lines, strings.Repeat(" ", inner-1)+bar)
			continue
		}
		l := m.log[i]
		in := false
		for _, f := range m.found {
			if i >= f.first && i <= f.last {
				in = true
				break
			}
		}
		num := ""
		if gutter > 0 {
			num = ink(c.faint, lpad(itoa(i+1), gutter-1)) + " "
		}
		text := m.logText(l, matchSet[i])
		var row string
		switch {
		case i == current:
			row = on(c.accentTint, c.surface, ink(c.accent, "▌")+num+fit(text, textW))
		case matchSet[i]:
			row = on(c.raised, c.surface, " "+num+fit(text, textW))
		case in:
			row = on(c.redTint, c.surface, ink(c.red, "▌")+num+fit(text, textW))
		default:
			row = " " + num + fit(text, textW)
		}
		lines = append(lines, row+" "+bar)
	}
	return m.area(logArea, m.surface(lines, w, h))
}

// logText is a line of the log as it shows: its own colours, else the
// colour of its kind. A match shows plain, with the query in the accent.
func (m *model) logText(l LogLine, match bool) string {
	c := m.pal
	if match && m.search.query != "" {
		var sb strings.Builder
		last := 0
		for _, r := range foldMatches(l.Text, m.search.query) {
			sb.WriteString(l.Text[last:r[0]])
			sb.WriteString(ink(c.accent, bold(l.Text[r[0]:r[1]])))
			last = r[1]
		}
		sb.WriteString(l.Text[last:])
		return sb.String()
	}
	switch l.Kind {
	case groupLine:
		return ink(c.accent, bold(l.Text))
	case errorLine:
		return ink(c.red, l.Text)
	case warningLine:
		return ink(c.yellow, l.Text)
	case commandLine:
		return ink(c.dim, l.Text)
	}
	if l.Styled != "" {
		// The line's own colours. A reset gives the surface back.
		back := "\x1b[0m"
		if c.surface != "" {
			back = "\x1b[0;" + c.surface + "m"
		}
		s := strings.ReplaceAll(l.Styled, "\x1b[0m", back)
		s = strings.ReplaceAll(s, "\x1b[m", back)
		return s + back
	}
	return l.Text
}

// emptyLog is what the log shows before it has lines: why, and what to do.
func (m *model) emptyLog(j Job, ok bool, w, h int) []string {
	c := m.pal
	center := func(s string) string {
		pad := max(0, (w-ansi.StringWidth(s))/2)
		return strings.Repeat(" ", pad) + s
	}
	var out []string
	for range max(0, h/3) {
		out = append(out, "")
	}
	switch {
	case ok && active(j.State) && len(j.Steps) > 0:
		out = append(out, center(m.mark("running")+" "+ink(c.accent, bold("This job is still in progress."))), "",
			center(ink(c.dim, "GitHub publishes its log once the runner sends the first part.")),
			center(ink(c.dim, "Press ")+m.button("empty:o", "o", ink(c.accent, "o"), c.raised, c.surface)+ink(c.dim, " to open the job on GitHub.")))
	case m.logNote != "":
		out = append(out, center(ink(c.dim, m.logNote)))
	}
	return out
}

func (m *model) failedStepName(j Job) string {
	for _, s := range j.Steps {
		if s.State == "failure" {
			return s.Name
		}
	}
	return j.Name
}

// overlay draws the menu of repositories in a box at the middle of base,
// whose text dims to the faintest ink.
func (m *model) overlay(base []string) []string {
	c := m.pal
	bw := min(100, m.width-8)
	rows := 8
	if m.picker != nil {
		rows = len(m.picker.shown) + 7
	}
	bh := min(max(9, rows), m.height-6)
	x, y := (m.width-bw)/2, (m.height-bh)/2
	// The menu is one area: a click outside it closes it.
	box := m.area(zone{area: "picker"}, m.pickerView(bw, bh))
	out := make([]string, len(base))
	for i, line := range base {
		plain := ansi.Strip(line)
		if i < y || i >= y+bh {
			out[i] = ink(c.line, plain)
			continue
		}
		out[i] = ink(c.line, ansi.Truncate(plain, x, "")) + box[i-y] + ink(c.line, ansi.TruncateLeft(plain, x+bw, ""))
	}
	return out
}

func (m *model) pickerView(w, h int) []string {
	c := m.pal
	p := m.picker
	inner := w - 4
	lines := []string{"", spread2(m.label("REPOSITORIES")+ink(c.faint, "  "+itoa(len(p.repos))+" you can see"), ink(c.faint, "type to narrow, enter to open, esc closes"), inner), "",
		m.chip(ink(c.accent, ">")+" "+fit(p.input+"▏", inner-6), c.raised, c.surface), ""}
	switch {
	case p.loading:
		lines = append(lines, ink(c.dim, "Loading the repositories."))
	case p.err != nil && len(p.repos) == 0:
		lines = append(lines, ink(c.red, "Cannot read the repositories: "+p.err.Error()))
	case len(p.shown) == 0 && strings.Count(p.input, "/") == 1:
		lines = append(lines, ink(c.dim, "Press enter to open "+strings.TrimSpace(p.input)+"."))
	case len(p.shown) == 0:
		lines = append(lines, ink(c.dim, "No repository matches. Type owner/name to open another one."))
	}
	rows := h - len(lines)
	start := max(0, p.at-rows+1)
	for i := start; i < len(p.shown) && i < start+rows; i++ {
		r := p.shown[i]
		tag := ""
		if !r.Pushed.IsZero() {
			tag = ink(c.faint, ago(m.now().Sub(r.Pushed)))
		}
		switch {
		case r.Name == m.repo:
			tag = ink(c.accent, "watching")
		case r.Archived:
			tag = ink(c.faint, "archived")
		}
		line := spread2(fit(ink(c.text, r.Name), min(36, inner/3))+" "+ink(c.dim, r.Description), tag, inner-1)
		// A click on a row opens the repository, as enter does.
		hover := "repo:" + r.Name
		line = m.zoned(zone{hover: hover, click: func(mouseMsg) Cmd { p.at = i; _, cmd := m.pickerKey(keyMsg{name: "enter"}); return cmd }}, line)
		lines = append(lines, m.lit(line, i == p.at, hover))
	}
	return m.surface(lines, w, h)
}
