package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// The sidebar's Ended and Archived (session-history): the coding agents'
// conversations that kerod remembers and no pane runs now, to resume in a
// session of their own, as the palette's Recent Conversations do.

// endedShown is how many of Ended show until Show All.
const endedShown = 5

type hostEnded struct {
	h *host
	c proto.Conversation
}

// askConversations asks the hosts whose conversations changed for them,
// as their states counted (State.Conversations).
func (a *App) askConversations() {
	for _, h := range a.hosts {
		cl := h.cl
		if cl == nil || h.convKnown || h.convAsked {
			continue
		}
		h.convAsked = true
		gen := h.convGen
		go func() {
			var list []proto.Conversation
			err := cl.Call("conversations", nil, &list)
			a.update(func() {
				h.convAsked = false
				if err != nil {
					// An older kerod knows of none: ask it no more.
					h.convs, h.convKnown = nil, true
					return
				}
				h.convs, h.convKnown = list, h.convGen == gen
			})
		}()
	}
}

func (a *App) endedSections(c *ui.Context, pal *palette) {
	a.askConversations()
	// Those a pane runs now show as their sessions.
	running := map[proto.AgentSession]bool{}
	for _, s := range a.st.Sessions {
		for _, p := range s.Panes {
			if p.AgentSession != nil {
				running[*p.AgentSession] = true
			}
		}
	}
	var ended, archived []hostEnded
	for _, h := range a.hosts {
		for _, cv := range h.convs {
			if running[proto.AgentSession{Agent: cv.Agent, ID: cv.ID}] {
				continue
			}
			if cv.Archived {
				archived = append(archived, hostEnded{h, cv})
			} else {
				ended = append(ended, hostEnded{h, cv})
			}
		}
	}
	newest := func(x, y hostEnded) int { return cmp.Compare(y.c.Updated, x.c.Updated) }
	slices.SortStableFunc(ended, newest)
	slices.SortStableFunc(archived, newest)
	if len(ended)+len(archived) == 0 {
		a.skillHint(c, pal)
		return
	}
	a.endedSection(c, pal, "ended", locales.EndedTitle(), ended, &a.prefs.EndedOpen)
	a.endedSection(c, pal, "archived", locales.EndedArchived(), archived, &a.prefs.ArchivedOpen)
}

func (a *App) endedSection(c *ui.Context, pal *palette, key, title string, list []hostEnded, open *bool) {
	if len(list) == 0 {
		return
	}
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	hdr := ui.Row(c.Key("section-"+key)).Height(28).Margin(18, 0, 3, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Radius(rRow)
	if hdr.Clicked() {
		*open = !*open
		a.savePrefs()
	}
	hdr.ContextMenu(func(m *ui.Menu) { a.endedSectionMenu(m, key, list) })
	hdr.Children(func() {
		ui.Text(c, title).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted).SingleLine()
		ui.Textf(c, "%d", len(list)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum")
		chev := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(pal.textFaint)
		chev.Rotate(chev.AnimateTo("turn", 90*b2f(*open), ui.Snappy))
	})
	if !*open {
		return
	}
	shown := list
	if key == "ended" && !a.endedAll && len(list) > endedShown {
		shown = list[:endedShown]
	}
	now := c.Now()
	for _, he := range shown {
		a.endedRow(c, pal, he.h, he.c, now)
	}
	if len(shown) < len(list) {
		more := ui.Row(c.Key("show-all")).Height(26).Padding(0, 6, 0, inset).AlignItems(ui.Center).Radius(rRow)
		if more.Hovered() {
			more.Background(pal.rowHover)
		}
		if more.Clicked() {
			a.endedAll = true
		}
		more.Children(func() {
			ui.Text(c, locales.EndedShowAll(len(list))).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
		})
	}
}

func (a *App) endedRow(c *ui.Context, pal *palette, h *host, cv proto.Conversation, now time.Time) {
	key := "e-" + h.name + "-" + cv.Agent + "-" + cv.ID
	row := ui.Row(c.Key(key)).Height(44).Padding(0, 6, 0, rowInset).Gap(10).Radius(rControl).AlignItems(ui.Center)
	rowTooltip(c, row, "tip-"+key, h.shortDir(cv.Dir))
	hovered := row.Hovered()
	if hovered {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		a.resumeConversation(h, cv)
	}
	row.ContextMenu(func(m *ui.Menu) { a.endedMenu(m, h, cv) })
	row.Children(func() {
		i := programIcons[cv.Agent]
		if i == nil {
			i = terminalIcon
		}
		ui.Icon(c, i).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cv.Label()).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if h.remote() {
					a.hostTag(c, pal, h)
				}
			})
			ui.Text(c, proto.DirName(cv.Dir)).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…")
		})
		if hovered && !cv.Archived {
			iconButton(c, "archive", archiveIcon, locales.EndedArchive(), pal, func() {
				a.archiveConversation(h, cv, true)
			})
		} else {
			ui.Text(c, ago(cv.Updated, now)).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Shrink(0)
		}
	})
}

func (a *App) endedMenu(m *ui.Menu, h *host, cv proto.Conversation) {
	if m.Item(locales.EndedResume()).Chosen() {
		a.resumeConversation(h, cv)
	}
	if cv.Archived {
		if m.Item(locales.EndedRestore()).Chosen() {
			a.archiveConversation(h, cv, false)
		}
	} else if m.Item(locales.EndedArchive()).Chosen() {
		a.archiveConversation(h, cv, true)
	}
	m.Separator()
	if m.Item(locales.SessionCopyId()).Chosen() {
		a.copyText = cv.ID
	}
	if m.Item(locales.SessionCopyDirectory()).Disabled(cv.Dir == "").Chosen() {
		a.copyText = cv.Dir
	}
	if m.Item(locales.SessionRevealIn(fileManager())).Disabled(cv.Dir == "" || h.remote()).Chosen() {
		openPath(cv.Dir)
	}
	m.Separator()
	if m.Item(locales.EndedDelete()).Chosen() {
		a.forgetConversation(h, cv)
	}
}

func (a *App) endedSectionMenu(m *ui.Menu, key string, list []hostEnded) {
	if key == "ended" {
		if m.Item(locales.EndedArchiveAll()).Chosen() {
			for _, he := range list {
				a.archiveConversation(he.h, he.c, true)
			}
		}
		return
	}
	if m.Item(locales.EndedDeleteAll()).Chosen() {
		a.confirm = &confirmation{
			open:    true,
			title:   locales.EndedDeleteAllTitle(len(list)),
			detail:  locales.EndedDeleteAllDetail(),
			action:  locales.EndedDelete(),
			destroy: true,
			run: func() {
				for _, he := range list {
					a.forgetConversation(he.h, he.c)
				}
			},
		}
	}
}

// archiveConversation moves a conversation to Archived, or back, at once,
// as its kerod will.
func (a *App) archiveConversation(h *host, cv proto.Conversation, archived bool) {
	if i := slices.IndexFunc(h.convs, func(o proto.Conversation) bool { return o.Agent == cv.Agent && o.ID == cv.ID }); i >= 0 {
		h.convs[i].Archived = archived
	}
	a.callOn(h, "conversation.archive", proto.ConversationParams{Agent: cv.Agent, ID: cv.ID, Archived: archived}, nil)
}

// forgetConversation has a conversation's kerod forget it; the agent
// keeps its own history of it.
func (a *App) forgetConversation(h *host, cv proto.Conversation) {
	h.convs = slices.DeleteFunc(h.convs, func(o proto.Conversation) bool { return o.Agent == cv.Agent && o.ID == cv.ID })
	a.callOn(h, "conversation.forget", proto.ConversationParams{Agent: cv.Agent, ID: cv.ID}, nil)
}

// skillHint tells, while there's no conversation to list, that listing
// them needs Kero's skill, which tells kerod each conversation's id: when
// no kind of agent has it.
func (a *App) skillHint(c *ui.Context, pal *palette) {
	if now := c.Now(); now.Sub(a.skillsAt) > 30*time.Second {
		a.skillsAt = now
		a.skillMissing = !slices.ContainsFunc(cli.SkillStates(), func(st cli.SkillState) bool { return st.State != "missing" })
	}
	if !a.skillMissing {
		return
	}
	hint := ui.Row(c.Key("ended-hint")).Margin(18, 0, 3, 0).Padding(6, 6, 6, rowInset).Radius(rRow)
	if hint.Hovered() {
		hint.Background(pal.rowHover)
	}
	if hint.Clicked() {
		a.openSettings("agents")
	}
	hint.Children(func() {
		ui.Text(c, locales.EndedSetUpSkill()).FontSize(fsSmall).TextColor(pal.textFaint).Grow(1).MinWidth(0)
	})
}
