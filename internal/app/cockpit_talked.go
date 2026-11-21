package app

import (
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type ckTalkPeer struct {
	window string
	peer   ckPeer
	n      int
	last   int64
}

type ckTalkCache struct {
	key   string
	gen   uint64
	peers uint64
	list  []ckTalkPeer
}

func (m *OS) ckTalkedWith(e sidebarAgentEntry) []ckTalkPeer {
	if e.Host != "" || e.Fold > 0 || e.Child != nil {
		return nil
	}
	ck := &m.Inbox.ck
	key, sig := agentKey(e), m.ckPeersSig()
	if c := &ck.talk; c.key == key && c.gen == m.AgentMail.Gen && c.peers == sig {
		return c.list
	}
	by := map[string]*ckTalkPeer{}
	var order []string
	for _, msg := range m.AgentMail.Messages {
		if msg.Session != e.SessionID || msg.Kind == "notice" {
			continue
		}
		other, label := "", ""
		switch {
		case msg.From == e.WindowID:
			other, label = msg.To, msg.ToLabel
		case msg.To == e.WindowID:
			other, label = msg.From, msg.FromLabel
		}
		if other == "" || other == e.WindowID || other == session.AgentInboxHuman {
			continue
		}
		tp := by[other]
		if tp == nil {
			p := m.ckResolvePeer(e, "", other)
			if p.unknown {
				if name := printableTitle(label); name != "" {
					p.name = name
				}
			}
			tp = &ckTalkPeer{window: other, peer: p}
			by[other] = tp
			order = append(order, other)
		}
		tp.n++
		tp.last = max(tp.last, msg.SentAt)
	}
	list := make([]ckTalkPeer, 0, len(order))
	for _, w := range order {
		list = append(list, *by[w])
	}
	slices.SortStableFunc(list, func(a, b ckTalkPeer) int {
		switch {
		case a.last > b.last:
			return -1
		case a.last < b.last:
			return 1
		}
		return 0
	})
	ck.talk = ckTalkCache{key: key, gen: m.AgentMail.Gen, peers: sig, list: list}
	return list
}

func (l ckLook) ckTalkChip(tp ckTalkPeer) []ckSeg {
	pal := l.pal
	mark, ink := l.g(ckGlyphIdle), pal.FgMute
	if tp.peer.state != "" {
		mark, ink = l.ckMark(tp.peer.state, false)
	}
	nameFg := pal.Fg
	name := tp.peer.name
	if tp.peer.closed || tp.peer.unknown {
		nameFg = pal.FgDim
	}
	segs := []ckSeg{{t: " " + mark + " ", fg: ink, bg: l.cursor}, {t: ckTrunc(name, 20), fg: nameFg, bg: l.cursor, attrs: ckBold}}
	if tp.peer.closed {
		segs = append(segs, ckSeg{t: " closed", fg: pal.FgMute, bg: l.cursor})
	}
	return append(segs, ckSeg{t: " " + strconv.Itoa(tp.n) + " ", fg: pal.FgMute, bg: l.cursor})
}

func (m *OS) drawTalkedWith(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, top int) bool {
	ck := &m.Inbox.ck
	ck.talkShown = 0
	list := m.ckTalkedWith(e)
	if len(list) == 0 {
		return false
	}
	pal := l.pal
	label := []ckSeg{{t: l.g(ckGlyphSent) + " ", fg: pal.FgMute}, {t: "Talked with", fg: pal.FgDim}}
	const gap = 2
	used := ckWidth(label...)
	chips := make([][]ckSeg, len(list))
	for i, tp := range list {
		chips[i] = l.ckTalkChip(tp)
	}
	more := func(n int) []ckSeg {
		return []ckSeg{{t: " +" + strconv.Itoa(n) + " ", fg: pal.FgDim, bg: l.cursor, attrs: ckBold}}
	}
	shown := 0
	for shown < len(list) {
		need := used + gap + ckWidth(chips[shown]...)
		if shown+1 < len(list) {
			need += gap + ckWidth(more(len(list)-shown-1)...)
		}
		if need > w {
			break
		}
		used += gap + ckWidth(chips[shown]...)
		shown++
	}
	if shown == 0 {
		return false
	}
	ck.talkShown = shown
	cx := c.text(x, y, x+w, label...)
	for i := range shown {
		cx += gap
		x0 := cx
		cx = c.text(cx, y, x+w, chips[i]...)
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: cx, y1: top + y + 1, kind: ckHitAction, action: "talk:" + list[i].window})
	}
	if shown < len(list) {
		cx += gap
		x0 := cx
		cx = c.text(cx, y, x+w, more(len(list)-shown)...)
		ck.hits = append(ck.hits, ckHit{x0: x0, y0: top + y, x1: cx, y1: top + y + 1, kind: ckHitAction, action: "talked"})
		ck.anchors["talked"] = ckAnchor{x: x0, y: y, w: cx - x0}
	}
	return true
}

func (m *OS) cockpitTalkClick(window string) tea.Cmd {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	for _, tp := range m.ckTalkedWith(e) {
		if tp.window == window {
			return m.ckGoPeer(e, m.cockpitThread(), tp.peer)
		}
	}
	return nil
}
