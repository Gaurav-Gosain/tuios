package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
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

var ckTabOrder = []string{tabThread, tabChanges, tabCheckpoints, tabMail, tabBoard}

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

func (a *App) ckSetTab(t string) {
	a.ck.tab = t
	a.ckTabOpened()
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
	}
	return locales.CockpitIdle(), pal.textMuted
}

// cockpitFocus draws the selected row's agent.
func (a *App) cockpitFocus(c *ui.Context, pal *palette) {
	ck := &a.ck
	r, ok := a.ckSelected()
	if !ok || r.kind == ckRowHead || r.kind == ckRowMore {
		if ck.tab == tabBoard {
			a.boardView(c, pal, "")
			return
		}
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
	pad := float32(24)
	if ck.stacked {
		pad = 14
	}
	ui.Column(c).Shrink(0).Padding(14, pad, 0, pad).Gap(4).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ck.stacked {
				iconButton(c, "ck-back", chevronLeftIcon, a.tip(locales.CockpitBack(), ""), pal, func() { ck.stackFocus = false })
			}
			ui.Icon(c, harnessIcon(ag.Harness)).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
			name := oneLine(c, ag.Name, fsTitle, wSemibold).TextColor(pal.text).Cursor(ui.CursorPointer)
			pointerTooltip(c, name, "ck-name", locales.CockpitOpenPane())
			if name.Clicked() && ag.Window != "" {
				a.goToAgent(ag)
			}
			if !ck.narrow {
				ui.Text(c, harnessName(ag.Harness)).FontSize(fsBody).TextColor(pal.textMuted).SingleLine().Shrink(0)
				if m, ok := ag.Meta["model"].(string); ok && m != "" {
					oneLine(c, "· "+m, fsBody, 0).TextColor(pal.textFaint).Shrink(2)
				}
			}
			ui.Spacer(c)
			word, col := ckStatePill(pal, ag, r.item)
			chip(c, pal, col.Alpha(0.15), col, func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(col).Shrink(0)
				ui.Text(c, word).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
			}).Shrink(0)
			switch {
			case ag.Window == "":
			case ck.narrow:
				iconButton(c, "ck-open", openFileIcon, locales.CockpitOpenPane()+" (↵)", pal, func() { a.goToAgent(ag) })
			default:
				if sizedButton(c, "ck-open", locales.CockpitOpenPane(), "↵", false, hControlSm, rControl, pal) {
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
		if ag.Source != "" && ag.Source != "report" {
			where = append(where, locales.CockpitStateFrom(ag.Source))
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
	ui.Column(c.Key("tab-" + ck.tab)).Grow(1).MinHeight(ckTabMinH).ClipY().AlignItems(ui.Stretch).Children(func() {
		switch ck.tab {
		case tabChanges:
			a.changesView(c, pal, ag)
		case tabCheckpoints:
			a.checkpointsView(c, pal, ag)
		case tabMail:
			a.mailView(c, pal, ag)
		case tabBoard:
			a.boardView(c, pal, ag.Session)
		default:
			a.threadView(c, pal, ag)
		}
	})
	card := r.item != nil && ckAnswerable(*r.item)
	if !card && ck.tab != tabThread {
		return
	}
	// The card and the reply box take what the window leaves the tab's
	// content beyond its least, and scroll past it: in a short window they
	// would push past the card's foot over the key line.
	_, winH := c.Size()
	bottom := max(140, winH-ckFocusChromeH-ckTabMinH)
	ui.Scroll(c.Key("ck-bottom")).Shrink(0).MaxHeight(bottom).Children(func() {
		ui.Column(c).AlignItems(ui.Stretch).Children(func() {
			if card {
				a.approvalCard(c, pal, ag, *r.item)
			}
			if ck.tab == tabThread {
				a.replyBox(c, pal, ag)
			}
		})
	})
}

// ckFocusChromeH is the height around the focus area's tab content: the
// window's header, the agent's header and tabs, the key line. ckTabMinH
// is the least the tab's content keeps.
const (
	ckFocusChromeH = 250
	ckTabMinH      = 120
)

// ckTab is one tab, with its count where it has one.
func (a *App) ckTab(c *ui.Context, pal *palette, t string, ag client.Agent) {
	on := a.ck.tab == t
	b := ui.ButtonBase(c.Key("tab-"+t)).Height(hControl+2).Padding(0, 10).Gap(6).AlignItems(ui.Center).Shrink(0).Radius(rRow).Label(ckTabTitle(t))
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
		strings.Join(it.AlwaysScope, "\n"), it.Kind, strings.Join(it.Risk, ","), it.PlanSHA}, "\x00")
}

// ckPeek is a pane's prompt as peek-prompt read it.
type ckPeek struct {
	window, promptID string
	lines            []string
	options          []string
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
	if ck.shown.key != key {
		ck.shown = ckShown{key: key, since: now}
		ck.armed = ckArmed{}
	}
	if d := ck.shown.since.Add(ckAnswerSettle).Sub(now); d > 0 {
		// The buttons wake when the card has been read.
		c.After(d)
	}
	held := ckHeld(it)
	if !held {
		a.ckFetchPeek(ag, it)
	}
	pk := ck.peek
	settled := now.Sub(ck.shown.since) >= ckAnswerSettle
	risky := len(it.Risk) > 0 || pk != nil && len(pk.risk) > 0
	// The card is the card's ground with a needs-you edge and a bar on its
	// left: amber, red and blue do not meet on one brown fill, and the risk
	// lines read at 4.5:1 (council round 1, visual 16).
	card := ui.Column(c.Key("approval")).Shrink(0).Margin(0, a.ckSide(24), 10, a.ckSide(24)).Padding(14, 16, 14, 19).Gap(10).Radius(rCard).
		Background(pal.card).Border(1, pal.attention.Alpha(0.55))
	card.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X + 5, Y: r.Y + 12, W: 3, H: r.H - 24}, pal.attention, 1.5)
	})
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(pal.attention).Shrink(0)
			verb, rest := ckLine2(ag, &it)
			if verb == locales.CockpitAsks() {
				verb = locales.CockpitQuestion()
			}
			oneLine(c, verb+"?", fsBody, wSemibold).TextColor(pal.text)
			oneLine(c, locales.CockpitWaits(ag.Name, ago(it.Since/int64(time.Millisecond), now)), fsSmall, 0).TextColor(pal.textMuted).Shrink(2)
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
		if !held && pk != nil && len(pk.lines) > 0 {
			ui.Column(c).Radius(rControl).Background(ckCodeBg(pal)).Padding(8, 12).Gap(1).Clip().Children(func() {
				for _, l := range pk.lines {
					ui.Text(c, l).Font(mono).FontSize(fsCode).TextColor(pal.text).SingleLine()
				}
			})
		} else if target != "" && it.Kind == session.AttentionQuestion {
			// A question is the agent's prose, not a command: no prompt
			// sign and no code ground (council round 1, visual 8).
			ui.Text(c, target).FontSize(fsProse).LineHeight(22.0 / fsProse).TextColor(pal.text).MaxLines(6).Selectable()
		} else if target != "" {
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
			ui.Text(c, locales.CockpitPressAgain(ckDecisionKey(ck.armed.decision))).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.attnText)
			c.After(ck.armed.at.Add(ckRiskWindow).Sub(now))
		}
		if len(it.AlwaysScope) > 0 {
			ui.Text(c, locales.CockpitAlwaysAllows(strings.Join(it.AlwaysScope, ", "))).FontSize(fsSmall).TextColor(pal.textMuted)
		}
		if ck.reasonFor == it.ID {
			in := textField(c, "reason", &ck.reason, locales.CockpitReasonPlaceholder(), 0, pal).AutoFocus()
			if in.Submitted() {
				a.ckSend(it, ag, "deny", ck.reason)
				ck.reasonFor, ck.reason = "", ""
				ck.fleetFocus.Focus()
			} else if in.Shortcut(0, ui.KeyEscape) {
				ck.reasonFor, ck.reason = "", ""
				ck.fleetFocus.Focus()
			}
		}
		// The buttons wrap rather than cut the last in a narrow card.
		ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Opacity(map[bool]float32{true: 1, false: 0.55}[settled]).Children(func() {
			opts := a.ckOptions(it, pk)
			if len(opts) == 0 && (pk == nil || !pk.loading) {
				// Nothing here can answer it: say so, and offer the pane.
				ui.Text(c, locales.CockpitAnswerInPane()).FontSize(fsSmall).TextColor(pal.textMuted).MaxLines(2).MinWidth(0)
				// The view's one primary is Send: this one is secondary.
				if ag.Window != "" && sizedButton(c, "ans-open", locales.CockpitOpenPane(), "↵", false, hControl, rControl, pal) {
					a.goToAgent(ag)
				}
			}
			for _, o := range opts {
				// A risky command gets no primary button to allow it.
				if sizedButton(c, "ans-"+o, ckDecisionWords(o), ckDecisionKey(o), o == "once" && !risky, hControl, rControl, pal) {
					a.ckAnswer(o, true)
				}
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
		for _, o := range []string{"once", "always", "deny"} {
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

func ckDecisionKey(d string) string {
	switch d {
	case "once":
		return "a"
	case "always":
		return "A"
	case "reason":
		return "R"
	}
	return "r"
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

// ckAnswer answers the selected row's prompt with decision, under the TUI's
// rules: the card must have shown the prompt as it is for ckAnswerSettle,
// a key typed in the reply box a moment ago is not an answer, and an allow
// of a risky call takes a second press. It reports whether the row had a
// prompt to answer.
func (a *App) ckAnswer(decision string, click bool) bool {
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
	if !click && now.Sub(ck.typedAt) < ckTypingQuiet {
		a.toast(locales.CockpitWasTyping())
		return true
	}
	if ck.shown.key != ckShownKey(it) || now.Sub(ck.shown.since) < ckAnswerSettle {
		a.toast(locales.CockpitJustChanged())
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
			a.toast(locales.CockpitReadingPrompt())
			return true
		case !slices.Contains(a.ckOptions(it, pk), decision):
			a.toast(locales.CockpitAnswerInPane())
			return true
		}
	}
	rules := it.Risk
	if !ckHeld(it) && ck.peek != nil {
		rules = ck.peek.risk
	}
	if (decision == "once" || decision == "always") && len(rules) > 0 {
		key := ckShownKey(it)
		if !(ck.armed.decision == decision && ck.armed.item == key && now.Sub(ck.armed.at) <= ckRiskWindow) {
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
		a.toast(locales.CockpitNoReason())
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
	go func() {
		raw, err := cl.Verb(verb, p, true)
		a.update(func() {
			if err != nil {
				a.toast(ckAnswerError(err))
				a.ck.peek = nil
				a.refreshCockpit()
				return
			}
			var out struct {
				Applied *bool  `json:"applied"`
				Reason  string `json:"reason"`
			}
			_ = jsonUnmarshal(raw, &out)
			if out.Applied != nil && !*out.Applied {
				a.toast(locales.CockpitNotApplied(out.Reason))
			} else {
				a.toast(locales.CockpitAnswered(name, ckDecisionWords(decision)))
			}
			a.ck.peek = nil
			a.refreshCockpit()
		})
	}()
}

var _ = fmt.Sprint

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
