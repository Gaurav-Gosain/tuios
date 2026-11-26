package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"github.com/charmbracelet/x/ansi"
)

// # Copy jobs
//
// One c or m of the Files view is one job, however many files it sends. The
// engine runs a copy per file, and the job holds their ids: the Copies list
// draws one row for the job with its totals and, under it, a row per file;
// and one notice says how the job ended. A copy started anywhere else is a
// job of one.

// filesJob is the copies one action started.
type filesJob struct {
	id int
	// src and dst are where the files come from and go: a machine and a
	// folder.
	srcHost, srcDir string
	dstHost, dstDir string
	move            bool
	// names is the files asked for, and ids the copies the engine took, by
	// name. A name with no id did not start.
	names []string
	ids   map[string]string
	// failed is the names whose copy could not start, and noticed says the
	// job's end was told.
	failed   map[string]string
	noticed  bool
	startsAt time.Time
}

// filesJobCallMsg is the answer to one transfer-start of a job.
type filesJobCallMsg struct {
	job  int
	call transferCallMsg
}

// filesNewJob keeps a job for names about to be sent.
func (m *OS) filesNewJob(srcHost, srcDir string, names []string, dstHost, dstDir string, move bool) *filesJob {
	e := &m.explorer
	e.jobSeq++
	j := &filesJob{id: e.jobSeq, srcHost: srcHost, srcDir: srcDir, dstHost: dstHost, dstDir: dstDir, move: move,
		names: names, ids: map[string]string{}, failed: map[string]string{}, startsAt: time.Now()}
	e.jobs = append(e.jobs, j)
	if e.jobOpen == nil {
		e.jobOpen = map[int]bool{}
	}
	e.jobOpen[j.id] = true
	return j
}

// filesJobByID is the job with id, or nil.
func (m *OS) filesJobByID(id int) *filesJob {
	for _, j := range m.explorer.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

// filesJobOfCopy is the job a copy belongs to, or nil.
func (m *OS) filesJobOfCopy(transferID string) *filesJob {
	for _, j := range m.explorer.jobs {
		for _, id := range j.ids {
			if id == transferID {
				return j
			}
		}
	}
	return nil
}

// HandleFilesJobCall keeps the copy's id in its job and folds the copy in,
// with no notice of its own: the job tells its end once.
func (m *OS) HandleFilesJobCall(msg filesJobCallMsg) tea.Cmd {
	j := m.filesJobByID(msg.job)
	if j != nil {
		switch {
		case msg.call.Err != nil:
			j.failed[msg.call.Name] = transferCallFailed(msg.call)
		case msg.call.Row != nil:
			j.ids[msg.call.Name] = msg.call.Row.ID
		}
	}
	msg.call.Quiet = true
	cmd := m.applyTransferCall(msg.call)
	return tea.Batch(cmd, m.filesJobMaybeEnded(j))
}

// filesJobRows is the engine's rows of a job's copies, in the order of its
// names. ok is false while a copy of it has not answered yet.
func (m *OS) filesJobRows(j *filesJob) (rows []session.TransferRow, ok bool) {
	ok = true
	for _, name := range j.names {
		id, started := j.ids[name]
		if !started {
			if _, failed := j.failed[name]; !failed {
				ok = false
			}
			continue
		}
		if i := m.transferRowIndex(id); i >= 0 {
			rows = append(rows, m.transfers.rows[i])
		}
	}
	return rows, ok
}

// filesJobMaybeEnded tells the end of job j once: all its copies ended.
func (m *OS) filesJobMaybeEnded(j *filesJob) tea.Cmd {
	if j == nil || j.noticed {
		return nil
	}
	rows, all := m.filesJobRows(j)
	if !all {
		return nil
	}
	for _, r := range rows {
		if !transferEnded(r.State) {
			return nil
		}
	}
	j.noticed = true
	done, checked := 0, true
	var why string
	for _, r := range rows {
		switch r.State {
		case "done":
			done++
			checked = checked && r.Verified
		case "failed":
			if why == "" {
				why = transferFailedText(r)
			}
		}
	}
	for _, text := range j.failed {
		if why == "" {
			why = text
		}
	}
	verb := "Copied"
	if j.move {
		verb = "Moved"
	}
	place := transferPlace(j.dstHost)
	d := m.Settings.NotificationDuration
	switch {
	case done == len(j.names):
		text := verb + " " + countOf(done, "file") + " to " + place + "."
		if checked {
			text += " Checked."
		}
		m.ShowNotification(text, "success", d)
	case done > 0:
		m.ShowNotification(verb+" "+strconv.Itoa(done)+" of "+countOf(len(j.names), "file")+" to "+place+". "+why, "warning", d*2)
	case why != "":
		m.ShowNotification(why, "error", d*2)
	}
	return nil
}

// filesCopyJob is one row group of the Copies list: a job of the view, or a
// copy started anywhere else.
type filesCopyJob struct {
	job  *filesJob
	rows []session.TransferRow
}

// filesCopyJobs is the Copies list's groups, newest first.
func (m *OS) filesCopyJobs() []filesCopyJob {
	var out []filesCopyJob
	taken := map[string]bool{}
	for _, job := range slices.Backward(m.explorer.jobs) {
		rows, _ := m.filesJobRows(job)
		if len(rows) == 0 {
			continue
		}
		for _, r := range rows {
			taken[r.ID] = true
		}
		out = append(out, filesCopyJob{job: job, rows: rows})
	}
	for _, r := range m.transfers.rows {
		if !taken[r.ID] {
			out = append(out, filesCopyJob{rows: []session.TransferRow{r}})
		}
	}
	return out
}

// state is the group's state: asks, running, paused, waiting, queued, done
// or failed, in that order of weight.
func (g filesCopyJob) state() string {
	has := map[string]bool{}
	for _, r := range g.rows {
		has[r.State] = true
	}
	for _, s := range []string{"conflict", "running", "verifying", "paused", "waiting", "queued", "failed"} {
		if has[s] {
			switch s {
			case "conflict":
				return "asks"
			case "verifying":
				return "running"
			}
			return s
		}
	}
	return "done"
}

// totals is the bytes done and in all, the rate, and the time left.
func (g filesCopyJob) totals() (done, size int64, rate float64, left time.Duration) {
	for _, r := range g.rows {
		done += r.Done
		size += r.Size
		if !transferEnded(r.State) {
			rate += r.Rate
		}
	}
	if rate > 0 && size > done {
		left = time.Duration(float64(size-done) / rate * float64(time.Second))
	}
	return done, size, rate, left
}

// filesCopiesSummary is the top bar's word for the copies: how many are on
// their way and how fast, or "" while none is.
func (m *OS) filesCopiesSummary() string {
	n, rate := 0, 0.0
	for _, r := range m.transfers.rows {
		if !transferEnded(r.State) {
			n++
			rate += r.Rate
		}
	}
	if n == 0 {
		return ""
	}
	text := "1 copy"
	if n != 1 {
		text = strconv.Itoa(n) + " copies"
	}
	if rate > 0 {
		text += " · " + units.Bytes(int64(rate)) + "/s"
	}
	return text
}

// filesBar draws a bar of cells cells for pct percent from x on row y, and
// answers the column after it: a filled part, a half cell for the part of a
// cell past it, and a dotted rest. It reads in shapes, so it holds at 16
// colours, and it is the one bar of the view.
func filesBar(c *ckCanvas, pal overlay.Palette, x, y, cells int, pct float64, ink color.Color) int {
	full, half, dot := "━", "╸", "·"
	if overlay.UseASCII() {
		full, half, dot = "=", "-", "."
	}
	pct = max(min(pct, 100), 0)
	n := int(pct * float64(cells) / 100)
	part := pct*float64(cells)/100 - float64(n)
	fill := strings.Repeat(full, n)
	left := cells - n
	if part >= 0.5 && left > 0 {
		fill += half
		left--
	} else if n == 0 && pct > 0 && left > 0 {
		// Under a cell of progress still shows: a bar that moved looks moved.
		fill += half
		left--
	}
	rests := strings.Repeat(dot, left)
	return c.text(x, y, x+cells, ckSeg{t: fill, fg: ink}, ckSeg{t: rests, fg: pal.FgMute})
}

// filesCopyHit is one click target of the Copies list.
type filesCopyHit struct {
	y, x0, x1 int
	// what is open, pause or cancel, and group the row's place in
	// filesCopyJobs.
	what  string
	group int
}

// filesCopiesRows is how many rows the Copies list takes under the sides:
// none while no copy is to be shown; else a header, a row per group, and
// the files of the open groups, up to a third of the view.
func (m *OS) filesCopiesRows(h int) int {
	if !m.transfersShown() || len(m.transfers.rows) == 0 {
		return 0
	}
	n := 1
	for _, g := range m.filesCopyJobs() {
		n++
		if g.job != nil && m.explorer.jobOpen[g.job.id] && len(g.rows) > 1 {
			n += len(g.rows)
		}
	}
	return min(n, max(h/3, 3))
}

// stateWord is the header's word for a state.
func filesStateWord(s string) string {
	switch s {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "asks":
		return "asks"
	case "waiting":
		return "waiting"
	case "queued":
		return "queued"
	case "failed":
		return "failed"
	}
	return "done"
}

// drawFilesCopies draws the Copies list from row y: a header that counts the
// groups by state, then a row per group with its route, its bar and its
// totals, and under an open group a row per file. top is the canvas's
// screen row, for the click targets.
func (m *OS) drawFilesCopies(c *ckCanvas, pal overlay.Palette, y, w, rows, top int) {
	e := &m.explorer
	e.copyHits = e.copyHits[:0]
	c.fill(0, y, w, rows, ckColor(pal.Panel))
	groups := m.filesCopyJobs()
	counts := map[string]int{}
	for _, g := range groups {
		counts[g.state()]++
	}
	x := c.text(1, y, w-1, ckSeg{t: "Copies", fg: pal.Fg, attrs: ckBold})
	first := true
	for _, s := range []string{"running", "paused", "asks", "waiting", "queued", "failed", "done"} {
		if counts[s] == 0 {
			continue
		}
		sep := "   "
		if !first {
			sep = "  ·  "
		}
		first = false
		ink := pal.FgDim
		if s == "running" {
			ink = pal.AccentBright
		}
		x = c.text(x, y, w-1, ckSeg{t: sep, fg: pal.FgMute}, ckSeg{t: strconv.Itoa(counts[s]) + " " + filesStateWord(s), fg: ink})
	}
	ry := y + 1
	barW := min(max(w/5, 10), 32)
	// Every bar of the list starts in one column, left of the widest
	// tail a running copy has (totals, rate, time left, pause, cancel).
	barX := max(w-1-barW-62, w/3)
	for gi, g := range groups {
		if ry >= y+rows {
			break
		}
		m.drawFilesCopyGroup(c, pal, g, gi, ry, w, barX, barW, top)
		ry++
		if g.job == nil || !e.jobOpen[g.job.id] || len(g.rows) < 2 {
			continue
		}
		for _, r := range g.rows {
			if ry >= y+rows {
				break
			}
			m.drawFilesCopyFile(c, pal, r, ry, w, barX, barW)
			ry++
		}
	}
}

// drawFilesCopyGroup draws one group's row.
func (m *OS) drawFilesCopyGroup(c *ckCanvas, pal overlay.Palette, g filesCopyJob, gi, y, w, barX, barW, top int) {
	e := &m.explorer
	state := g.state()
	glyph, ink := "▸", pal.AccentBright
	switch state {
	case "paused":
		glyph, ink = "‖", pal.Warning
	case "asks":
		glyph, ink = "?", pal.Warning
	case "queued", "waiting":
		glyph, ink = "·", pal.FgDim
	case "done":
		glyph, ink = "✓", pal.Success
	case "failed":
		glyph, ink = "✕", pal.Warn
	}
	if overlay.UseASCII() {
		glyph = map[string]string{"‖": "=", "✓": "+", "✕": "x", "▸": ">", "?": "?", "·": "."}[glyph]
	}
	srcHost, srcDir, dstHost, dstDir := "", "", "", ""
	if g.job != nil {
		srcHost, srcDir, dstHost, dstDir = g.job.srcHost, g.job.srcDir, g.job.dstHost, g.job.dstDir
	} else {
		r := g.rows[0]
		srcHost, srcDir, dstHost, dstDir = r.Src.Host, r.Src.Path, r.Dst.Host, r.Dst.Path
	}
	// The controls on the right: pause or go on, and cancel. They are words,
	// and a click on one runs it.
	var controls []ckSeg
	pauseWord := "pause"
	if state == "paused" || state == "waiting" {
		pauseWord = "go on"
	}
	live := state != "done" && state != "failed"
	if live {
		controls = []ckSeg{{t: pauseWord, fg: pal.AccentBright}, {t: "   "}, {t: "cancel", fg: pal.AccentBright}}
	}
	cw := ckWidth(controls...)
	right := w - 1
	if cw > 0 {
		cx := right - cw
		c.text(cx, y, right, controls...)
		e.copyHits = append(e.copyHits,
			filesCopyHit{y: top + y, x0: cx, x1: cx + ansi.StringWidth(pauseWord), what: "pause", group: gi},
			filesCopyHit{y: top + y, x0: right - len("cancel"), x1: right, what: "cancel", group: gi})
		right = cx - 3
	}
	// The totals.
	done, size, rate, left := g.totals()
	pct := 0.0
	if size > 0 {
		pct = float64(done) * 100 / float64(size)
	}
	var tail []ckSeg
	switch state {
	case "running":
		t := units.Bytes(done) + " of " + units.Bytes(size)
		if rate > 0 {
			t += "  " + units.Bytes(int64(rate)) + "/s"
		}
		if left > 0 {
			t += " · " + units.Duration(left) + " left"
		}
		tail = []ckSeg{{t: strconv.Itoa(int(pct)) + "%", fg: pal.AccentBright, attrs: ckBold}, {t: "  " + t, fg: pal.FgDim}}
	case "paused":
		tail = []ckSeg{{t: strconv.Itoa(int(pct)) + "%", fg: pal.Warning}, {t: "  paused", fg: pal.FgDim}}
	case "asks":
		name := g.rows[0].Name
		for _, r := range g.rows {
			if r.State == "conflict" {
				name = r.Name
			}
		}
		tail = []ckSeg{{t: printableTitle(name) + " is there and is different", fg: pal.Warning}}
	case "queued", "waiting":
		tail = []ckSeg{{t: "queued: starts when a copy ends", fg: pal.FgDim}}
	case "done":
		t := "done"
		if g.job != nil && len(g.rows) > 1 {
			t = countOf(len(g.rows), "file") + " done"
		}
		tail = []ckSeg{{t: t + ", checked", fg: pal.Success}}
	case "failed":
		tail = []ckSeg{{t: "stopped", fg: pal.Warn}}
	}
	tw := ckWidth(tail...)
	c.textR(right, y, tail...)
	right -= tw + 2
	if state == "running" || state == "paused" {
		barInk := pal.Accent
		if state == "paused" {
			barInk = pal.Warning
		}
		bx := min(barX, right-barW)
		filesBar(c, pal, bx, y, barW, pct, barInk)
		right = bx - 2
	}
	// The route on the left: a click on it opens the group's files.
	x := c.text(1, y, right, ckSeg{t: glyph + " ", fg: ink})
	route := []ckSeg{
		{t: transferPlaceShort(srcHost), fg: pal.Fg, attrs: ckBold}, {t: ":" + m.filesShortOn(srcHost, srcDir), fg: pal.FgDim},
		{t: "  " + transferArrow() + "  ", fg: pal.FgMute},
		{t: transferPlaceShort(dstHost), fg: pal.Fg, attrs: ckBold}, {t: ":" + m.filesShortOn(dstHost, dstDir), fg: pal.FgDim},
	}
	end := c.text(x, y, right, route...)
	e.copyHits = append(e.copyHits, filesCopyHit{y: top + y, x0: 0, x1: end, what: "open", group: gi})
}

// drawFilesCopyFile draws one file of an open group.
func (m *OS) drawFilesCopyFile(c *ckCanvas, pal overlay.Palette, r session.TransferRow, y, w, barX, barW int) {
	glyph, ink, nameInk := "·", pal.FgMute, pal.FgDim
	switch r.State {
	case "done":
		glyph, ink, nameInk = "✓", pal.Success, pal.Fg
	case "running", "verifying":
		glyph, ink, nameInk = "▸", pal.AccentBright, pal.Fg
	case "paused":
		glyph, ink, nameInk = "‖", pal.Warning, pal.Fg
	case "failed":
		glyph, ink = "✕", pal.Warn
	}
	if overlay.UseASCII() {
		glyph = map[string]string{"✓": "+", "▸": ">", "·": ".", "✕": "x", "‖": "="}[glyph]
	}
	c.text(4, y, barX-20, ckSeg{t: glyph + " ", fg: ink}, ckSeg{t: printableTitle(r.Name), fg: nameInk})
	size := units.Bytes(r.Size)
	if r.State == "running" || r.State == "paused" {
		size = units.Bytes(r.Done) + " of " + units.Bytes(r.Size)
	}
	c.textR(barX-3, y, ckSeg{t: size, fg: pal.FgMute})
	switch r.State {
	case "done":
		t := "done"
		if r.Verified {
			t = "sha256 matches"
		}
		c.textR(w-1, y, ckSeg{t: t, fg: pal.Success})
	case "running", "verifying":
		pct := float64(transferPercent(r.Done, r.Size))
		filesBar(c, pal, barX, y, barW, pct, pal.Accent)
		if r.Rate > 0 {
			c.textR(w-1, y, ckSeg{t: units.Bytes(int64(r.Rate)) + "/s", fg: pal.FgDim})
		}
	case "paused":
		filesBar(c, pal, barX, y, barW, float64(transferPercent(r.Done, r.Size)), pal.Warning)
		c.textR(w-1, y, ckSeg{t: "paused", fg: pal.FgDim})
	case "failed":
		c.textR(w-1, y, ckSeg{t: "stopped", fg: pal.Warn})
	default:
		c.textR(w-1, y, ckSeg{t: "waits", fg: pal.FgMute})
	}
}

// FilesCopiesClick acts on a click on the Copies list: a route opens or
// closes a group's files, and pause and cancel run on every copy of it.
// ok is false off the list.
func (m *OS) FilesCopiesClick(x, y int) (tea.Cmd, bool) {
	e := &m.explorer
	for _, h := range e.copyHits {
		if y != h.y || x < h.x0 || x >= h.x1 {
			continue
		}
		groups := m.filesCopyJobs()
		if h.group >= len(groups) {
			return nil, true
		}
		g := groups[h.group]
		e.gen++
		switch h.what {
		case "open":
			if g.job != nil {
				e.jobOpen[g.job.id] = !e.jobOpen[g.job.id]
			}
			return nil, true
		case "pause":
			verb := "transfer-pause"
			if s := g.state(); s == "paused" || s == "waiting" {
				verb = "transfer-resume"
			}
			var cmds []tea.Cmd
			for _, r := range g.rows {
				if !transferEnded(r.State) {
					cmds = append(cmds, m.transferCallCmd(verb, r.Name, map[string]any{"id": r.ID}))
				}
			}
			return tea.Sequence(cmds...), true
		case "cancel":
			var cmds []tea.Cmd
			for _, r := range g.rows {
				if !transferEnded(r.State) {
					cmds = append(cmds, m.transferCallCmd("transfer-cancel", r.Name, map[string]any{"id": r.ID}))
				}
			}
			return tea.Sequence(cmds...), true
		}
	}
	return nil, false
}
