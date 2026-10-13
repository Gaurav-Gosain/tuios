package app

import (
	"strconv"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The rail's space layouts, picked by appearance.sidebar.spaces.
//
//   - grouped: a heading per space inside the sessions section. See
//     sidebar_spaces.go.
//   - split: a spaces section directly above the sessions section, one row per
//     space plus "none" for the sessions in no space, and the sessions section
//     lists only the current space's sessions, flat. The spaces section picks
//     the space and the sessions section lists its members, the way herdr
//     splits workspaces from tabs.
//   - off: the sessions section is one flat list and the rail shows no space.
//
// With no session in any space all three draw the rail exactly as it was
// before spaces: grouping by space is then a no-op, the spaces section has no
// rows and so is dropped header and all, and "the current space" is the one
// every session is in.

// sidebarSpacesShare is the share of the rail the spaces section may claim. It
// is a ceiling like every share: a rail with three spaces spends three lines on
// them. A quarter keeps a long list of spaces from crowding the sessions it
// chooses between, and the rest scrolls.
const sidebarSpacesShare = 25

// sidebarSpaceNoneLabel names the row standing for the sessions in no space.
const sidebarSpaceNoneLabel = "none"

// sidebarSpaceAttachedTag is the figure on a space's row while a client is
// attached to any of its sessions.
const sidebarSpaceAttachedTag = "attached"

// spacesSeen reports whether any session this client knows of is in a space,
// which is when the settings page offers the space layout. Reads only what the
// client already holds.
func (m *OS) spacesSeen() bool {
	if m.SessionSpace != "" {
		return true
	}
	if m.DaemonClient == nil {
		return false
	}
	for _, info := range m.DaemonClient.CachedSessions() {
		if info.Space != "" {
			return true
		}
	}
	return false
}

// sidebarSpacesMode is the layout in force, read as grouped when unset.
func (m *OS) sidebarSpacesMode() string {
	if m.Settings.SidebarSpaces == "" {
		return config.SidebarSpacesGrouped
	}
	return m.Settings.SidebarSpaces
}

// sidebarCurrentSpace is the space the attached session is in, "" for none,
// read by the rule the rail groups by. It falls back to the session state's
// own label before the attached row is in the list.
func (m *OS) sidebarCurrentSpace(local []sessiontree.Node) string {
	for _, n := range local {
		if n.Kind == sessiontree.KindSession && n.IsCurrent {
			return sessiontree.SpaceOf(n)
		}
	}
	return m.SessionSpace
}

// sidebarLayoutRows is one machine's sessions as the sessions section lists
// them under the layout in force, before folded groups are taken out.
func (m *OS) sidebarLayoutRows(local []sessiontree.Node) []sessiontree.Node {
	switch m.sidebarSpacesMode() {
	case config.SidebarSpacesSplit:
		current := m.sidebarCurrentSpace(local)
		members := make([]sessiontree.Node, 0, len(local))
		for _, n := range local {
			if sessiontree.SpaceOf(n) == current {
				members = append(members, unspaced(n))
			}
		}
		return sessiontree.GroupByRepo(members)
	case config.SidebarSpacesOff:
		flat := make([]sessiontree.Node, len(local))
		for i, n := range local {
			flat[i] = unspaced(n)
		}
		return sessiontree.GroupByRepo(flat)
	default:
		return sessiontree.GroupBySpace(local, sessiontree.GroupByRepo)
	}
}

// unspaced is a session row with its space cleared, so it sits at the
// section's own indent rather than stepped in under a heading that is not
// drawn.
func unspaced(n sessiontree.Node) sessiontree.Node {
	n.Space = ""
	return n
}

// sidebarSpaceSwitchRows is the spaces section's rows: nil unless the layout is
// split and some session on this machine is in a space.
func (m *OS) sidebarSpaceSwitchRows(sessions []sessiontree.Node) []sessiontree.Node {
	if m.sidebarSpacesMode() != config.SidebarSpacesSplit || !sidebarLayoutHas(sidebarSectionSessions, &m.Settings) {
		return nil
	}
	return sessiontree.SpaceRows(localSessionNodes(sessions))
}

// sidebarSpaceSwitchRow renders one row of the spaces section.
//
//	▎● work      attached 2
//	 ○ ops                1
//	 · none               1
//	^^ ^         ^        ^ sessions in it
//	|| |         a client is attached to one of them
//	|| the members' rolled-up agent state
//	|gutter: the focus mark on the current space, else severity
//
// The roll-up is the one a folded space's heading carries, drawn whatever the
// row: the members of every space but the current one are off screen, and the
// current one's gutter is spent on the focus mark.
func (m *OS) sidebarSpaceSwitchRow(node sessiontree.Node, current bool, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	state := node.AgentState

	glyph := sidebarQuietDot(rowBg, pal, &m.Settings)
	if agentStateIndicator(state) != "" {
		glyph = sidebarGlyph(state, node.DoneSeen, node.Subagents, rowBg, pal, &m.Settings)
	}

	title, fg := printableTitle(node.Title), pal.Fg
	if node.ID == "" {
		// Not a name anybody chose, so it is not drawn as one.
		title, fg = sidebarSpaceNoneLabel, pal.FgMute
	}

	var figures [2]string
	if node.Attached {
		figures[0] = sidebarSpaceAttachedTag
	}
	figures[1] = strconv.Itoa(node.WindowCount)
	s := &m.railFit
	s.tokens = s.tokens[:0]
	for _, f := range figures {
		s.tokens = append(s.tokens, railToken{Cost: sidebarFigureCost(f), Right: true})
	}
	titleW := lipgloss.Width(title)
	keep, avail := railRowFitInto(s.keep, titleW, railNameKeep(titleW), s.tokens, sidebarNameAvailIn(cw, 0, 0))
	s.keep = keep
	right := sidebarJoinFigures(figures[:], keep, sidebarStyle(rowBg, pal.FgMute))

	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(state)).Render(overlay.Truncate(title, max(avail, 1)))
	gutter := sidebarGutter(current, state, rowBg, pal, &m.Settings)
	return sidebarComposeGroupRow(0, gutter, glyph, name, right, cw, rowBg)
}

// sidebarSwitchSpace switches this client to a space's row, "" for the none
// row, through switchToSpaceCmd: the daemon picks the session, and the switch
// to it is a session row's.
func (m *OS) sidebarSwitchSpace(space string) {
	m.queueSidebarCmd(m.switchToSpaceCmd(space))
}
