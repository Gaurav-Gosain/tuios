package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The cockpit's look: the fleet column on the sidebar's ground, the focus
// area on the card's, rows two lines high with the TUI's anatomy (state,
// name, harness, session, age; then the one thing worth reading).

const (
	ckFleetW = 316
	ckSideW  = 268
	ckRowH   = 46
	ckRowH1  = 30
)

// ckGroupTitle names a group, as its heading says it.
func ckGroupTitle(g int) string {
	switch g {
	case ckNeeds:
		return locales.CockpitGroupNeeds()
	case ckReview:
		return locales.CockpitGroupReview()
	case ckWorking:
		return locales.CockpitGroupWorking()
	case ckIdle:
		return locales.CockpitGroupIdle()
	case ckSettled:
		return locales.CockpitGroupSettled()
	}
	return locales.CockpitGroupSnoozed()
}

// harnessName is a harness id as people say it.
func harnessName(id string) string {
	switch id {
	case "claude-code":
		return "claude"
	case "gemini-cli":
		return "gemini"
	case "":
		return locales.CockpitNoHarness()
	}
	return id
}

// harnessIcon is a harness's logo, the terminal's for one without.
func harnessIcon(id string) *ui.SVG {
	if i := programIcons[harnessName(id)]; i != nil {
		return i
	}
	return terminalIcon
}

// ckGround is the fleet and side columns' ground: a step off the card's.
func ckGround(pal *palette) ui.Color {
	if pal.dark {
		return pal.card.Mix(ui.Hex("#000000"), 0.18)
	}
	return pal.card.Mix(pal.sidebar, 0.6)
}

// cockpitCard draws the cockpit in the place of the session's card.
func (a *App) cockpitCard(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32) {
	ck := &a.ck
	ck.rows = a.buildCockpitRows(c)
	a.ckClamp()
	card := paned(ui.Column(c.Key("cockpit")).Grow(1).MinHeight(0).Radius(14).Background(pal.card), pal).Clip()
	card.Children(func() {
		a.cockpitHeader(c, pal, bar, shown)
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		w, _ := c.Size()
		body := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
		body.Children(func() {
			a.cockpitFleet(c, pal)
			ui.Box(c).Width(1).Shrink(0).Background(pal.divider)
			ui.Column(c.Key("focus")).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
				a.cockpitFocus(c, pal)
			})
			if w-a.chrome.sideW > 1080 {
				ui.Box(c).Width(1).Shrink(0).Background(pal.divider)
				a.cockpitSide(c, pal)
			}
		})
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		a.cockpitHints(c, pal)
	})
}

// cockpitHeader is "Agents", a count per state that filters, and close.
func (a *App) cockpitHeader(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32) {
	ck := &a.ck
	var n [ckGroups]int
	for _, r := range ck.rows {
		if r.kind == ckRowHead {
			n[r.group] = r.count
		}
	}
	ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Gap(14).Padding(0, 10, 0, a.chrome.headerPad).DragWindow().Children(func() {
		if shown < 0.5 {
			ui.Row(c).Absolute().Left(a.chrome.buttonsLeft(bar)).Top(0).Bottom(0).AlignItems(ui.Center).Gap(2).Opacity(1 - 2*shown).Children(func() {
				iconButton(c, "show-sidebar", sidebarIcon, a.tip(locales.SidebarShow(), "sidebar.toggle"), pal, a.toggleSidebar)
			})
		}
		ui.Text(c, locales.CockpitTitle()).FontSize(14).FontWeight(650).TextColor(pal.text)
		count := func(key string, g int, st proto.Status, unread bool, label string) {
			if n[g] == 0 {
				return
			}
			b := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Label(label)
			on := g == ckNeeds && ck.needsOnly
			switch {
			case on:
				b.Background(pal.attnSoft)
			case b.Hovered():
				b.Background(pal.rowHover)
			}
			b.Children(func() {
				statusIcon(c, pal, st, unread, 12)
				ui.Text(c, itoa(n[g])).FontSize(12.5).FontWeight(650).TextColor(pal.text).FontFeatures("tnum")
				ui.Text(c, label).FontSize(12.5).TextColor(pal.textMuted)
			})
			if b.Clicked() {
				if g == ckNeeds {
					ck.needsOnly = !ck.needsOnly
				} else if ck.folded[g] {
					a.ckToggleFold(g)
				}
			}
		}
		count("c-needs", ckNeeds, proto.Waiting, true, locales.CockpitCountNeeds())
		count("c-review", ckReview, proto.Idle, true, locales.CockpitCountReview())
		count("c-working", ckWorking, proto.Working, false, locales.CockpitCountWorking())
		count("c-idle", ckIdle, proto.Idle, false, locales.CockpitCountIdle())
		ui.Spacer(c)
		if ck.err != "" {
			ui.Text(c, ck.err).FontSize(12).TextColor(pal.danger).SingleLine().Ellipsis("…").MaxWidth(320)
		}
		iconButton(c, "ck-close", closeIcon, a.tip(locales.CockpitClose(), "agents.toggle"), pal, a.closeCockpit)
		a.controlsRoom(c, 0)
	})
}

// cockpitFleet is the column of agents, grouped, with its keys.
func (a *App) cockpitFleet(c *ui.Context, pal *palette) {
	ck := &a.ck
	col := ui.Column(c.Key("fleet")).Width(ckFleetW).Shrink(0).AlignItems(ui.Stretch).Background(ckGround(pal)).
		Focusable().FocusRing(false).Bind(&ck.fleetFocus).Label(locales.CockpitTitle())
	a.cockpitKeys(c, col)
	col.Children(func() {
		if !ck.loaded {
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
			return
		}
		if len(ck.rows) == 0 {
			a.cockpitEmpty(c, pal)
			return
		}
		ui.Scroll(c.Key("rows")).Grow(1).Padding(8, 8, 12, 8).Children(func() {
			for _, r := range ck.rows {
				switch r.kind {
				case ckRowHead:
					a.ckHead(c, pal, r)
				case ckRowMore:
					a.ckMore(c, pal, r)
				default:
					a.ckRowView(c, pal, r)
				}
			}
		})
	})
}

// cockpitEmpty says no agent runs, and how to start one.
func (a *App) cockpitEmpty(c *ui.Context, pal *palette) {
	ui.Column(c).Grow(1).Center().Gap(8).Padding(20).Children(func() {
		ui.Text(c, locales.CockpitEmptyTitle()).FontSize(14).FontWeight(650).TextColor(pal.text)
		ui.Text(c, locales.CockpitEmptyDetail()).FontSize(12.5).TextColor(pal.textMuted).TextAlign(ui.Center).LineHeight(1.45).MaxWidth(240)
	})
}

// ckHead is a group's heading: its title, its count and a fold mark. A
// click folds it.
func (a *App) ckHead(c *ui.Context, pal *palette, r ckRow) {
	ck := &a.ck
	row := ui.Row(c.Key(r.key)).Height(28).Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(6).Margin(6, 0, 0, 0).Cursor(ui.CursorPointer)
	if r.key == ck.sel {
		chosen(row.Background(pal.rowSelected), pal)
	} else if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		a.ckToggleFold(r.group)
	}
	row.Children(func() {
		ic := chevronDownIcon
		if r.folded {
			ic = chevronIcon
		}
		ui.Icon(c, ic).Size(11, 11).TextColor(pal.textFaint)
		t := ui.Text(c, ckGroupTitle(r.group)).FontSize(12).FontWeight(650).TextColor(pal.textMuted)
		if r.group == ckNeeds {
			t.TextColor(pal.attention)
		}
		ui.Text(c, itoa(r.count)).FontSize(12).TextColor(pal.textFaint).FontFeatures("tnum")
		ui.Box(c).Grow(1).Height(1).Background(pal.divider).Margin(0, 0, 0, 6)
	})
}

// ckMore is the row a long group folds its tail into.
func (a *App) ckMore(c *ui.Context, pal *palette, r ckRow) {
	row := ui.Row(c.Key(r.key)).Height(ckRowH1).Padding(0, 10, 0, 32).AlignItems(ui.Center).Radius(6).Cursor(ui.CursorPointer)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		a.ckOpenMore(r.group)
	}
	row.Children(func() {
		ui.Text(c, locales.CockpitMore(r.count)).FontSize(12.5).TextColor(pal.textMuted)
	})
}

// ckLine2 is what a row's second line says, and whether it is an ask, whose
// verb takes the attention colour.
func ckLine2(ag client.Agent, it *session.AttentionItem) (verb, rest string) {
	if it != nil {
		s := it.Summary
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan:
			if tool, target, ok := ckAsk(s); ok {
				return locales.CockpitAllowTool(tool), target
			}
			return locales.CockpitAsks(), s
		case session.AttentionQuestion, session.AttentionAsk:
			return locales.CockpitAsks(), s
		case session.AttentionMail:
			return locales.CockpitMail(), s
		case session.AttentionErrored:
			return locales.CockpitFailed(), s
		}
		return "", s
	}
	if ag.Message != "" {
		if t, r, ok := strings.Cut(ag.Message, " "); ok && len(t) < 14 && t[0] >= 'A' && t[0] <= 'Z' {
			return "", t + "\x00" + r
		}
		return "", ag.Message
	}
	return "", ""
}

// ckRowView is one agent's or item's row.
func (a *App) ckRowView(c *ui.Context, pal *palette, r ckRow) {
	ck := &a.ck
	ag := ck.rowAgent(r)
	name, harness, sess := ag.Name, ag.Harness, ag.Session
	if r.item != nil && r.agent < 0 {
		name, harness, sess = r.item.Name, r.item.Harness, r.item.Session
		if r.item.Host != "" {
			sess = r.item.Host + ":" + sess
		}
	}
	if name == "" {
		name = locales.CockpitUnnamed()
	}
	verb, rest := ckLine2(ag, r.item)
	two := (verb != "" || rest != "") && r.group != ckIdle && r.group != ckSettled
	h := float32(ckRowH1)
	if two {
		h = ckRowH
	}
	sel := r.key == ck.sel
	row := ui.Row(c.Key(r.key)).Height(h).Padding(0, 10, 0, 8).Gap(10).AlignItems(ui.Center).Radius(8).Margin(0, 0, 1, 0)
	if pal.dark {
		row.Border(1, ui.Transparent)
	}
	bg := ui.Transparent
	switch {
	case sel && ck.fleetFocus.FocusWithin(c):
		bg = pal.rowSelected
		chosen(row, pal)
	case sel:
		bg = pal.rowSelected.Alpha(0.6)
	case row.Hovered():
		bg = pal.rowHover
	}
	row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 110 * time.Millisecond}))
	if row.Pressed() || row.Clicked() {
		a.ckSelect(r)
		ck.fleetFocus.Focus()
	}
	if row.DoubleClicked() && r.agent >= 0 {
		a.goToAgent(ag)
	}
	row.ContextMenu(func(m *ui.Menu) { a.ckRowMenu(m, r) })
	tip := filepath.Clean(ag.Cwd)
	if ag.Source != "" && ag.Source != "report" {
		tip += "\n" + locales.CockpitStateFrom(ag.Source)
	}
	pointerTooltip(c, row, "ck-tip-"+r.key, tip)
	st, unread := ckStatus(ag, r.item)
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				statusIcon(c, pal, st, unread, 13)
				nm := ui.Text(c, name).FontSize(13.5).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).TextColor(pal.text)
				if unread || r.group == ckNeeds {
					nm.FontWeight(650)
				}
				ui.Icon(c, harnessIcon(harness)).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
				ui.Text(c, harnessName(harness)).FontSize(12).TextColor(pal.textMuted).SingleLine().Shrink(0)
				ui.Spacer(c)
				ui.Text(c, sess).FontSize(11.5).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(20).MaxWidth(90)
				since := ag.StateAt / int64(time.Millisecond)
				if r.item != nil && r.item.Since > 0 {
					since = r.item.Since / int64(time.Millisecond)
				}
				ui.Text(c, ago(since, c.Now())).FontSize(11.5).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Shrink(0)
			})
			if two {
				ui.RichText(c, ckLine2Spans(pal, verb, rest)...).FontSize(12).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, 0, 20)
			}
		})
	})
}

// ckLine2Spans draws a second line: an ask's verb in the attention colour,
// a tool's name in bold, the rest muted.
func ckLine2Spans(pal *palette, verb, rest string) []ui.Span {
	var out []ui.Span
	if verb != "" {
		out = append(out, ui.Span{Text: verb + "  ", Color: pal.attention, Weight: 650})
	}
	if t, r, ok := strings.Cut(rest, "\x00"); ok {
		out = append(out, ui.Span{Text: t + "  ", Color: pal.text, Weight: 600}, ui.Span{Text: r, Color: pal.textMuted})
	} else {
		out = append(out, ui.Span{Text: rest, Color: pal.textMuted})
	}
	return out
}

// ckRowMenu is a row's context menu: every action the row has.
func (a *App) ckRowMenu(m *ui.Menu, r ckRow) {
	ck := &a.ck
	a.ckSelect(r)
	if r.agent >= 0 {
		ag := ck.agents[r.agent]
		if m.Item(locales.CockpitOpenPane()).Chosen() {
			a.goToAgent(ag)
		}
		if m.Item(locales.CockpitReply()).Chosen() {
			a.ckFocusReply()
		}
		label := locales.CockpitSettle()
		if ag.Settled {
			label = locales.CockpitUnsettle()
		}
		if m.Item(label).Chosen() {
			a.ckSettle()
		}
	}
	if r.item != nil {
		m.Separator()
		if held := ckHeld(*r.item); held {
			if m.Item(locales.CockpitAllowOnce()).Chosen() {
				a.ckAnswer("once", true)
			}
			if m.Item(locales.CockpitReject()).Chosen() {
				a.ckAnswer("deny", true)
			}
		}
		label := locales.CockpitSnooze()
		if r.item.SnoozedUntil != 0 {
			label = locales.CockpitWake()
		}
		if m.Item(label).Chosen() {
			a.ckSnooze()
		}
		if m.Item(locales.CockpitDismiss()).Chosen() {
			a.ckDismiss()
		}
	}
}

// cockpitKeys are the fleet's keys, as the TUI's cockpit has them. They act
// while the fleet holds the focus, so a key typed in the reply box is text.
func (a *App) cockpitKeys(c *ui.Context, col ui.Element) {
	ck := &a.ck
	if !ck.fleetFocus.FocusWithin(c) {
		return
	}
	key := func(mods ui.Modifiers, k ui.Key) bool { return col.Shortcut(mods, k) }
	switch {
	case key(0, ui.KeyJ), key(0, ui.KeyDown):
		a.ckMove(1)
	case key(0, ui.KeyK), key(0, ui.KeyUp):
		a.ckMove(-1)
	case key(0, ui.KeyEnter):
		if r, ok := a.ckSelected(); ok {
			switch {
			case r.kind == ckRowHead:
				a.ckToggleFold(r.group)
			case r.agent >= 0:
				a.goToAgent(ck.agents[r.agent])
			}
		}
	case key(0, ui.KeySpace), key(0, ui.KeyRight), key(0, ui.KeyLeft):
		if r, ok := a.ckSelected(); ok && r.kind == ckRowHead {
			a.ckToggleFold(r.group)
		}
	case key(0, ui.KeyA):
		a.ckAnswer("once", false)
	case key(ui.Shift, ui.KeyA):
		a.ckAnswer("always", false)
	case key(0, ui.KeyR):
		if !a.ckAnswer("deny", false) {
			a.ckFocusReply()
		}
	case key(ui.Shift, ui.KeyR):
		a.ckOpenReason()
	case key(0, ui.KeyI):
		a.ckFocusReply()
	case key(ui.Shift, ui.KeyD), key(0, ui.KeyD):
		a.ckSettle()
	case key(0, ui.KeyU):
		a.ckUndoSettle()
	case key(0, ui.KeyZ):
		a.ckSnooze()
	case key(0, ui.KeyX):
		a.ckDismiss()
	case key(0, ui.KeyF):
		ck.needsOnly = !ck.needsOnly
	case key(0, ui.KeyBracketRight):
		a.ckStepTab(1)
	case key(0, ui.KeyBracketLeft):
		a.ckStepTab(-1)
	case key(0, ui.KeyEscape):
		a.closeCockpit()
	}
}

// cockpitHints is the key line at the foot.
func (a *App) cockpitHints(c *ui.Context, pal *palette) {
	ui.Row(c).Height(30).Shrink(0).AlignItems(ui.Center).Gap(14).Padding(0, 14).Children(func() {
		hint := func(k, what string) {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				kbd(c, pal, k)
				ui.Text(c, what).FontSize(12).TextColor(pal.textMuted).SingleLine()
			})
		}
		hint("↵", locales.CockpitHintOpen())
		hint("a", locales.CockpitHintAllow())
		hint("r", locales.CockpitHintReply())
		hint("D", locales.CockpitHintSettle())
		hint("z", locales.CockpitHintSnooze())
		hint("f", locales.CockpitHintFilter())
		hint("[ ]", locales.CockpitHintTabs())
		ui.Spacer(c)
		n := 0
		for _, ag := range a.ck.agents {
			if ag.Window != "" {
				n++
			}
		}
		ui.Text(c, locales.CockpitAgentCount(n)).FontSize(12).TextColor(pal.textFaint).SingleLine()
	})
}

// agentsRow is the sidebar's row that opens the cockpit, with how many
// agents need the person.
func (a *App) agentsRow(c *ui.Context, pal *palette) {
	// The agents that need the person: the cockpit's count once read, else
	// the panes shown and the other workspaces' marks.
	waiting := 0
	if a.ck.loaded {
		for _, ag := range a.ck.agents {
			if ag.State == "needs_input" || ag.State == "errored" {
				waiting++
			}
		}
	} else {
		for _, s := range a.st.Sessions {
			for _, p := range s.Panes {
				if p.Status == proto.Waiting {
					waiting++
				}
			}
			for _, w := range s.Workspaces {
				if w.N != s.Workspace && w.Status == proto.Waiting {
					waiting++
				}
			}
		}
	}
	on := a.ck.open
	row := ui.Row(c.Key("agents-row")).Height(rowH).Padding(0, 10, 0, rowInset).Gap(10).AlignItems(ui.Center).Radius(8).Margin(0, 0, 6, 0).
		Label(locales.ActionAgents()).Cursor(ui.CursorPointer)
	if pal.dark {
		row.Border(1, ui.Transparent)
	}
	switch {
	case on:
		chosen(row.Background(pal.rowSelected), pal)
	case row.Hovered():
		row.Background(pal.rowHover)
	}
	pointerTooltip(c, row, "agents-tip", a.tip(locales.ActionAgents(), "agents.toggle"))
	if row.Clicked() {
		a.toggleCockpit()
	}
	row.Children(func() {
		ui.Icon(c, agentsIcon).Size(15, 15).TextColor(pal.textMuted)
		ui.Text(c, locales.ActionAgents()).FontSize(13.5).TextColor(pal.text)
		ui.Spacer(c)
		if waiting > 0 {
			ui.Text(c, itoa(waiting)).FontSize(11.5).FontWeight(700).TextColor(pal.onDone).Padding(1, 7).Radius(9).Background(pal.attention)
		}
	})
}

// ckAsk reads an approval's summary, as a hook or a screen rule writes it
// ("approve Bash: go test ./...", "Allow Bash go test ./..."), into the
// tool and what it acts on.
func ckAsk(summary string) (tool, target string, ok bool) {
	v, rest, found := strings.Cut(summary, " ")
	if !found {
		return "", "", false
	}
	switch strings.ToLower(v) {
	case "allow", "approve":
	default:
		return "", "", false
	}
	if t, r, found := strings.Cut(rest, ": "); found && !strings.Contains(t, " ") {
		return t, r, true
	}
	if t, r, found := strings.Cut(rest, " "); found {
		return t, r, true
	}
	return rest, "", true
}

// ckMessage is a quiet centred message in the focus area.
func (a *App) ckMessage(c *ui.Context, pal *palette, title, detail string) {
	ui.Column(c).Grow(1).Center().Gap(6).Padding(24).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.text).TextAlign(ui.Center)
		if detail != "" {
			ui.Text(c, detail).FontSize(12.5).TextColor(pal.textMuted).MaxWidth(380).TextAlign(ui.Center).LineHeight(1.45)
		}
	})
}

// clock is a time in unix milliseconds as hours and minutes.
func clock(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("15:04")
}

// ckCodeBg is the ground of code and diffs: a step under the card.
func ckCodeBg(pal *palette) ui.Color {
	if pal.dark {
		return pal.card.Mix(ui.Hex("#000000"), 0.28)
	}
	return pal.card.Mix(pal.sidebar, 0.75)
}
