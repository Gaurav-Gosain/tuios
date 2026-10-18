package app

import (
	"image/color"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The thread as cards: what the selected agent said and did, drawn from its
// transcript the way DESIGN.md 4.4 lays it out. Your prompts sit in a panel
// with an accent bar, the agent's words under a label with its harness and
// the time, reads and searches fold to one line and merge, an edit opens on
// its diff (drawn by internal/diffview, the review's renderer), a failed
// command unfolds its output, and a todo list is a plan with a progress rule.
//
// The cards are built from the entries the client already holds and kept
// with the thread until it, the width or the folds change.

// ckGlyphOpen is an open card's fold mark.
var ckGlyphOpen = ckGlyph{"\uf078", "▽", "v"}

// ckDiffShown is how many lines of a diff a card shows until it is expanded.
const ckDiffShown = 8

// ckFailShown is how many lines of a failed command's output a card shows.
const ckFailShown = 6

// threadDiff is a diff as agent-transcript sends it.
type threadDiff struct {
	File      string `json:"file"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Truncated bool   `json:"truncated"`
	Hunks     []threadHunk `json:"hunks"`
}

// threadHunk is one hunk of a threadDiff.
type threadHunk struct {
	OldStart int              `json:"old_start"`
	NewStart int              `json:"new_start"`
	Lines    []threadDiffLine `json:"lines"`
}

// threadDiffLine is one line of a hunk, with the changed words in UTF-16
// units of its text.
type threadDiffLine struct {
	Op    string `json:"op"`
	Text  string `json:"text"`
	Words []struct {
		S int `json:"s"`
		E int `json:"e"`
	} `json:"words"`
}

// ckLine is one line of a card, before it is drawn.
type ckLine struct {
	// bg is the line's ground across the width, nil for the canvas.
	bg color.Color
	// bar is a mark in the first cell, such as a card's edge.
	bar    string
	barInk color.Color
	indent int
	segs   []ckSeg
	right  []ckSeg
	// raw is a styled string drawn at indent in place of segs, such as a
	// diff line from diffview.
	raw string
	// hit is what a click on the line does: "fold:" and an entry's id.
	hit string
	// label marks a speaker's label, which starts after a blank line.
	label bool
}

// blank reports whether the line draws nothing.
func (ln ckLine) blank() bool {
	return ln.bg == nil && ln.bar == "" && ln.raw == "" && len(ln.segs) == 0
}

// ckReadTool is the word a read or search folds under, "" for any other
// tool. A run of them is one line.
func ckReadTool(tool string) string {
	switch strings.ToLower(tool) {
	case "read", "ls", "notebookread":
		return "Read"
	case "grep", "glob", "websearch":
		return "Search"
	case "webfetch":
		return "Fetch"
	}
	return ""
}

// threadCardLines draws the thread of a transcript agent as cards, the newest
// at the bottom, at least need lines of it. With changes it draws the edits
// of the last turn alone, each whole.
func (m *OS) threadCardLines(e sidebarAgentEntry, t *agentThread, width, need int, changes bool) ([]string, []string) {
	pal := theme.UI()
	l := m.cockpitLook()
	who := ckHarness(e.Harness)
	if who == "" {
		who = "agent"
	}
	entries := t.entries
	if changes {
		last := 0
		for i, en := range entries {
			if en.Kind == "text" && en.Role == "user" {
				last = i
			}
		}
		entries = entries[last:]
	}
	results := map[string]threadEntry{}
	turn := 0
	for _, en := range t.entries {
		if en.Kind == "tool_result" && en.ToolID != "" {
			results[en.ToolID] = en
		}
	}
	var items [][]ckLine
	add := func(ls ...ckLine) { items = ckAddItem(items, ls) }
	prevText := false
	for i := 0; i < len(entries); i++ {
		en := entries[i]
		isEdit := en.Kind == "tool_call" && en.Diff != nil
		if changes && !isEdit {
			continue
		}
		switch {
		case en.Kind == "text" && en.Role == "user":
			turn++
			label := []ckSeg{{t: "You", fg: pal.AccentBright, attrs: ckBold}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}
			if turn > 1 {
				label = append(label, ckSeg{t: sepWord() + "turn " + strconv.Itoa(turn), fg: pal.FgMute})
			}
			ls := []ckLine{{segs: label, label: true}, {bg: l.panel, bar: "▎", barInk: pal.AccentBright}}
			for _, s := range threadWrap(en.Text, max(width-6, 8)) {
				ls = append(ls, ckLine{bg: l.panel, bar: "▎", barInk: pal.AccentBright, indent: 3, segs: []ckSeg{{t: s, fg: pal.Fg}}})
			}
			add(append(ls, ckLine{bg: l.panel, bar: "▎", barInk: pal.AccentBright}, ckLine{})...)
			prevText = false
		case en.Kind == "text":
			var ls []ckLine
			if !prevText {
				ls = append(ls, ckLine{label: true, segs: []ckSeg{{t: who, fg: pal.FgDim, attrs: ckBold}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}})
			}
			for _, s := range threadWrap(en.Text, max(width-2, 8)) {
				ls = append(ls, ckLine{segs: []ckSeg{{t: s, fg: pal.Fg}}})
			}
			add(append(ls, ckLine{})...)
			prevText = true
			continue
		case en.Kind == "thinking":
			first, _, _ := strings.Cut(strings.TrimSpace(en.Text), "\n")
			add(ckLine{segs: []ckSeg{{t: l.g(ckGlyphFold) + " ", fg: pal.FgMute}, {t: "Thought", fg: pal.FgDim, attrs: ckItalic},
				{t: "  " + ckTrunc(printableTitle(first), width-14), fg: pal.FgMute, attrs: ckItalic}}})
		case en.Kind == "todos":
			add(m.ckPlanCard(l, en, width, i == m.ckLastTodos(entries))...)
		case en.Kind == "plan":
			add(m.ckPlanText(l, t, en, width)...)
		case isEdit:
			add(m.ckDiffCard(l, t, en, width, changes)...)
		case en.Kind == "tool_call" && ckReadTool(en.Tool) != "":
			// A run of reads and searches is one line.
			j := i
			var targets []string
			counts := map[string]int{}
			var order []string
			worst := "ok"
			for ; j < len(entries) && (entries[j].Kind == "tool_result" || entries[j].Kind == "tool_call" && ckReadTool(entries[j].Tool) != ""); j++ {
				if entries[j].Kind == "tool_result" {
					continue
				}
				k := ckReadTool(entries[j].Tool)
				if counts[k] == 0 {
					order = append(order, k)
				}
				counts[k]++
				targets = append(targets, path.Base(printableTitle(entries[j].Target)))
				worst = ckWorse(worst, entries[j].Status)
			}
			i = j - 1
			var what []string
			for _, k := range order {
				what = append(what, k+" "+strconv.Itoa(counts[k]))
			}
			add(m.ckToolLine(l, strings.Join(what, sepWord()), strings.Join(targets, ", "), worst, width))
		case en.Kind == "tool_call":
			ls := []ckLine{m.ckToolLine(l, printableTitle(en.Tool), printableTitle(en.Target), en.Status, width)}
			if en.Status == "error" {
				if res, ok := results[en.ToolID]; ok && res.Text != "" {
					out := threadWrap(res.Text, max(width-6, 8))
					if len(out) > ckFailShown {
						out = out[len(out)-ckFailShown:]
					}
					for _, s := range out {
						ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
					}
				}
			}
			add(ls...)
		default:
			continue
		}
		prevText = false
	}
	if changes && len(items) == 0 {
		return []string{ckRender(l, width, ckLine{indent: 2, segs: []ckSeg{{t: "No edits in the last turn.", fg: pal.FgDim}}})}, []string{""}
	}
	// The newest items, enough for need lines, drawn oldest first.
	var picked [][]ckLine
	n := 0
	for i := len(items) - 1; i >= 0 && n < need; i-- {
		picked = append(picked, items[i])
		n += len(items[i])
	}
	var out, hits []string
	for i := len(picked) - 1; i >= 0; i-- {
		for _, ln := range picked[i] {
			out = append(out, ckRender(l, width, ln))
			hits = append(hits, ln.hit)
		}
	}
	return out, hits
}

// ckAddItem appends a card's lines. A card or a new speaker starts after a
// blank line; folded tool lines run together. Kept out of line: the thread
// calls it from every case, and inlined it was most of the thread's code.
//
//go:noinline
func ckAddItem(items [][]ckLine, ls []ckLine) [][]ckLine {
	if n := len(items); n > 0 && len(ls) > 0 && (ls[0].bg != nil || ls[0].label) {
		if prev := items[n-1]; len(prev) > 0 && !prev[len(prev)-1].blank() {
			items[n-1] = append(prev, ckLine{})
		}
	}
	return append(items, ls)
}

// ckLastTodos is the index of the newest todo list, which is the one drawn
// whole.
func (m *OS) ckLastTodos(entries []threadEntry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == "todos" {
			return i
		}
	}
	return -1
}

// ckWorse is the worse of two tool statuses: an error, then a run, then ok.
func ckWorse(a, b string) string {
	rank := map[string]int{"ok": 0, "": 0, "running": 1, "error": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// ckShortPath is the end of a long path: its last three parts, which tell
// files apart.
func ckShortPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) > 3 && strings.HasPrefix(p, "/") {
		return strings.Join(parts[len(parts)-3:], "/")
	}
	return p
}

// ckClock is a transcript time as the label shows it: hours and minutes.
func ckClock(at int64) string {
	if at <= 0 {
		return ""
	}
	return time.UnixMilli(at).Format("15:04")
}

// ckToolLine is a folded tool call: the fold mark, the kind, what it acted
// on, and its status on the right, the word beside the mark.
func (m *OS) ckToolLine(l ckLook, kind, target, status string, width int) ckLine {
	pal := l.pal
	mark, ink := l.g(ckGlyphReview), overlay.MixColors(pal.Success, pal.FgMute, 0.5)
	if pal.Depth == overlay.Depth16 {
		ink = pal.Success
	}
	var right []ckSeg
	switch status {
	case "error":
		mark, ink = l.g(ckGlyphFailed), pal.Warn
		right = []ckSeg{{t: " failed ", fg: overlay.Readable(pal.Warn, l.warnTint), bg: l.warnTint, attrs: ckBold}}
	case "running":
		mark, ink = l.g(ckGlyphWorking), pal.Busy
		right = []ckSeg{{t: "running ", fg: pal.FgMute}}
	}
	right = append(right, ckSeg{t: " " + mark, fg: ink})
	room := max(width-ansi.StringWidth(kind)-ckWidth(right...)-8, 4)
	return ckLine{segs: []ckSeg{{t: l.g(ckGlyphFold) + " ", fg: pal.FgMute}, {t: kind, fg: pal.FgDim, attrs: ckBold},
		{t: "  " + ckTrunc(target, room), fg: pal.FgMute}}, right: right}
}

// ckPlanCard is a todo list as a plan: done rows struck and muted, the
// running row bold, and a rule that fills as rows are done. An older list
// folds to its count.
func (m *OS) ckPlanCard(l ckLook, en threadEntry, width int, whole bool) []ckLine {
	pal := l.pal
	done := 0
	for _, td := range en.Todos {
		if td.Status == "completed" {
			done++
		}
	}
	count := strconv.Itoa(done) + " of " + strconv.Itoa(len(en.Todos)) + " done"
	if !whole {
		return []ckLine{{segs: []ckSeg{{t: l.g(ckGlyphFold) + " ", fg: pal.FgMute}, {t: "Plan", fg: pal.FgDim, attrs: ckBold}, {t: "  " + count, fg: pal.FgMute}}}}
	}
	rule := 12
	filled := 0
	if len(en.Todos) > 0 {
		filled = rule * done / len(en.Todos)
	}
	ls := []ckLine{{bg: l.panel, indent: 1, segs: []ckSeg{{t: l.g(ckGlyphOpen) + " ", fg: pal.FgDim}, {t: "Plan", fg: pal.FgDim, attrs: ckBold}, {t: "   " + count, fg: pal.FgMute}},
		right: []ckSeg{{t: strings.Repeat("━", filled), fg: pal.Accent}, {t: strings.Repeat("─", rule-filled), fg: l.edgePanel}, {t: " "}}}}
	for _, td := range en.Todos {
		g, ink, attrs := l.g(ckGlyphIdle), pal.FgDim, uint8(0)
		switch td.Status {
		case "completed":
			g, ink, attrs = l.g(ckGlyphReview), pal.FgMute, ckStrike
		case "in_progress":
			g, ink, attrs = l.g(ckGlyphWorking), pal.Fg, ckBold
		}
		gInk := ink
		if td.Status == "completed" {
			gInk = pal.Success
		} else if td.Status == "in_progress" {
			gInk = pal.Busy
		}
		ls = append(ls, ckLine{bg: l.panel, indent: 3, segs: []ckSeg{{t: g + " ", fg: gInk}, {t: ckTrunc(printableTitle(td.Text), width-8), fg: ink, attrs: attrs}}})
	}
	return append(ls, ckLine{bg: l.panel}, ckLine{})
}

// ckPlanText is a plan the agent proposed: its first lines, all of them once
// expanded.
func (m *OS) ckPlanText(l ckLook, t *agentThread, en threadEntry, width int) []ckLine {
	pal := l.pal
	lines := threadWrap(en.Plan, max(width-6, 8))
	open := !t.folded[en.ID]
	mark := ckGlyphOpen
	if !open {
		mark = ckGlyphFold
	}
	ls := []ckLine{{bg: l.panel, indent: 1, hit: "fold:" + en.ID, segs: []ckSeg{{t: l.g(mark) + " ", fg: pal.FgDim}, {t: "Plan", fg: pal.FgDim, attrs: ckBold},
		{t: "   " + strconv.Itoa(len(lines)) + " lines", fg: pal.FgMute}}}}
	if !open {
		return append(ls, ckLine{})
	}
	shown := lines
	if !t.expandAll && len(shown) > ckDiffShown {
		shown = shown[:ckDiffShown]
	}
	for _, s := range shown {
		ls = append(ls, ckLine{bg: l.panel, indent: 3, segs: []ckSeg{{t: s, fg: pal.Fg}}})
	}
	if len(shown) < len(lines) {
		ls = append(ls, ckLine{bg: l.panel, indent: 3, segs: []ckSeg{{t: strconv.Itoa(len(lines)-len(shown)) + " more lines   ", fg: pal.FgMute}, {t: "e", fg: pal.AccentBright, attrs: ckBold}, {t: " expand all", fg: pal.FgMute}}})
	}
	return append(ls, ckLine{bg: l.panel}, ckLine{})
}

// ckDiffCard is an edit opened on its diff: line numbers, signs, the code
// highlighted and the changed words lit, as the review draws them. The first
// lines show until e expands every card; a click on the header folds it.
func (m *OS) ckDiffCard(l ckLook, t *agentThread, en threadEntry, width int, whole bool) []ckLine {
	pal := l.pal
	d := en.Diff
	open := !t.folded[en.ID] || whole
	mark := ckGlyphOpen
	if !open {
		mark = ckGlyphFold
	}
	file := ckShortPath(printableTitle(d.File))
	right := ckDiffCounts(l, d.Added, d.Removed)
	st := m.ckToolLine(l, "", "", en.Status, width)
	right = append(right, st.right...)
	head := ckLine{bg: l.panel, indent: 1, hit: "fold:" + en.ID, right: append(right, ckSeg{t: " "}),
		segs: []ckSeg{{t: l.g(mark) + " ", fg: pal.FgDim}, {t: printableTitle(en.Tool), fg: pal.FgDim, attrs: ckBold},
			{t: "   " + ckTrunc(file, width-24), fg: pal.Fg}}}
	if !open {
		return []ckLine{head, {}}
	}
	ls := []ckLine{head}
	dv := m.ckDiffTheme(l)
	shown := 0
	total := 0
	for _, h := range d.Hunks {
		total += len(h.Lines)
	}
	limit := ckDiffShown
	if whole || t.expandAll {
		limit = total
	}
	gw := 5
	codeW := max(width-gw-diffview.SignWidth-1, 8)
	for hi, h := range d.Hunks {
		texts := make([]string, len(h.Lines))
		for i, ln := range h.Lines {
			texts[i] = printableRunes(strings.ReplaceAll(ln.Text, "\t", "    "))
		}
		spans := ckSideSpans(d.File, h.Lines, texts)
		oldN, newN := h.OldStart, h.NewStart
		if hi > 0 && shown < limit {
			ls = append(ls, ckLine{bg: l.panel, indent: gw + 1, segs: []ckSeg{{t: "⋯", fg: pal.FgMute}}})
		}
		for i, ln := range h.Lines {
			kind, n := diffview.Context, newN
			switch ln.Op {
			case "+":
				kind, n = diffview.Add, newN
				newN++
			case "-":
				kind, n = diffview.Delete, oldN
				oldN++
			default:
				oldN++
				newN++
			}
			if shown >= limit {
				continue
			}
			shown++
			var changed diffview.Range
			if len(ln.Words) > 0 {
				changed = diffview.Range{Start: ckUTF16Byte(texts[i], ln.Words[0].S), End: ckUTF16Byte(texts[i], ln.Words[len(ln.Words)-1].E)}
			}
			raw := dv.Gutter(n, gw, kind, false) + dv.Sign(kind, false, false) + dv.Code(texts[i], spans[i], changed, kind, false, 0, codeW)
			ls = append(ls, ckLine{bg: l.panel, raw: raw, indent: 0})
		}
	}
	foot := []ckSeg{}
	if shown < total {
		foot = append(foot, ckSeg{t: strconv.Itoa(total-shown) + " more lines   ", fg: pal.FgMute}, ckSeg{t: "e", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " expand all   ", fg: pal.FgMute})
	}
	foot = append(foot, ckSeg{t: m.inboxKeyOr(config.ActionInboxReview, "v"), fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " open in review", fg: pal.FgMute})
	return append(ls, ckLine{bg: l.panel, indent: gw + 3, segs: foot}, ckLine{})
}

// ckDiffCounts are a diff's added and removed counts, calm: carried toward
// the secondary ink.
func ckDiffCounts(l ckLook, add, del int) []ckSeg {
	var segs []ckSeg
	if add > 0 {
		segs = append(segs, ckSeg{t: "+" + strconv.Itoa(add), fg: l.add})
	}
	if del > 0 {
		if segs != nil {
			segs = append(segs, ckSeg{t: " "})
		}
		segs = append(segs, ckSeg{t: "−" + strconv.Itoa(del), fg: l.del})
	}
	return append(segs, ckSeg{t: "  "})
}

// ckSideSpans highlights a hunk's lines, each side as one text, so a string
// or comment that spans lines keeps its colour.
func ckSideSpans(file string, lines []threadDiffLine, texts []string) [][]diffview.Span {
	out := make([][]diffview.Span, len(lines))
	for _, side := range []string{"-", "+"} {
		var idx []int
		var text []string
		for i, ln := range lines {
			if ln.Op == " " || ln.Op == "" || ln.Op == side {
				idx = append(idx, i)
				text = append(text, texts[i])
			}
		}
		spans := diffview.Highlight(file, text)
		for k, i := range idx {
			if k < len(spans) && (lines[i].Op == side || side == "+") {
				out[i] = spans[k]
			}
		}
	}
	return out
}

// ckUTF16Byte turns an offset in UTF-16 code units into one in bytes of s.
func ckUTF16Byte(s string, u int) int {
	n := 0
	for i, r := range s {
		if n >= u {
			return i
		}
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return len(s)
}

// ckDiffTheme is the diff theme on the card's ground, kept until the theme
// or the depth changes.
func (m *OS) ckDiffTheme(l ckLook) *diffview.Theme {
	ck := &m.Inbox.ck
	key := strconv.FormatUint(theme.Generation(), 10) + l.pal.Depth.String()
	if ck.dv != nil && ck.dvKey == key {
		return ck.dv
	}
	syntax := diffview.DefaultSyntax()
	if t := theme.Current(); t != nil {
		var slots [16]color.Color
		for i, c := range theme.ANSIOrder(t) {
			if c != nil {
				slots[i] = c
			}
		}
		syntax = diffview.SyntaxFromANSI(slots)
	}
	ground := l.panel
	if l.pal.Depth == overlay.Depth16 {
		ground = l.pal.Surface
	}
	ck.dv = diffview.NewTheme(diffview.Palette{Depth: l.pal.Depth, Ground: ground, Fg: l.pal.Fg, Accent: l.pal.Accent,
		Add: l.pal.Success, Delete: l.pal.Warn, Syntax: syntax})
	ck.dvKey = key
	return ck.dv
}

// ckRender draws one card line at width.
func ckRender(l ckLook, width int, ln ckLine) string {
	c := newCkCanvas(width, 1, l.canvas)
	if ln.bg != nil {
		c.fill(0, 0, width, 1, ln.bg)
	}
	if ln.bar != "" {
		c.text(0, 0, 0, ckSeg{t: ln.bar, fg: ln.barInk})
	}
	right := width
	if ln.right != nil {
		right = c.textR(width, 0, ln.right...) - 1
	}
	if ln.raw != "" {
		c.draw(ln.indent, 0, width-ln.indent, ln.raw)
	} else {
		c.text(ln.indent, 0, right, ln.segs...)
	}
	return c.render()
}

// newCkCanvas is a blank w by h canvas on bg.
func newCkCanvas(w, h int, bg color.Color) *ckCanvas { return (*ckCanvas)(nil).reset(w, h, bg) }
