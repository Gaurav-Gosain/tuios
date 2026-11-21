package app

import (
	"image/color"
	"path"
	"path/filepath"
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

var ckGlyphOpen = ckGlyph{"\ueab4", "˅", "v"}

const ckDiffShown = 8

const ckFailShown = 6

type threadDiff struct {
	File      string       `json:"file"`
	Added     int          `json:"added"`
	Removed   int          `json:"removed"`
	Truncated bool         `json:"truncated"`
	Hunks     []threadHunk `json:"hunks"`
}

type threadHunk struct {
	OldStart int              `json:"old_start"`
	NewStart int              `json:"new_start"`
	Lines    []threadDiffLine `json:"lines"`
}

type threadDiffLine struct {
	Op    string `json:"op"`
	Text  string `json:"text"`
	Words []struct {
		S int `json:"s"`
		E int `json:"e"`
	} `json:"words"`
}

type ckLine struct {
	bg      color.Color
	bgAt    int
	bar     string
	barInk  color.Color
	indent  int
	segs    []ckSeg
	right   []ckSeg
	raw     string
	hit     string
	label   bool
	wrap    bool
	cont    bool
	mark    string
	markAt  int
	markInk color.Color
	lmark   string
	code    string
}

func (ln ckLine) blank() bool {
	return ln.bg == nil && ln.bar == "" && ln.raw == "" && len(ln.segs) == 0
}

func ckReadTool(tool string) string {
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

func (m *OS) threadCardLines(e sidebarAgentEntry, t *agentThread, width int, changes bool, now time.Time) ([]string, []string) {
	l := m.cockpitLook()
	pal := l.pal
	who := ckHarness(e.Harness)
	if who == "" {
		who = "agent"
	}
	t.root = m.ckAgentRoot(e)
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
	var items []ckItem
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
	cur := ""
	add := func(ls ...ckLine) { items = ckAddItem(items, ckItem{lines: ls, out: draw(ls), id: cur}, blank) }
	gen := theme.Generation()
	cached := func(en threadEntry, variant int, build func() []ckLine) {
		key := ckCardKey{width: width, theme: gen, look: m.Inbox.ck.lookGen, ascii: l.ascii, nerd: l.nerd, depth: l.pal.Depth, root: t.root,
			folded: t.folded[en.ID], expand: t.expandAll, changes: changes, variant: variant, status: en.Status, sum: en.sum, codeX: t.codeGen[en.ID], blocks: m.blockGen + m.blockView}
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
	pinned := !changes && t.pinned
	for i := from; i < len(entries); i++ {
		en := ckEntryIn(t.root, entries[i])
		cur = en.ID
		isEdit := en.Kind == "tool_call" && en.Diff != nil
		if changes && (!isEdit || !ckInRoot(t.root, entries[i].Diff.File)) {
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
			if !open && tn.Hidden(steps, i) && !(en.Kind == "tool_call" && len(en.Calls) > 0) {
				continue
			}
		}
		switch {
		case en.Image != nil:
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
				ls = append(ls, m.ckMarkdownFold(l, en.Text, max(width-2, 8), en.ID, t.expandAll || t.folded[en.ID], t)...)
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
			j := i
			var calls []threadEntry
			images := 0
			for ; j < len(entries) && (entries[j].Kind == "tool_result" || entries[j].Role == "tool" && entries[j].Image == nil || entries[j].Kind == "tool_call" && ckReadTool(entries[j].Tool) != ""); j++ {
				if entries[j].Role == "tool" {
					if entries[j].Kind == "text" {
						images++
					}
					continue
				}
				calls = append(calls, ckEntryIn(t.root, entries[j]))
			}
			i = j - 1
			key := threadEntry{ID: en.ID}
			for _, c := range calls {
				key.Text += c.Tool + "\x00" + c.Target + "\x00" + c.Status + "\x00"
				key.Status = ckWorse(key.Status, c.Status)
			}
			key.sum = threadSum(&key)
			open := t.expandAll || t.folded[en.ID]
			cached(key, images, func() []ckLine { return m.ckReadGroup(l, en.ID, calls, images, width, open) })
		case en.Kind == "error":
			ls := []ckLine{ckFoldMark(l, m.ckToolLine(l, "Error", "", "error", width), false, false)}
			for _, s := range threadWrap(en.Text, max(width-6, 8)) {
				ls = append(ls, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
			}
			add(ls...)
		case en.Kind == "tool_call" && len(en.Calls) > 0:
			res := results[en.ToolID]
			open := t.expandAll || t.folded[en.ID]
			card := func() []ckLine { return m.ckTalkCard(l, e, t, en, res, width, open) }
			if p := pills[en.ID]; len(p) > 0 {
				add(append(card(), m.ckPillLines(l, p, t.pill, width)...)...)
				break
			}
			key := en
			key.sum = (en.sum*1099511628211^res.sum)*1099511628211 ^ m.ckTalkSig(e, t, en)
			cached(key, 0, card)
		case en.Kind == "tool_call":
			res := results[en.ToolID]
			open := t.expandAll || t.folded[en.ID]
			card := func() []ckLine { return m.ckToolCard(l, en, res.Text, width, open, t.expandAll) }
			if p := pills[en.ID]; len(p) > 0 {
				add(append(card(), m.ckPillLines(l, p, t.pill, width)...)...)
				break
			}
			key := en
			key.sum = en.sum*1099511628211 ^ res.sum
			cached(key, 0, card)
		default:
			continue
		}
		prevText = false
	}
	if changes && len(items) == 0 {
		c := (*ckCanvas)(nil).reset(width, 1, l.canvas)
		return []string{ckRender(c, width, ckLine{indent: 2, segs: []ckSeg{{t: "No edits in the last turn.", fg: pal.FgDim}}})}, []string{""}
	}
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

func ckPrompt(en threadEntry) bool {
	return en.Image == nil && en.Kind == "text" && en.Role == "user" && !transcriptview.Interrupt(en.Role, en.Kind, en.Text)
}

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

func ckTurnHead(steps []transcriptview.Step, tn *transcriptview.Turn) int {
	if s := steps[tn.Start]; s.Role == "user" && s.Kind == "text" && tn.Start+1 < tn.End {
		i := tn.Start + 1
		for i+1 < tn.End && steps[i].Role == "user" && steps[i].Image {
			i++
		}
		return i
	}
	return tn.Start
}

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

type ckItem struct {
	lines []ckLine
	out   []string
	id    string
}

type ckCard struct {
	key  ckCardKey
	item ckItem
}

type ckCardKey struct {
	width                   int
	theme, look             uint64
	depth                   overlay.Depth
	ascii, nerd             bool
	folded, expand, changes bool
	variant                 int
	status                  string
	sum                     uint64
	root                    string
	codeX                   int
	blocks                  int
}

//go:noinline
func ckAddItem(items []ckItem, it ckItem, blank string) []ckItem {
	if n := len(items); n > 0 && len(it.lines) > 0 && (it.lines[0].bg != nil || it.lines[0].label) {
		if prev := items[n-1]; len(prev.lines) > 0 && !prev.lines[len(prev.lines)-1].blank() {
			items[n-1] = ckItem{lines: append(slices.Clip(prev.lines), ckLine{}), out: append(slices.Clip(prev.out), blank), id: prev.id}
		}
	}
	return append(items, it)
}

func (m *OS) ckLastTodos(entries []threadEntry) int {
	for i, en := range slices.Backward(entries) {
		if en.Kind == "todos" {
			return i
		}
	}
	return -1
}

//go:noinline
func ckQuietLine(l ckLook, en threadEntry) ckLine {
	pal := l.pal
	if en.Role == "tool" {
		return ckLine{indent: 4, segs: []ckSeg{{t: ckImageWhat(en, nil), fg: pal.FgMute}}}
	}
	return ckLine{indent: 2, segs: []ckSeg{{t: "You stopped the agent", fg: pal.FgDim, attrs: ckItalic}, {t: "  " + ckClock(en.At), fg: pal.FgMute}}}
}

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
		return []ckLine{ckFoldMark(l, line, false, false)}
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

func (m *OS) ckAgentRoot(e sidebarAgentEntry) string {
	if p := m.ckWorktreeOf(e).path; p != "" {
		return p
	}
	if e.Host != "" {
		return ""
	}
	if w := m.windowByID(e.WindowID); w != nil {
		return firstNonEmptyStr(w.DaemonCwd, w.Cwd)
	}
	return ""
}

func ckInRoot(root, p string) bool {
	if root == "" || !filepath.IsAbs(p) {
		return true
	}
	root = strings.TrimSuffix(root, "/")
	return p == root || strings.HasPrefix(p, root+"/")
}

func ckRelPath(root, p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	if root == "" {
		if parts := strings.Split(strings.TrimPrefix(p, "/"), "/"); len(parts) > 3 {
			return strings.Join(parts[len(parts)-3:], "/")
		}
		return p
	}
	if ckInRoot(root, p) {
		if rel := strings.TrimPrefix(p[len(strings.TrimSuffix(root, "/")):], "/"); rel != "" {
			return rel
		}
		return "."
	}
	return ckHomePath(p)
}

func ckRelTarget(root, tool, target string) string {
	if transcriptview.ShellTool(tool) || !filepath.IsAbs(strings.TrimSpace(target)) {
		return target
	}
	return ckRelPath(root, strings.TrimSpace(target))
}

func ckEntryIn(root string, en threadEntry) threadEntry {
	en.Target = ckRelTarget(root, en.Tool, en.Target)
	if en.Diff != nil {
		d := *en.Diff
		d.File = ckRelPath(root, d.File)
		en.Diff = &d
	}
	return en
}

func ckClock(at int64) string {
	if at <= 0 {
		return ""
	}
	return time.UnixMilli(at).Format("15:04")
}

func ckToolTarget(tool, target string) string {
	if transcriptview.ShellTool(tool) {
		return transcriptview.CommandLabel(target)
	}
	return target
}

func toolLineLabel(line string) string {
	if tool, target, ok := strings.Cut(line, ": "); ok && transcriptview.ShellTool(tool) {
		return tool + ": " + transcriptview.CommandLabel(target)
	}
	return line
}

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
		mark, ink = l.g(ckGlyphIdle), pal.FgMute
		right = []ckSeg{{t: "declined ", fg: pal.FgDim}}
	}
	right = append(right, ckSeg{t: " " + mark, fg: ink}, ckSeg{t: " "})
	room := max(width-ansi.StringWidth(kind)-ckWidth(right...)-7, 4)
	return ckLine{segs: []ckSeg{{t: l.g(ckGlyphFold) + " ", fg: pal.FgMute}, {t: kind, fg: pal.FgDim, attrs: ckBold},
		{t: "  " + ckTrunc(target, room), fg: pal.FgMute}}, right: right}
}

func ckFoldMark(l ckLook, ln ckLine, more, open bool) ckLine {
	g := ""
	switch {
	case more && open:
		g = l.g(ckGlyphOpen)
	case more:
		g = l.g(ckGlyphFold)
	}
	segs := slices.Clone(ln.segs)
	segs[0].t = ckPad(g, ansi.StringWidth(l.g(ckGlyphFold))) + " "
	ln.segs = segs
	return ln
}

//go:noinline
func (m *OS) ckToolCard(l ckLook, en threadEntry, out string, width int, open, all bool) []ckLine {
	pal := l.pal
	label := printableTitle(ckToolTarget(en.Tool, en.Target))
	head := m.ckToolLine(l, printableTitle(en.Tool), label, en.Status, width)
	hit := "fold:" + en.ID
	cmd := strings.TrimSpace(en.Target)
	cmdMore := cmd != "" && (head.segs[2].t != "  "+label || strings.ContainsRune(cmd, '\n') || printableTitle(cmd) != label)
	wrapW := max(width-8, 8)
	outLines := ckOutputLines(out, wrapW, l.ascii)
	var folded []ckLine
	switch {
	case en.Status == transcriptview.StatusDeclined:
		folded = ckSaidLines(l, nil, out, width)
		outLines = nil
	case en.Status == "error":
		tail := outLines
		if len(tail) > ckFailShown {
			tail = tail[len(tail)-ckFailShown:]
		}
		for _, s := range tail {
			folded = append(folded, ckLine{indent: 4, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
		}
		if len(tail) == len(outLines) {
			outLines = nil
		}
	}
	more := cmdMore || len(outLines) > 0
	head = ckFoldMark(l, head, more, open)
	if more {
		head.hit = hit
	}
	if !more || !open {
		return append([]ckLine{head}, folded...)
	}
	ls := []ckLine{head}
	if cmdMore {
		prompt := ""
		if transcriptview.ShellTool(en.Tool) {
			prompt = "$ "
		}
		for k, s := range threadWrap(cmd, wrapW-len(prompt)) {
			if k == 0 {
				s = prompt + s
			} else {
				s = strings.Repeat(" ", len(prompt)) + s
			}
			ls = append(ls, ckLine{indent: 4, hit: hit, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
		}
	}
	if en.Status == transcriptview.StatusDeclined {
		return append(ls, folded...)
	}
	if en.Status == "error" && len(outLines) == 0 {
		outLines = ckOutputLines(out, wrapW, l.ascii)
	}
	if len(outLines) == 0 {
		word := "No output."
		if en.Status == "running" {
			word = "No output yet."
		}
		return append(ls, ckLine{indent: 4, hit: hit, segs: []ckSeg{{t: word, fg: pal.FgMute, attrs: ckItalic}}})
	}
	shown := outLines
	if !all && len(shown) > ckDiffShown {
		shown = shown[:ckDiffShown]
	}
	for _, s := range shown {
		ls = append(ls, ckLine{indent: 4, hit: hit, segs: []ckSeg{{t: s, fg: pal.FgMute}}})
	}
	if len(shown) < len(outLines) {
		ls = append(ls, ckLine{indent: 4, hit: hit, segs: []ckSeg{{t: strconv.Itoa(len(outLines)-len(shown)) + " more lines   ", fg: pal.FgMute}, {t: "e", fg: pal.AccentBright, attrs: ckBold}, {t: " expand all", fg: pal.FgMute}}})
	}
	return ls
}

func ckOutputLines(text string, width int, ascii bool) []string {
	var out []string
	for line := range strings.SplitSeq(strings.TrimRight(text, " \t\r\n"), "\n") {
		line = strings.TrimRight(ckCodeText(ckExpandTabs(line), ascii), " ")
		body := strings.TrimLeft(line, " ")
		lead := min(len(line)-len(body), width/2)
		if body == "" {
			out = append(out, "")
			continue
		}
		for _, r := range wrapPlain(body, width-lead) {
			out = append(out, strings.Repeat(" ", lead)+r)
		}
	}
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

func ckExpandTabs(line string) string {
	if !strings.Contains(line, "\t") {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	return b.String()
}

//go:noinline
func (m *OS) ckReadGroup(l ckLook, id string, calls []threadEntry, images, width int, open bool) []ckLine {
	pal := l.pal
	counts := map[string]int{}
	var order, targets []string
	worst := "ok"
	for _, c := range calls {
		k := ckReadTool(c.Tool)
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
		targets = append(targets, path.Base(printableTitle(c.Target)))
		worst = ckWorse(worst, c.Status)
	}
	var what []string
	for _, k := range order {
		what = append(what, k+" "+strconv.Itoa(counts[k]))
	}
	if images > 0 {
		what = append(what, plural.CountAs(images, "image", "images"))
	}
	names := strings.Join(targets, ", ")
	head := m.ckToolLine(l, strings.Join(what, sepWord()), names, worst, width)
	more := len(calls) > 1 || head.segs[2].t != "  "+names
	if len(calls) == 1 {
		more = more || printableTitle(calls[0].Target) != names
	}
	head = ckFoldMark(l, head, more, open)
	if !more {
		return []ckLine{head}
	}
	head.hit = "fold:" + id
	if !open {
		return []ckLine{head}
	}
	ls := []ckLine{head}
	kw := 0
	for _, k := range order {
		kw = max(kw, ansi.StringWidth(k))
	}
	for _, c := range calls {
		k := ckReadTool(c.Tool)
		var right []ckSeg
		if c.Status != "ok" && c.Status != "" {
			right = m.ckToolLine(l, "", "", c.Status, width).right
		}
		room := max(width-kw-ckWidth(right...)-8, 4)
		ls = append(ls, ckLine{indent: 4, hit: head.hit, right: right, segs: []ckSeg{{t: ckPad(k, kw), fg: pal.FgMute}, {t: "  " + ckTruncMid(printableTitle(c.Target), room), fg: pal.FgDim}}})
	}
	return ls
}

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

func (m *OS) ckPlanText(l ckLook, t *agentThread, en threadEntry, width int) []ckLine {
	pal := l.pal
	var lines []ckLine
	if strings.TrimSpace(en.Plan) != "" {
		lines = m.ckMarkdown(l, en.Plan, max(width-6, 8))
	}
	open := !t.folded[en.ID] && len(lines) > 0
	mark := ckGlyphOpen
	if !open {
		mark = ckGlyphFold
	}
	head := []ckSeg{{t: l.g(mark) + " ", fg: pal.FgDim}, {t: "Plan", fg: pal.FgDim, attrs: ckBold}}
	if len(lines) > 0 {
		head = append(head, ckSeg{t: "   " + plural.Count(len(lines), "line"), fg: pal.FgMute})
	}
	ls := []ckLine{{bg: l.panel, indent: 1, hit: "fold:" + en.ID, segs: head}}
	if !open {
		return append(ls, ckLine{})
	}
	shown := lines
	if !t.expandAll && len(shown) > ckDiffShown {
		shown = shown[:ckDiffShown]
	}
	for _, ln := range shown {
		if ln.bg == nil {
			ln.bg, ln.bgAt = l.panel, 0
		} else {
			ln.bgAt += 3
		}
		ln.indent += 3
		ls = append(ls, ln)
	}
	if len(shown) < len(lines) {
		ls = append(ls, ckLine{bg: l.panel, indent: 3, segs: []ckSeg{{t: strconv.Itoa(len(lines)-len(shown)) + " more lines   ", fg: pal.FgMute}, {t: "e", fg: pal.AccentBright, attrs: ckBold}, {t: " expand all", fg: pal.FgMute}}})
	}
	return append(ls, ckLine{bg: l.panel}, ckLine{})
}

func (m *OS) ckDiffCard(l ckLook, t *agentThread, en threadEntry, width int, whole bool) []ckLine {
	pal := l.pal
	d := en.Diff
	open := !t.folded[en.ID] || whole
	mark := ckGlyphOpen
	if !open {
		mark = ckGlyphFold
	}
	file := ckTruncMid(printableTitle(d.File), max(width-24, 8))
	right := ckDiffCounts(l, d.Added, d.Removed)
	st := m.ckToolLine(l, "", "", en.Status, width)
	right = append(right, st.right...)
	head := ckLine{bg: l.panel, indent: 1, hit: "fold:" + en.ID, right: right,
		segs: []ckSeg{{t: l.g(mark) + " ", fg: pal.FgDim}, {t: printableTitle(en.Tool), fg: pal.FgDim, attrs: ckBold},
			{t: "   " + file, fg: pal.Fg}}}
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
			break
		}
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
	if ln.mark != "" {
		c.text(ln.markAt, 0, width, ckSeg{t: ln.mark, fg: ln.markInk})
	}
	if ln.lmark != "" && ln.indent > 0 {
		c.text(ln.indent-1, 0, width, ckSeg{t: ln.lmark, fg: ln.markInk})
	}
	return c.render()
}

func (m *OS) threadActivityLine(l ckLook, e sidebarAgentEntry, t *agentThread, width int, now time.Time) (string, bool) {
	if e.Host != "" {
		return "", false
	}
	tool, target := "", ""
	if v := ckMeta(e, "now"); v != "" {
		tool, target, _ = strings.Cut(v, ": ")
		target = ckRelTarget(m.ckAgentRoot(e), tool, target)
	} else if sum, ok := programSummary(e.Program); ok && sum.State == "working" {
		target = printableTitle(firstNonEmptyStr(sum.Msg, sum.Title))
	}
	var last *transcriptview.Entry
	reads := false
	if t != nil && m.threadReadsTranscript(e) {
		for i := len(t.entries) - 1; i >= 0; i-- {
			if en := &t.entries[i]; en.Kind == transcriptview.KindToolCall {
				last = &transcriptview.Entry{Kind: en.Kind, Tool: en.Tool, Target: en.Target}
				reads = ckReadTool(en.Tool) != "" && ckJoinsReads(t.entries[i+1:])
				break
			}
		}
	}
	text := ""
	if transcriptview.ActivityShown(e.State, tool, target, last) {
		text = transcriptview.ActivityRow(tool, target)
	}
	if t != nil && (t.act != text || !t.actSeen) {
		t.act, t.actAt, t.actTimed, t.actSeen = text, now, t.actSeen, true
	}
	if text == "" || reads && ckReadTool(tool) != "" {
		return "", false
	}
	if tool == "" {
		tool = transcriptview.ActivityWorking
	}
	en := threadEntry{Kind: transcriptview.KindToolCall, Tool: tool, Target: target, Status: "running"}
	var ln ckLine
	if ckReadTool(tool) != "" {
		ln = m.ckReadGroup(l, "", []threadEntry{en}, 0, width, false)[0]
	} else {
		ln = m.ckToolCard(l, en, "", width, false, false)[0]
	}
	ln.hit = ""
	ln = ckFoldMark(l, ln, false, false)
	if t != nil && t.actTimed {
		if age := agentElapsed("working", t.actAt.UnixNano(), now); age != "" && !strings.HasPrefix(age, "<") {
			ln.right = append([]ckSeg{{t: age + "  ", fg: l.pal.FgMute}}, ln.right...)
		}
	}
	return ckRender((*ckCanvas)(nil).reset(width, 1, l.canvas), width, ln), true
}

func ckJoinsReads(entries []threadEntry) bool {
	for _, en := range entries {
		if en.Kind != "tool_result" && (en.Role != "tool" || en.Image != nil) {
			return false
		}
	}
	return true
}
