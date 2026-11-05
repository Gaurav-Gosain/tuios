package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The cockpit's look: the fleet column on the sidebar's ground, the focus
// area on the card's, rows two lines high with the TUI's anatomy (state,
// name, harness, session, age; then the one thing worth reading).

const (
	ckFleetW    = 316
	ckFleetMinW = 236
	// ckStackBelow is the card width under which the cockpit shows one
	// column at a time: the agents column at its narrowest beside a focus
	// area too narrow for the thread and the reply box.
	ckStackBelow = 680
	// ckNarrowBelow is the card width under which the cockpit drops what
	// it shows twice: the counts in its header, the harness by the name.
	ckNarrowBelow = 440
	ckSideW       = 268
	ckRowH        = hRow2
	ckRowH1       = 30
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
	a.ckTakeJump()
	a.ckClamp()
	a.ckTakeAdvance(c)
	// The card's own width, as the frame lays it out: the window less
	// both sidebars and the gaps (view.go).
	cw := a.side.rowW
	ck.stacked = cw < ckStackBelow
	ck.narrow = cw < ckNarrowBelow
	ck.fleetW = ckFleetWidth(cw)
	if ck.stacked {
		ck.fleetW = cw - 2*cardGap
	}
	card := paned(ui.Column(c.Key("cockpit")).Grow(1).MinHeight(0).Radius(rPane).Background(pal.card), pal).Clip()
	card.Children(func() {
		a.cockpitHeader(c, pal, bar, shown)
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		// With no agent, the empty state takes the whole card: no column
		// says "Select an agent", no hint names a key that does nothing.
		empty := ck.loaded && len(ck.rows) == 0
		body := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
		body.Children(func() {
			// The board and the mail for the person take the whole card.
			// The agents column stays, zero wide: it holds the keys that
			// come back to it.
			switch ck.view {
			case viewBoard:
				a.cockpitFleet(c, pal, 0, false)
				a.boardView(c, pal, "")
				return
			case viewMail:
				a.cockpitFleet(c, pal, 0, false)
				a.humanMailView(c, pal)
				return
			}
			if empty {
				a.cockpitFleet(c, pal, 0, true)
				return
			}
			if ck.stacked {
				// One column at a time: the agents, or the chosen one.
				// The agents column stays, empty, while the focus shows:
				// it holds the keys.
				focus := ck.stackFocus
				a.cockpitFleet(c, pal, 0, !focus)
				if focus {
					fc := ui.Column(c.Key("focus")).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch)
					ck.focusW = fc.Bounds().W
					fc.Children(func() {
						a.cockpitFocus(c, pal)
					})
				}
				return
			}
			a.cockpitFleet(c, pal, ckFleetWidth(cw), true)
			ui.Box(c).Width(1).Shrink(0).Background(pal.divider)
			fc := ui.Column(c.Key("focus")).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch)
			ck.focusW = fc.Bounds().W
			fc.Children(func() {
				a.cockpitFocus(c, pal)
			})
			if cw > 1080 {
				ui.Box(c).Width(1).Shrink(0).Background(pal.divider)
				a.cockpitSide(c, pal)
			}
		})
		if !empty || ck.view != viewAgents {
			ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
			a.cockpitHints(c, pal)
		}
		a.lightbox(c, pal)
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
	// Needs you counts what the badge counts (cockpit_needs.go).
	if ck.loaded {
		n[ckNeeds] = len(ck.needs())
	}
	ui.Row(c).Height(headerH).Shrink(0).AlignItems(ui.Center).Gap(sp3).Padding(0, 10, 0, a.chrome.headerPad).DragWindow().Children(func() {
		if shown < 0.5 {
			ui.Row(c).Absolute().Left(a.chrome.buttonsLeft(bar)).Top(0).Bottom(0).AlignItems(ui.Center).Gap(2).Opacity(1 - 2*shown).Children(func() {
				iconButton(c, "show-sidebar", sidebarIcon, a.tip(locales.SidebarShow(), "sidebar.toggle"), pal, a.toggleSidebar)
			})
		}
		ui.Text(c, locales.CockpitTitle()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		count := func(key string, g int, st proto.Status, unread bool, label string) {
			if n[g] == 0 || ck.narrow {
				// A narrow card has no room: the groups' heads count them.
				return
			}
			b := ui.ButtonBase(c.Key(key)).Height(hControlSm).Padding(0, 8).Gap(6).Radius(rRow).AlignItems(ui.Center).Shrink(0).Label(label)
			on := g == ckNeeds && ck.needsOnly
			switch {
			case on:
				b.Background(pal.attnSoft)
			case b.Hovered():
				b.Background(pal.rowHover)
			}
			b.Children(func() {
				ckMark(c, pal, st, unread, 12)
				ui.Text(c, itoa(n[g])).FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum")
				if !ck.stacked {
					ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted)
				}
			})
			if ck.stacked {
				pointerTooltip(c, b, key+"-tip", label)
			}
			if b.Clicked() {
				c.Invalidate()
				if ck.view != viewAgents {
					a.ckSetView(viewAgents)
				} else if g == ckNeeds {
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
		ui.Box(c).Size(1, 16).Shrink(0).Background(pal.divider)
		a.ckViewButton(c, pal, viewBoard, locales.CockpitTabBoard(), 0, locales.CockpitShowBoard())
		a.ckViewButton(c, pal, viewMail, locales.CockpitTabMail(), a.ck.mailNeeds(), locales.CockpitShowMail())
		ui.Spacer(c)
		if ck.err != "" {
			oneLine(c, ck.err, fsSmall, 0).TextColor(pal.dangerText).MaxWidth(320)
		}
		if ck.narrow {
			iconButton(c, "ck-new", plusIcon, a.tip(locales.CockpitNewAgent(), "agents.new")+" (n)", pal, a.newAgent)
		} else if newAgentButton(c, pal, a.tip(locales.CockpitNewAgent(), "agents.new")) {
			a.newAgent()
		}
		iconButton(c, "ck-close", closeIcon, a.tip(locales.CockpitClose(), "agents.toggle"), pal, a.closeCockpit)
		a.controlsRoom(c, 0)
	})
}

// cockpitFleet is the column of agents, grouped, with its keys.
// ckFleetWidth is the agents column's width in a cockpit w DIPs wide: its
// full width while the focus keeps room for two board columns, narrower
// down to ckFleetMinW in a narrow window.
func ckFleetWidth(w float32) float32 {
	return min(max(w-560, ckFleetMinW), ckFleetW)
}

// A width of 0 fills the card, as the stacked cockpit draws it, and shown
// false draws the column empty and zero wide: it still holds the focus
// and the keys while the stacked cockpit shows the focus area.
func (a *App) cockpitFleet(c *ui.Context, pal *palette, width float32, shown bool) {
	ck := &a.ck
	col := ui.Column(c.Key("fleet")).AlignItems(ui.Stretch).Background(ckGround(pal)).
		Focusable().FocusRing(false).Bind(&ck.fleetFocus).Label(locales.CockpitTitle())
	if shown && width > 0 {
		// Toasts show over the foot of the list (toasts.go).
		ck.fleetRect = col.Bounds()
	} else {
		ck.fleetRect = ui.Rect{}
	}
	switch {
	case !shown:
		col.Width(0).Shrink(0)
	case width == 0:
		col.Grow(1).Basis(0).MinWidth(0)
	default:
		col.Width(width).Shrink(0)
	}
	a.cockpitKeys(c, col)
	if !shown {
		return
	}
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
	emptyBlock(c, pal, agentsIcon, locales.CockpitEmptyTitle(), locales.CockpitEmptyDetail(), false, func() {
		if button(c, "ck-empty-new", locales.CockpitNewAgent(), "n", true, pal) {
			a.newAgent()
		}
	})
}

// ckHead is a group's heading: its title, its count and a fold mark. A
// click folds it.
func (a *App) ckHead(c *ui.Context, pal *palette, r ckRow) {
	ck := &a.ck
	row := ui.Row(c.Key(r.key)).Height(28).Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(rRow).Margin(6, 0, 0, 0).Cursor(ui.CursorPointer)
	if r.key == ck.sel {
		chosen(row.Background(pal.rowSelected), pal)
	} else if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		c.Invalidate()
		a.ckToggleFold(r.group)
	}
	row.Children(func() {
		ic := chevronDownIcon
		if r.folded {
			ic = chevronIcon
		}
		ui.Icon(c, ic).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
		t := oneLine(c, ckGroupTitle(r.group), fsSmall, wMedium).TextColor(pal.textMuted)
		if r.group == ckNeeds {
			t.TextColor(pal.attnText)
		}
		ui.Text(c, itoa(r.count)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").Shrink(0)
		ui.Box(c).Grow(1).Height(1).Background(pal.divider).Margin(0, 0, 0, 6)
	})
}

// ckMore is the row a long group folds its tail into.
func (a *App) ckMore(c *ui.Context, pal *palette, r ckRow) {
	row := ui.Row(c.Key(r.key)).Height(ckRowH1).Padding(0, 10, 0, 32).AlignItems(ui.Center).Radius(rRow).Cursor(ui.CursorPointer)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		c.Invalidate()
		a.ckOpenMore(r.group)
	}
	row.Children(func() {
		ui.Text(c, locales.CockpitMore(r.count)).FontSize(fsSmall).TextColor(pal.textMuted)
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
	name, harness := a.ckAgentName(ag), ag.Harness
	if r.item != nil && r.agent < 0 {
		name, harness = r.item.Name, r.item.Harness
	}
	if name == "" {
		name = locales.CockpitUnnamed()
	}
	verb, rest := ckLine2(ag, r.item)
	// Every agent's row is two lines high, an idle one too: a row with
	// nothing to say says so, so that the rows keep one height (council
	// round 1, visual 23).
	h := float32(ckRowH)
	sel := r.key == ck.sel
	row := ui.Row(c.Key(r.key)).Height(h).Padding(0, 10, 0, 8).Gap(10).AlignItems(ui.Center).Radius(rControl).Margin(0, 0, 1, 0)
	keysRing(row, sel && ck.fleetFocus.FocusWithin(c) && ck.lightbox == nil && !a.keysOwned(), pal, rControl)
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
	row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: dHoverOut}))
	if row.Pressed() || row.Clicked() {
		a.ckSelect(r)
		ck.fleetFocus.Focus()
		ck.guard.prev = nil
	}
	if row.Clicked() && ck.stacked {
		ck.stackFocus = true
		c.Invalidate()
	}
	if row.DoubleClicked() && r.agent >= 0 {
		a.goToAgent(ag)
	}
	row.ContextMenu(func(m *ui.Menu) { a.ckRowMenu(m, r) })
	// The folder, beside the row: the state's source is the Details
	// column's, not the tooltip's.
	rowTooltip(c, row, "ck-tip-"+r.key, a.hostOf(ag.Session).shortDir(filepath.Clean(ag.Cwd)))
	st, unread := ckStatus(ag, r.item)
	since := ag.StateAt / int64(time.Millisecond)
	if r.item != nil && r.item.Since > 0 {
		since = r.item.Since / int64(time.Millisecond)
	}
	age := ago(since, c.Now())
	weight := wMedium
	if unread || r.group == ckNeeds {
		weight = wSemibold
	}
	// The name keeps its room: the harness's name gives way first, which
	// its icon still tells (DESIGN.md section 7). The trailing slot holds
	// the age alone: the session is the focus column's to say. The row's
	// content: the column less the list's and the row's padding and edge,
	// then the mark, the name, the age and four gaps.
	letter := a.ckAttemptLetter(ag)
	room := ck.fleetW - 16 - 18 - 2 - 14 - 4*7 - natural(c, age, fsSmall, 0) - natural(c, name, fsBody, weight)
	if letter != "" {
		room -= attemptChipW + 7
	}
	harnessW := 12 + 4 + natural(c, harnessName(harness), fsSmall, 0)
	showHarness := room >= harnessW
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ckMark(c, pal, st, unread, 14)
				if letter != "" {
					attemptChip(c, pal, letter)
				}
				oneLine(c, name, fsBody, weight).TextColor(pal.text)
				ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
					ic := ui.Icon(c, harnessIcon(harness)).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
					if showHarness {
						ui.Text(c, harnessName(harness)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
					} else {
						ic.Tooltip(harnessName(harness))
					}
				})
				ui.Spacer(c)
				ui.Text(c, age).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Shrink(0)
			})
			if verb == "" && rest == "" && ag.Message == "" {
				// An agent just started says its task until it reports.
				rest = ck.spawnTasks[ag.Name]
			}
			if verb != "" || rest != "" {
				ui.RichText(c, ckLine2Spans(pal, verb, flat(rest))...).FontSize(fsSmall).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, 0, 21)
			} else {
				ui.Text(c, locales.CockpitNoActivity()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Margin(0, 0, 0, 21)
			}
		})
	})
}

// attemptChipW is the width of an attempt's letter chip.
const attemptChipW = 16

// attemptChip is an attempt's letter, A, B, C, in a chip before its name:
// the attempts of a fan run the same agent, so the letter tells them apart.
func attemptChip(c *ui.Context, pal *palette, letter string) {
	ui.Row(c).Size(attemptChipW, attemptChipW).Shrink(0).Radius(rChip).Background(pal.accent.Alpha(0.16)).Center().
		Label(locales.CockpitAttempt(letter)).Children(func() {
		ui.Text(c, letter).FontSize(fsCaption).FontWeight(wSemibold).TextColor(pal.accentText).SingleLine()
	})
}

// ckLine2Spans draws a second line: an ask's verb in the attention colour,
// a tool's name in bold, the rest muted.
func ckLine2Spans(pal *palette, verb, rest string) []ui.Span {
	var out []ui.Span
	if verb != "" {
		out = append(out, ui.Span{Text: verb + "  ", Color: pal.attnText, Weight: wSemibold})
	}
	if t, r, ok := strings.Cut(rest, "\x00"); ok {
		out = append(out, ui.Span{Text: t + "  ", Color: pal.text, Weight: wMedium}, ui.Span{Text: r, Color: pal.textMuted})
	} else {
		out = append(out, ui.Span{Text: rest, Color: pal.textMuted})
	}
	return out
}

// ckRowMenu is a row's context menu: every action the row has.
func (a *App) ckRowMenu(m *ui.Menu, r ckRow) {
	// No state changes here but in a chosen item's: the builder also runs
	// to collect the menu's shortcuts.
	ck := &a.ck
	if r.agent >= 0 {
		ag := ck.agents[r.agent]
		if m.Item(locales.CockpitOpenPane()).Chosen() {
			a.ckSelect(r)
			a.goToAgent(ag)
		}
		if m.Item(locales.CockpitReply()).Chosen() {
			a.ckSelect(r)
			a.ckFocusReply()
		}
		label := locales.CockpitSettle()
		if ag.Settled {
			label = locales.CockpitUnsettle()
		}
		if m.Item(label).Chosen() {
			a.ckSelect(r)
			a.ckSettle()
		}
	}
	if r.item != nil {
		m.Separator()
		if held := ckHeld(*r.item); held {
			// An allow from the menu asks as the keys do: the confirm
			// step says what runs, and Enter does it.
			if m.Item(locales.CockpitAllowOnce()).Chosen() {
				a.ckSelect(r)
				a.ckAskFor(askOnce)
			}
			if m.Item(locales.CockpitReject()).Chosen() {
				a.ckSelect(r)
				a.ckAnswer("deny")
			}
		}
		if r.item.HeldID != 0 && m.Item(locales.CockpitPassMail()).Chosen() {
			a.ckSelect(r)
			a.ckPassHeld(*r.item)
		}
		label := locales.CockpitSnooze()
		if r.item.SnoozedUntil != 0 {
			label = locales.CockpitWake()
		}
		// A held approval waits on its hook and cannot be snoozed.
		if !ckHeld(*r.item) && m.Item(label).Chosen() {
			a.ckSelect(r)
			a.ckSnooze()
		}
		if m.Item(locales.CockpitDismiss()).Chosen() {
			a.ckSelect(r)
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
	if ck.lightbox != nil {
		if key(0, ui.KeyEscape) || key(0, ui.KeySpace) {
			ck.lightbox = nil
			c.Invalidate()
		}
		return
	}
	if a.ownedKeys(col) {
		return
	}
	if ck.question != nil && a.ckQuestionKeys(c, col) {
		return
	}
	if k, ok := a.guard(col, &ck.guard, a.ckListKeys()); ok {
		a.ckLetter(k)
		c.Invalidate()
		return
	}
	switch {
	case key(ui.Cmd, ui.KeyEnter):
		// The chord allows a call that is not risky at once; a risky one
		// still asks.
		a.ckAllowChord()
	case key(0, ui.KeyDown):
		a.ckMove(1)
	case key(0, ui.KeyUp):
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
	case ck.stacked && ck.stackFocus && (key(0, ui.KeyLeft) || key(0, ui.KeyEscape)):
		ck.stackFocus = false
	case ck.stacked && !ck.stackFocus && key(0, ui.KeyRight) && a.ckOnAgent():
		ck.stackFocus = true
	case key(0, ui.KeyRight), key(0, ui.KeyLeft):
		if r, ok := a.ckSelected(); ok && r.kind == ckRowHead {
			a.ckToggleFold(r.group)
		}
	case key(0, ui.KeyBracketRight):
		a.ckStepTab(1)
		ck.guard.setPrev(']', func() { a.ckStepTab(-1) }, a.ckProse)
		c.Invalidate()
		return
	case key(0, ui.KeyBracketLeft):
		a.ckStepTab(-1)
		ck.guard.setPrev('[', func() { a.ckStepTab(1) }, a.ckProse)
		c.Invalidate()
		return
	case key(0, ui.KeyEscape):
		if ck.view != viewAgents {
			a.ckSetView(viewAgents)
		} else {
			a.closeCockpit()
		}
	default:
		return
	}
	// A key that is no letter: what ran before it was no sentence's start.
	ck.guard.prev = nil
	// The key came in this build: build again to show what it did.
	c.Invalidate()
}

// ckLetter runs a list's letter or Space that the typing guard let
// through (cockpit_guard.go). A letter that changes something only asks:
// the confirm step does it on Enter (cockpit_ask.go).
func (a *App) ckLetter(k ckKey) {
	ck := &a.ck
	defer func() {
		// The letter that opened a confirm step goes with the text, if
		// the next key shows the person was typing.
		if q := ck.question; q != nil && q.opener == "" {
			q.opener = string(keyRune(k))
			if b := ck.guard.before; b != nil {
				// A move or view letter came first: it is the sentence's
				// first letter, and is taken back with the step.
				q.opener, q.openerUndo = string(b.r)+q.opener, b.undo
			}
		}
		ck.guard.before = nil
	}()
	// A move or view letter may be the first letter of a sentence: it
	// notes how to take itself back, and typed text right after it does
	// (cockpit_guard.go).
	g := &ck.guard
	prev := func(undo func()) { g.setPrev(keyRune(k), undo, a.ckProse) }
	sel := ck.sel
	backSel := func() {
		if i := ck.index(sel); i >= 0 {
			a.ckSelect(ck.rows[i])
		}
	}
	view, tab := ck.view, ck.tab
	backView := func() { a.ckSetView(view); ck.fleetFocus.Focus() }
	backTab := func() { a.ckSetTab(tab) }
	if k.shift {
		switch k.key {
		case ui.KeyA:
			a.ckAskFor(askAlways)
		case ui.KeyR:
			a.ckOpenReason()
		case ui.KeyD:
			a.ckAskFor(askSettle)
		case ui.KeyM:
			a.ckSetView(viewMail)
			prev(backView)
		}
		return
	}
	switch k.key {
	case ui.KeyJ:
		a.ckMove(1)
		prev(backSel)
	case ui.KeyK:
		a.ckMove(-1)
		prev(backSel)
	case ui.KeyH:
		if ck.stacked && ck.stackFocus {
			ck.stackFocus = false
			prev(func() { ck.stackFocus = true })
		}
	case ui.KeyL:
		if ck.stacked && !ck.stackFocus && a.ckOnAgent() {
			ck.stackFocus = true
			prev(func() { ck.stackFocus = false })
		}
	case ui.KeySpace:
		if r, ok := a.ckSelected(); ok && r.kind == ckRowHead {
			a.ckToggleFold(r.group)
			prev(func() { a.ckToggleFold(r.group) })
		} else if im := a.ckNewestImage(); im != nil {
			// Space shows the agent's newest image whole.
			ck.lightbox = im
			prev(func() { ck.lightbox = nil })
		}
	case ui.KeyA:
		a.ckAskFor(askOnce)
	case ui.KeyR:
		// r rejects an approval the card answers, as the TUI's, after the
		// confirm step; on any other row it puts the keys in the reply box.
		if !a.ckCanReject() {
			a.ckWriteAfter(k)
		} else {
			a.ckAskFor(askDeny)
		}
	case ui.KeyI:
		a.ckWriteAfter(k)
	case ui.KeyD:
		a.ckAskFor(askSettle)
	case ui.KeyU:
		a.ckUndoSettle()
	case ui.KeyZ:
		a.ckAskFor(askSnooze)
	case ui.KeyX:
		a.ckAskFor(askDismiss)
	case ui.KeyP:
		a.ckAskFor(askPass)
	case ui.KeyM:
		a.ckSetTab(tabMail)
		prev(backTab)
	case ui.KeyB:
		a.ckSetView(viewBoard)
		prev(backView)
	case ui.KeyV:
		a.ckSetTab(tabChanges)
		prev(backTab)
	case ui.KeyF:
		ck.needsOnly = !ck.needsOnly
		prev(func() { ck.needsOnly = !ck.needsOnly })
	case ui.KeyN:
		// A sheet a letter opened starts only on Ctrl+Enter or a click:
		// typed text that lands in its task never starts an agent.
		a.newAgent()
		if sp := ck.spawn; sp != nil {
			sp.byKey = true
			sp.byKeyFresh = strings.TrimSpace(sp.task.text) == ""
		}
	case ui.KeyC:
		a.openCompare()
	}
}

// ckWriteAfter is i, or r where it writes: the keys go to the reply box.
// Right after a move or view letter it is a sentence's second letter, as
// the i of "fix": both are taken back and go to the box with the text.
func (a *App) ckWriteAfter(k ckKey) {
	g := &a.ck.guard
	if b := g.before; b != nil {
		g.before = nil
		if b.undo != nil {
			b.undo()
		}
		if a.ckProse(string(b.r) + string(keyRune(k))) {
			return
		}
	}
	a.ckFocusReply()
}

// cockpitHints is the key line at the foot.
func (a *App) cockpitHints(c *ui.Context, pal *palette) {
	// The hints that fit, from the left; the count of agents keeps its
	// room at the right.
	room := a.side.rowW - 28 - 100
	ui.Row(c).Height(hRow).Shrink(0).AlignItems(ui.Center).Gap(sp3).Padding(0, 14).Clip().Children(func() {
		hint := func(k, what string) {
			need := natural(c, k, fsCaption, wMedium) + 10 + 6 + natural(c, what, fsSmall, 0) + sp3
			if need > room {
				room = -1
				return
			}
			room -= need
			keyHint(c, pal, k, what)
		}
		if a.ck.question != nil && a.ck.view == viewAgents {
			// The open confirm step takes the key line: what Enter does.
			a.ckQuestionLine(c, pal)
			return
		}
		switch {
		case a.ck.view == viewBoard:
			hint("h l", locales.CockpitHintColumns())
			hint("⇧H ⇧L", locales.CockpitHintMoveCard())
			hint("n", locales.CockpitHintAddCard())
			hint("esc", locales.CockpitHintList())
		case a.ck.view == viewMail && a.ck.reply.box.FocusWithin(c):
			hint("↵", locales.CockpitHintSend())
			hint("esc", locales.CockpitHintBack())
		case a.ck.view == viewMail:
			hint("j k", locales.CockpitHintMove())
			hint("r", locales.CockpitHintReply())
			hint("esc", locales.CockpitHintList())
		case a.ck.reply.box.FocusWithin(c):
			// The keys of the field that has them.
			hint("↵", locales.CockpitHintSend())
			hint("⇧↵", locales.CockpitHintNewLine())
			hint(keyName("CmdOrCtrl+Enter"), locales.CockpitHintSendNow())
			hint("esc", locales.CockpitHintList())
		case a.ck.stacked && a.ck.stackFocus:
			hint("esc", locales.CockpitHintBack())
			hint("a", locales.CockpitHintAllow())
			hint("r", locales.CockpitHintReply())
		case a.ck.stacked:
			hint("↵", locales.CockpitHintOpen())
			hint("→", locales.CockpitHintDetails())
		default:
			if ag, ok := a.ckSelectedAgent(); ok && a.ckHasSiblings(ag) {
				hint("c", locales.CockpitHintCompare())
			}
			a.ckAllHints(hint)
		}
		ui.Spacer(c)
		if a.ck.narrow {
			return
		}
		n := 0
		for _, ag := range a.ck.agents {
			if ag.Window != "" {
				n++
			}
		}
		ui.Text(c, locales.CockpitAgentCount(n)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Shrink(0)
	})
}

// ckAllHints is the key line of a cockpit wide enough for both
// columns: only the keys that do something on the selected row (council
// round 4, S4).
func (a *App) ckAllHints(hint func(k, what string)) {
	does := func(k ui.Key, shift bool) bool { return a.ckKind(ckKey{key: k, shift: shift}) != kindProse }
	onAgent := a.ckOnAgent()
	if onAgent {
		hint("↵", locales.CockpitHintOpen())
	}
	if does(ui.KeyA, false) {
		hint("a", locales.CockpitHintAllow())
	}
	if onAgent {
		hint("r", locales.CockpitHintReply())
	}
	if ag, ok := a.ckSelectedAgent(); ok && (ag.Settled || ag.State != "working") {
		// A working agent has nothing to settle yet.
		hint("D", locales.CockpitHintSettle())
	}
	if does(ui.KeyZ, false) {
		hint("z", locales.CockpitHintSnooze())
	}
	hint("n", locales.CockpitHintNewAgent())
	hint("f", locales.CockpitHintFilter())
	if onAgent {
		hint("[ ]", locales.CockpitHintTabs())
	}
}

// agentsRow is the sidebar's row that opens the cockpit, with how many
// agents need the person.
func (a *App) agentsRow(c *ui.Context, pal *palette) {
	// The agents that need the person, from the one list the keys to them
	// read too (cockpit_needs.go).
	waiting := a.needsCount()
	on := a.ck.open
	row := ui.Row(c.Key("agents-row")).Height(rowH).Padding(0, 10, 0, rowInset).Gap(10).AlignItems(ui.Center).Radius(rRow).Margin(0, 0, 6, 0).
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
	if row.Clicked() && !a.ck.newHover {
		c.Invalidate()
		a.toggleCockpit()
	}
	row.Children(func() {
		ui.Icon(c, agentsIcon).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
		oneLine(c, locales.ActionAgents(), fsBody, wMedium).TextColor(pal.text)
		ui.Spacer(c)
		if waiting > 0 {
			countBadge(c, waiting, pal.attention, onAttention(pal))
		}
		// New agent, in space the row keeps for it: it shows while the
		// pointer is over the row, and its place never moves the badge.
		nb := ui.ButtonBase(c.Key("agents-new")).Size(22, 22).Radius(rRow).Center().Shrink(0).
			Label(locales.CockpitNewAgent()).Tooltip(a.tip(locales.CockpitNewAgent(), "agents.new"))
		if !row.Hovered() && !nb.Hovered() {
			nb.Opacity(0)
		} else if nb.Hovered() {
			nb.Background(pal.rowHover)
		}
		a.ck.newHover = nb.Hovered()
		nb.Children(func() { ui.Icon(c, plusIcon).Size(14, 14).TextColor(pal.textMuted) })
		if nb.Clicked() {
			c.Invalidate()
			a.newAgent()
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
	emptyBlock(c, pal, nil, title, detail, false, nil)
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

// ckMark is a state's mark as the cockpit draws it: the sidebar's shapes,
// held still. The sidebar's working ring turns and its needs-you dot
// pulses, which draws 60 frames a second for as long as one shows; the
// cockpit lists many agents and keeps the TUI's rule of no spinners, so it
// costs no frame while nothing changes.
func ckMark(c *ui.Context, pal *palette, st proto.Status, unread bool, size float32) ui.Element {
	return statusIcon(c, pal, st, unread, size)
}

// ckSide is the room at each side of the thread, its approval card and
// its reply box: at least min, more once the focus column is wider than
// threadW, so that no line of the thread runs longer (DESIGN.md
// section 9).
func (a *App) ckSide(min float32) float32 {
	return max(min, (a.ck.focusW-threadW)/2)
}

// newAgentButton is the agents view's New agent button: a plus and its
// label, with n as its key.
func newAgentButton(c *ui.Context, pal *palette, tip string) bool {
	b := ui.ButtonBase(c.Key("ck-new")).Height(hControlSm).Padding(0, 10, 0, 8).Gap(6).Radius(rControl).AlignItems(ui.Center).Shrink(0).
		Label(locales.CockpitNewAgent()).Tooltip(tip + " (n)")
	face(b, pal, pal.control)
	switch {
	case b.Pressed():
		b.Background(pal.control.Mix(pal.text, 0.12))
	case b.Hovered():
		b.Background(pal.controlHover)
	}
	b.Children(func() {
		ui.Icon(c, plusIcon).Size(14, 14).TextColor(pal.text).Shrink(0)
		ui.Text(c, locales.CockpitNewAgent()).FontSize(fsBody).FontWeight(wMedium).TextColor(pal.text).SingleLine()
	})
	return b.Clicked()
}
