package main

import (
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
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
	// Toasts sit in one place: centred over the card, above its key line
	// (council round 5, visual S5).
	ck.cardRect = card.Bounds()
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
		ui.Row(c).Gap(sp2).AlignItems(ui.Center).Shrink(0).Children(func() {
			// Tilly, with the selected session on her screen (tilly.go).
			a.tilly(c, pal, "tilly", 24, a.session(a.selected))
			ui.Text(c, locales.CockpitTitle()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Shrink(0)
		})
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
			iconButton(c, "ck-new", plusIcon, a.tip(locales.CockpitNewAgent(), "agents.new"), pal, a.newAgent)
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
		if button(c, "ck-empty-new", locales.CockpitNewAgent(), chordName(chNewAgent), true, pal) {
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

// ckLine2 is what a row's second line says: a verb that names what the
// person is asked, in the attention colour, and the text. The verb comes
// from the item's kind, never from the agent's own words: a message's first
// word is not a state, and bolding it read as one (Mac round 1, V-M2).
func ckLine2(ag client.Agent, it *session.AttentionItem) (verb, rest string) {
	if it != nil {
		s := it.Summary
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan:
			if tool, target, ok := ckAsk(s); ok {
				return locales.CockpitAllowTool(tool), ckShortTarget(tool, target, ag.Cwd)
			}
			return locales.CockpitKindApproval(), s
		case session.AttentionQuestion, session.AttentionAsk:
			return locales.CockpitKindQuestion(), s
		case session.AttentionMail:
			return locales.CockpitMail(), s
		case session.AttentionErrored:
			return locales.CockpitFailed(), s
		}
		return "", s
	}
	if ag.Message != "" {
		if ckStopped(ag, it) {
			return "", locales.CockpitStopped(ag.Message)
		}
		return "", ag.Message
	}
	return "", ""
}

// ckShortTarget is what a row shows of an approval's target: an edit's path
// relative to the agent's folder ("retry/backoff.go"), or from home when it
// is outside it, so the file name is not cut off behind a long folder
// (Mac round 1, workflow S1). Any other tool's target is shown as sent.
func ckShortTarget(tool, target, cwd string) string {
	if !slices.Contains(integration.EditTools, tool) || !filepath.IsAbs(target) {
		return target
	}
	if cwd = strings.TrimSuffix(cwd, "/"); cwd != "" && strings.HasPrefix(target, cwd+"/") {
		return strings.TrimPrefix(target, cwd+"/")
	}
	return proto.ShortDir(target)
}

// ckStopped reports whether a row's text is old: the agent is at rest or
// went silent (the daemon shows a hooked pane that stopped reporting as
// unknown) and said nothing since, so its last message ("Running the retry
// tests") no longer says what it does. The line then reads "Stopped · ..."
// in text3.
func ckStopped(ag client.Agent, it *session.AttentionItem) bool {
	if it != nil || ag.Message == "" {
		return false
	}
	return ag.State == string(session.AgentStateIdle) || ag.State == string(session.AgentStateUnknown)
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
	// A row with no harness reported shows its terminal icon alone, not
	// the word "agent" (Mac round 1, visual S3).
	showHarness := room >= harnessW && harness != ""
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
					} else if harness != "" {
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
				ui.RichText(c, ckLine2Spans(pal, verb, flat(rest), ckStopped(ag, r.item))...).FontSize(fsSmall).SingleLine().Ellipsis("…").MinWidth(0).Margin(0, 0, 0, 21)
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
// the rest muted, or faint when it is old (ckStopped).
func ckLine2Spans(pal *palette, verb, rest string, stopped bool) []ui.Span {
	var out []ui.Span
	if verb != "" {
		out = append(out, ui.Span{Text: verb + " ", Color: pal.attnText, Weight: wSemibold})
	}
	col := pal.textMuted
	if stopped {
		col = pal.textFaint
	}
	return append(out, ui.Span{Text: rest, Color: col})
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

// cockpitKeys are the fleet's keys while it holds the focus. A printable
// key goes to the reply box (cockpit_guard.go). The rest are the list's
// moves, Enter and Escape, and the chords of ckChords.
func (a *App) cockpitKeys(c *ui.Context, col ui.Element) {
	ck := &a.ck
	if !ck.fleetFocus.FocusWithin(c) {
		return
	}
	key := func(mods ui.Modifiers, k ui.Key) bool { return col.Shortcut(mods, k) }
	if ck.lightbox != nil {
		if key(0, ui.KeyEscape) {
			ck.lightbox = nil
			c.Invalidate()
		}
		return
	}
	if a.ownedKeys(col) {
		return
	}
	if a.keysHeld() && a.ownListKey(c, col) {
		// The owner has the keys: they wait for where they go
		// (key_owner.go). Typed text goes on to the reply box.
		c.Invalidate()
		return
	}
	if ck.question != nil && a.ckQuestionKeys(c, col) {
		return
	}
	if text, ok := typedText(col); ok {
		// Typed text: it never acts. It goes to the selected agent's
		// reply box, which takes the keys that follow.
		if !a.ckProse(text) {
			a.toast(locales.CockpitKeysNeedAgent())
		}
		c.Invalidate()
		return
	}
	if m, k, ok := listKeyIn(col); ok && a.listKey(m, k) {
		// The key came in this build: build again to show what it did.
		c.Invalidate()
		return
	}
	if a.ckChordKeys(col) {
		c.Invalidate()
	}
}

// listKeys are the keys of the agents list that type no text and are not
// chords: Tab, the moves, Enter, the arrows and Escape.
var listKeys = []struct {
	mods ui.Modifiers
	key  ui.Key
}{
	{0, ui.KeyTab}, {ui.Shift, ui.KeyTab}, {0, ui.KeyDown}, {0, ui.KeyUp},
	{0, ui.KeyPageDown}, {0, ui.KeyPageUp}, {0, ui.KeyHome}, {0, ui.KeyEnd},
	{0, ui.KeyEnter}, {0, ui.KeyLeft}, {0, ui.KeyRight}, {0, ui.KeyEscape},
}

// listKeyIn is the key of listKeys the list got in this build.
func listKeyIn(col ui.Element) (ui.Modifiers, ui.Key, bool) {
	for _, k := range listKeys {
		if col.Shortcut(k.mods, k.key) {
			return k.mods, k.key, true
		}
	}
	return 0, 0, false
}

// listKey is the agents list's key route for a key that types no text: a
// key it got, and a key the owner lands there (key_owner.go). It reports
// whether the key did something.
func (a *App) listKey(mods ui.Modifiers, k ui.Key) bool {
	ck := &a.ck
	is := func(m ui.Modifiers, x ui.Key) bool { return mods == m && k == x }
	if is(0, ui.KeyTab) {
		// Tab goes on to the reply box, the next stop in the view, as a
		// form's Tab does; it was a second Down that stopped at the last
		// row (council round 5, feel S3). Shift+Tab in the box comes back.
		if ag, ok := a.ckSelectedAgent(); ok && ag.Window != "" && ag.Host == "" {
			a.ckFocusReply()
			return true
		}
	}
	switch {
	case is(0, ui.KeyDown), is(0, ui.KeyTab):
		a.ckMoveBy(1, 0)
	case is(0, ui.KeyUp), is(ui.Shift, ui.KeyTab):
		a.ckMoveBy(-1, 0)
	case is(0, ui.KeyPageDown):
		a.ckMoveBy(8, 0)
	case is(0, ui.KeyPageUp):
		a.ckMoveBy(-8, 0)
	case is(0, ui.KeyHome):
		a.ckMoveBy(0, -1)
	case is(0, ui.KeyEnd):
		a.ckMoveBy(0, 1)
	case is(0, ui.KeyEnter):
		if r, ok := a.ckSelected(); ok {
			switch {
			case r.kind == ckRowHead:
				a.ckToggleFold(r.group)
			case r.agent >= 0:
				a.goToAgent(ck.agents[r.agent])
			}
		}
	case ck.stacked && ck.stackFocus && (is(0, ui.KeyLeft) || is(0, ui.KeyEscape)):
		ck.stackFocus = false
	case ck.stacked && !ck.stackFocus && is(0, ui.KeyRight) && a.ckOnAgent():
		ck.stackFocus = true
	case is(0, ui.KeyRight), is(0, ui.KeyLeft):
		if r, ok := a.ckSelected(); ok && r.kind == ckRowHead {
			a.ckToggleFold(r.group)
		}
	case is(0, ui.KeyEscape):
		if ck.view != viewAgents {
			a.ckSetView(viewAgents)
		} else {
			a.closeCockpit()
		}
	default:
		return false
	}
	return true
}

// ckMoveBy moves the cursor by d selectable rows, or to the first (to -1)
// or the last (to 1).
func (a *App) ckMoveBy(d, to int) {
	ck := &a.ck
	switch to {
	case -1:
		for _, r := range ck.rows {
			if r.selectable() {
				a.ckSelect(r)
				return
			}
		}
	case 1:
		for _, r := range slices.Backward(ck.rows) {
			if r.selectable() {
				a.ckSelect(r)
				return
			}
		}
	}
	step := 1
	if d < 0 {
		step, d = -1, -d
	}
	for range d {
		a.ckMove(step)
	}
}

// ckChordKeys runs the agents list's chords (ckChords). It reports whether
// one came.
func (a *App) ckChordKeys(col ui.Element) bool {
	for _, ch := range ckChords {
		if ch.list && col.Shortcut(ch.mods, ch.key) {
			a.runChord(ch.id)
			return true
		}
	}
	return false
}

// chordKey runs the list's chord of a key the owner lands in the list
// (key_owner.go). It reports whether the key is one.
func (a *App) chordKey(mods ui.Modifiers, key ui.Key) bool {
	for _, ch := range ckChords {
		if ch.list && ch.mods == mods && ch.key == key {
			a.runChord(ch.id)
			return true
		}
	}
	return false
}

// runChord runs one of the agents list's chords.
func (a *App) runChord(id chordID) {
	ck := &a.ck
	switch id {
	case chAllow:
		a.ckAllowChord()
	case chAlways:
		a.ckAskFor(askAlways)
	case chReject:
		if a.ckCanReject() {
			a.ckAnswer("deny")
		}
	case chSettle:
		a.ckSettle()
	case chUndoSettle:
		a.ckUndoSettle()
	case chSnooze:
		r, ok := a.ckSelected()
		switch {
		case ok && r.item != nil && !ckHeld(*r.item):
			a.ckSnooze()
		case ok && r.item != nil:
			a.toast(locales.CockpitNoSnoozeHeld())
		default:
			// Say what the press found: nothing to snooze here.
			name := locales.CockpitUnnamed()
			if ag, ok := a.ckSelectedAgent(); ok && ag.Name != "" {
				name = ag.Name
			}
			a.toast(locales.CockpitNothingToSnooze(name))
		}
	case chBoard:
		if ck.view == viewBoard {
			a.ckSetView(viewAgents)
		} else {
			a.ckSetView(viewBoard)
		}
	case chMail:
		if ck.view == viewMail {
			a.ckSetView(viewAgents)
		} else {
			a.ckSetView(viewMail)
		}
	case chFilter:
		ck.needsOnly = !ck.needsOnly
	case chTabPrev:
		a.ckStepTab(-1)
	case chTabNext:
		a.ckStepTab(1)
	}
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
			if k == "" {
				// Not a key: plain text, as "Type to reply".
				ui.Text(c, what).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
				return
			}
			keyHint(c, pal, k, what)
		}
		if d := c.Now().Sub(a.ck.nothingAt); d < 1500*time.Millisecond && a.ck.view == viewAgents {
			// A chord found nothing to answer.
			c.After(1500*time.Millisecond - d)
			msg := locales.CockpitNothingWaits()
			if a.ck.nothingQuestion {
				msg = locales.CockpitQuestionTypeReply()
			}
			ui.Text(c, msg).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).SingleLine()
			return
		}
		if a.ck.question != nil && a.ck.view == viewAgents {
			// The open confirm step takes the key line: what Enter does.
			a.ckQuestionLine(c, pal)
			return
		}
		if b := a.ck.tabs.board; a.ck.view == viewBoard && b != nil && b.ask != "" {
			// The board's give asks in the key line, as the list's
			// confirm step does.
			t, _ := b.card(b.ask)
			ag, _ := a.ckSelectedAgent()
			ui.Text(c, locales.CockpitAskGive(t.Title, ag.Name)).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			keyHint(c, pal, "↵", locales.CockpitGiveWord())
			keyHint(c, pal, "Esc", locales.CommonCancel())
			return
		}
		switch {
		case a.ck.view == viewBoard:
			hint("← →", locales.CockpitHintColumns())
			hint(chordName(chCardLeft)+" "+chordName(chCardRight), locales.CockpitHintMoveCard())
			hint(chordName(chClaim), locales.CockpitHintClaim())
			hint(chordName(chNewAgent), locales.CockpitHintNewAgent())
			hint("", locales.CockpitTypeToAddCard())
			hint("Esc", locales.CockpitHintList())
		case a.ck.view == viewMail && a.ck.reply.box.FocusWithin(c):
			hint("↵", locales.CockpitHintSend())
			hint("Esc", locales.CockpitHintBack())
		case a.ck.view == viewMail:
			hint("↑ ↓", locales.CockpitHintMove())
			hint("", locales.CockpitTypeToReply())
			hint("Esc", locales.CockpitHintList())
		case a.ck.reply.box.FocusWithin(c):
			// The keys of the field that has them.
			hint("↵", locales.CockpitHintSend())
			hint("⇧↵", locales.CockpitHintNewLine())
			if ag, d, ok := a.ckDraftWaiting(); ok && d != nil && a.ckCardWaits(ag) {
				hint(keyName("CmdOrCtrl+Enter"), locales.CockpitHintQueue())
			} else if ok {
				hint(keyName("CmdOrCtrl+Enter"), locales.CockpitHintSendNow())
			}
			hint("Esc", locales.CockpitHintList())
		case a.ck.stacked && a.ck.stackFocus:
			hint("Esc", locales.CockpitHintBack())
			hint(chordName(chAllow), locales.CockpitHintAllow())
			hint("", locales.CockpitTypeToReply())
		case a.ck.stacked:
			hint("↵", locales.CockpitHintOpen())
			hint("→", locales.CockpitHintDetails())
		default:
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
// columns: the chords that do something on the selected row, Ctrl+Enter
// allow first (council round 4, S2).
func (a *App) ckAllHints(hint func(k, what string)) {
	r, ok := a.ckSelected()
	item := ok && r.item != nil
	// A question gets only its answers and reply: nothing to allow, reject
	// or snooze (council round 5, visual S4).
	question := item && ckIsQuestion(*r.item)
	answerable := item && ckAnswerable(*r.item) && !question
	// The chord to allow only where the card has the button for it.
	allows := item && a.ckCardAllows(*r.item)
	if ag, _, waiting := a.ckDraftWaiting(); waiting && a.ckCardWaits(ag) {
		// A card waits: Ctrl+Enter queues the draft, and types nothing
		// into the pane.
		hint(chordName(chAllow), locales.CockpitHintQueue())
	} else if waiting {
		// The reply box holds text: Ctrl+Enter sends it.
		hint(chordName(chAllow), locales.CockpitHintSendNow())
	} else if allows {
		hint(chordName(chAllow), locales.CockpitHintAllow())
	}
	if answerable {
		if a.ckCanReject() {
			hint(chordName(chReject), locales.CockpitHintReject())
		}
	}
	if a.ckOnAgent() {
		hint("↵", locales.CockpitHintOpen())
		hint("", locales.CockpitTypeToReply())
	}
	if ag, ok := a.ckSelectedAgent(); ok && (ag.Settled || ag.State != "working") {
		hint(chordName(chSettle), locales.CockpitHintSettle())
	}
	if item && !ckHeld(*r.item) && !question {
		hint(chordName(chSnooze), locales.CockpitHintSnooze())
	}
	hint(chordName(chNewAgent), locales.CockpitHintNewAgent())
	hint(chordName(chFilter), locales.CockpitHintFilter())
	if a.ckOnAgent() {
		hint(chordName(chTabPrev)+" "+chordName(chTabNext), locales.CockpitHintTabs())
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
			countBadge(c, waiting, pal.attnFill, pal.attnInk)
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

// ckAsk reads an approval's summary, as a hook, a screen rule or a report
// writes it ("approve Bash: go test ./...", "Allow Bash go test ./...",
// "Bash: rm -rf build/"), into the tool and what it acts on.
func ckAsk(summary string) (tool, target string, ok bool) {
	v, rest, found := strings.Cut(summary, " ")
	if !found {
		return "", "", false
	}
	switch strings.ToLower(v) {
	case "allow", "approve":
	default:
		// "Tool: target", a tool's name as one word before the colon.
		if t, ok := strings.CutSuffix(v, ":"); ok && ckToolName(t) {
			return t, rest, true
		}
		return "", "", false
	}
	if t, r, found := strings.Cut(rest, ": "); found && !strings.Contains(t, " ") {
		return t, r, true
	}
	if t, r, found := strings.Cut(rest, " "); found {
		return t, r, true
	}
	// A screen rule's "Allow Bash?" names the tool and asks: the row and
	// the button already ask, so the mark goes.
	return strings.TrimSuffix(rest, "?"), "", true
}

// ckToolName reports whether s reads as a tool's name: one word of letters,
// digits, _ . and -, that starts with a letter ("Bash", "mcp__fs__read").
func ckToolName(s string) bool {
	if s == "" || !unicode.IsLetter(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '-' {
			return false
		}
	}
	return true
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
		Label(locales.CockpitNewAgent()).Tooltip(tip)
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

// ownListKey gives the key the list got in this build to the owner while it
// has the keys, and reports whether one came.
func (a *App) ownListKey(c *ui.Context, col ui.Element) bool {
	if text, ok := typedText(col); ok {
		if !a.ckProse(text) {
			a.keepKey(heldKey{text: text, seq: c.ShortcutSeq()})
		}
		return true
	}
	if m, k, ok := listKeyIn(col); ok {
		a.keepKey(heldKey{mods: m, key: k, seq: c.ShortcutSeq()})
		return true
	}
	return false
}
