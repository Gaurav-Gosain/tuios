package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The thread: the selected agent's conversation, read with agent-transcript
// (decoded by internal/transcriptview in the daemon) and drawn as a chat.
// The person's prompts sit in bubbles on the right; the agent's answers are
// Markdown (internal/markdown) with code highlighted by internal/diffview;
// tool calls fold to one line each, consecutive reads and searches join one
// row, and edits open on their diff.
//
// Cost: entries are read once and then only those after the cursor, when
// the daemon tells the transcript grew. Markdown is parsed once per entry,
// code highlighted once per block. The list builds only the items in view,
// and nothing draws while nothing changes.

// ckThreadKeep is how many agents' threads stay read, the TUI's 3.
const ckThreadKeep = 3

// ckThreadMax bounds the entries a thread keeps: the oldest go first.
const ckThreadMax = 2000

// ckThread is one agent's conversation as read.
type ckThread struct {
	agent   client.Agent
	entries []transcriptview.Entry
	cursor  string
	older   string
	more    bool
	loading bool
	err     string
	usedAt  time.Time

	items []ckItem
	list  ui.ListState
	// scroll moves the list on the frame clock (smoothScroll).
	scroll smoothScroll
	// open holds the tool items the person unfolded, by item key; shut the
	// edits they folded.
	open, shut map[string]bool
	// md holds each text entry's blocks, parsed once, by entry id.
	md map[string][]markdown.Block
}

type ckItemKind uint8

const (
	ckItemUser ckItemKind = iota
	ckItemText
	ckItemThinking
	ckItemTools // reads and searches joined
	ckItemTool
	ckItemEdit
	ckItemTodos
	ckItemPlan
	ckItemError
	ckItemOlder
)

// ckItem is one item of the chat: an entry, or several joined.
type ckItem struct {
	kind  ckItemKind
	key   string
	at    int64
	e     transcriptview.Entry
	calls []transcriptview.Entry // the joined reads and searches
	res   *transcriptview.Entry  // a call's result
	md    []markdown.Block
	label bool // the first of a turn's answers, which carries the agent's name
}

// threadKey names an agent's thread.
func threadKey(ag client.Agent) string { return ag.Session + "\x00" + ag.Window }

// fetchThread reads the agent's conversation: all of it when fresh (the
// newest page), else what came after the cursor.
func (a *App) fetchThread(ag client.Agent, fresh bool) {
	cl := a.cockpitClient()
	if cl == nil || ag.Window == "" {
		return
	}
	ck := &a.ck
	if ck.threads == nil {
		ck.threads = map[string]*ckThread{}
	}
	key := threadKey(ag)
	th := ck.threads[key]
	if th == nil {
		th = &ckThread{agent: ag, open: map[string]bool{}, shut: map[string]bool{}, md: map[string][]markdown.Block{}}
		th.list.FollowEnd = true
		th.list.Key = func(i int) any { return th.items[i].key }
		ck.threads[key] = th
		a.dropOldThreads()
		fresh = true
	} else if fresh && th.cursor != "" {
		fresh = false
	}
	th.agent, th.usedAt = ag, time.Now()
	if th.loading {
		return
	}
	th.loading = true
	p := map[string]any{"session": ag.Session, "window": ag.Window, "limit": 400}
	if !fresh && th.cursor != "" {
		p["after"] = th.cursor
	}
	go func() {
		var page struct {
			Entries []transcriptview.Entry `json:"entries"`
			Cursor  string                 `json:"cursor"`
			Reset   bool                   `json:"reset"`
			More    bool                   `json:"more"`
			Older   string                 `json:"older"`
		}
		err := cl.VerbInto("agent-transcript", p, true, &page)
		a.update(func() {
			th.loading = false
			if err != nil {
				th.err = ckTranscriptError(err)
				return
			}
			th.err = ""
			if page.Reset || p["after"] == nil {
				th.entries = page.Entries
				th.older, th.more = page.Older, page.More
			} else {
				th.entries = append(th.entries, page.Entries...)
			}
			if n := len(th.entries); n > ckThreadMax {
				th.entries = th.entries[n-ckThreadMax:]
			}
			if page.Cursor != "" {
				th.cursor = page.Cursor
			}
			th.items = buildItems(th.entries, th.more, th.md)
		})
	}()
}

// fetchOlder reads the page before the oldest entry read.
func (a *App) fetchOlder(th *ckThread) {
	cl := a.cockpitClient()
	if cl == nil || th.loading || th.older == "" {
		return
	}
	th.loading = true
	ag := th.agent
	go func() {
		var page struct {
			Entries []transcriptview.Entry `json:"entries"`
			More    bool                   `json:"more"`
			Older   string                 `json:"older"`
		}
		err := cl.VerbInto("agent-transcript", map[string]any{"session": ag.Session, "window": ag.Window, "before": th.older, "limit": 400}, true, &page)
		a.update(func() {
			th.loading = false
			if err != nil {
				th.err = ckTranscriptError(err)
				return
			}
			th.entries = append(page.Entries, th.entries...)
			th.older, th.more = page.Older, page.More
			th.items = buildItems(th.entries, th.more, th.md)
		})
	}()
}

// ckTranscriptError says why a thread cannot be read, in the app's words.
func ckTranscriptError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "no transcript") || strings.Contains(s, "not joined") || strings.Contains(s, "no_transcript"):
		return locales.CockpitNoTranscript()
	case strings.Contains(s, "not_human"):
		return locales.CockpitNotHuman()
	}
	return s
}

// dropOldThreads keeps the ckThreadKeep threads used last.
func (a *App) dropOldThreads() {
	for len(a.ck.threads) > ckThreadKeep {
		var oldest string
		var at time.Time
		for k, th := range a.ck.threads {
			if oldest == "" || th.usedAt.Before(at) {
				oldest, at = k, th.usedAt
			}
		}
		delete(a.ck.threads, oldest)
	}
}

// ckFold reports whether a tool is a read or a search, which fold together.
func ckFold(tool string) bool {
	switch tool {
	case "Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch", "read_file", "list_directory", "glob", "search_file_content", "grep", "read", "list", "view":
		return true
	}
	return false
}

// buildItems turns entries into the chat's items: results join their calls,
// reads and searches in a row join one item.
func buildItems(entries []transcriptview.Entry, more bool, mdc map[string][]markdown.Block) []ckItem {
	parse := func(id, src string) []markdown.Block {
		if b, ok := mdc[id]; ok && id != "" {
			return b
		}
		b, _ := markdown.Parse(src)
		if id != "" {
			mdc[id] = b
		}
		return b
	}
	results := map[string]int{}
	for i, e := range entries {
		if e.Kind == transcriptview.KindToolResult && e.ToolID != "" {
			results[e.ToolID] = i
		}
	}
	calls := map[string]bool{}
	for _, e := range entries {
		if e.Kind != transcriptview.KindToolResult && e.ToolID != "" {
			calls[e.ToolID] = true
		}
	}
	var items []ckItem
	if more {
		items = append(items, ckItem{kind: ckItemOlder, key: "older"})
	}
	lastRole := ""
	for i := range entries {
		e := entries[i]
		var it ckItem
		it.e, it.key, it.at = e, e.ID, e.At
		if it.key == "" {
			it.key = fmt.Sprint("e", i)
		}
		switch {
		case e.Kind == transcriptview.KindToolResult:
			if calls[e.ToolID] {
				// Drawn with its call.
				continue
			}
			it.kind = ckItemTool
		case e.Role == transcriptview.RoleUser && e.Kind == transcriptview.KindText:
			it.kind = ckItemUser
		case e.Kind == transcriptview.KindText:
			it.kind = ckItemText
			it.md = parse(e.ID, e.Text)
			it.label = lastRole != "assistant"
		case e.Kind == transcriptview.KindThinking:
			it.kind = ckItemThinking
		case e.Kind == transcriptview.KindTodos:
			it.kind = ckItemTodos
		case e.Kind == transcriptview.KindPlan:
			it.kind = ckItemPlan
			it.md = parse(e.ID, e.Plan)
		case e.Kind == transcriptview.KindError:
			it.kind = ckItemError
		case e.Kind == transcriptview.KindToolCall && e.Diff != nil:
			it.kind = ckItemEdit
		case e.Kind == transcriptview.KindToolCall && ckFold(e.Tool):
			it.kind = ckItemTools
			it.calls = []transcriptview.Entry{e}
		default:
			it.kind = ckItemTool
		}
		if e.ToolID != "" && e.Kind != transcriptview.KindToolResult {
			if j, ok := results[e.ToolID]; ok {
				r := entries[j]
				it.res = &r
				if it.kind == ckItemEdit && r.Diff != nil && it.e.Diff == nil {
					it.e.Diff = r.Diff
				}
			}
		}
		if it.kind == ckItemTools && len(items) > 0 {
			if prev := &items[len(items)-1]; prev.kind == ckItemTools {
				prev.calls = append(prev.calls, e)
				continue
			}
		}
		if it.kind == ckItemUser {
			lastRole = "user"
		} else {
			lastRole = "assistant"
		}
		items = append(items, it)
	}
	return items
}

// threadView draws the selected agent's thread.
func (a *App) threadView(c *ui.Context, pal *palette, ag client.Agent) {
	th := a.ck.threads[threadKey(ag)]
	if th == nil {
		a.fetchThread(ag, true)
		th = a.ck.threads[threadKey(ag)]
	}
	// Every thread with no messages shows the same empty state, with its
	// icon tile, whatever the reason (council round 1, visual 6).
	switch {
	case th == nil:
		a.ckEmptyThread(c, pal, locales.CockpitNoTranscript(), locales.CockpitNoTranscriptDetail())
		return
	case len(th.items) == 0 && th.loading:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		return
	case len(th.items) == 0 && th.err != "":
		a.ckEmptyThread(c, pal, th.err, locales.CockpitNoTranscriptDetail())
		return
	case len(th.items) == 0:
		a.ckEmptyThread(c, pal, locales.CockpitThreadEmpty(), "")
		return
	}
	mono := a.termFamily()
	list := ui.List(c.Key("thread-"+threadKey(ag)), &th.list, len(th.items))
	th.scroll.track(c, list).Grow(1).MinHeight(0).Padding(18, a.ckSide(28), 22, a.ckSide(28)).Gap(14).Selectable().CopyData(copyThread).
		Rows(func(r ui.ListRow) { a.itemView(r.Context, pal, th, &th.items[r.Index], mono) })
}

// threadIcon is a speech bubble, for a thread's empty state.
var threadIcon = icon(`<path d="M3 3.5h10a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H8l-3 2.5v-2.5H3a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1z"/>`)

// ckEmptyThread is a thread's empty state: it fills the tab's room, so
// that a thread with nothing to show is never a blank column.
func (a *App) ckEmptyThread(c *ui.Context, pal *palette, title, detail string) {
	ui.Column(c.Key("thread-empty")).Grow(1).MinHeight(0).Children(func() {
		emptyBlock(c, pal, threadIcon, title, detail, false, nil)
	})
}

// itemView draws one chat item.
func (a *App) itemView(c *ui.Context, pal *palette, th *ckThread, it *ckItem, mono string) {
	switch it.kind {
	case ckItemOlder:
		ui.Row(c).Center().Children(func() {
			if button(c, "older", locales.CockpitOlder(), "", false, pal) {
				a.fetchOlder(th)
			}
		})
	case ckItemUser:
		// The person's prompt, in a bubble on the right.
		ui.Column(c).AlignItems(ui.End).Gap(4).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, locales.CockpitYou()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted)
				ui.Text(c, clock(it.at)).FontSize(fsSmall).TextColor(pal.textFaint)
			})
			ui.Text(c, it.e.Text).FontSize(fsProse).LineHeight(1.55).TextColor(pal.text).MaxWidthPercent(82).
				Padding(10, 14).Radius(14, 14, 4, 14).Background(pal.accent.Alpha(0.16))
		})
	case ckItemText:
		ui.Column(c).Gap(8).Children(func() {
			if it.label {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, harnessIcon(th.agent.Harness)).Size(14, 14).TextColor(pal.textMuted)
					ui.Text(c, harnessName(th.agent.Harness)).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted)
					ui.Text(c, clock(it.at)).FontSize(fsSmall).TextColor(pal.textFaint)
				})
			}
			a.markdownView(c, pal, it.md, mono)
		})
	case ckItemThinking:
		a.foldItem(c, pal, th, it, func() {
			ui.Text(c, locales.CockpitThought()).FontSize(fsSmall).Italic().TextColor(pal.textMuted)
		}, func() {
			ui.Text(c, it.e.Text).FontSize(fsSmall).Italic().LineHeight(1.5).TextColor(pal.textMuted)
		})
	case ckItemTools:
		a.toolsItem(c, pal, th, it, mono)
	case ckItemTool:
		a.toolItem(c, pal, th, it, mono)
	case ckItemEdit:
		a.editItem(c, pal, th, it, mono)
	case ckItemTodos:
		a.todosItem(c, pal, it)
	case ckItemPlan:
		ui.Column(c).Gap(8).Padding(12, 14).Radius(rCard).Background(ckGround(pal)).Children(func() {
			ui.Text(c, locales.CockpitPlan()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted)
			a.markdownView(c, pal, it.md, mono)
		})
	case ckItemError:
		ui.Row(c).Gap(8).Padding(8, 12).Radius(rControl).Background(pal.danger.Alpha(0.12)).Children(func() {
			ui.Text(c, "✕").FontSize(fsSmall).TextColor(pal.danger)
			ui.Text(c, it.e.Text).FontSize(fsBody).TextColor(pal.text).Grow(1).MinWidth(0)
		})
	}
}

// foldItem is an item drawn as one line that a click opens.
func (a *App) foldItem(c *ui.Context, pal *palette, th *ckThread, it *ckItem, head, body func()) {
	open := th.open[it.key]
	ui.Column(c).Gap(6).Children(func() {
		row := ui.Row(c.Key("h")).Gap(7).AlignItems(ui.Center).Cursor(ui.CursorPointer).Unselectable()
		if row.Clicked() {
			c.Invalidate()
			th.open[it.key] = !open
		}
		row.Children(func() {
			ic := chevronIcon
			if open {
				ic = chevronDownIcon
			}
			ui.Icon(c, ic).Size(12, 12).TextColor(pal.textFaint)
			head()
		})
		if open {
			ui.Column(c).Padding(0, 0, 0, 17).Children(body)
		}
	})
}

// statusMark is a call's status: a check, a cross, or a dot while it runs.
func statusMark(c *ui.Context, pal *palette, status string) {
	switch status {
	case transcriptview.StatusError:
		ui.Text(c, "✕").FontSize(fsSmall).TextColor(pal.danger)
	case transcriptview.StatusRunning:
		ui.Text(c, "●").FontSize(fsCaption).TextColor(pal.accent)
	case "":
	default:
		ui.Text(c, "✓").FontSize(fsSmall).TextColor(pal.done)
	}
}

func callStatus(it *ckItem) string {
	if it.res != nil {
		return it.res.Status
	}
	return it.e.Status
}

// toolsItem is reads and searches in a row, joined to one line.
func (a *App) toolsItem(c *ui.Context, pal *palette, th *ckThread, it *ckItem, mono string) {
	counts := map[string]int{}
	var order []string
	var targets []string
	for _, e := range it.calls {
		if counts[e.Tool] == 0 {
			order = append(order, e.Tool)
		}
		counts[e.Tool]++
		targets = append(targets, e.Target)
	}
	var head []string
	for _, t := range order {
		if counts[t] > 1 {
			head = append(head, fmt.Sprintf("%s %d", t, counts[t]))
		} else {
			head = append(head, t)
		}
	}
	a.foldItem(c, pal, th, it, func() {
		ui.Text(c, strings.Join(head, " · ")).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).Shrink(0)
		ui.Text(c, strings.Join(targets, ", ")).Font(mono).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
		statusMark(c, pal, transcriptview.StatusOK)
	}, func() {
		for _, e := range it.calls {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, e.Tool).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).Shrink(0)
				ui.Text(c, e.Target).Font(mono).FontSize(fsSmall).TextColor(pal.text).MinWidth(0)
			})
		}
	})
}

// toolItem is one call with its result: folded, except a failed one.
func (a *App) toolItem(c *ui.Context, pal *palette, th *ckThread, it *ckItem, mono string) {
	status := callStatus(it)
	failed := status == transcriptview.StatusError
	if failed && !th.shut[it.key] && !th.open[it.key] {
		th.open[it.key] = true
	}
	tool := it.e.Tool
	if tool == "" {
		tool = locales.CockpitResult()
	}
	a.foldItem(c, pal, th, it, func() {
		ui.Text(c, tool).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).Shrink(0)
		ui.Text(c, it.e.Target).Font(mono).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
		if failed {
			ui.Text(c, locales.CockpitFailedPill()).FontSize(fsCaption).FontWeight(wSemibold).TextColor(pal.danger).Padding(1, 7).Radius(rChip).Background(pal.danger.Alpha(0.14))
		} else {
			statusMark(c, pal, status)
		}
	}, func() {
		out := it.e.Text
		if it.res != nil {
			out = it.res.Text
		}
		if strings.TrimSpace(out) == "" {
			ui.Text(c, locales.CockpitNoOutput()).FontSize(fsSmall).TextColor(pal.textFaint)
			return
		}
		ui.ScrollHorizontal(c).Radius(rControl).Background(ckCodeBg(pal)).Children(func() {
			ui.Text(c, strings.ReplaceAll(strings.TrimRight(out, "\n"), "\t", "    ")).Font(mono).FontSize(fsCode).LineHeight(1.5).NoWrap().TextColor(pal.text).Padding(10, 12)
		})
	})
}

// todosItem is a todo list as a checklist with its progress.
func (a *App) todosItem(c *ui.Context, pal *palette, it *ckItem) {
	done := 0
	for _, t := range it.e.Todos {
		if t.Status == "completed" {
			done++
		}
	}
	ui.Column(c).Gap(6).Padding(12, 14).Radius(rCard).Background(ckGround(pal)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.CockpitTodos()).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text)
			ui.Text(c, locales.CockpitTodosDone(done, len(it.e.Todos))).FontSize(fsSmall).TextColor(pal.textMuted)
			ui.Spacer(c)
			frac := float32(0)
			if n := len(it.e.Todos); n > 0 {
				frac = float32(done) / float32(n)
			}
			ui.Box(c).Width(80).Height(4).Radius(rChip).Background(pal.divider).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, pal.accent, 2)
			})
		})
		for _, t := range it.e.Todos {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				switch t.Status {
				case "completed":
					ui.Text(c, "✓").FontSize(fsSmall).TextColor(pal.done).Width(14)
					ui.Text(c, t.Text).FontSize(fsBody).TextColor(pal.textFaint).Strikethrough()
				case "in_progress":
					ui.Text(c, "●").FontSize(fsCaption).TextColor(pal.accent).Width(14)
					ui.Text(c, t.Text).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text)
				default:
					ui.Text(c, "○").FontSize(fsSmall).TextColor(pal.textFaint).Width(14)
					ui.Text(c, t.Text).FontSize(fsBody).TextColor(pal.text)
				}
			})
		}
	})
}

// ckEditLines is how many diff lines an edit shows before "Show all".
const ckEditLines = 12

// editItem is an edit, open on its diff.
func (a *App) editItem(c *ui.Context, pal *palette, th *ckThread, it *ckItem, mono string) {
	d := it.e.Diff
	open := !th.shut[it.key]
	ui.Column(c).Radius(rControl).Background(ckCodeBg(pal)).Clip().Children(func() {
		row := ui.Row(c.Key("h")).Gap(8).AlignItems(ui.Center).Padding(8, 12).Cursor(ui.CursorPointer).Unselectable()
		if row.Clicked() {
			c.Invalidate()
			th.shut[it.key] = open
		}
		row.Children(func() {
			ic := chevronIcon
			if open {
				ic = chevronDownIcon
			}
			ui.Icon(c, ic).Size(12, 12).TextColor(pal.textFaint)
			ui.Text(c, it.e.Tool).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text)
			ui.Text(c, d.File).Font(mono).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
			added, removed := a.diffColorsFor(pal)
			if d.Added > 0 {
				ui.Text(c, fmt.Sprint("+", d.Added)).Font(mono).FontSize(fsSmall).TextColor(added)
			}
			if d.Removed > 0 {
				ui.Text(c, fmt.Sprint("−", d.Removed)).Font(mono).FontSize(fsSmall).TextColor(removed)
			}
			statusMark(c, pal, callStatus(it))
		})
		if open {
			a.diffView(c, pal, d, th.open[it.key+"+all"], mono)
			if n := diffLineCount(d); n > ckEditLines && !th.open[it.key+"+all"] {
				r := ui.Row(c.Key("all")).Padding(5, 12).Cursor(ui.CursorPointer).Unselectable()
				if r.Clicked() {
					c.Invalidate()
					th.open[it.key+"+all"] = true
				}
				r.Children(func() {
					ui.Text(c, locales.CockpitShowAllLines(n)).FontSize(fsSmall).TextColor(pal.accent)
				})
			}
		}
	})
}

func diffLineCount(d *transcriptview.Diff) int {
	n := 0
	for _, h := range d.Hunks {
		n += len(h.Lines)
	}
	return n
}

// diffColorsFor is the added and removed colours.
func (a *App) diffColorsFor(pal *palette) (ui.Color, ui.Color) {
	added, removed, _ := a.diffColors(pal)
	return added, removed
}

// diffView draws a diff's hunks: line numbers, the sign, the code
// highlighted, the changed words lit.
func (a *App) diffView(c *ui.Context, pal *palette, d *transcriptview.Diff, all bool, mono string) {
	added, removed := a.diffColorsFor(pal)
	syn := a.syntaxColors(c, pal)
	shown := 0
	sc := ui.ScrollHorizontal(c)
	// The lines' colors run the card's width, however short the lines:
	// a percent of a sideways scroll's content is of the content.
	view := sc.Bounds().W
	sc.Children(func() {
		ui.Column(c).Padding(0, 0, 6, 0).MinWidth(view).AlignItems(ui.Stretch).Children(func() {
			for hi, h := range d.Hunks {
				if hi > 0 {
					ui.Text(c, "⋯").FontSize(fsCaption).TextColor(pal.textFaint).Padding(0, 12)
				}
				texts := make([]string, len(h.Lines))
				for i, l := range h.Lines {
					texts[i] = l.Text
				}
				spans := cachedSpans(&d.Hunks[hi], func() [][]diffview.Span { return diffview.Highlight(d.File, texts) })
				oldN, newN := h.OldStart, h.NewStart
				for i, l := range h.Lines {
					if !all && shown >= ckEditLines {
						return
					}
					shown++
					num, sign, bg, word := newN, " ", ui.Transparent, ui.Transparent
					switch l.Op {
					case "+":
						sign, bg, word = "+", added.Alpha(0.13), added.Alpha(0.32)
						newN++
					case "-":
						num, sign, bg, word = oldN, "−", removed.Alpha(0.13), removed.Alpha(0.32)
						oldN++
					default:
						oldN++
						newN++
					}
					var sp []diffview.Span
					if i < len(spans) {
						sp = spans[i]
					}
					runs := codeSpans(l.Text, sp, syn, pal.text)
					runs = litWords(runs, l.Text, l.Words, word)
					ui.Row(c).Background(bg).Children(func() {
						ui.Text(c, fmt.Sprint(num)).Font(mono).FontSize(fsSmall).TextColor(pal.textFaint).Width(40).TextAlign(ui.End).Padding(0, 8, 0, 0).FontFeatures("tnum").Unselectable()
						ui.Text(c, sign).Font(mono).FontSize(fsSmall).TextColor(pal.textMuted).Width(16).Unselectable()
						ui.RichText(c, detab(runs)...).Font(mono).FontSize(fsCode).LineHeight(1.5).NoWrap().Padding(0, 12, 0, 0)
					})
				}
			}
		})
	})
}

// syntaxColors are the colours of diffview's classes, from the code theme.
func (a *App) syntaxColors(c *ui.Context, pal *palette) [diffview.NumClasses]ui.Color {
	t := a.codeTheme(c, pal)
	var out [diffview.NumClasses]ui.Color
	for cl := range diffview.NumClasses {
		out[cl] = pal.text
	}
	tok := func(k int) ui.Color {
		if col := t.Tokens[k].Color; col.A > 0 {
			return col
		}
		return pal.text
	}
	// diffview's classes as the editor's token kinds.
	out[diffview.Keyword] = tok(1)
	out[diffview.Type] = tok(2)
	out[diffview.Func] = tok(3)
	out[diffview.Builtin] = tok(3)
	out[diffview.String] = tok(8)
	out[diffview.Number] = tok(7)
	out[diffview.Comment] = tok(11)
	out[diffview.Operator] = tok(12)
	out[diffview.Punct] = tok(13)
	out[diffview.Preproc] = tok(16)
	out[diffview.Tag] = tok(14)
	out[diffview.Attr] = tok(15)
	return out
}

// codeSpans cuts a line into runs by its highlight spans.
func codeSpans(text string, spans []diffview.Span, syn [diffview.NumClasses]ui.Color, plain ui.Color) []ui.Span {
	if text == "" {
		return []ui.Span{{Text: " ", Color: plain}}
	}
	var out []ui.Span
	at := 0
	for _, s := range spans {
		if s.Start < at || s.End > len(text) || s.Start >= s.End {
			continue
		}
		if s.Start > at {
			out = append(out, ui.Span{Text: text[at:s.Start], Color: plain})
		}
		out = append(out, ui.Span{Text: text[s.Start:s.End], Color: syn[s.Class]})
		at = s.End
	}
	if at < len(text) {
		out = append(out, ui.Span{Text: text[at:], Color: plain})
	}
	return out
}

// litWords puts a ground behind the changed words of a line. Words are in
// UTF-16 code units of text.
func litWords(runs []ui.Span, text string, words []transcriptview.Range, bg ui.Color) []ui.Span {
	if len(words) == 0 {
		return runs
	}
	// Byte offsets of each UTF-16 unit.
	var at []int
	for i, r := range text {
		at = append(at, i)
		if r >= 0x10000 {
			at = append(at, i)
		}
	}
	at = append(at, len(text))
	lit := make([]bool, len(text)+1)
	for _, w := range words {
		s, e := w.S, w.E
		if s < 0 || e > len(at)-1 || s >= e {
			continue
		}
		for b := at[s]; b < at[e]; b++ {
			lit[b] = true
		}
	}
	var out []ui.Span
	off := 0
	for _, r := range runs {
		start := 0
		for i := 1; i <= len(r.Text); i++ {
			if i == len(r.Text) || lit[off+i] != lit[off+start] {
				s := r
				s.Text = r.Text[start:i]
				if lit[off+start] {
					s.Background, s.Radius = bg, 3
				}
				out = append(out, s)
				start = i
			}
		}
		off += len(r.Text)
	}
	return out
}

// detab draws a tab as four spaces: the text engine gives a tab at the
// start of a run no width.
func detab(runs []ui.Span) []ui.Span {
	for i := range runs {
		if strings.Contains(runs[i].Text, "\t") {
			runs[i].Text = strings.ReplaceAll(runs[i].Text, "\t", "    ")
		}
	}
	return runs
}
