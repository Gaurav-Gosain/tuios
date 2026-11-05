package main

import (
	"sort"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The cockpit's views beside the agents: the task board of every session,
// with every stage, and the mail agents sent to the person. Each takes the
// whole card. b and Shift+M open them from the agents, Escape comes back,
// and prefix M opens the mail.

// ckSetView shows a view of the cockpit.
func (a *App) ckSetView(v int) {
	ck := &a.ck
	ck.view = v
	switch v {
	case viewBoard:
		a.fetchBoard()
		ck.focusView = true
	case viewMail:
		a.fetchMail()
		ck.focusView = true
	default:
		ck.fleetFocus.Focus()
	}
}

// openMailView opens the cockpit on the mail for the person.
func (a *App) openMailView() {
	if !a.ck.open {
		a.toggleCockpit()
	}
	a.ckSetView(viewMail)
}

// mailNeeds is how many agents have mail for the person: the Mail view's
// count, from the list the badge reads.
func (ck *cockpit) mailNeeds() int {
	if !ck.loaded {
		return 0
	}
	n := 0
	for _, x := range ck.needs() {
		if x.item != nil && x.tier == needMail {
			n++
		}
	}
	return n
}

// ckViewButton is a header button that shows a view, and the agents again
// while the view shows.
func (a *App) ckViewButton(c *ui.Context, pal *palette, v int, label string, n int, tip string) {
	ck := &a.ck
	on := ck.view == v
	b := ui.ButtonBase(c.Key("view-"+label)).Height(hControlSm).Padding(0, 10).Gap(6).Radius(rRow).AlignItems(ui.Center).Shrink(0).Label(label)
	switch {
	case on:
		chosen(b.Background(pal.rowSelected), pal)
	case b.Hovered():
		b.Background(pal.rowHover)
	}
	pointerTooltip(c, b, "view-tip-"+label, tip)
	b.Children(func() {
		col := pal.textMuted
		if on {
			col = pal.text
		}
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).TextColor(col).SingleLine()
		if n > 0 {
			countBadge(c, n, pal.attention, onAttention(pal))
		}
	})
	if b.Clicked() {
		c.Invalidate()
		if on {
			a.ckSetView(viewAgents)
		} else {
			a.ckSetView(v)
		}
	}
}

// humanMailView lists every message to the person, unread first, newest
// first within each, with a line to answer the one chosen.
func (a *App) humanMailView(c *ui.Context, pal *palette) {
	ck := &a.ck
	m := ck.tabs.mail
	if m == nil {
		a.fetchMail()
		m = ck.tabs.mail
	}
	var mine []ckMsg
	for _, msg := range m.msgs {
		if msg.To == "human" {
			mine = append(mine, msg)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].Unread != mine[j].Unread {
			return mine[i].Unread
		}
		return mine[i].SentAt > mine[j].SentAt
	})
	col := ui.Column(c.Key("human-mail")).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).
		Focusable().FocusRing(false).Bind(&m.focus).Label(locales.CockpitMailForYou())
	if ck.focusView {
		ck.focusView = false
		m.focus.Focus()
	}
	if m.focus.FocusWithin(c) && m.replyTo == 0 {
		a.mailKeys(c, col, m, mine)
	} else if m.focus.FocusWithin(c) && col.Shortcut(0, ui.KeyEscape) {
		a.ckSetView(viewAgents)
		c.Invalidate()
	}
	col.Children(func() {
		if len(mine) == 0 {
			emptyBlock(c, pal, nil, locales.CockpitNoMailForYou(), locales.CockpitNoMailForYouDetail(), false, nil)
			return
		}
		list := ui.Scroll(c.Key("hm")).Grow(1).MinHeight(0).Padding(14, a.ckSide(24), 10, a.ckSide(24))
		// Toasts show over the foot of the list, above the reply box.
		ck.fleetRect = list.Bounds()
		list.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				ui.Text(c, locales.CockpitMailForYou()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text)
				for _, msg := range mine {
					a.mailCard(c, pal, m, msg, true)
				}
			})
		})
		if m.replyTo != 0 {
			for _, msg := range mine {
				if msg.ID != m.replyTo || msg.Session != m.replySess {
					continue
				}
				// The agent's own reply box, as the thread has it, set
				// to mail; a sender that is no agent here gets the line.
				if ag, ok := ck.agentByWindow(msg.Session, msg.From); ok {
					a.replyBox(c, pal, ag)
				} else {
					a.mailCompose(c, pal, m, msg.Session, msg.From, msg.FromName)
				}
			}
		}
	})
}

// agentByWindow is the agent of this machine in a session's pane.
func (ck *cockpit) agentByWindow(sess, window string) (client.Agent, bool) {
	for _, ag := range ck.agents {
		if ag.Host == "" && ag.Session == sess && ag.Window == window {
			return ag, true
		}
	}
	return client.Agent{}, false
}

// mailKeys are the Mail view's keys while its list has them: j k and the
// arrows move between the messages, r and Enter answer the one the cursor
// is on, Escape goes back to the agents. Other letters are typed text: they
// take back the key that opened the view, and run nothing.
func (a *App) mailKeys(c *ui.Context, col ui.Element, m *ckMail, mine []ckMsg) {
	ck := &a.ck
	at := -1
	for i, msg := range mine {
		if mailKey(msg) == m.cur {
			at = i
		}
	}
	if at < 0 && len(mine) > 0 {
		at, m.cur = 0, mailKey(mine[0])
	}
	move := func(d int) {
		if at >= 0 {
			m.cur = mailKey(mine[min(max(at+d, 0), len(mine)-1)])
		}
	}
	reply := func() {
		if at >= 0 {
			a.mailReplyTo(m, mine[at])
		}
	}
	kind := func(k ckKey) keyKind {
		switch {
		case !k.shift && (k.key == ui.KeyJ || k.key == ui.KeyK):
			return kindMove
		case !k.shift && k.key == ui.KeyR && at >= 0:
			return kindView
		}
		return kindProse
	}
	lk := listKeys{kind: kind, hint: locales.CockpitKeysToMail}
	if k, ok := a.guard(col, &ck.guard, lk); ok {
		switch k.key {
		case ui.KeyJ:
			move(1)
		case ui.KeyK:
			move(-1)
		case ui.KeyR:
			reply()
		}
		c.Invalidate()
		return
	}
	key := func(k ui.Key) bool { return col.Shortcut(0, k) }
	switch {
	case key(ui.KeyDown):
		move(1)
	case key(ui.KeyUp):
		move(-1)
	case key(ui.KeyEnter):
		reply()
	case key(ui.KeyEscape):
		a.ckSetView(viewAgents)
	default:
		return
	}
	c.Invalidate()
}
