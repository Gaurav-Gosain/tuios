package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The cockpit's other tabs: Changes (review-diff, with notes), Checkpoints
// (list-checkpoints, checkpoint-diff, restore-checkpoint), Mail (the
// agents' mailbox) and Board (the session's task cards as kanban columns,
// as tuios PR #1548 draws them in the TUI). Each reads the daemon when its
// tab opens and when the daemon tells of a change, never on a timer.

// ckTabs holds what the tabs read.
type ckTabs struct {
	changes map[string]*ckDiff // by agent
	cps     map[string]*ckCheckpoints
	mail    *ckMail
	board   *ckBoard
}

// ckDiff is a diff as review-diff or checkpoint-diff gave it.
type ckDiff struct {
	loading bool
	err     string
	base    string
	files   []ckFile
	notes   []ckNote
	// note is the line a note is being written on, "" for none.
	note, noteText string
	scroll         smoothScroll
}

type ckFile struct {
	Path    string   `json:"path"`
	Status  string   `json:"status"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Hunks   []ckHunk `json:"hunks"`
	diff    *transcriptview.Diff
}

type ckHunk struct {
	Header   string `json:"header"`
	OldStart int    `json:"old_start"`
	NewStart int    `json:"new_start"`
	Lines    []struct {
		Op   string `json:"op"`
		Old  int    `json:"old"`
		New  int    `json:"new"`
		Text string `json:"text"`
	} `json:"lines"`
}

type ckNote struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Text string `json:"text"`
	Done bool   `json:"resolved"`
}

// asDiff is a file as the thread's diff, which diffView draws.
func (f *ckFile) asDiff() *transcriptview.Diff {
	if f.diff != nil {
		return f.diff
	}
	d := &transcriptview.Diff{File: f.Path, Added: f.Added, Removed: f.Removed}
	for _, h := range f.Hunks {
		th := transcriptview.Hunk{OldStart: h.OldStart, NewStart: h.NewStart}
		for _, l := range h.Lines {
			op := " "
			switch l.Op {
			case "add", "+":
				op = "+"
			case "del", "delete", "-":
				op = "-"
			}
			th.Lines = append(th.Lines, transcriptview.DiffLine{Op: op, Text: l.Text})
		}
		d.Hunks = append(d.Hunks, th)
	}
	f.diff = d
	return d
}

// ckTabOpened reads what the tab shows, when it opens.
func (a *App) ckTabOpened() {
	ag, ok := a.ckSelectedAgent()
	switch a.ck.tab {
	case tabChanges:
		if ok {
			a.fetchAgentChanges(ag)
		}
	case tabCheckpoints:
		if ok {
			a.fetchCheckpoints(ag)
		}
	case tabMail:
		a.fetchMail()
	case tabBoard:
		a.fetchBoard()
	}
}

// ckMailChanged reads the mail again when the Mail tab shows.
func (a *App) ckMailChanged(*proto.DaemonEvent) {
	if a.ck.tab == tabMail {
		a.fetchMail()
	}
}

// ckTasksChanged reads the cards again when the Board shows.
func (a *App) ckTasksChanged() {
	if a.ck.tab == tabBoard {
		a.fetchBoard()
	}
}

// --- Changes ---

func (a *App) fetchAgentChanges(ag client.Agent) {
	cl := a.cockpitClient()
	t := &a.ck.tabs
	if t.changes == nil {
		t.changes = map[string]*ckDiff{}
	}
	d := t.changes[ag.Key()]
	if d == nil {
		d = &ckDiff{}
		t.changes[ag.Key()] = d
	}
	if cl == nil || d.loading {
		return
	}
	d.loading = true
	go func() {
		var out struct {
			Base  string   `json:"base"`
			Files []ckFile `json:"files"`
			Notes []ckNote `json:"notes"`
		}
		err := cl.VerbInto("review-diff", map[string]any{"session": ag.Session, "window": ag.Window}, false, &out)
		a.update(func() {
			d.loading = false
			if err != nil {
				d.err = err.Error()
				return
			}
			d.err, d.base, d.files, d.notes = "", out.Base, out.Files, out.Notes
		})
	}()
}

func (a *App) changesView(c *ui.Context, pal *palette, ag client.Agent) {
	d := a.ck.tabs.changes[ag.Key()]
	if d == nil {
		a.fetchAgentChanges(ag)
		d = a.ck.tabs.changes[ag.Key()]
	}
	a.diffPage(c, pal, ag, d, true, func() {
		if sizedButton(c, "send-review", locales.CockpitSendNotes(), "", true, 26, 7, pal) {
			a.sendReview(ag)
		}
	})
}

// diffPage draws a diff by file, with a line's note on a click when notes
// is set.
func (a *App) diffPage(c *ui.Context, pal *palette, ag client.Agent, d *ckDiff, notes bool, actions func()) {
	switch {
	case d == nil || d.loading && len(d.files) == 0:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		return
	case d.err != "":
		a.ckMessage(c, pal, locales.CockpitNoChanges(), d.err)
		return
	case len(d.files) == 0:
		a.ckMessage(c, pal, locales.CockpitNoChanges(), "")
		return
	}
	mono := a.termFamily()
	added, removed := a.diffColorsFor(pal)
	d.scroll.track(c, ui.Scroll(c.Key("diff"))).Grow(1).MinHeight(0).Padding(14, 24, 18, 24).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ta, tr := 0, 0
				for _, f := range d.files {
					ta += f.Added
					tr += f.Removed
				}
				ui.Text(c, locales.CockpitFilesChanged(len(d.files))).FontSize(13).FontWeight(650).TextColor(pal.text)
				ui.Text(c, fmt.Sprint("+", ta)).Font(mono).FontSize(12).TextColor(added)
				ui.Text(c, fmt.Sprint("−", tr)).Font(mono).FontSize(12).TextColor(removed)
				if d.base != "" {
					ui.Text(c, locales.CockpitAgainst(d.base)).FontSize(12).TextColor(pal.textFaint)
				}
				ui.Spacer(c)
				if actions != nil {
					actions()
				}
			})
			for i := range d.files {
				f := &d.files[i]
				ui.Column(c.Key(f.Path)).Radius(10).Background(ckCodeBg(pal)).Clip().Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 12).Children(func() {
						fileIconImage(c, pal, f.Path, false, false)
						ui.Text(c, f.Path).Font(mono).FontSize(12.5).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
						ui.Text(c, fmt.Sprint("+", f.Added)).Font(mono).FontSize(12).TextColor(added)
						ui.Text(c, fmt.Sprint("−", f.Removed)).Font(mono).FontSize(12).TextColor(removed)
					})
					a.diffView(c, pal, f.asDiff(), true, mono)
					if notes {
						a.fileNotes(c, pal, ag, d, f)
					}
				})
			}
		})
	})
}

// fileNotes lists a file's review notes, and adds one.
func (a *App) fileNotes(c *ui.Context, pal *palette, ag client.Agent, d *ckDiff, f *ckFile) {
	ui.Column(c).Gap(6).Padding(4, 12, 10, 12).Children(func() {
		for _, n := range d.notes {
			if n.Path != f.Path {
				continue
			}
			ui.Row(c.Key("n"+n.ID)).Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(7).Background(pal.accent.Alpha(0.1)).Children(func() {
				ui.Text(c, locales.CockpitNoteOn(n.Line)).FontSize(11.5).FontWeight(650).TextColor(pal.accent).Shrink(0)
				t := ui.Text(c, n.Text).FontSize(12.5).TextColor(pal.text).Grow(1).MinWidth(0)
				if n.Done {
					t.Strikethrough().TextColor(pal.textMuted)
				}
				if sizedButton(c, "rm", locales.CockpitRemove(), "", false, 22, 5, pal) {
					a.reviewNote(ag, map[string]any{"action": "remove", "id": n.ID})
				}
			})
		}
		if d.note == f.Path {
			in := textField(c, "note", &d.noteText, locales.CockpitNotePlaceholder(), 0, pal).AutoFocus()
			if in.Submitted() && strings.TrimSpace(d.noteText) != "" {
				p := map[string]any{"action": "add", "path": f.Path, "text": d.noteText}
				if len(f.Hunks) > 0 {
					// A note on the file goes on its first hunk.
					p["hunk"] = f.Hunks[0].Header
				}
				a.reviewNote(ag, p)
				d.note, d.noteText = "", ""
			} else if in.Shortcut(0, ui.KeyEscape) {
				d.note = ""
			}
			return
		}
		r := ui.Row(c.Key("add")).Cursor(ui.CursorPointer)
		if r.Clicked() {
			c.Invalidate()
			d.note, d.noteText = f.Path, ""
		}
		r.Children(func() {
			ui.Text(c, locales.CockpitAddNote()).FontSize(12).TextColor(pal.accent)
		})
	})
}

func (a *App) reviewNote(ag client.Agent, p map[string]any) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	p["session"], p["window"] = ag.Session, ag.Window
	go func() {
		_, err := cl.Verb("review-note", p, false)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.fetchAgentChanges(ag)
		})
	}()
}

// sendReview gives the notes to the agent (send-review), which queues them
// for its next turn, or sends them now when it waits.
func (a *App) sendReview(ag client.Agent) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	go func() {
		_, err := cl.Verb("send-review", map[string]any{"session": ag.Session, "window": ag.Window}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast(locales.CockpitNotesSent(ag.Name))
			a.fetchAgentChanges(ag)
		})
	}()
}

// --- Checkpoints ---

// ckCheckpoints are an agent's checkpoints and the diff of the one chosen.
type ckCheckpoints struct {
	loading bool
	err     string
	list    []ckCheckpoint
	chosen  int
	diff    *ckDiff
	confirm int // the checkpoint a restore waits on its second click for
}

type ckCheckpoint struct {
	N     int    `json:"n"`
	Label string `json:"label"`
	State string `json:"state"`
	At    int64  `json:"at"`
	Turn  int    `json:"turn"`
}

func (a *App) fetchCheckpoints(ag client.Agent) {
	cl := a.cockpitClient()
	t := &a.ck.tabs
	if t.cps == nil {
		t.cps = map[string]*ckCheckpoints{}
	}
	cp := t.cps[ag.Key()]
	if cp == nil {
		cp = &ckCheckpoints{}
		t.cps[ag.Key()] = cp
	}
	if cl == nil || cp.loading {
		return
	}
	cp.loading = true
	go func() {
		var out struct {
			Checkpoints []ckCheckpoint `json:"checkpoints"`
		}
		err := cl.VerbInto("list-checkpoints", map[string]any{"session": ag.Session, "window": ag.Window}, false, &out)
		a.update(func() {
			cp.loading = false
			if err != nil {
				cp.err = err.Error()
				return
			}
			cp.err, cp.list = "", out.Checkpoints
			slices.Reverse(cp.list)
			if len(cp.list) > 0 && cp.chosen == 0 {
				a.chooseCheckpoint(ag, cp, cp.list[0].N)
			}
		})
	}()
}

func (a *App) chooseCheckpoint(ag client.Agent, cp *ckCheckpoints, n int) {
	cl := a.cockpitClient()
	cp.chosen, cp.confirm = n, 0
	d := &ckDiff{loading: true}
	cp.diff = d
	if cl == nil {
		return
	}
	go func() {
		var out struct {
			Base  string   `json:"base"`
			Files []ckFile `json:"files"`
		}
		err := cl.VerbInto("checkpoint-diff", map[string]any{"session": ag.Session, "window": ag.Window, "n": n}, false, &out)
		a.update(func() {
			d.loading = false
			if err != nil {
				d.err = err.Error()
				return
			}
			d.base, d.files = out.Base, out.Files
		})
	}()
}

func (a *App) checkpointsView(c *ui.Context, pal *palette, ag client.Agent) {
	cp := a.ck.tabs.cps[ag.Key()]
	if cp == nil {
		a.fetchCheckpoints(ag)
		cp = a.ck.tabs.cps[ag.Key()]
	}
	switch {
	case cp == nil || cp.loading && len(cp.list) == 0:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		return
	case cp.err != "":
		a.ckMessage(c, pal, locales.CockpitNoCheckpoints(), cp.err)
		return
	case len(cp.list) == 0:
		a.ckMessage(c, pal, locales.CockpitNoCheckpoints(), locales.CockpitNoCheckpointsDetail())
		return
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c.Key("cps")).Width(240).Shrink(0).Padding(12, 10).Children(func() {
			for i, k := range cp.list {
				on := k.N == cp.chosen
				row := ui.Row(c.Key(k.N)).Gap(10).Padding(7, 10).Radius(7).AlignItems(ui.Start).Cursor(ui.CursorPointer)
				switch {
				case on:
					chosen(row.Background(pal.rowSelected), pal)
				case row.Hovered():
					row.Background(pal.rowHover)
				}
				if row.Clicked() {
					c.Invalidate()
					a.chooseCheckpoint(ag, cp, k.N)
				}
				row.Children(func() {
					dot := pal.textFaint
					if i == 0 {
						dot = pal.accent
					}
					ui.Box(c).Size(8, 8).Radius(4).Background(dot).Margin(5, 0, 0, 0)
					ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
						label := k.Label
						if label == "" {
							label = locales.CockpitTurn(k.Turn)
						}
						ui.Text(c, label).FontSize(12.5).TextColor(pal.text).MaxLines(2).Ellipsis("…")
						ui.Text(c, fmt.Sprintf("#%d · %s", k.N, clock(k.At/int64(time.Millisecond)))).FontSize(11.5).TextColor(pal.textFaint).FontFeatures("tnum")
					})
				})
			}
		})
		ui.Box(c).Width(1).Shrink(0).Background(pal.divider)
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			a.diffPage(c, pal, ag, cp.diff, false, func() {
				label := locales.CockpitRestore()
				if cp.confirm == cp.chosen {
					label = locales.CockpitRestoreConfirm()
				}
				if sizedButton(c, "restore", label, "", cp.confirm == cp.chosen, 26, 7, pal) {
					if cp.confirm != cp.chosen {
						cp.confirm = cp.chosen
					} else {
						a.restoreCheckpoint(ag, cp)
					}
				}
			})
		})
	})
}

func (a *App) restoreCheckpoint(ag client.Agent, cp *ckCheckpoints) {
	cl := a.cockpitClient()
	n := cp.chosen
	cp.confirm = 0
	if cl == nil {
		return
	}
	go func() {
		_, err := cl.Verb("restore-checkpoint", map[string]any{"session": ag.Session, "window": ag.Window, "n": n}, false)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			a.toast(locales.CockpitRestored(n))
			a.fetchCheckpoints(ag)
		})
	}()
}

// --- Mail ---

type ckMail struct {
	loading bool
	err     string
	msgs    []ckMsg
	draft   string
	replyTo int64
	md      map[int64][]markdown.Block
}

type ckMsg struct {
	ID       int64  `json:"id"`
	From     string `json:"from"`
	FromName string `json:"from_label"`
	To       string `json:"to"`
	ToName   string `json:"to_label"`
	Subject  string `json:"subject"`
	Text     string `json:"text"`
	Thread   int64  `json:"thread_id"`
	SentAt   int64  `json:"sent_at"`
	Unread   bool   `json:"was_unread"`
	Session  string `json:"session"`
	Held     bool   `json:"held"`
}

func (a *App) fetchMail() {
	cl := a.cockpitClient()
	t := &a.ck.tabs
	if t.mail == nil {
		t.mail = &ckMail{md: map[int64][]markdown.Block{}}
	}
	m := t.mail
	ag, ok := a.ckSelectedAgent()
	if cl == nil || m.loading || !ok {
		return
	}
	m.loading = true
	go func() {
		var out struct {
			Messages []ckMsg `json:"messages"`
		}
		err := cl.VerbInto("read-agent-messages", map[string]any{"session": ag.Session, "peek": true, "limit": 200}, false, &out)
		a.update(func() {
			m.loading = false
			if err != nil {
				m.err = err.Error()
				return
			}
			m.err, m.msgs = "", out.Messages
		})
	}()
}

func (a *App) mailView(c *ui.Context, pal *palette, ag client.Agent) {
	m := a.ck.tabs.mail
	if m == nil {
		a.fetchMail()
		m = a.ck.tabs.mail
	}
	var mine []ckMsg
	for _, msg := range m.msgs {
		if msg.From == ag.Window || msg.To == ag.Window || msg.To == "" {
			mine = append(mine, msg)
		}
	}
	mono := a.termFamily()
	ui.Scroll(c.Key("mail")).Grow(1).MinHeight(0).Padding(14, 24, 10, 24).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			if len(mine) == 0 {
				a.ckMessage(c, pal, locales.CockpitNoMail(), locales.CockpitNoMailDetail(ag.Name))
			}
			for _, msg := range mine {
				in := msg.To == ag.Window
				ui.Column(c.Key(msg.ID)).Gap(6).Padding(10, 14).Radius(10).Background(ckGround(pal)).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						arrow := "→"
						if in {
							arrow = "←"
						}
						ui.Text(c, arrow).FontSize(13).TextColor(pal.accent)
						to := msg.ToName
						if to == "" {
							to = locales.CockpitEveryone()
						}
						ui.Text(c, msg.FromName).FontSize(12.5).FontWeight(650).TextColor(pal.text)
						ui.Text(c, "→ "+to).FontSize(12.5).TextColor(pal.textMuted)
						if msg.Subject != "" {
							ui.Text(c, "· "+msg.Subject).FontSize(12.5).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
						}
						ui.Spacer(c)
						if msg.Unread {
							ui.Box(c).Size(7, 7).Radius(4).Background(pal.attention)
						}
						ui.Text(c, ago(msg.SentAt/int64(time.Millisecond), c.Now())).FontSize(11.5).TextColor(pal.textFaint)
					})
					blocks, ok := m.md[msg.ID]
					if !ok {
						blocks, _ = markdown.Parse(msg.Text)
						m.md[msg.ID] = blocks
					}
					a.markdownView(c, pal, blocks, mono)
					r := ui.Row(c.Key("reply")).Cursor(ui.CursorPointer)
					if r.Clicked() {
						c.Invalidate()
						m.replyTo = msg.ID
					}
					r.Children(func() {
						ui.Text(c, locales.CockpitReplyAsYou()).FontSize(12).TextColor(pal.accent)
					})
				})
			}
		})
	})
	// Mail to the agent, as the person.
	ui.Row(c.Key("mail-compose")).Shrink(0).Gap(8).AlignItems(ui.Center).Padding(8, 24, 8, 24).Children(func() {
		ph := locales.CockpitMailTo(ag.Name)
		if m.replyTo != 0 {
			ph = locales.CockpitMailReply(int(m.replyTo))
		}
		in := textField(c, "mail-text", &m.draft, ph, 0, pal).Grow(1)
		send := sizedButton(c, "mail-send", locales.CockpitSendMail(), "", false, 28, 7, pal)
		if (in.Submitted() || send) && strings.TrimSpace(m.draft) != "" {
			a.sendMail(ag, m)
		}
		if in.Shortcut(0, ui.KeyEscape) {
			m.replyTo = 0
		}
	})
}

func (a *App) sendMail(ag client.Agent, m *ckMail) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	p := map[string]any{"session": ag.Session, "to": ag.Window, "text": m.draft, "from": "human"}
	if m.replyTo != 0 {
		p["reply_to"] = m.replyTo
	}
	go func() {
		_, err := cl.Verb("send-agent-message", p, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			m.draft, m.replyTo = "", 0
			a.fetchMail()
		})
	}()
}

// ckPassHeld passes held mail on to the agent it was for.
func (a *App) ckPassHeld(it session.AttentionItem) {
	cl := a.cockpitClient()
	if cl == nil || it.HeldID == 0 {
		return
	}
	go func() {
		_, err := cl.Verb("release-agent-message", map[string]any{"session": it.Session, "id": it.HeldID}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshCockpit()
		})
	}()
}
