package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Every colour is one of the 16 colours of the terminal or its own text
// colour. A tuios pane takes them from the tuios theme, so the plugin
// follows the theme, and Omarchy's through it.
var (
	dim    = style("90")
	bold   = style("1")
	red    = style("31")
	green  = style("32")
	yellow = style("33")
	accent = style("34")
	other  = style("35")
	picked = style("7")
)

// style is the parameters of an SGR sequence.
type style string

func (s style) Render(text string) string { return "\x1b[" + string(s) + "m" + text + "\x1b[0m" }

// Bold is the style, bold too.
func (s style) Bold() style { return s + ";1" }

func mark(s string) string {
	switch s {
	case "running":
		return yellow.Render("●")
	case "queued":
		return dim.Render("○")
	case "pending":
		return dim.Render("◌")
	case "success":
		return green.Render("✓")
	case "failure":
		return red.Render("✗")
	case "cancelled":
		return dim.Render("–")
	case "skipped":
		return dim.Render("⊘")
	}
	return dim.Render("·")
}

func stateStyle(s string) style {
	switch s {
	case "running", "queued":
		return yellow
	case "success":
		return green
	case "failure":
		return red
	}
	return dim
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

// frame draws a panel: a thin line with the title in the top edge.
func frame(title, note string, w, h int, body []string, hot bool) []string {
	if w < 4 || h < 2 {
		return nil
	}
	edge := dim
	if hot {
		edge = accent
	}
	head := "─ " + bold.Render(title) + " "
	if note != "" {
		head += dim.Render(note) + " "
	}
	head = ansi.Truncate(head, w-2, "")
	out := []string{edge.Render("┌") + head + edge.Render(strings.Repeat("─", max(0, w-2-ansi.StringWidth(head)))+"┐")}
	for i := range h - 2 {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, edge.Render("│")+fit(line, w-2)+edge.Render("│"))
	}
	return append(out, edge.Render("└"+strings.Repeat("─", w-2)+"┘"))
}

// beside joins two columns of lines.
func beside(a, b []string) []string {
	n := max(len(a), len(b))
	out := make([]string, n)
	for i := range n {
		if i < len(a) {
			out[i] = a[i]
		}
		if i < len(b) {
			out[i] += b[i]
		}
	}
	return out
}

type layout struct {
	listW, rightW, body int
	buildH, jobsH, lowH int
	stepsW, logW        int
}

func (m *model) layout() layout {
	l := layout{body: max(0, m.height-2)}
	if m.width >= 90 {
		l.listW = min(44, max(28, m.width*3/10))
	}
	l.rightW = m.width - l.listW
	if m.tall {
		l.lowH, l.logW = l.body, l.rightW
		return l
	}
	l.buildH = 5
	rows := 1
	if b, ok := m.selected(); ok {
		rows = len(m.timeline(b, m.fc(b)))
		if slices.ContainsFunc(m.jobsOfBuild(b), func(r jobRow) bool { return r.job.State == "skipped" }) {
			rows++
		}
	}
	// The jobs take the rows they need, and leave the steps and the log at
	// least six.
	l.jobsH = min(rows+2, max(4, l.body-l.buildH-6))
	l.lowH = max(0, l.body-l.buildH-l.jobsH)
	l.stepsW = min(34, max(22, l.rightW/4))
	l.logW = l.rightW - l.stepsW
	return l
}

// logRows is how many log lines are in view.
func (m *model) logRows() int {
	n := m.layout().lowH - 2 - len(m.failureLines(3))
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
	if m.width < 20 || m.height < 6 {
		return "The pane is too small."
	}
	l := m.layout()
	var body []string
	switch {
	case m.picker != nil:
		body = m.pickerView(m.width, l.body)
	case m.help:
		body = frame("keys", "esc to close", m.width, l.body, helpLines, true)
	default:
		var right []string
		if !m.tall {
			b, ok := m.selected()
			if ok {
				fc, like := m.estimate(b)
				right = append(right, frame("build", strings.Join(b.Workflows, ", "), l.rightW, l.buildH, m.buildLines(b, fc, like, l.rightW-2), false)...)
				right = append(right, m.jobsPanel(b, fc, l.rightW, l.jobsH)...)
				right = append(right, beside(m.stepsPanel(l.stepsW, l.lowH), m.logPanel(l.logW, l.lowH))...)
			} else {
				msg := "No builds yet."
				switch {
				case m.err != "":
					msg = ""
				case m.updated.IsZero():
					msg = "Loading the builds."
				case len(m.all) > 0:
					msg = "No build matches the filters."
				}
				right = frame("build", "", l.rightW, l.body, []string{"", "  " + msg}, false)
			}
		} else {
			right = m.logPanel(l.logW, l.lowH)
		}
		if l.listW > 0 {
			body = beside(m.listPanel(l.listW, l.body), right)
		} else {
			body = right
		}
	}
	for len(body) < l.body {
		body = append(body, "")
	}
	return strings.Join(append(append([]string{m.header()}, body[:l.body]...), m.footer()), "\n")
}

func box(on bool, key, name string) string {
	b := "[ ]"
	if on {
		b = accent.Render("[x]")
	}
	return b + " " + accent.Render(key) + " " + name
}

func (m *model) header() string {
	main := m.defaultBranch
	if main == "" {
		main = "main"
	}
	mainOn := m.f.branch != "" && m.f.branch == m.defaultBranch
	branch := "branch"
	if m.f.branch != "" && !mainOn {
		branch = m.f.branch
	}
	left := " " + bold.Render(m.repo) + "   " + box(mainOn, "M", main) + "  " + box(m.f.branch != "" && !mainOn, "F", branch) +
		"  " + box(m.f.activeOnly, "A", "active") + "  " + box(m.f.showSkipped, "S", "skipped")
	right := ""
	if m.err != "" {
		right = red.Render(m.err)
	} else {
		n := 0
		for _, b := range m.all {
			if active(b.State) {
				n++
			}
		}
		if n > 0 {
			right = yellow.Render(itoa(n)+" running") + dim.Render(" · ")
		}
		if !m.updated.IsZero() {
			right += dim.Render("Updated " + m.updated.Format("15:04:05"))
		}
	}
	return spread2(left, right+" ", m.width)
}

const hints = "j k build  h l job  n p step  e failure  c copy link  z tall log  G repository  r refresh  ? keys  q quit"

func (m *model) footer() string {
	if m.toast != "" {
		return fit(" "+accent.Render(m.toast), m.width)
	}
	return fit(" "+dim.Render(hints), m.width)
}

var helpLines = func() []string {
	rows := [][2]string{
		{"j  k", "Next or previous build"},
		{"h  l  tab", "Previous or next job"},
		{"n  p", "Next or previous step: show where it ends in the log"},
		{"e  E", "Next or previous failure"},
		{"c", "Copy the link of the job"},
		{"M", "Show only the builds of the default branch"},
		{"F", "Show only the branch of this build"},
		{"A", "Show only the builds that run or wait"},
		{"S", "Show the builds in which every run was skipped"},
		{"z", "Give the whole height to the log"},
		{"pgup  pgdown", "Scroll the log"},
		{"home  end", "Go to the top of the log, or follow its end"},
		{"r  space", "Read the builds now"},
		{"G", "Watch another repository"},
		{"?  esc", "Close this help"},
		{"q", "Quit"},
	}
	out := []string{""}
	for _, r := range rows {
		out = append(out, "  "+accent.Render(fit(r[0], 14))+r[1])
	}
	return out
}()

// listPanel is the builds, newest first. The builds of the default branch
// are on the left edge. The builds of other branches are set in, with their
// branch in another colour.
func (m *model) listPanel(w, h int) []string {
	inner := w - 2
	var lines []string
	sel := 0
	for i, b := range m.list {
		if b.Key == m.key {
			sel = i
		}
		onMain := b.Branch == m.defaultBranch && b.IsBuild
		indent := ""
		branch := other.Render(b.Branch)
		if onMain {
			branch = accent.Render(b.Branch)
		} else {
			indent = "  "
		}
		edge := " "
		if b.Key == m.key {
			edge = accent.Render("▌")
		}
		title := b.Title
		if !onMain {
			title = dim.Render(title)
		}
		sha := b.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		lines = append(lines, edge+indent+mark(b.State)+" "+title)
		lines = append(lines, edge+indent+"  "+spread2(branch+dim.Render(" · "+sha), m.rowTime(b), inner-3-len(indent)))
	}
	// Keep the selected build in view.
	rows := h - 2
	start := 0
	if 2*sel+2 > rows {
		start = 2*sel + 2 - rows
	}
	if start > 0 && start < len(lines) {
		lines = lines[start:]
	}
	return frame("builds", itoa(len(m.list)), w, h, lines, false)
}

// rowTime is the time in the list: how long a finished build took, and
// for a running one about how long it has left, from whole builds alone.
func (m *model) rowTime(b Build) string {
	if !active(b.State) {
		if b.Seconds > 0 {
			return dim.Render(dur(b.Seconds))
		}
		return ""
	}
	if fc := m.fc(b); fc.known && !fc.late {
		return yellow.Render("~" + dur(rounded(max(5, fc.total-fc.elapsed))) + " left")
	}
	return yellow.Render(dur(m.now().Sub(b.Started).Seconds()))
}

// headline is the answer the window is opened for, and its colour.
func (m *model) headline(b Build, fc forecast) (string, style) {
	switch b.State {
	case "queued":
		return "Waits for a runner", yellow
	case "running":
		elapsed := fc.elapsed
		switch {
		case !fc.known:
			return "Running for " + dur(elapsed), yellow
		case fc.late:
			return "Taking longer than usual, " + dur(rounded(fc.over)) + " over", yellow
		}
		text := "About " + dur(rounded(max(5, fc.total-elapsed))) + " left"
		if b.Failing {
			return "A job failed. " + text, red
		}
		return text, yellow
	case "success":
		return "Passed in " + dur(b.Seconds), green
	case "failure":
		return "Failed after " + dur(b.Seconds), red
	case "cancelled":
		return "Cancelled after " + dur(b.Seconds), dim
	case "skipped":
		return "Skipped", dim
	}
	return "Ended after " + dur(b.Seconds), dim
}

func (m *model) buildLines(b Build, fc forecast, like []Build, w int) []string {
	text, style := m.headline(b, fc)
	sha := b.SHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	meta := dim.Render(strings.Join(slices.DeleteFunc([]string{b.Branch, sha, b.Event, b.Actor}, func(s string) bool { return s == "" }), " · "))
	note := ""
	switch {
	case active(b.State) && fc.known:
		note = "usually " + dur(rounded(fc.low))
		if hi := dur(rounded(fc.high)); hi != dur(rounded(fc.low)) {
			note += " to " + hi
		}
		note += ", from " + itoa(fc.samples) + " earlier builds"
	case active(b.State):
		note = "Fewer than 3 earlier builds passed, so no estimate yet"
	case fc.known && b.State == "success" && b.Seconds > 0:
		d := b.Seconds - fc.total
		switch {
		case math.Abs(d) < 5 || math.Abs(d) < .15*fc.total:
			note = "As fast as usual"
		case d > 0:
			note = dur(rounded(d)) + " slower than usual"
		default:
			note = dur(rounded(-d)) + " faster than usual"
		}
	}
	spark := sparkline(like, 16)
	barW := max(10, w-ansi.StringWidth(spark)-2)
	return []string{
		spread2(" "+bold.Render(b.Title), style.Bold().Render(text)+" ", w),
		spread2(" "+meta, dim.Render(note)+" ", w),
		" " + m.bar(b, fc, barW-1) + "  " + spark,
	}
}

// bar is the time gone, filled, on a scale to the slow end of the earlier
// builds. A tick marks where a build like this usually ends, and a shaded
// band how far apart the earlier ones ended.
func (m *model) bar(b Build, fc forecast, w int) string {
	elapsed := fc.elapsed
	if !active(b.State) {
		elapsed = b.Seconds
	}
	scale := max(elapsed, fc.high, fc.total) * 1.05
	if scale <= 0 || !fc.known {
		if active(b.State) {
			return dim.Render(strings.Repeat("░", w))
		}
		return stateStyle(b.State).Render(strings.Repeat("█", w))
	}
	fill := stateStyle(b.State)
	if b.Failing {
		fill = red
	}
	tick := int(fc.total / scale * float64(w))
	var sb strings.Builder
	for i := range w {
		t := (float64(i) + .5) / float64(w) * scale
		switch {
		case i == tick:
			sb.WriteString(bold.Render("┃"))
		case t <= elapsed:
			sb.WriteString(fill.Render("█"))
		case t >= fc.low && t <= fc.high:
			sb.WriteString(dim.Render("▒"))
		default:
			sb.WriteString(dim.Render("░"))
		}
	}
	return sb.String()
}

// sparkline is the earlier builds, oldest first, one character each, as
// tall as the build was long and coloured by how it ended.
func sparkline(like []Build, most int) string {
	if len(like) == 0 {
		return ""
	}
	like = like[:min(most, len(like))]
	s := spreadOf(like)
	top := 0.0
	for _, b := range like {
		top = max(top, b.Seconds)
	}
	if s.typical > 0 {
		top = min(top, 2*s.typical)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var sb strings.Builder
	for _, b := range slices.Backward(like) {
		n := 0
		if top > 0 {
			n = min(len(levels)-1, int(b.Seconds/top*float64(len(levels)-1)+.5))
		}
		sb.WriteString(stateStyle(b.State).Render(string(levels[n])))
	}
	return sb.String()
}

// timeline is the rows of the jobs panel: the jobs, then the jobs the
// earlier builds say come. The skipped jobs share one row.
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
				_, name, _ := strings.Cut(f.key, "/")
				rows = append(rows, jobRow{key: f.key, job: Job{Name: name, State: "pending"}, fc: f})
			}
		}
	}
	return rows
}

func (m *model) jobsPanel(b Build, fc forecast, w, h int) []string {
	rows := m.timeline(b, fc)
	var skipped []string
	for _, r := range m.jobsOfBuild(b) {
		if r.job.State == "skipped" {
			skipped = append(skipped, r.job.Name)
		}
	}
	inner := w - 2
	nameW := min(24, max(10, inner/5))
	rightW := 18
	tw := max(8, inner-nameW-rightW-5)
	elapsed := fc.elapsed
	if !active(b.State) {
		elapsed = b.Seconds
	}
	end := elapsed
	off := func(t Job) (float64, float64) {
		s, e := -1.0, -1.0
		if !t.Started.IsZero() {
			s = max(0, t.Started.Sub(b.Started).Seconds())
		}
		if !t.Completed.IsZero() {
			e = max(0, t.Completed.Sub(b.Started).Seconds())
		}
		return s, e
	}
	for _, r := range rows {
		_, e := off(r.job)
		end = max(end, e, r.fc.high, r.fc.end)
	}
	scale := max(1, end*1.02)
	cell := func(t float64) int { return min(tw-1, int(t/scale*float64(tw))) }
	running, coming := 0, 0
	var lines []string
	for _, r := range rows {
		s, e := off(r.job)
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
		}
		cells := make([]string, tw)
		for i := range cells {
			cells[i] = " "
		}
		paint := func(from, to float64, glyph string, style style) {
			if from < 0 || to < from {
				return
			}
			for i := cell(from); i <= cell(to); i++ {
				cells[i] = style.Render(glyph)
			}
		}
		switch st {
		case "pending", "queued":
			paint(s, e, "░", dim)
			paint(e, r.fc.high, "╌", dim)
		case "running":
			paint(s, e, "█", stateStyle(st))
			paint(e, r.fc.end, "▒", dim)
			paint(r.fc.end, r.fc.high, "╌", dim)
		default:
			paint(s, max(s, e), "█", stateStyle(st))
		}
		if active(b.State) {
			cells[cell(elapsed)] = bold.Render("│")
		}
		right := ""
		switch {
		case st == "running" && r.fc.typical > 0:
			right = dur(elapsed-s) + " / ~" + dur(r.fc.typical)
		case st == "running":
			right = dur(elapsed - s)
		case st == "pending" || st == "queued":
			if r.fc.typical > 0 {
				right = "~" + dur(r.fc.typical)
			} else {
				right = "waits"
			}
		case s >= 0 && e >= s:
			right = dur(e - s)
		}
		edge := " "
		name := r.job.Name
		if r.job.ID != 0 && r.job.ID == m.jobID {
			edge = accent.Render("▌")
			name = bold.Render(name)
		}
		lines = append(lines, edge+mark(st)+" "+fit(name, nameW)+" "+strings.Join(cells, "")+" "+fit(dim.Render(right), rightW))
	}
	if len(skipped) > 0 {
		lines = append(lines, " "+mark("skipped")+" "+dim.Render(itoa(len(skipped))+" skipped: "+strings.Join(skipped, ", ")))
	}
	if len(lines) == 0 {
		lines = append(lines, "  "+dim.Render("No jobs yet."))
	}
	var note []string
	if running > 0 {
		note = append(note, itoa(running)+" running")
	}
	if coming > 0 {
		note = append(note, itoa(coming)+" to come")
	}
	// Keep the selected job in view.
	if over := len(lines) - (h - 2); over > 0 {
		sel := slices.IndexFunc(rows, func(r jobRow) bool { return r.job.ID == m.jobID })
		start := min(over, max(0, sel-(h-3)))
		lines = lines[start:]
	}
	return frame("jobs", strings.Join(note, ", "), w, h, lines, false)
}

func (m *model) stepsPanel(w, h int) []string {
	j, ok := m.selectedJob()
	if !ok {
		return frame("steps", "", w, h, nil, false)
	}
	inner := w - 2
	var lines []string
	sel := -1
	for i, s := range j.Steps {
		right := ""
		if !s.Started.IsZero() && !s.Completed.IsZero() {
			right = dur(s.Completed.Sub(s.Started).Seconds())
		}
		name := s.Name
		if s.State == "failure" {
			name = red.Render(name)
			if sel < 0 {
				sel = i
			}
		}
		line := " " + mark(s.State) + " " + spread2(name, dim.Render(right), inner-4)
		if i == m.step {
			line = picked.Render(ansi.Strip(line))
			sel = i
		}
		lines = append(lines, line)
	}
	if over := len(lines) - (h - 2); over > 0 && sel >= 0 {
		lines = lines[min(over, max(0, sel-(h-3)/2)):]
	}
	return frame("steps", j.Name, w, h, lines, false)
}

// failureLines name each failure with its place, for the top of the log.
func (m *model) failureLines(most int) []string {
	var out []string
	for i, f := range m.found {
		if i == most {
			out[most-1] = dim.Render("  and " + itoa(len(m.found)-most+1) + " more. Press e for the next.")
			break
		}
		where := f.location
		if where != "" {
			where = "  " + dim.Render(where)
		}
		out = append(out, " "+red.Render("✗ "+f.title)+where)
	}
	return out
}

func (m *model) logPanel(w, h int) []string {
	j, ok := m.selectedJob()
	title, note := "log", ""
	if ok {
		note = j.Name
		if m.step >= 0 && m.step < len(j.Steps) {
			note += " · step " + itoa(m.step+1) + " of " + itoa(len(j.Steps))
		}
		if n := len(m.found); n > 0 {
			word := " failures"
			if n == 1 {
				word = " failure"
			}
			note += " · " + itoa(n) + word
		}
	}
	lines := m.failureLines(3)
	if len(m.log) == 0 {
		lines = append(lines, "", "  "+dim.Render(m.logNote))
		return frame(title, note, w, h, lines, !m.follow)
	}
	rows := max(1, h-2-len(lines))
	top := min(max(0, m.top), max(0, len(m.log)-rows))
	if m.follow {
		top = max(0, len(m.log)-rows)
	}
	inner := w - 2
	for i := top; i < len(m.log) && i < top+rows; i++ {
		l := m.log[i]
		edge := " "
		for _, f := range m.found {
			if i >= f.first && i <= f.last {
				edge = red.Render("▌")
				break
			}
		}
		text := l.Text
		switch l.Kind {
		case groupLine:
			text = accent.Render(ansi.Truncate(text, inner-2, "…"))
		case errorLine:
			text = red.Render(ansi.Truncate(text, inner-2, "…"))
		case warningLine:
			text = yellow.Render(ansi.Truncate(text, inner-2, "…"))
		case commandLine:
			text = dim.Render(ansi.Truncate(text, inner-2, "…"))
		}
		lines = append(lines, edge+" "+text)
	}
	if !m.follow {
		note += " · end to follow"
	}
	return frame(title, note, w, h, lines, false)
}

func (m *model) pickerView(w, h int) []string {
	p := m.picker
	lines := []string{" " + accent.Render(">") + " " + p.input + "▏", ""}
	switch {
	case p.loading:
		lines = append(lines, "  "+dim.Render("Loading the repositories."))
	case p.err != nil && len(p.repos) == 0:
		lines = append(lines, "  "+red.Render("Cannot read the repositories: "+p.err.Error()))
	case len(p.shown) == 0 && strings.Count(p.input, "/") == 1:
		lines = append(lines, "  "+dim.Render("Press enter to open "+strings.TrimSpace(p.input)+"."))
	case len(p.shown) == 0:
		lines = append(lines, "  "+dim.Render("No repository matches. Type owner/name to open another one."))
	}
	rows := h - 2 - len(lines)
	start := max(0, p.at-rows+1)
	for i := start; i < len(p.shown) && i < start+rows; i++ {
		r := p.shown[i]
		text := r.Name
		if r.Name == m.repo {
			text += " (now)"
		}
		if r.Archived {
			text += " (archived)"
		}
		line := "  " + fit(text, 40) + " " + dim.Render(r.Description)
		if i == p.at {
			line = picked.Render(fit(ansi.Strip(line), w-2))
		}
		lines = append(lines, line)
	}
	note := "type to narrow, enter to open"
	if m.repo != "" {
		note += ", esc to go back"
	}
	return frame("repositories", note, w, h, lines, true)
}
