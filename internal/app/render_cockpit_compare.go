package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// ckCompareColMin is the narrowest an attempt's column is drawn. Fewer
// columns are shown on a narrow screen, the focused one among them.
const ckCompareColMin = 26

// ckTint is c carried t of the way into ground, at the depth in force: at
// 16 colours there are no tints.
func ckTint(l ckLook, c, ground color.Color, t float64) color.Color {
	switch l.pal.Depth {
	case overlay.Depth16:
		return overlay.NoColor
	case overlay.Depth256:
		return overlay.To256(overlay.MixColors(c, ground, t))
	}
	return ckFlat(overlay.MixColors(c, ground, t))
}

// ckSpan is a duration as the comparison says it: "9m 05s", "2h 10m".
func ckSpan(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		s := int(d.Seconds()) % 60
		return strconv.Itoa(int(d.Minutes())) + "m " + ckZero(s) + "s"
	}
	return strconv.Itoa(int(d.Hours())) + "h " + ckZero(int(d.Minutes())%60) + "m"
}

// ckZero writes n in two digits.
func ckZero(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// drawCompare draws the comparison of a fan's attempts at x, y, w cells wide,
// down to y1.
func (m *OS) drawCompare(c *ckCanvas, l ckLook, x, y, w, y1, top int, now time.Time) {
	ck := &m.Inbox.ck
	cp := ck.cmp
	pal := l.pal
	right := x + w
	res := cp.res
	if res == nil {
		words := "Reading the attempts..."
		if cp.err != "" {
			words = cp.err
		}
		for i, s := range wrapPlain(words, w) {
			c.text(x, y+i, right, ckSeg{t: s, fg: pal.FgMute})
		}
		return
	}
	rows := res.Rows
	// The header: the fan, how far its attempts are, and the task.
	done, last := 0, int64(0)
	for _, r := range rows {
		if r.State == "done" || r.State == "idle" {
			done++
		}
		last = max(last, r.DoneAt)
	}
	status := strconv.Itoa(len(rows)-done) + " working"
	if done == len(rows) {
		status = "all finished"
		if last > 0 {
			status += " " + time.Unix(0, last).Format("15:04")
		}
	}
	c.text(x, y, right-20, ckSeg{t: printableTitle(res.Group), fg: pal.Fg, attrs: ckBold},
		ckSeg{t: "   fan of " + strconv.Itoa(len(rows)), fg: pal.FgDim}, ckSeg{t: sepWord() + status, fg: pal.FgMute})
	var pill ckSeg
	switch {
	case res.Verdict != nil:
		pill = ckSeg{t: " " + l.g(ckGlyphReview) + " judged ", fg: overlay.Readable(pal.Success, l.okTint), bg: l.okTint, attrs: ckBold}
	case done == len(rows):
		pill = ckSeg{t: " " + l.g(ckGlyphReview) + " pick one ", fg: overlay.Readable(pal.Success, l.okTint), bg: l.okTint, attrs: ckBold}
	default:
		pill = ckSeg{t: " " + l.g(ckGlyphWorking) + " working ", fg: overlay.Readable(pal.Busy, l.busyTint), bg: l.busyTint, attrs: ckBold}
	}
	px := c.textR(right, y, pill)
	c.mark(pal, px, y, right-px, 1, ckOn)
	if task := printableTitle(strings.Join(strings.Fields(res.Prompt), " ")); task != "" {
		c.text(x, y+1, right, ckSeg{t: ckTrunc(task, w), fg: pal.FgMute})
	}
	ty := y + 3
	tx := c.text(x, ty, right, ckSeg{t: "Compare", fg: pal.Fg, attrs: ckBold})
	c.hline(x, ty+1, w, hzGlyph(), l.edge)
	c.hline(x, ty+1, tx-x, "━", pal.Accent)
	cy := ty + 3

	// The judge's card.
	cy = m.drawJudgeCard(c, l, x, cy, w, top, now) + 1

	// What a keep does, or what the last action said.
	pick := ""
	if res.Verdict != nil {
		pick = res.Verdict.Pick
	}
	if a, ok := cp.ckFocused(); ok && cp.armed == a.Session {
		var others []string
		for i := range rows {
			if i != cp.focus {
				others = append(others, ckLetter(i))
			}
		}
		var lines []ckSeg
		add := func(text string, ink color.Color, attrs uint8) {
			for _, l := range wrapPlain(text, w) {
				lines = append(lines, ckSeg{t: l, fg: ink, attrs: attrs})
			}
		}
		failed := ckIgnoredFailed(rows, cp.focus)
		switch {
		case cp.loading:
			add("Reading the attempts again...", pal.Attention, ckBold)
		case failed != "":
			// No keep on a list that could not be read.
			add("tuios could not list the files git ignores, so nothing is kept. "+failed, pal.Warn, ckBold)
		default:
			words := "Press k again to keep " + ckLetter(cp.focus) + ". " + ckJoin(others) + " are removed. Their branches stay, and their changes go to git stash."
			if len(others) == 1 {
				words = strings.Replace(words, " are removed. Their branches stay, and their", " is removed. Its branch stays, and its", 1)
			}
			add(words, pal.Attention, ckBold)
			// Files git ignores are not stashed: they go with the worktree.
			// Every one is named, attempt by attempt.
			if lost := ckIgnoredLost(rows, cp.focus); len(lost) > 0 {
				add("These files git ignores are deleted, not stashed:", pal.Warn, ckBold)
				for _, l := range lost {
					add(l, pal.Warn, 0)
				}
			}
		}
		if cp.note != "" && !cp.loading {
			add(cp.note, pal.FgDim, 0)
		}
		// At most ckWarnRows rows, scrolled with up and down.
		show := min(len(lines), ckWarnRows)
		cp.warnScroll = min(cp.warnScroll, len(lines)-show)
		for i := range show {
			seg := lines[cp.warnScroll+i]
			if i == show-1 && cp.warnScroll+show < len(lines) {
				seg = ckSeg{t: strconv.Itoa(len(lines)-cp.warnScroll-show) + " more lines: down shows them", fg: pal.FgMute}
			}
			c.text(x, cy+i, right, seg)
		}
		cy += show - 1
	} else if cp.busy != "" {
		words := "Starting a judge..."
		if cp.busy == "keep" {
			words = "Keeping the attempt..."
		}
		c.text(x, cy, right, ckSeg{t: words, fg: pal.FgDim})
	} else if cp.note != "" {
		c.text(x, cy, right, ckSeg{t: ckTrunc(cp.note, w), fg: pal.FgDim})
	}
	cy += 2

	// One column per attempt, the focused one always among those shown.
	if len(rows) == 0 || y1-cy < 6 {
		return
	}
	shown := max(min(len(rows), (w+3)/(ckCompareColMin+3)), 1)
	first := min(max(cp.focus-shown+1, 0), len(rows)-shown)
	gap := 3
	cw := (w - gap*(shown-1)) / shown
	for i := first; i < first+shown; i++ {
		ax := x + (i-first)*(cw+gap)
		m.drawAttempt(c, l, rows[i], i, ax, cy, cw, y1, top, rows[i].Session == pick, i == cp.focus)
	}
	if first > 0 || first+shown < len(rows) {
		c.textR(right, cy-1, ckSeg{t: strconv.Itoa(len(rows)-shown) + " more. A digit or an arrow shows another.", fg: pal.FgMute})
	}
}

// ckAttemptName is an attempt's name as the fleet says it: its agent pane's
// name, else its session's.
func (m *OS) ckAttemptName(session string) string {
	for _, e := range m.Inbox.ck.agents {
		if e.Host == "" && m.ckAgentSession(e) == session {
			if t := printableTitle(e.Title); t != "" {
				return t
			}
		}
	}
	return printableTitle(session)
}

// ckBullet is the mark of a list item.
func ckBullet(l ckLook) string {
	if l.ascii {
		return "*"
	}
	return "•"
}

// ckJoin joins letters as a sentence does: "A", "A and C", "A, C and D".
func ckJoin(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// drawJudgeCard draws the judge's verdict, or the way to ask for one, and
// returns the row under the card.
func (m *OS) drawJudgeCard(c *ckCanvas, l ckLook, x, y, w, top int, now time.Time) int {
	ck := &m.Inbox.ck
	cp := ck.cmp
	res := cp.res
	pal := l.pal
	right := x + w
	type line struct {
		segs []ckSeg
		x    int
	}
	var lines []line
	var btns [][3]string // key, label, action
	if v := res.Verdict; v != nil {
		// Who recorded it, as the daemon says: the judge's pane, or you.
		meta := "recorded by you"
		if v.By == "judge" {
			meta = "the judge tuios started"
			if v.Judge != "" {
				meta = ckHarness(v.Judge) + sepWord() + meta
			}
		}
		if v.At > 0 {
			meta += sepWord() + inboxWait(v.At, now) + " ago"
		}
		lines = append(lines, line{segs: []ckSeg{{t: "Judge", fg: pal.FgDim, attrs: ckBold}, {t: "   " + meta, fg: pal.FgMute}}})
		summary := printableTitle(v.Summary)
		head, rest := summary, ""
		if i := strings.Index(summary, ". "); i > 0 && i < 40 {
			head, rest = summary[:i+1], summary[i+1:]
		}
		wrapped := wrapPlain(head+rest, w-6)
		for i, s := range wrapped {
			if i == 2 {
				break
			}
			segs := []ckSeg{{t: s, fg: pal.Fg}}
			if i == 0 && strings.HasPrefix(s, head) {
				segs = []ckSeg{{t: head, fg: pal.Fg, attrs: ckBold}, {t: s[len(head):], fg: pal.Fg}}
			}
			lines = append(lines, line{segs: segs})
		}
		if len(v.Reasons) > 0 {
			lines = append(lines, line{})
			for i, r := range v.Reasons {
				if i == 4 {
					break
				}
				lines = append(lines, line{x: 2, segs: []ckSeg{{t: ckBullet(l) + "  ", fg: pal.FgMute}, {t: ckTrunc(printableTitle(r), w-10), fg: pal.FgDim}}})
			}
		}
		letter := ""
		for i, r := range res.Rows {
			if r.Session == v.Pick {
				letter = ckLetter(i)
			}
		}
		if a, ok := cp.ckFocused(); ok && letter != "" && a.Session == v.Pick {
			btns = append(btns, [3]string{"k", "Keep " + letter, "cmp:keep"})
		} else if ok {
			btns = append(btns, [3]string{"k", "Keep " + ckLetter(cp.focus), "cmp:keep"})
		}
		btns = append(btns, [3]string{"j", "Judge again", "cmp:judge"})
	} else {
		words := "No verdict yet. A judge is an agent that reads every attempt and picks one. Through tuios it can only read them: their diffs, screens and scrollback."
		if m.ckFanJudge() {
			words = "A judge is reading the attempts. Its verdict shows here when it reports."
		}
		lines = append(lines, line{segs: []ckSeg{{t: "Judge", fg: pal.FgDim, attrs: ckBold}}})
		for _, s := range wrapPlain(words, w-6) {
			lines = append(lines, line{segs: []ckSeg{{t: s, fg: pal.FgDim}}})
		}
		btns = append(btns, [3]string{"j", "Ask a judge", "cmp:judge"})
		if _, ok := cp.ckFocused(); ok {
			btns = append(btns, [3]string{"k", "Keep " + ckLetter(cp.focus), "cmp:keep"})
		}
	}
	h := len(lines) + 4
	c.fill(x, y, w, h, l.panel)
	for i := range h {
		c.text(x, y+i, 0, ckSeg{t: "▌", fg: pal.AccentBright})
	}
	for i, ln := range lines {
		c.text(x+3+ln.x, y+1+i, right-2, ln.segs...)
	}
	by := y + 1 + len(lines) + 1
	bx := x + 3
	for i, b := range btns {
		keyBg, keyInk, labelBg, labelInk := pal.Card, pal.AccentBright, l.cursor, pal.Fg
		if i == 0 {
			keyBg, keyInk, labelBg, labelInk = pal.Accent, pal.PillFg, pal.Accent, pal.PillFg
		}
		x0 := bx
		bx = c.text(bx, by, right-2, ckSeg{t: " " + b[0] + " ", fg: overlay.Readable(keyInk, keyBg), bg: keyBg, attrs: ckBold},
			ckSeg{t: " " + b[1] + " ", fg: overlay.Readable(labelInk, labelBg), bg: labelBg})
		c.mark(pal, x0, by, bx-x0, 1, ckOn)
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + by, x1: bx, y1: top + by + 1, kind: ckHitAction, action: b[2]})
		bx += 2
	}
	return y + h
}

// ckFanJudge reports whether a judge runs for the compared fan.
func (m *OS) ckFanJudge() bool {
	cp := m.Inbox.ck.cmp
	for _, w := range m.Inbox.ck.branches {
		if w.group == cp.res.Group && w.fanID == cp.res.FanID && w.repo == cp.res.RepoRoot && w.judgeWindow != "" {
			return true
		}
	}
	return false
}

// ckIgnoredLost names, attempt by attempt, the files git ignores that a keep
// of attempt keep deletes: "C: .env, build/". Every name the read gave is
// there, and a count for any past the read's bound.
func ckIgnoredLost(rows []ckFanAttempt, keep int) []string {
	var out []string
	for i, r := range rows {
		if i == keep || r.IgnoredCount == 0 {
			continue
		}
		names := make([]string, 0, len(r.Ignored))
		for _, f := range r.Ignored {
			names = append(names, printableTitle(f))
		}
		line := ckLetter(i) + ": " + strings.Join(names, ", ")
		if more := r.IgnoredCount - len(r.Ignored); more > 0 {
			line += " and " + strconv.Itoa(more) + " more"
		}
		out = append(out, line)
	}
	return out
}

// ckWarnRows bounds the rows of the keep warning, which scrolls.
const ckWarnRows = 6

// drawAttempt draws one attempt's column at x, y, cw wide, down to y1.
func (m *OS) drawAttempt(c *ckCanvas, l ckLook, a ckFanAttempt, i, x, y, cw, y1, top int, recommended, focused bool) {
	ck := &m.Inbox.ck
	pal := l.pal
	ground := l.panel
	if recommended {
		ground = l.cursor
	}
	c.fill(x, y, cw, y1-y, ground)
	ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + cw, y1: top + y1, kind: ckHitAction, action: "cmp:focus:" + strconv.Itoa(i)})
	ink := func(c color.Color) color.Color { return overlay.Readable(c, ground) }
	right := x + cw - 2
	yy := y + 1
	if recommended {
		c.hline(x, y, cw, "▀", pal.Accent)
		yy++
	}
	chipBg, chipInk := pal.Card, pal.AccentBright
	if focused {
		chipBg, chipInk = pal.Accent, pal.PillFg
	}
	px := right
	if recommended {
		tint := ckTint(l, pal.Accent, ground, 0.55)
		px = c.textR(right, yy, ckSeg{t: " recommended ", fg: overlay.Readable(pal.Fg, tint), bg: tint, attrs: ckBold}) - 1
		c.mark(pal, px+1, yy, right-px-1, 1, ckOn)
	}
	cx := c.text(x+2, yy, px, ckSeg{t: " " + ckLetter(i) + " ", fg: overlay.Readable(chipInk, chipBg), bg: chipBg, attrs: ckBold})
	c.mark(pal, x+2, yy, 3, 1, overlay.RowState{Cursor: focused, Focused: focused})
	c.text(cx+2, yy, px, ckSeg{t: ckTrunc(m.ckAttemptName(a.Session), px-cx-2), fg: ink(pal.Fg), attrs: ckBold})
	yy++
	who := ckHarness(firstNonEmptyStr(a.Harness, a.Agent))
	c.text(x+2, yy, right, ckSeg{t: who, fg: ink(pal.FgDim)}, ckSeg{t: sepWord() + l.g(ckGlyphHost) + " " + m.ckLocalLabel(), fg: ink(pal.FgMute)})
	yy++
	// How it went: done in how long, or still at work.
	state := []ckSeg{{t: l.g(ckGlyphWorking) + " ", fg: pal.Busy}, {t: sidebarStateWords(a.State), fg: ink(pal.FgMute)}}
	switch {
	case a.Gone:
		state = []ckSeg{{t: l.g(ckGlyphFailed) + " ", fg: pal.Warn}, {t: "worktree gone", fg: ink(pal.FgMute)}}
	case a.State == "done" || a.State == "idle":
		words := "done"
		if a.StartedAt > 0 && a.DoneAt > a.StartedAt {
			words += " in " + ckSpan(time.Duration(a.DoneAt-a.StartedAt))
		}
		state = []ckSeg{{t: l.g(ckGlyphReview) + " ", fg: l.add}, {t: words, fg: ink(pal.FgMute)}}
	}
	c.text(x+2, yy, right, state...)
	yy += 2

	// The figures.
	row := func(label string, v ...ckSeg) {
		if yy >= y1-2 {
			return
		}
		c.text(x+2, yy, right, ckSeg{t: ckPad(label, 9), fg: ink(pal.FgMute)})
		c.text(x+11, yy, right, v...)
		yy++
	}
	if a.Added != nil && a.Removed != nil {
		segs := ckDiffCounts(l, *a.Added, *a.Removed)
		segs = segs[:len(segs)-1]
		if len(segs) == 0 {
			segs = []ckSeg{{t: "no changes", fg: ink(pal.FgMute)}}
		}
		if a.Files != nil {
			files := strconv.Itoa(*a.Files) + " file"
			if *a.Files != 1 {
				files += "s"
			}
			segs = append(segs, ckSeg{t: "  in " + files, fg: ink(pal.FgMute)})
		}
		row("Diff", segs...)
	} else if a.Note != "" {
		row("Diff", ckSeg{t: ckTrunc(printableTitle(a.Note), right-x-11), fg: ink(pal.FgMute)})
	}
	check := []ckSeg{{t: "no check yet", fg: ink(pal.FgMute)}}
	if v := a.Verify; v != nil {
		switch v.State {
		case "passed":
			check = []ckSeg{{t: "passed ", fg: ink(pal.Fg)}, {t: l.g(ckGlyphReview), fg: pal.Success}}
		case "failed":
			check = []ckSeg{{t: "failed ", fg: ink(pal.Fg)}, {t: l.g(ckGlyphFailed), fg: pal.Warn}}
		default:
			check = []ckSeg{{t: "running", fg: ink(pal.FgDim)}}
		}
	}
	row("Check", check...)
	if a.Cost != "" {
		row("Cost", ckSeg{t: printableTitle(a.Cost), fg: ink(pal.FgDim)})
	}
	yy++

	// The first changed lines.
	if len(a.Lines) > 0 && yy < y1-4 {
		c.text(x+2, yy, right, ckSeg{t: "First lines", fg: ink(pal.FgDim), attrs: ckBold})
		yy++
		file := ""
		for _, dl := range a.Lines {
			if yy >= y1-3 {
				break
			}
			// Each file's lines under its name.
			if dl.File != file && yy < y1-4 {
				file = dl.File
				c.text(x+2, yy, right, ckSeg{t: ckTruncMid(printableTitle(file), right-x-2), fg: ink(pal.FgMute)})
				yy++
			}
			lg, sign, sInk := ground, " ", pal.FgMute
			switch dl.Op {
			case "+":
				lg, sign, sInk = ckTint(l, pal.Success, ground, 0.86), "+", l.add
			case "-":
				lg, sign, sInk = ckTint(l, pal.Warn, ground, 0.84), "-", l.del
			}
			if noColor(lg) {
				lg = ground
			}
			c.fill(x+2, yy, cw-4, 1, lg)
			text := printableTitle(strings.ReplaceAll(dl.Text, "\t", "    "))
			c.text(x+3, yy, right, ckSeg{t: ckPadLeft(strconv.Itoa(dl.Line), 4) + " ", fg: overlay.Readable(pal.FgMute, lg)},
				ckSeg{t: sign + " ", fg: sInk}, ckSeg{t: ckTrunc(text, max(right-x-11, 1)), fg: overlay.Readable(pal.Fg, lg)})
			yy++
		}
		yy++
	}

	// The files.
	if len(a.FileList) > 0 && yy < y1-4 {
		c.text(x+2, yy, right, ckSeg{t: "Files", fg: ink(pal.FgDim), attrs: ckBold})
		yy++
		for _, f := range a.FileList {
			if yy >= y1-3 {
				break
			}
			drawFileStat(c, l, x+2, yy, cw-4, printableTitle(f.Path), f.Added, f.Removed)
			yy++
		}
		yy++
	}

	// The judge's notes.
	if len(a.Notes) > 0 && yy < y1-4 {
		c.text(x+2, yy, right, ckSeg{t: "Notes", fg: ink(pal.FgDim), attrs: ckBold})
		yy++
		for _, n := range a.Notes {
			for j, s := range wrapPlain(printableTitle(n), cw-6) {
				if yy >= y1-3 {
					break
				}
				lead := ckBullet(l) + " "
				if j > 0 {
					lead = "  "
				}
				c.text(x+2, yy, right, ckSeg{t: lead, fg: ink(pal.FgMute)}, ckSeg{t: s, fg: ink(pal.FgDim)})
				yy++
			}
		}
	}

	// The keys of the column.
	if i < 9 {
		c.text(x+2, y1-2, right, ckSeg{t: strconv.Itoa(i + 1), fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " focus   ", fg: ink(pal.FgMute)},
			ckSeg{t: overlay.EnterGlyph, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " pane", fg: ink(pal.FgMute)})
	}
}
