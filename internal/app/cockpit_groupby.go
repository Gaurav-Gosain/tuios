package app

import (
	"path/filepath"
	"sort"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Grouping the agents view's list. By default the list is grouped by what
// each agent needs from the person (Needs you, Ready for review, Working,
// Idle, Settled). The person can group it by session, by machine or by
// project (the repository) instead. Needs you stays on top in every
// grouping, as it is, so nothing waiting is pushed down by the bands. In a
// band the rows keep the order of their state groups, and each row keeps its
// state group for its mark, its lines and its keys.
//
// s opens a small picker: 1 to 4 choose the grouping, s again steps the sort
// order as s did before, and any other key closes it. The grouping is kept in
// the client's state file.

// The groupings the picker offers, in its order. "" is by state.
const (
	ckGroupState   = ""
	ckGroupSession = "session"
	ckGroupMachine = "machine"
	ckGroupProject = "project"
)

var ckGroupings = [...]string{ckGroupState, ckGroupSession, ckGroupMachine, ckGroupProject}

// ckGroupingNames is what the picker and the header call each grouping.
var ckGroupingNames = [...]string{"state", "session", "machine", "project"}

// ckBand is one session's, machine's or project's rows.
type ckBand struct {
	key, title string
	// local sorts this machine first among the machines.
	local bool
	count int
	rows  []ckBandRow
}

// ckBandRow is a row of a band and the state group it came from.
type ckBandRow struct {
	group int
	row   ckRow
}

// ckGroupBy is the grouping in force, "" for by state. The board has its
// own grouping, by stage, and keeps it.
func (m *OS) ckGroupBy() string {
	ck := &m.Inbox.ck
	if ck.board {
		return ckGroupState
	}
	return ck.groupBy
}

// ckBandOf is the band a row goes in under grouping by: its key and title.
func (m *OS) ckBandOf(by string, r ckRow, list []sidebarAgentEntry) (key, title string, local bool) {
	host, sess, label := "", "", ""
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = list[r.agent]
		host, sess, label = e.Host, e.SessionID, e.SessionLabel
	} else if r.hasItem {
		host, sess = r.item.Host, r.item.Session
	}
	switch by {
	case ckGroupSession:
		return host + "\x00" + sess, printableTitle(firstNonEmptyStr(firstNonEmptyStr(label, sess), "No session")), false
	case ckGroupMachine:
		return host, m.ckMachine(host), host == ""
	case ckGroupProject:
		repo := ""
		if r.agent >= 0 {
			repo = m.ckWorktreeOf(e).repo
		}
		if repo == "" {
			return "", "No repository", false
		}
		return repo, printableTitle(filepath.Base(repo)), false
	}
	return "", "", false
}

// ckBandAdd puts the rows of state group g in their bands, and keeps the
// bands sorted: this machine first, then by title.
func (m *OS) ckBandAdd(bands []ckBand, by string, g int, items []ckRow, itemsOnly bool, list []sidebarAgentEntry) []ckBand {
	for _, r := range items {
		if itemsOnly && !r.hasItem && r.kind != ckRowNote {
			continue
		}
		key, title, local := m.ckBandOf(by, r, list)
		at := -1
		for i := range bands {
			if bands[i].key == key {
				at = i
				break
			}
		}
		if at < 0 {
			bands = append(bands, ckBand{key: key, title: title, local: local})
			at = len(bands) - 1
		}
		if r.selectable() {
			bands[at].count++
		}
		bands[at].rows = append(bands[at].rows, ckBandRow{group: g, row: r})
	}
	sort.SliceStable(bands, func(i, j int) bool {
		if bands[i].local != bands[j].local {
			return bands[i].local
		}
		return bands[i].title < bands[j].title
	})
	return bands
}

// CockpitGroupMenuOpen reports whether the group and sort picker is open.
func (m *OS) CockpitGroupMenuOpen() bool { return m.Inbox.ck.groupMenu }

// cockpitOpenGroupMenu opens the group and sort picker.
func (m *OS) cockpitOpenGroupMenu() {
	m.Inbox.ck.groupMenu = true
	m.cockpitTouch()
}

// CockpitGroupMenuKey takes the key after s: a digit chooses the grouping,
// s steps the sort, and any other key closes the picker.
func (m *OS) CockpitGroupMenuKey(key string) tea.Cmd {
	ck := &m.Inbox.ck
	ck.groupMenu = false
	switch {
	case len(key) == 1 && key[0] >= '1' && int(key[0]-'1') < len(ckGroupings):
		ck.groupBy = ckGroupings[key[0]-'1']
		m.cockpitTouchRows()
		m.cockpitRows()
		m.cockpitClamp()
		m.cockpitSync()
		m.saveSidebarState()
	case key == "s":
		m.InboxAgentsCycleSort()
		m.cockpitTouchRows()
	}
	m.cockpitTouch()
	return nil
}

// ckGroupingName is the grouping in force as the header names it.
func (m *OS) ckGroupingName() string {
	by := m.ckGroupBy()
	for i, g := range ckGroupings {
		if g == by {
			return ckGroupingNames[i]
		}
	}
	return ckGroupingNames[0]
}

// cockpitGroupMenuHints are the picker's keys for the key bar.
func (m *OS) cockpitGroupMenuHints() []overlay.Hint {
	hints := []overlay.Hint{{Key: "Group by", Label: m.ckGroupingName()}}
	for i, name := range ckGroupingNames {
		hints = append(hints, overlay.Hint{Key: string(rune('1' + i)), Label: name})
	}
	return append(hints, overlay.Hint{Key: "s", Label: "sort order"}, overlay.Hint{Key: "esc", Label: "close", Priority: overlay.HintEssential})
}
