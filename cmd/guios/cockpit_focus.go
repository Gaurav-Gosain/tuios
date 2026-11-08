package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/roles"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The focus area: the selected agent's header, its tabs, the approval card
// when it waits for the person, and the reply box.

// The focus tabs.
const (
	tabThread      = "thread"
	tabChanges     = "changes"
	tabCheckpoints = "checkpoints"
	tabMail        = "mail"
	tabBoard       = "board"
)

// ckTabOrder is the agent's tabs. The board is the session's, not the
// agent's: it is a view of its own (ck.view).
var ckTabOrder = []string{tabThread, tabChanges, tabCheckpoints, tabMail}

func ckTabTitle(t string) string {
	switch t {
	case tabChanges:
		return locales.CockpitTabChanges()
	case tabCheckpoints:
		return locales.CockpitTabCheckpoints()
	case tabMail:
		return locales.CockpitTabMail()
	case tabBoard:
		return locales.CockpitTabBoard()
	}
	return locales.CockpitTabThread()
}

// ckStepTab moves to the next or the previous tab.
func (a *App) ckStepTab(d int) {
	i := slices.Index(ckTabOrder, a.ck.tab)
	if i < 0 {
		i = 0
	}
	a.ckSetTab(ckTabOrder[(i+d+len(ckTabOrder))%len(ckTabOrder)])
}

// ckSetTab shows a tab the person chose. It stays for that agent: the
// next agent opens on what it asks for (ckTabFor).
func (a *App) ckSetTab(t string) {
	ck := &a.ck
	ck.tab = t
	ck.picked = t
	if ck.chosen == nil {
		ck.chosen = map[string]string{}
	}
	if ag, ok := a.ckSelectedAgent(); ok {
		ck.chosen[ag.Key()] = t
	}
	a.ckTabOpened()
}

// ckSourceShown tells that where an agent's state came from is worth
// saying: not its own report, and not tuios's inner guesses (a stall, a
// detection, activity), which say nothing to the person.
func ckSourceShown(src string) bool {
	switch src {
	case "", "report", "stall", "detect", "activity", "hook", "recent":
		return false
	}
	return true
}

// ckTabFor is the tab a row opens on: what its agent asks for, mail on
// Mail and an approval, a question or an error on Thread with its card;
// else the tab the person chose for that agent; else the tab they chose
// last.
func (a *App) ckTabFor(r ckRow) string {
	ck := &a.ck
	it := r.item
	if it != nil && needItemKind(it) {
		if it.Kind == session.AttentionMail {
			// A mail item is answered by mail.
			a.ckSendAs(ck.rowAgent(r), true)
			return tabMail
		}
		return tabThread
	}
	ag := ck.rowAgent(r)
	if ag.State == "needs_input" || ag.State == "errored" {
		return tabThread
	}
	if t, ok := ck.chosen[ag.Key()]; ok && r.agent >= 0 && slices.Contains(ckTabOrder, t) {
		return t
	}
	if ck.picked != "" && slices.Contains(ckTabOrder, ck.picked) {
		return ck.picked
	}
	return tabThread
}

// ckStatePill is a state's word and colour.
func ckStatePill(pal *palette, ag client.Agent, it *session.AttentionItem) (string, ui.Color) {
	st, unread := ckStatus(ag, it)
	switch {
	case st == proto.Waiting:
		return locales.StatusNeedsYou(), pal.attnText
	case st == proto.Working:
		return locales.StatusWorking(), pal.work
	case st == proto.Failed:
		return locales.StatusFailed(), pal.dangerText
	case unread:
		return locales.CockpitToReview(), pal.done
	case st == statusDone:
		return locales.StatusDone(), pal.textMuted
	}
	return locales.CockpitIdle(), pal.textMuted
}

// cockpitFocus draws the selected row's agent.
func (a *App) cockpitFocus(c *ui.Context, pal *palette) {
	ck := &a.ck
	r, ok := a.ckSelected()
	if !ok || r.kind == ckRowHead || r.kind == ckRowMore {
		a.ckMessage(c, pal, locales.CockpitPickAgent(), locales.CockpitPickAgentDetail())
		return
	}
	ag := ck.rowAgent(r)
	if r.agent < 0 && r.item != nil {
		ag = client.Agent{Session: r.item.Session, Window: r.item.Window, Name: r.item.Name, Harness: r.item.Harness, Host: r.item.Host}
	}
	if ck.tab == "" {
		ck.tab = tabThread
	}
	// Header: name, harness, the state pill; then where it works.
	// The header and tabs keep the thread's column: at 1920 they start
	// where the thread, its card and the reply box do (council round 1,
	// visual 14).
	pad := a.ckSide(24)
	if ck.stacked {
		pad = 14
	}
	ui.Column(c).Shrink(0).Padding(14, pad, 0, pad).Gap(4).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ck.stacked {
				iconButton(c, "ck-back", chevronLeftIcon, a.tip(locales.CockpitBack(), ""), pal, func() { ck.stackFocus = false })
			}
			ui.Icon(c, harnessIcon(ag.Harness)).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
			if l := a.ckAttemptLetter(ag); l != "" {
				attemptChip(c, pal, l)
			}
			name := oneLine(c, a.ckAgentName(ag), fsTitle, wSemibold).TextColor(pal.text).Cursor(ui.CursorPointer)
			pointerTooltip(c, name, "ck-name", locales.CockpitOpenPane())
			if name.Clicked() && ag.Window != "" {
				a.goToAgent(ag)
			}
			// No harness reported yet: nothing, not the word "agent"
			// (Mac round 1, visual S3).
			if !ck.narrow && ag.Harness != "" {
				ui.Text(c, harnessName(ag.Harness)).FontSize(fsBody).TextColor(pal.textMuted).SingleLine().Shrink(0)
				if m, ok := ag.Meta["model"].(string); ok && m != "" {
					oneLine(c, "· "+m, fsBody, 0).TextColor(pal.textFaint).Shrink(2)
				}
			}
			ui.Spacer(c)
			word, col := ckStatePill(pal, ag, r.item)
			ground, ink := col.Alpha(0.15), col
			if st, _ := ckStatus(ag, r.item); st == proto.Waiting {
				// Needs you is the pane chip's solid need (attnFill, its
				// ink at 5:1): on its own amber tint the deepened amber
				// read 4.55:1 in light themes (Mac round 3, visual S1).
				ground, ink = pal.attnFill, pal.attnInk
			}
			chip(c, pal, ground, ink, func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(ink).Shrink(0)
				ui.Text(c, word).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
			}).Shrink(0)
			if a.ckHasSiblings(ag) && !ck.narrow {
				if sizedButton(c, "ck-compare", locales.CockpitCompare(), "", false, hControlSm, rControl, pal) {
					a.openCompare()
				}
			}
			switch {
			case ag.Window == "":
			case r.item != nil && a.ckCardOpensPane(*r.item):
				// The card under it has the one Open pane (council mac-1,
				// V-M1): two of them read as two different actions.
			case ck.narrow:
				iconButton(c, "ck-open", openFileIcon, locales.CockpitOpenPane()+" (↵)", pal, func() { a.goToAgent(ag) })
			default:
				// The ↵ cap goes on the card's own Open pane when it has one,
				// and on Send while the reply box has the keys.
				cap := "↵"
				if ck.cardShown || ck.reply.box.FocusWithin(c) {
					cap = ""
				}
				if sizedButton(c, "ck-open", locales.CockpitOpenPane(), cap, false, hControlSm, rControl, pal) {
					a.goToAgent(ag)
				}
			}
		})
		var where []string
		where = append(where, ag.Session)
		if ag.Workspace > 0 {
			where = append(where, locales.CockpitWorkspace(ag.Workspace))
		}
		if ag.Cwd != "" {
			where = append(where, a.local().shortDir(ag.Cwd))
		}
		if ckSourceShown(ag.Source) {
			where = append(where, locales.CockpitStateFrom(ckSource(ag.Source)))
		}
		oneLine(c, strings.Join(where, " · "), fsSmall, 0).TextColor(pal.textFaint)
		a.agentUsage(c, pal, ag)
		// Tabs.
		// A narrow card scrolls the tabs sideways rather than cut the last.
		tabs := ui.Row(c)
		if ck.narrow {
			tabs = ui.ScrollHorizontal(c.Key("tabs")).Shrink(0)
		}
		tabs.Gap(2).Margin(10, 0, 0, 0).FocusGroup(ui.Horizontal).Children(func() {
			for _, t := range ckTabOrder {
				a.ckTab(c, pal, t, ag)
			}
		})
	})
	ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
	ck.cardShown = r.item != nil && ckAnswerable(*r.item)
	// A held edit is answered from its whole body: the tab gives way to
	// it down to a line, so the body grows into the room an empty thread
	// left above it (Mac round 2, V2-M2).
	ck.tabMin = ckTabMinH
	if r.item != nil && ckIsEdit(*r.item) {
		ck.tabMin = ckTabMinEdit
	}
	// Over an empty thread the card comes right under its one line, as a
	// pending approval follows the last message, and the room is left
	// under the card, above the pinned reply box (Mac round 4, visual S2).
	cardUnder := ck.cardShown && ck.tab == tabThread && a.ckThreadEmpty(ag)
	tabCol := ui.Column(c.Key("tab-" + ck.tab))
	if cardUnder {
		tabCol.Shrink(0)
	} else {
		tabCol.Grow(1).MinHeight(ck.tabMin)
	}
	ck.tabH = tabCol.Bounds().H
	tabCol.ClipY().AlignItems(ui.Stretch).Children(func() {
		switch ck.tab {
		case tabChanges:
			a.changesView(c, pal, ag)
		case tabCheckpoints:
			a.checkpointsView(c, pal, ag)
		case tabMail:
			a.mailView(c, pal, ag)
		default:
			a.threadView(c, pal, ag)
		}
	})
	card := r.item != nil && ckAnswerable(*r.item)
	if !card && ck.tab != tabThread && ck.tab != tabMail {
		return
	}
	// The reply box is pinned above the key line: the approval card
	// above it scrolls in what the window leaves, so a card that grows
	// (a long command, risk lines) never pushes the box under the key
	// line (council round 4, M4).
	if card {
		outer := ui.Scroll(c.Key("ck-card")).Shrink(1).MinHeight(64)
		// The card's own viewport, which a held edit's body is clipped by
		// too: only rows inside both count as shown (ckEditBlock).
		ck.cardView = outer.Bounds()
		outer.Children(func() {
			inner := ui.Column(c.Key("ck-card-in")).AlignItems(ui.Stretch)
			ck.cardH = inner.Bounds().H
			inner.Children(func() {
				a.approvalCard(c, pal, ag, *r.item)
			})
		})
	}
	ck.gapH = 0
	if cardUnder {
		gap := ui.Box(c.Key("ck-card-gap")).Grow(1).MinHeight(0)
		ck.gapH = gap.Bounds().H
	}
	if ck.tab == tabThread || ck.tab == tabMail {
		a.replyBox(c, pal, ag)
	}
}

// ckThreadEmpty reports whether ag's thread shows no message, so its tab
// is the one quiet line over the card (ckEmptyThread).
func (a *App) ckThreadEmpty(ag client.Agent) bool {
	th := a.ck.threads[threadKey(ag)]
	return th != nil && len(th.items) == 0 && !th.unsupported && !th.failed
}

// ckTabMinH is the least height the tab's content keeps above the card,
// and ckTabMinEdit the least while the card holds an edit.
const (
	ckTabMinH    = 120
	ckTabMinEdit = 32
)

// ckTab is one tab, with its count where it has one.
func (a *App) ckTab(c *ui.Context, pal *palette, t string, ag client.Agent) {
	on := a.ck.tab == t
	b := ui.ButtonBase(c.Key("tab-"+t)).Height(hControl+2).Padding(0, 10).Gap(6).AlignItems(ui.Center).Shrink(0).Radius(rRow).Label(ckTabTitle(t))
	if b.Pressed() {
		// The press state, as the other controls have (council round 4,
		// feel S4).
		b.Background(pressedQuiet(pal))
	}
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		if on {
			p.Fill(ui.Rect{X: r.X + 6, Y: r.Y + r.H - 2, W: r.W - 12, H: 2}, pal.accent, 1)
		}
	})
	col := pal.textMuted
	if on || b.Hovered() {
		col = pal.text
	}
	b.Children(func() {
		// One weight for every tab, so that the chosen one is no wider.
		ui.Text(c, ckTabTitle(t)).FontSize(fsBody).FontWeight(wMedium).TextColor(col).SingleLine()
	})
	if b.Clicked() {
		c.Invalidate()
		a.ckSetTab(t)
		// The keys stay on the agents: a tab is chosen with the pointer
		// or with [ and ], and Up, Down and Enter still move the list.
		a.ck.fleetFocus.Focus()
	}
}

// ckHeld reports whether an item is an approval or plan a hook holds for an
// answer from here (reply-approval).
func ckHeld(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan) && it.RequestID != ""
}

// ckAnswerable reports whether the card answers the item: a held approval
// or plan, or a prompt on the pane's screen (respond).
func ckAnswerable(it session.AttentionItem) bool {
	switch it.Kind {
	case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion:
		return it.Host == ""
	}
	return false
}

// ckShownKey is everything about an item an answer depends on, as the TUI's
// inboxShownKey.
func ckShownKey(it session.AttentionItem) string {
	return strings.Join([]string{it.ID, it.RequestID, it.Summary, strings.Join(it.Options, ","),
		strings.Join(it.AlwaysScope, "\n"), it.Kind, strings.Join(it.Risk, ","), it.PlanSHA, strconv.FormatBool(it.HeldEdit)}, "\x00")
}

// ckPeek is a pane's prompt as peek-prompt read it.
type ckPeek struct {
	window, promptID string
	lines            []string
	actions          []string
	risk             []string
	loading          bool
	at               time.Time
}

// approvalCard is the card the person answers from: the question, the
// command or the prompt's lines, the risk rules it matched, and the buttons.
func (a *App) approvalCard(c *ui.Context, pal *palette, ag client.Agent, it session.AttentionItem) {
	ck := &a.ck
	now := c.Now()
	key := ckShownKey(it)
	ck.cardKey = key
	a.ckNoteHeld(it)
	if ck.shown.key != key {
		ck.shown = ckShown{key: key, since: now}
		ck.armed = ckArmed{}
	}
	// A new prompt in the card shows that it is new: its edge flashes the
	// accent for ckNewFlash, so a fast chord that found a new prompt under
	// it sees why (council round 4, S2).
	fresh := now.Sub(ck.shown.since) < ckNewFlash
	if fresh {
		c.After(ckNewFlash - now.Sub(ck.shown.since))
	}
	held := ckHeld(it)
	if !held {
		a.ckFetchPeek(ag, it)
	}
	question := !held && (it.Kind == session.AttentionQuestion || it.Kind == session.AttentionAsk)
	pk := ck.peek
	risky := len(it.Risk) > 0 || pk != nil && len(pk.risk) > 0
	// The card is the card's ground with one needs-you cue, its rounded
	// edge: amber, red and blue do not meet on one brown fill, and the risk
	// lines read at 4.5:1 (council round 1, visual 16). A bar inside the
	// edge drew a second amber rule (council round 2, S3).
	card := ui.Column(c.Key("approval")).Shrink(0).Margin(0, a.ckSide(24), 10, a.ckSide(24)).Padding(14, 16).Gap(10).Radius(rCard).
		Background(pal.card).Border(1, pal.attention.Alpha(0.7))
	if fresh {
		// A new prompt shows by its edge going to 2 px, in the same need
		// amber: the card's colour never changes with no change of state
		// (council mac-1, V-M1).
		card.Border(2, pal.attention)
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal.attention).Shrink(0)
			verb, rest := ckLine2(ag, &it)
			if question {
				// "{name} asks", and how long it waits.
				oneLine(c, locales.CockpitNameAsks(ag.Name), fsBody, wSemibold).TextColor(pal.text)
				ui.Text(c, ago(it.Since/int64(time.Millisecond), now)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
			} else if tool, path, ok := ckAsk(it.Summary); ok && (ckIsEdit(it) || ckEditTool(tool)) {
				// The file is the title: "Edit backoff.go", its folder in
				// text3, and how big the change is once it is read. The
				// full path is in the folder's tooltip.
				title, folder, size := a.ckEditTitle(it, tool, path, ag.Cwd)
				oneLine(c, title, fsBody, wSemibold).TextColor(pal.text).Shrink(1)
				if folder != "" {
					oneLine(c, folder, fsSmall, 0).TextColor(pal.textFaint).Shrink(3).Tooltip(path)
				}
				if size != "" {
					ui.Text(c, size).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
				}
				ui.Text(c, "·").FontSize(fsSmall).TextColor(pal.textFaint).Shrink(0)
				oneLine(c, locales.CockpitWaits(ag.Name, ago(it.Since/int64(time.Millisecond), now)), fsSmall, 0).TextColor(pal.textMuted).Shrink(4)
			} else if tool, _, ok := ckAsk(it.Summary); ok {
				// The tool and who waits: "Bash · review waits now". What
				// the call acts on is in the code box under it, once.
				ui.Text(c, tool).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
				ui.Text(c, "·").FontSize(fsSmall).TextColor(pal.textFaint).Shrink(0)
				oneLine(c, locales.CockpitWaits(ag.Name, ago(it.Since/int64(time.Millisecond), now)), fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
			} else if it.Kind == session.AttentionApproval || it.Kind == session.AttentionPlan {
				// No tool to name: who waits is the title, as a sentence,
				// not "Asks?" (council mac-1, V-M1).
				oneLine(c, locales.CockpitWaits(ag.Name, ago(it.Since/int64(time.Millisecond), now)), fsBody, wSemibold).TextColor(pal.text)
			} else {
				oneLine(c, verb+"?", fsBody, wSemibold).TextColor(pal.text)
				oneLine(c, locales.CockpitWaits(ag.Name, ago(it.Since/int64(time.Millisecond), now)), fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
			}
			ui.Spacer(c)
			if risky {
				chip(c, pal, pal.danger.Alpha(0.14), pal.dangerText, func() {
					ui.Text(c, locales.CockpitRisky()).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
				}).Shrink(0)
			}
			_ = rest
		})
		target := it.Summary
		if _, t, ok := ckAsk(target); ok {
			target = t
		}
		mono := a.termFamily()
		if ckIsEdit(it) {
			a.ckEditBlock(c, pal, it)
		} else if a.ckEndedEdit(c, pal, it) {
			// Drawn: what the edit was, after its hold ended.
		} else if !held && pk != nil && len(pk.lines) > 0 && (!question || len(pk.actions) > 0) {
			ui.Column(c).Radius(rControl).Background(ckCodeBg(pal)).Padding(8, 12).Gap(1).Clip().Children(func() {
				for _, l := range pk.lines {
					ui.Text(c, l).Font(mono).FontSize(fsCode).TextColor(pal.text).SingleLine()
				}
			})
		} else if target != "" && question {
			// A question is the agent's prose, not a command: no prompt
			// sign and no code ground (council round 1, visual 8).
			ui.Text(c, target).FontSize(fsProse).LineHeight(22.0 / fsProse).TextColor(pal.text).MaxLines(6).Selectable()
		} else if tool, _, _ := ckAsk(it.Summary); target != "" && !ckEditTool(tool) {
			// An edit whose hold ended keeps its title and no "$" row: a
			// path drawn as a command reads as "run this" (Mac round 2,
			// workflow S1).
			// A long command keeps the card's height in bounds: six lines,
			// its whole text in a tooltip.
			ui.Row(c).Radius(rControl).Background(ckCodeBg(pal)).Padding(8, 12).Gap(8).Children(func() {
				ui.Text(c, "$").Font(mono).FontSize(fsCode).TextColor(pal.textFaint).Shrink(0)
				ui.Text(c, target).Font(mono).FontSize(fsCode).LineHeight(1.5).TextColor(pal.text).Grow(1).MinWidth(0).MaxLines(6).Selectable()
			})
		}
		rules := it.Risk
		if !held && pk != nil {
			rules = pk.risk
		}
		for _, name := range rules {
			why, ok := risk.Why(name)
			if !ok {
				why = locales.CockpitRiskYours()
			}
			if why != "" {
				why = strings.ToUpper(why[:1]) + why[1:]
			}
			ui.Text(c, locales.CockpitRiskLine(name, why)).FontSize(fsSmall).TextColor(pal.dangerText).MaxLines(2)
		}
		if ck.armed.decision != "" && ck.armed.item == key && now.Sub(ck.armed.at) <= ckRiskWindow {
			ui.Text(c, locales.CockpitClickAgain()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.attnText)
			c.After(ck.armed.at.Add(ckRiskWindow).Sub(now))
		}
		// Reject and say why is a quiet button on the line of what always
		// allows, at its right: the card keeps one row of answers, and fits
		// over the reply box at 1100x700.
		reason := !question && slices.Contains(a.ckOptions(it, pk), "reason")
		if len(it.AlwaysScope) > 0 {
			ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.CockpitAlwaysAllows(strings.Join(it.AlwaysScope, ", "))).FontSize(fsSmall).TextColor(pal.textMuted).
					Grow(1).MinWidth(0).MaxLines(2)
				if reason && ghostButton(c, "ans-reason", ckDecisionWords("reason"), pal) {
					a.ckAnswer("reason")
					a.moveKeys("card")
				}
			})
		}
		// With no scope line, it sits after the answers instead.
		reasonInRow := reason && len(it.AlwaysScope) == 0
		if ck.reasonFor == it.ID {
			in := textField(c, "reason", &ck.reason, locales.CockpitReasonPlaceholder(), 0, pal).AutoFocus()
			if in.Submitted() && strings.TrimSpace(ck.reason) != "" {
				a.ckSend(it, ag, "deny", ck.reason)
				ck.reasonFor, ck.reason = "", ""
				a.moveKeys("card")
			} else if in.Shortcut(0, ui.KeyEscape) {
				ck.reasonFor, ck.reason, ck.question = "", "", nil
				a.moveKeys("card")
			}
		}
		// The buttons wrap rather than cut the last in a narrow card.
		ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			opts := a.ckOptions(it, pk)
			if question && (pk == nil || len(pk.actions) == 0) {
				opts = nil
				// Send, in the reply box under it, is the view's one primary.
				if sizedButton(c, "ans-reply", locales.CockpitReply(), "", false, hControl, rControl, pal) {
					a.ckFocusReply()
				}
				oneLine(c, locales.CockpitAnswerByReply(), fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
			} else if len(opts) == 0 && pk != nil && pk.loading && !held {
				// Say what the card waits for, rather than show nothing
				// (QM-004): the header's Open pane stays until it knows.
				ui.Text(c, locales.CockpitReadingPrompt()).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).MinWidth(0)
				// The row keeps a button's height while it reads, so the
				// card does not grow under the pointer when the answers
				// come (Mac round 1, feel S4).
				ui.Box(c).Size(0, hControl).Shrink(0)
			} else if len(opts) == 0 && (pk == nil || !pk.loading) {
				// Nothing here can answer it: say so, and offer the pane.
				// With approvals off the offer says it all, in one line:
				// "cannot read" beside it named a second app and said the
				// same twice (Mac round 3, workflow S2).
				switch {
				case a.ckHoldEnded(it):
					// tuios read it: the hold ended, and "cannot read"
					// said what was not so (Mac round 3, workflow S1).
					ui.Text(c, locales.CockpitHoldEnded()).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).MinWidth(0)
				case !a.ckApprovalsOff(it, ag):
					ui.Text(c, locales.CockpitAnswerInPane()).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).MinWidth(0)
				}
				a.ckApprovalsOffLine(c, pal, it, ag)
				// The view's one primary is Send: this one is secondary.
				if ag.Window != "" && sizedButton(c, "ans-open", locales.CockpitOpenPane(), "↵", false, hControl, rControl, pal) {
					a.goToAgent(ag)
				}
			}
			// While the confirm step asks about one of them, that button is
			// the primary one with Enter on it, and the others fade
			// (council round 4, S2).
			asked := ""
			if q := ck.question; q != nil && q.item == key {
				asked = q.decision
			}
			for _, o := range ckButtonOrder(opts) {
				if o == "reason" {
					// On the line above (reason).
					continue
				}
				primary, keys := o == "once" && !risky, a.ckDecisionKey(o)
				fade := float32(1)
				switch {
				case asked == o:
					primary, keys = true, "↵"
				case asked != "":
					primary, fade = false, 0.55
				}
				ui.Row(c).Opacity(fade).Children(func() {
					// A risky command gets no primary button to allow it,
					// until the confirm step asks about it.
					words := ckDecisionWords(o)
					if o == "always" && ckSessionScope(it.AlwaysScope) {
						words = locales.CockpitAllowSession()
					}
					if capButton(c, "ans-"+o, words, keys, primary, pal) {
						a.ckAnswer(o)
						a.moveKeys("card")
					}
				})
			}
			if reasonInRow && ghostButton(c, "ans-reason", ckDecisionWords("reason"), pal) {
				a.ckAnswer("reason")
				a.moveKeys("card")
			}
			ui.Spacer(c)
			if !held && len(opts) > 0 {
				oneLine(c, locales.CockpitScreenPrompt(), fsSmall, 0).TextColor(pal.textFaint)
			}
		})
	})
}

// ckOptions are the answers an item takes, in the card's order.
func (a *App) ckOptions(it session.AttentionItem, pk *ckPeek) []string {
	if ckHeld(it) {
		var out []string
		// An edit is allowed only once the card drew its body
		// (cockpit_edit.go), and never always.
		_, editShown := a.ckEditAllowSHA(it)
		for _, o := range []string{"once", "always", "deny"} {
			if ckIsEdit(it) && o != "deny" && (o == "always" || !editShown) {
				continue
			}
			if slices.Contains(it.Options, o) {
				out = append(out, o)
			}
		}
		if it.DenyMessage && slices.Contains(it.Options, "deny") {
			out = append(out, "reason")
		}
		return out
	}
	if pk == nil {
		return []string{"once", "deny"}
	}
	var out []string
	for _, act := range pk.actions {
		switch act {
		case "approve":
			out = append(out, "once")
		case "approve_always":
			out = append(out, "always")
		case "deny":
			out = append(out, "deny")
		}
	}
	return out
}

func ckDecisionWords(d string) string {
	switch d {
	case "once":
		return locales.CockpitAllowOnce()
	case "always":
		return locales.CockpitAllowAlways()
	case "reason":
		return locales.CockpitRejectWhy()
	}
	return locales.CockpitReject()
}

func (a *App) ckDecisionKey(d string) string {
	switch d {
	case "once":
		if _, _, waiting := a.ckDraftWaiting(); waiting {
			// Ctrl+Enter sends the reply while the box holds text.
			return ""
		}
		return chordName(chAllow)
	case "always":
		return chordName(chAlways)
	case "deny":
		return chordName(chReject)
	}
	return ""
}

// ckFetchPeek reads the prompt on the pane's screen once per prompt.
func (a *App) ckFetchPeek(ag client.Agent, it session.AttentionItem) {
	ck := &a.ck
	cl := a.cockpitClient()
	if cl == nil || ck.peek != nil && ck.peek.window == ag.Window && (ck.peek.loading || ck.peek.at.After(time.Unix(0, it.Since))) {
		return
	}
	pk := &ckPeek{window: ag.Window, loading: true, at: time.Now()}
	ck.peek = pk
	go func() {
		var out struct {
			PromptID string   `json:"prompt_id"`
			Lines    []string `json:"lines"`
			Options  []any    `json:"options"`
			Actions  []any    `json:"actions"`
			Risk     []string `json:"risk"`
		}
		err := cl.VerbInto("peek-prompt", map[string]any{"session": ag.Session, "window": ag.Window}, false, &out)
		a.update(func() {
			pk.loading = false
			if err != nil {
				return
			}
			pk.promptID, pk.lines, pk.risk = out.PromptID, out.Lines, out.Risk
			for _, x := range out.Actions {
				switch v := x.(type) {
				case string:
					pk.actions = append(pk.actions, v)
				case map[string]any:
					if s, ok := v["action"].(string); ok {
						pk.actions = append(pk.actions, s)
					}
				}
			}
		})
	}()
}

// ckAnswer answers the selected row's prompt with decision. It answers only
// the prompt the card drew: one that arrived since, under a fast chord,
// is not answered, and its card flashes as new. A click that allows a
// risky call takes a second click; the chord asks first (cockpit_ask.go).
// It reports whether the row had a prompt to answer.
func (a *App) ckAnswer(decision string) bool {
	ck := &a.ck
	r, ok := a.ckSelected()
	if !ok || r.item == nil || !ckAnswerable(*r.item) {
		return false
	}
	it := *r.item
	ag := ck.rowAgent(r)
	if r.agent < 0 {
		ag = client.Agent{Session: it.Session, Window: it.Window, Name: it.Name}
	}
	now := time.Now()
	if ck.shown.key != ckShownKey(it) {
		// Not drawn yet: the card shows it next, flashing as new.
		return true
	}
	if decision == "reason" {
		a.ckOpenReason()
		return true
	}
	if !ckHeld(it) {
		// A prompt on the pane's screen is answered only with the keys
		// its harness's rule declares for it: with none read, a key
		// would press nothing, and the daemon would refuse it.
		pk := ck.peek
		switch {
		case pk == nil || pk.window != ag.Window || pk.loading:
			a.ckPromptToast(locales.CockpitReadingPrompt())
			return true
		case !slices.Contains(a.ckOptions(it, pk), decision):
			a.ckPromptToast(locales.CockpitAnswerInPane())
			return true
		}
	}
	rules := it.Risk
	if !ckHeld(it) && ck.peek != nil {
		rules = ck.peek.risk
	}
	if (decision == "once" || decision == "always") && len(rules) > 0 && !ck.confirmed {
		// A click allows a risky call only on a second click; a key does
		// so only through the confirm step, which is the second step.
		key := ckShownKey(it)
		armed := ck.armed.decision == decision && ck.armed.item == key && now.Sub(ck.armed.at) <= ckRiskWindow
		if !armed {
			ck.armed = ckArmed{decision: decision, item: key, at: now}
			return true
		}
		ck.armed = ckArmed{}
	}
	a.ckSend(it, ag, decision, "")
	return true
}

// ckOpenReason opens the line for a reject with a reason.
func (a *App) ckOpenReason() {
	r, ok := a.ckSelected()
	if !ok || r.item == nil || !ckHeld(*r.item) {
		return
	}
	if !r.item.DenyMessage {
		a.ckPromptToast(locales.CockpitNoReason())
		return
	}
	a.ck.reasonFor, a.ck.reason = r.item.ID, ""
}

// ckSend sends the answer: reply-approval for a held approval, respond for
// a prompt on the pane's screen.
func (a *App) ckSend(it session.AttentionItem, ag client.Agent, decision, message string) {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	var verb string
	var p map[string]any
	allow := decision == "once" || decision == "always"
	if ckHeld(it) {
		verb = "reply-approval"
		p = map[string]any{"request_id": it.RequestID, "session": it.Session, "decision": decision, "summary": it.Summary}
		if message != "" {
			p["message"] = message
		}
		if allow && len(it.Risk) > 0 {
			p["risk_ack"] = it.Risk
		}
		if allow && it.Kind == session.AttentionPlan && it.PlanSHA != "" {
			p["plan_sha"] = it.PlanSHA
		}
		if allow && ckIsEdit(it) {
			sha, ok := a.ckEditAllowSHA(it)
			if !ok || decision != "once" {
				a.ckPromptToast(locales.CockpitAnswerInPane())
				return
			}
			p["edit_sha"] = sha
		}
	} else {
		verb = "respond"
		action := map[string]string{"once": "approve", "always": "approve_always", "deny": "deny"}[decision]
		p = map[string]any{"session": ag.Session, "window": ag.Window, "action": action, "timeout": 3000}
		if pk := a.ck.peek; pk != nil && pk.window == ag.Window {
			if pk.promptID != "" {
				p["prompt_id"] = pk.promptID
			}
			if allow && len(pk.risk) > 0 {
				p["risk_ack"] = pk.risk
			}
		}
	}
	name := ag.Name
	a.ckAnswered(it, ag)
	go func() {
		raw, err := cl.Verb(verb, p, true)
		a.update(func() {
			if err != nil {
				// Not answered: the item shows again.
				delete(a.ck.answered, it.ID)
				_, toast := ckAnswerOutcome(verb, name, decision, nil, err)
				a.toast(toast)
				a.ck.peek = nil
				a.refreshCockpit()
				return
			}
			taken, toast := ckAnswerOutcome(verb, name, decision, raw, nil)
			a.toast(toast)
			if taken {
				a.ckAdvanceAfter(ag)
			} else {
				// Not taken: the item shows again, as it is now.
				delete(a.ck.answered, it.ID)
			}
			a.ck.peek = nil
			a.refreshCockpit()
		})
	}()
}

var _ = fmt.Sprint

// ckAnswerOutcome reads the daemon's reply to an answer: whether the agent
// took it, and the toast that says what happened. The toast names an answer
// only when the daemon says the agent took it, and it names the daemon's
// decision, which is the one the hook prints. A hold can end between the
// card's draw and the answer (it timed out, or its hook stopped), and keys
// sent to a screen prompt can leave it where it was: then the agent still
// waits, and the toast says so (Mac round 1, W-M1).
func ckAnswerOutcome(verb, name, decision string, raw []byte, err error) (bool, string) {
	if err != nil {
		var ve *session.VerbCallError
		if verb == "reply-approval" && errors.As(err, &ve) && ve.Code == session.ErrVerbInvalidParams &&
			ve.Hint != nil && ve.Hint.Param == "request_id" {
			// The hold ended before the reply: the harness asks in its
			// pane now.
			return false, locales.CockpitStoppedWaiting(name)
		}
		return false, ckAnswerError(err)
	}
	if verb == "reply-approval" {
		var out struct {
			Applied  bool   `json:"applied"`
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if jsonUnmarshal(raw, &out) != nil || !out.Applied || out.Decision == "" {
			if out.Reason == "changed" {
				return false, locales.CockpitCallChanged(name)
			}
			return false, locales.CockpitStoppedWaiting(name)
		}
		return true, locales.CockpitAnswered(name, ckDecisionWords(out.Decision))
	}
	var out struct {
		SettledBy string `json:"settled_by"`
	}
	if jsonUnmarshal(raw, &out) != nil {
		return false, locales.CockpitStillWaits(name)
	}
	switch out.SettledBy {
	case "state", "prompt":
		// The agent left the prompt, or moved on to another one.
		return true, locales.CockpitAnswered(name, ckDecisionWords(decision))
	case "gone":
		return false, locales.CockpitStoppedWaiting(name)
	}
	return false, locales.CockpitStillWaits(name)
}

// ckCanReject reports whether the selected row's card takes a reject: an
// approval or a plan a hook holds, or a prompt on the screen whose rule
// has a deny.
func (a *App) ckCanReject() bool {
	r, ok := a.ckSelected()
	if !ok || r.item == nil {
		return false
	}
	it := *r.item
	if ckHeld(it) {
		return slices.Contains(it.Options, "deny")
	}
	if it.Kind != session.AttentionApproval && it.Kind != session.AttentionPlan {
		return false
	}
	pk := a.ck.peek
	return pk != nil && pk.window == it.Window && !pk.loading && slices.Contains(pk.actions, "deny")
}

// ckAnswerError says why an answer was not sent, and what to do: the
// daemon's codes are not words a person reads.
func ckAnswerError(err error) string {
	var ve *session.VerbCallError
	if !errors.As(err, &ve) {
		return locales.CockpitAnswerFailed()
	}
	switch ve.Code {
	case "prompt_changed", "not_blocked", "no_prompt", "no_rule":
		return locales.CockpitAnswerInPane()
	case "not_human":
		return locales.CockpitNotHuman()
	case "risk_unacknowledged":
		return locales.CockpitJustChanged()
	}
	return locales.CockpitAnswerFailed()
}

// ckSource is where an agent's state came from, in words.
func ckSource(src string) string {
	switch src {
	case "stall":
		return locales.CockpitSourceStall()
	case "screen":
		return locales.CockpitSourceScreen()
	case "osc":
		return locales.CockpitSourceOsc()
	case "detect":
		return locales.CockpitSourceDetect()
	case "transcript":
		return locales.CockpitSourceTranscript()
	case "program":
		return locales.CockpitSourceProgram()
	}
	return src
}

// ckButtonOrder is the card's answers in the order they are read: reject
// first, then allow, then allow from now on, so the safe answer is the
// first and the primary one sits between them. Reject and say why is
// drawn apart, as a quiet button by the scope line.
func ckButtonOrder(opts []string) []string {
	rank := map[string]int{"deny": 0, "once": 1, "always": 2, "reason": 3}
	out := slices.Clone(opts)
	slices.SortStableFunc(out, func(x, y string) int { return rank[x] - rank[y] })
	return out
}

// ckSessionScope reports whether every rule always adds lasts for this
// session only, so its button may say so.
func ckSessionScope(scope []string) bool {
	if len(scope) == 0 {
		return false
	}
	for _, l := range scope {
		if !strings.HasSuffix(l, " for this session") {
			return false
		}
	}
	return true
}

// capButton is a card's answer: a button with its chord as key caps, so the
// keys read as keys (DESIGN.md section 6). The caps take the label's colour
// on a faint ground of it, so they read on the accent fill and on a
// control's.
func capButton(c *ui.Context, key, label, keys string, primary bool, pal *palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(hControl).Padding(0, sp3, 0, sp3).Gap(sp2).Radius(rControl).AlignItems(ui.Center).Shrink(0).Label(label)
	if keys != "" {
		b.Tooltip(label + " (" + keys + ")")
	}
	bg, fg := pal.control, pal.text
	if primary {
		bg, fg = pal.accentFill, pal.accentInk
	}
	switch {
	case b.Pressed() && primary:
		bg = awayFrom(bg, fg, roles.PressStep)
	case b.Pressed():
		bg = pressedQuiet(pal).Over(bg)
	case b.Hovered() && primary:
		bg = awayFrom(bg, fg, roles.HoverStep)
	case b.Hovered():
		bg = pal.controlHover
	}
	face(b, pal, b.AnimateColor("bg", bg, ui.Animation{Duration: dPress})).TextColor(fg)
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).SingleLine()
		if keys != "" {
			// One cap for the chord, as the key line under the view draws it.
			ui.Row(c).Height(hKbd).MinWidth(hKbd).Padding(0, 5).Radius(rChip).Background(fg.Alpha(0.12)).Shrink(0).
				AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, keys).FontSize(fsCaption).FontWeight(wMedium).TextColor(hintOn(fg, bg)).SingleLine()
			})
		}
	})
	return b.Clicked()
}

// ghostButton is a quiet button: no fill, text2, the hover ground under the
// pointer (DESIGN.md section 6, ghost), at the small height.
func ghostButton(c *ui.Context, key, label string, pal *palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(hControlSm).Padding(0, sp2).Radius(rControl).AlignItems(ui.Center).Shrink(0).Label(label)
	fg, bg := pal.textMuted, ui.Transparent
	switch {
	case b.Pressed():
		bg, fg = pressedQuiet(pal), pal.text
	case b.Hovered():
		bg, fg = pal.rowHover, pal.text
	}
	b.Background(bg).TextColor(fg).Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).SingleLine()
	})
	return b.Clicked()
}

// ckCardOpensPane reports whether the approval card offers only Open pane
// for it: nothing here can answer the prompt.
func (a *App) ckCardOpensPane(it session.AttentionItem) bool {
	if !ckAnswerable(it) || ckIsQuestion(it) {
		return false
	}
	pk := a.ck.peek
	return len(a.ckOptions(it, pk)) == 0 && (pk == nil || !pk.loading)
}

// ckCardAllows reports whether the approval card for it offers Allow once,
// so the key line offers its chord only then (council mac-1, W-M2).
func (a *App) ckCardAllows(it session.AttentionItem) bool {
	return ckAnswerable(it) && !ckIsQuestion(it) && slices.Contains(a.ckOptions(it, a.ck.peek), "once")
}

// ckPromptKey names a prompt across its hold: the agent's pane and what it
// asks. A held prompt and the same prompt once its hold ends are two items.
func ckPromptKey(it session.AttentionItem) string {
	return it.Session + "\x00" + it.Window + "\x00" + it.Summary
}

// ckNoteHeld notes the prompts the card drew held, for ckHoldEnded.
func (a *App) ckNoteHeld(it session.AttentionItem) {
	if !ckHeld(it) {
		return
	}
	if a.ck.wasHeld == nil {
		a.ck.wasHeld = map[string]bool{}
	}
	a.ck.wasHeld[ckPromptKey(it)] = true
}

// ckHoldEnded reports whether the card drew the prompt held before, and
// its hold has ended: the prompt waits in the pane.
func (a *App) ckHoldEnded(it session.AttentionItem) bool {
	return !ckHeld(it) && a.ck.wasHeld[ckPromptKey(it)]
}
