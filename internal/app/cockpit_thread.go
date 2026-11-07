package app

import (
	"image/color"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
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
	File      string       `json:"file"`
	Added     int          `json:"added"`
	Removed   int          `json:"removed"`
	Truncated bool         `json:"truncated"`
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
	// bg is the line's ground from column bgAt to the width, nil for the
	// canvas.
	bg   color.Color
	bgAt int
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
	// wrap says the next line goes on with the same paragraph, so copy mode
	// joins them.
	wrap bool
}

// blank reports whether the line draws nothing.
func (ln ckLine) blank() bool {
	return ln.bg == nil && ln.bar == "" && ln.raw == "" && len(ln.segs) == 0
}

// ckReadTool is the word a read or search folds under, "" for any other
// tool. A run of them is one line.
func ckReadTool(tool string) string {
	// Claude Code's names, then those of the other harnesses whose
	// transcripts tuios reads: Codex, Gemini CLI, Qwen Code, Copilot CLI and
	// Amp.
	switch strings.ToLower(tool) {
	case "read", "ls", "notebookread",
		"read_file", "read_many_files", "list_directory", "view", "view_image":
		return "Read"
	case "grep", "glob", "websearch",
		"grep_search", "search_file_content", "google_web_search", "web_search":
		return "Search"
	case "webfetch", "web_fetch", "read_web_page":
		return "Fetch"
	}
	return ""
}

// threadCardLines draws the thread of a transcript agent as cards, the newest
// at the bottom. With changes it draws the edits
// of the last turn alone, each whole.
func (m *OS) threadCardLines(e sidebarAgentEntry, t *agentThread, width int, changes bool, now time.Time) ([]string, []string) {
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
			if en.Kind == "text" && en.Role == "user" && en.Image == nil && !transcriptview.InterruptText(en.Text) {
				last = i
			}
		}
		entries = entries[last:]
	}
	results := map[string]threadEntry{}
	pills := m.ckChildPills(e, t.entries)
	turn := 0
	for _, en := range t.entries {
		if en.Kind == "tool_result" && en.ToolID != "" {
			results[en.ToolID] = en
		}
	}
	// Each item carries its lines drawn: a line is drawn once, when its
	// card is built, and a rebuild only joins them.
	var items []ckItem
	// prompts are the items that are prompts, for the thread's ruler.
	var prompts []int
	var c *ckCanvas
	draw := func(ls []ckLine) []string {
		out := make([]string, len(ls))
		for i, ln := range ls {
			c = c.reset(width, 1, l.canvas)
			out[i] = ckRender(c, width, ln)
		}
		return out
	}
	c = c.reset(width, 1, l.canvas)
	blank := c.render()
	// cur is the entry the loop draws, for copy mode's Markdown yank.
	cur := ""
	add := func(ls ...ckLine) { items = ckAddItem(items, ckItem{lines: ls, out: draw(ls), id: cur}, blank) }
	// cached draws an entry's card once per width, fold and theme: a diff's
	// highlighting and a long text's wrapping are the costly part, and a
	// thread that grows by one entry draws only that one.
	gen := theme.Generation()
	cached := func(en threadEntry, variant int, build func() []ckLine) {
		key := ckCardKey{width: width, theme: gen, look: m.Inbox.ck.lookGen, ascii: l.ascii, nerd: l.nerd, depth: l.pal.Depth,
			folded: t.folded[en.ID], expand: t.expandAll, changes: changes, variant: variant, status: en.Status, sum: en.sum}
		if card, ok := t.cards[en.ID]; ok && card.key == key {
			items = ckAddItem(items, card.item, blank)
			return
		}
		ls := build()
		if t.cards == nil {
			t.cards = make(map[string]ckCard)
		}
		it := ckItem{lines: ls, out: draw(ls), id: en.ID}
		t.cards[en.ID] = ckCard{key: key, item: it}
		items = ckAddItem(items, it, blank)
	}
	// A finished turn folds its work to one line under its prompt. The
	// Changes tab draws edits alone and folds nothing.
	var steps []transcriptview.Step
	var turns []transcriptview.Turn
	var turnOf []int
	if !changes {
		steps, turns, turnOf = ckTurns(e, entries)
	}
	from := 0
	t.older = false
	if turnOf != nil && t.keep > 0 && t.keep < len(turns) {
		from = turns[len(turns)-t.keep].Start
		t.older = from > 0
		for _, en := range entries[:from] {
			if ckPrompt(en) {
				turn++
			}
		}
	}
	prevText := false
	working := e.State == string(session.AgentStateWorking)
	// While the agent works its newest todo list is pinned above the
	// thread, so in the thread it is folded to its count.
	pinned := !changes && t.pinned
	for i := from; i < len(entries); i++ {
		en := entries[i]
		cur = en.ID
		isEdit := en.Kind == "tool_call" && en.Diff != nil
		if changes && !isEdit {
			continue
		}
		if turnOf != nil {
			tn := &turns[turnOf[i]]
			id := "turn:" + entries[tn.Start].ID
			open := t.expandAll || tn.Folds() == t.folded[id]
			if tn.Work() > 0 && i == ckTurnHead(steps, tn) && (tn.Finished || working && turnOf[i] == len(turns)-1) {
				what, span := ckTurnWords(tn, now), ""
				key := threadEntry{ID: id, Text: what, Plan: span}
				key.sum = threadSum(&key)
				cached(key, boolInt(open), func() []ckLine { return []ckLine{m.ckTurnLine(l, tn, id, what, span, width, open)} })
				prevText = false
			}
			if !open && tn.Hidden(steps, i) {
				continue
			}
		}
		switch {
		case en.Image != nil:
			// An image the person pasted or a tool gave back: a card under
			// its prompt or its call.
			add(m.ckImageCard(l, t, en, width)...)
		case en.Kind == "text" && en.Role == "tool" || transcriptview.Interrupt(en.Role, en.Kind, en.Text):
			add(ckQuietLine(l, en))
		case en.Kind == "text" && en.Role == "user":
			turn++
			prompts = append(prompts, len(items))
			cached(en, turn, func() []ckLine {
				label := []ckSeg{{t: "You", fg: pal.AccentBright, attrs: ckBold}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}
				if turn > 1 {
					label = append(label, ckSeg{t: sepWord() + "turn " + strconv.Itoa(turn), fg: pal.FgMute})
				}
				ls := []ckLine{{segs: label, label: true}, {bg: l.panel, bar: "▎", barInk: pal.AccentBright}}
				rows, more := threadWrapMore(en.Text, max(width-6, 8))
				for k, s := range rows {
					ls = append(ls, ckLine{bg: l.panel, bar: "▎", barInk: pal.AccentBright, indent: 3, wrap: more[k], segs: []ckSeg{{t: s, fg: pal.Fg}}})
				}
				return append(ls, ckLine{bg: l.panel, bar: "▎", barInk: pal.AccentBright}, ckLine{})
			})
		case en.Kind == "text":
			variant := 0
			if prevText {
				variant = 1
			}
			cached(en, variant, func() []ckLine {
				var ls []ckLine
				if variant == 0 {
					ls = append(ls, ckLine{label: true, segs: []ckSeg{{t: who, fg: pal.FgDim, attrs: ckBold}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}})
				}
				ls = append(ls, m.ckMarkdownFold(l, en.Text, max(width-2, 8), en.ID, t.expandAll || t.folded[en.ID])...)
				return append(ls, ckLine{})
			})
			prevText = true
			continue
		case en.Kind == "thinking":
			add(ckThoughtLines(l, en, steps, i, width, t.expandAll || t.folded[en.ID])...)
		case en.Kind == "todos":
			last := i == m.ckLastTodos(entries) && !pinned
			variant := 0
			if last {
				variant = 1
			}
			cached(en, variant, func() []ckLine { return m.ckPlanCard(l, en, width, last) })
		case en.Kind == "plan":
			cached(en, 0, func() []ckLine { return m.ckPlanText(l, t, en, width) })
		case isEdit:
			cached(en, 0, func() []ckLine { return m.ckDiffCard(l, t, en, width, changes) })
		case en.Kind == "tool_call" && ckReadTool(en.Tool) != "":
			// A run of reads and searches is one line.
			j := i
			var targets []string
			counts := map[string]int{}
			var order []string
			worst := "ok"
			images := 0
			for ; j < len(entries) && (entries[j].Kind == "tool_result" || entries[j].Role == "tool" && entries[j].Image == nil || entries[j].Kind == "tool_call" && ckReadTool(entries[j].Tool) != ""); j++ {
				if entries[j].Role == "tool" {
					// A result, or an image a read gave.
					if entries[j].Kind == "text" {
						images++
					}
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
			if images > 0 {
				what = append(what, plural.CountAs(images, "image", "images"))
			}
			add(m.ckToolLine(l, strings.Join(what, sepWord()), strings.Join(targets, ", "), worst, width))
		case en.Kind == "error":
			ls := []ckLine{m.ckToolLine(l, "Error", "", "error", width)}
			for _, s := range threadWrap(en.Text, max(width-6, 8)) {
				ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
			}
			add(ls...)
		case en.Kind == "tool_call":
			ls := []ckLine{m.ckToolLine(l, printableTitle(en.Tool), printableTitle(ckToolTarget(en.Tool, en.Target)), en.Status, width)}
			if en.Status == transcriptview.StatusDeclined {
				// What the person said when they declined, if anything.
				ls = ckSaidLines(l, ls, results[en.ToolID].Text, width)
			}
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
			ls = append(ls, m.ckPillLines(l, pills[en.ID], t.pill, width)...)
			add(ls...)
		default:
			continue
		}
		prevText = false
	}
	if changes && len(items) == 0 {
		c := (*ckCanvas)(nil).reset(width, 1, l.canvas)
		return []string{ckRender(c, width, ckLine{indent: 2, segs: []ckSeg{{t: "No edits in the last turn.", fg: pal.FgDim}}})}, []string{""}
	}
	// Every item, oldest first. The thread keeps the lines until it, the
	// width or a fold changes; scrolling picks from them.
	var out, hits []string
	for i, it := range items {
		if len(prompts) > 0 && prompts[0] == i {
			prompts = prompts[1:]
			t.prompts = append(t.prompts, len(out))
		}
		out = append(out, it.out...)
		t.lines = append(t.lines, it.lines...)
		for _, ln := range it.lines {
			hits = append(hits, ln.hit)
			t.lineIDs = append(t.lineIDs, it.id)
		}
	}
	return out, hits
}

// ckPrompt reports whether en is drawn as a prompt of the person's.
func ckPrompt(en threadEntry) bool {
	return en.Image == nil && en.Kind == "text" && en.Role == "user" && !transcriptview.Interrupt(en.Role, en.Kind, en.Text)
}

// ckTurns splits a thread's entries into turns, and gives each entry its
// turn. The last turn is finished once the agent rests on its answer.
func ckTurns(e sidebarAgentEntry, entries []threadEntry) ([]transcriptview.Step, []transcriptview.Turn, []int) {
	steps := make([]transcriptview.Step, len(entries))
	for i := range entries {
		en := &entries[i]
		steps[i] = transcriptview.Step{Role: en.Role, Kind: en.Kind, Tool: en.Tool, Status: en.Status, Text: en.Text, Edit: en.Diff != nil, Image: en.Image != nil, At: en.At}
	}
	turns := transcriptview.Turns(steps, e.State != string(session.AgentStateWorking))
	turnOf := make([]int, len(entries))
	for k, tn := range turns {
		for i := tn.Start; i < tn.End; i++ {
			turnOf[i] = k
		}
	}
	return steps, turns, turnOf
}

// ckTurnHead is where a turn's summary line goes: under its prompt, or first
// for a thread that starts inside the turn.
func ckTurnHead(steps []transcriptview.Step, tn *transcriptview.Turn) int {
	if s := steps[tn.Start]; s.Role == "user" && s.Kind == "text" && tn.Start+1 < tn.End {
		// After the prompt and the images pasted with it.
		i := tn.Start + 1
		for i+1 < tn.End && steps[i].Role == "user" && steps[i].Image {
			i++
		}
		return i
	}
	return tn.Start
}

// ckTurnLine is a turn's summary line: the fold mark, what the turn did and
// how long it took. A failure in it is lit. A click opens or closes the turn.
func (m *OS) ckTurnLine(l ckLook, tn *transcriptview.Turn, id, what, span string, width int, open bool) ckLine {
	pal := l.pal
	mark := ckGlyphFold
	if open {
		mark = ckGlyphOpen
	}
	var fail string
	if k := strings.LastIndex(what, " ("); k > 0 && (tn.Failed > 0 || tn.Interrupted || tn.Declined > 0) {
		what, fail = what[:k], what[k:]
	}
	if span != "" {
		span = sepWord() + span
	}
	room := max(width-ansi.StringWidth(fail+span)-4, 8)
	segs := []ckSeg{{t: l.g(mark) + " ", fg: pal.FgMute}, {t: ckTrunc(what, room), fg: pal.FgDim}}
	if fail != "" {
		// A failure is lit. A stop or a decline is the person's own call,
		// so it is only named.
		if tn.Failed > 0 {
			segs = append(segs, ckSeg{t: fail, fg: pal.Warn, attrs: ckBold})
		} else {
			segs = append(segs, ckSeg{t: fail, fg: pal.FgDim})
		}
	}
	if span != "" {
		segs = append(segs, ckSeg{t: span, fg: pal.FgMute})
	}
	return ckLine{segs: segs, hit: "fold:" + id}
}

// ckItem is a card's lines and the lines drawn, and the entry they draw.
type ckItem struct {
	lines []ckLine
	out   []string
	id    string
}

// ckCard is an entry's card as last built, and what it was built from.
type ckCard struct {
	key  ckCardKey
	item ckItem
}

// ckCardKey is what a card's lines depend on beside the entry's id.
type ckCardKey struct {
	width                   int
	theme, look             uint64
	depth                   overlay.Depth
	ascii, nerd             bool
	folded, expand, changes bool
	variant                 int
	// status is a tool call's, which its result can change.
	status string
	// sum is the entry's text hash: a message written again keeps its id.
	sum uint64
}

// ckAddItem appends a card's lines. A card or a new speaker starts after a
// blank line; folded tool lines run together. Kept out of line: the thread
// calls it from every case, and inlined it was most of the thread's code.
//
//go:noinline
func ckAddItem(items []ckItem, it ckItem, blank string) []ckItem {
	if n := len(items); n > 0 && len(it.lines) > 0 && (it.lines[0].bg != nil || it.lines[0].label) {
		if prev := items[n-1]; len(prev.lines) > 0 && !prev.lines[len(prev.lines)-1].blank() {
			// Clipped, so a card the thread keeps is never written into.
			items[n-1] = ckItem{lines: append(slices.Clip(prev.lines), ckLine{}), out: append(slices.Clip(prev.out), blank), id: prev.id}
		}
	}
	return append(items, it)
}

// ckLastTodos is the index of the newest todo list, which is the one drawn
// whole.
func (m *OS) ckLastTodos(entries []threadEntry) int {
	for i, en := range slices.Backward(entries) {
		if en.Kind == "todos" {
			return i
		}
	}
	return -1
}

// ckQuietLine is a line of the thread that is not a card: where the person
// stopped the agent, or an image a tool result holds, under its call. Kept
// out of line, as ckAddItem is.
//
//go:noinline
func ckQuietLine(l ckLook, en threadEntry) ckLine {
	pal := l.pal
	if en.Role == "tool" {
		return ckLine{indent: 4, segs: []ckSeg{{t: ckImageWhat(en, nil), fg: pal.FgMute}}}
	}
	return ckLine{indent: 2, segs: []ckSeg{{t: "You stopped the agent", fg: pal.FgDim, attrs: ckItalic}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}}
}

// ckThoughtLines is a thinking entry: one line that says how long the agent
// thought and starts the thought, and the whole thought under it when open.
// A click on the line opens or closes it; a thought with no text has nothing
// to open.
//
//go:noinline
func ckThoughtLines(l ckLook, en threadEntry, steps []transcriptview.Step, i, width int, open bool) []ckLine {
	pal := l.pal
	what := "Thought"
	if steps != nil {
		if span := transcriptview.ThoughtFor(steps, i); span != "" {
			what += " for " + span
		}
	}
	text := strings.TrimSpace(en.Text)
	mark := ckGlyphFold
	if open && text != "" {
		mark = ckGlyphOpen
	}
	segs := []ckSeg{{t: l.g(mark) + " ", fg: pal.FgMute}, {t: what, fg: pal.FgDim, attrs: ckItalic}}
	line := ckLine{segs: segs}
	if text == "" {
		return []ckLine{line}
	}
	line.hit = "fold:" + en.ID
	if !open {
		first, _, _ := strings.Cut(text, "\n")
		line.segs = append(line.segs, ckSeg{t: "  " + ckTrunc(printableTitle(first), width-ansi.StringWidth(what)-8), fg: pal.FgMute, attrs: ckItalic})
		return []ckLine{line}
	}
	ls := []ckLine{line}
	for _, s := range threadWrap(text, max(width-6, 8)) {
		ls = append(ls, ckLine{indent: 4, hit: line.hit, segs: []ckSeg{{t: s, fg: pal.FgMute, attrs: ckItalic}}})
	}
	return ls
}

// ckTurnWords is a turn's line: how long it worked and what it did, such as
// "Worked for 2m 42s · ran 6 commands and read 3 files". A turn still at
// work says "Working for 3m", in whole minutes, so the line changes once a
// minute and not every second.
func ckTurnWords(tn *transcriptview.Turn, now time.Time) string {
	what := tn.Summary()
	lower := strings.ToLower(what[:1]) + what[1:]
	if !tn.Finished {
		if mins := (now.UnixMilli() - tn.StartAt) / 60000; tn.StartAt > 0 && mins >= 1 {
			return "Working for " + strconv.FormatInt(mins, 10) + "m" + sepWord() + lower
		}
		return "Working" + sepWord() + lower
	}
	if span := tn.Span(); span != "" {
		return "Worked for " + span + sepWord() + lower
	}
	return what
}

// threadPinsTodos reports whether the thread pins its newest todo list
// above itself: the agent works, the Thread tab shows, and the list has an
// item not done.
func (m *OS) threadPinsTodos(e sidebarAgentEntry, t *agentThread) bool {
	if t == nil || e.State != string(session.AgentStateWorking) || m.cockpitTab() != "Thread" {
		return false
	}
	i := m.ckLastTodos(t.entries)
	if i < 0 {
		return false
	}
	for _, td := range t.entries[i].Todos {
		if td.Status != "completed" {
			return true
		}
	}
	return false
}

// ThreadPinnedLines are the lines pinned above the thread of the selected
// agent, in room rows: its newest todo list while it works, drawn whole. It
// is nil when nothing is pinned, or when the list would leave the thread
// fewer than 8 rows. It records whether it pinned, and the thread folds its
// own copy of the list only then.
func (m *OS) ThreadPinnedLines(e sidebarAgentEntry, width, room int) []string {
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil {
		return nil
	}
	pin := m.threadReadsTranscript(e) && m.threadPinsTodos(e, t)
	var ls []ckLine
	l := m.cockpitLook()
	if pin {
		ls = m.ckPlanCard(l, t.entries[m.ckLastTodos(t.entries)], width, true)
		pin = room > len(ls)+8
	}
	if pin != t.pinned {
		t.pinned = pin
		t.gen++
	}
	if !pin {
		return nil
	}
	c := (*ckCanvas)(nil).reset(width, 1, l.canvas)
	out := make([]string, 0, len(ls))
	for _, ln := range ls {
		c = c.reset(width, 1, l.canvas)
		out = append(out, ckRender(c, width, ln))
	}
	return out
}

// ckSaidLines appends what the person said when they declined a call.
//
//go:noinline
func ckSaidLines(l ckLook, ls []ckLine, said string, width int) []ckLine {
	if said == "" {
		return ls
	}
	out := threadWrap(said, max(width-6, 8))
	for _, s := range out[:min(len(out), ckFailShown)] {
		ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: l.pal.FgDim}}})
	}
	return ls
}

// ckWorse is the worse of two tool statuses: an error, then a run, then ok.
func ckWorse(a, b string) string {
	rank := func(s string) int {
		switch s {
		case "error":
			return 3
		case "declined":
			return 2
		case "running":
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
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

// ckToolTarget is what a tool line says the call acted on: for a shell
// call, the program it ran without bash -lc, cd dir && and the like.
func ckToolTarget(tool, target string) string {
	if transcriptview.ShellTool(tool) {
		return transcriptview.CommandLabel(target)
	}
	return target
}

// toolLineLabel is a "Tool: target" line, as the daemon writes what an agent
// or a subagent runs now, with a shell call's target as its command label.
func toolLineLabel(line string) string {
	if tool, target, ok := strings.Cut(line, ": "); ok && transcriptview.ShellTool(tool) {
		return tool + ": " + transcriptview.CommandLabel(target)
	}
	return line
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
	case transcriptview.StatusDeclined:
		// The person's choice, not a failure: no warning colour.
		mark, ink = l.g(ckGlyphIdle), pal.FgMute
		right = []ckSeg{{t: "declined ", fg: pal.FgDim}}
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
		if shown >= limit {
			// Nothing past the limit is drawn, so nothing past it is
			// highlighted: a 400-line diff costs what its first lines do.
			break
		}
		// Only the lines the card shows are made and highlighted.
		take := min(len(h.Lines), limit-shown)
		texts := make([]string, take)
		for i, ln := range h.Lines[:take] {
			texts[i] = ckCodeText(ln.Text, l.ascii)
		}
		spans := ckSideSpans(d.File, h.Lines[:take], texts)
		oldN, newN := h.OldStart, h.NewStart
		if hi > 0 {
			ls = append(ls, ckLine{bg: l.panel, indent: gw + 1, segs: []ckSeg{{t: overlay.Ellipsis(), fg: pal.FgMute}}})
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
				break
			}
			shown++
			// The changed words are in UTF-16 units of the raw text: each
			// goes to bytes of the raw text, then through the tab
			// expansion and the runes the card drops.
			var changed []diffview.Range
			for _, w := range ln.Words {
				changed = append(changed, diffview.Range{
					Start: ckCodeOffset(ln.Text, ckUTF16Byte(ln.Text, w.S), l.ascii),
					End:   ckCodeOffset(ln.Text, ckUTF16Byte(ln.Text, w.E), l.ascii),
				})
			}
			raw := dv.Gutter(n, gw, kind, false) + dv.Sign(kind, false, false) + dv.CodeRanges(texts[i], spans[i], changed, kind, false, 0, codeW)
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

// ckCodeText is a diff line as the card draws it: tabs as four spaces, and
// only the runes the terminal can show.
func ckCodeText(raw string, ascii bool) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case printableRune(r, ascii):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ckCodeOffset is where byte off of raw lands in ckCodeText(raw).
func ckCodeOffset(raw string, off int, ascii bool) int {
	n := 0
	for i, r := range raw {
		if i >= off {
			break
		}
		switch {
		case r == '\t':
			n += 4
		case printableRune(r, ascii):
			n += utf8.RuneLen(r)
		}
	}
	return n
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
	ground := l.panel
	if l.pal.Depth == overlay.Depth16 {
		ground = l.pal.Surface
	}
	ck.dv = diffTheme(l.pal, ground)
	ck.dvKey = key
	return ck.dv
}

// ckRender draws one card line at width on c, a blank canvas one line high.
func ckRender(c *ckCanvas, width int, ln ckLine) string {
	if ln.bg != nil {
		c.fill(ln.bgAt, 0, width-ln.bgAt, 1, ln.bg)
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

// drawThreadActivity draws the live activity row at row y, under the
// thread's newest line, and reports whether it drew one. The row says what
// the agent does now ("Running go test ./... · 3m"), from the daemon's now
// key, which a hook or a protocol pane sets, or from a working program's OSC
// 7501 message. transcriptview.ActivityShown decides when it shows. The time
// is how long the activity has run as of this frame, drawn only when the
// thread saw it start. It rides the redraws the cockpit already makes,
// the minute tick among them, and no timer of its own.
func (m *OS) drawThreadActivity(c *ckCanvas, l ckLook, e sidebarAgentEntry, t *agentThread, x, y, w int, now time.Time) bool {
	if e.Host != "" {
		return false
	}
	tool, target := "", ""
	if v := ckMeta(e, "now"); v != "" {
		tool, target, _ = strings.Cut(v, ": ")
	} else if sum, ok := programSummary(e.Program); ok && sum.State == "working" {
		target = printableTitle(firstNonEmptyStr(sum.Msg, sum.Title))
	}
	var last *transcriptview.Entry
	if t != nil && m.threadReadsTranscript(e) {
		for i := len(t.entries) - 1; i >= 0; i-- {
			if en := &t.entries[i]; en.Kind == transcriptview.KindToolCall {
				last = &transcriptview.Entry{Kind: en.Kind, Tool: en.Tool, Target: en.Target}
				break
			}
		}
	}
	text := ""
	if transcriptview.ActivityShown(e.State, tool, target, last) {
		text = transcriptview.ActivityRow(tool, target)
	}
	if t != nil && (t.act != text || !t.actSeen) {
		// The first frame that draws a thread finds its activity under way,
		// so it has no time. Every later change is a start the thread saw.
		t.act, t.actAt, t.actTimed, t.actSeen = text, now, t.actSeen, true
	}
	if text == "" {
		return false
	}
	pal := l.pal
	verb, rest, _ := strings.Cut(text, " ")
	var right []ckSeg
	if t != nil && t.actTimed {
		if age := agentElapsed("working", t.actAt.UnixNano(), now); age != "" {
			right = []ckSeg{{t: " · " + age, fg: pal.FgMute}}
		}
	}
	room := max(w-ansi.StringWidth(verb)-ckWidth(right...)-5, 4)
	segs := []ckSeg{{t: l.g(ckGlyphWorking) + " ", fg: pal.Busy}, {t: verb, fg: pal.FgDim, attrs: ckBold}}
	if rest != "" {
		segs = append(segs, ckSeg{t: " " + ckTrunc(rest, room), fg: pal.FgMute})
	}
	c.text(c.text(x+2, y, x+w, segs...), y, x+w, right...)
	return true
}
