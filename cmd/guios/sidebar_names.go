package main

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Names and detail lines that tell sessions apart in the sidebar and the
// palette, as sessions started in one folder (QA-010).

// sessionLabels tells apart the sessions no one named that show the same
// name, as several shells in one folder: the first keeps the name, the
// next are "demo 2", "demo 3", in the daemon's order (QA-010). A session's
// label is its name when it is not in the map.
func (a *App) sessionLabels() map[string]string {
	// The maps live on across frames, cleared, so that a frame allocates
	// for a label only when two sessions share a name.
	if a.sbSeen == nil {
		a.sbSeen = map[[2]string]int{}
	}
	clear(a.sbSeen)
	out := a.sbLabels
	clear(out)
	for i := range a.st.Sessions {
		s := &a.st.Sessions[i]
		if s.Name != "" {
			continue
		}
		name := s.DisplayName(a.set.SessionName)
		key := [2]string{a.hostOf(s.ID).name, name}
		a.sbSeen[key]++
		if n := a.sbSeen[key]; n > 1 {
			if out == nil {
				out = map[string]string{}
			}
			out[s.ID] = name + " " + strconv.Itoa(n)
		}
	}
	return out
}

// railName is a session's name in the sidebar.
func (a *App) railName(s *proto.Session) string {
	if l, ok := a.sbLabels[s.ID]; ok {
		return l
	}
	return s.DisplayName(a.set.SessionName)
}

// sessionMarked tells whether a session's row has a state mark: one that
// runs an agent or a program that reported a state, or has news. A plain
// shell at rest has none, as in the TUI rail: an idle ring on every shell
// said nothing (QA-010).
func (a *App) sessionMarked(s *proto.Session) bool {
	if s.Status != proto.Idle || s.Unread || s.Progress >= 0 {
		return true
	}
	for i := range s.Panes {
		if s.Panes[i].Agent != "" || s.Panes[i].Status != proto.Idle {
			return true
		}
	}
	return len(a.sessionAgents(s, a.sbNeeds)) > 0
}

// sessionDetail is the second line of a session's detailed row. A status
// message comes first. Else: the program or agent in the focused pane,
// the branch (or the folder outside git), and the panes when there are
// several, so that sessions in one folder read apart (QA-010).
func (a *App) sessionDetail(s *proto.Session, g *proto.GitInfo) (line string, msg bool) {
	if s.Message != "" {
		// A notification's text can run over lines: the row has one.
		return strings.Join(strings.Fields(s.Message), " "), true
	}
	var parts []string
	if f := s.FocusPane(); f != nil {
		switch {
		case f.Agent != "":
			parts = append(parts, harnessName(f.Agent))
		case f.Process != "":
			parts = append(parts, f.Process)
		}
	}
	switch {
	case g != nil:
		parts = append(parts, gitSummary(g))
	default:
		parts = append(parts, a.hostOf(s.ID).shortDir(s.WorkDir()))
	}
	if n := len(s.Panes) + len(s.Others); n > 1 {
		parts = append(parts, locales.SidebarPanes(n))
	}
	return strings.Join(parts, " · "), false
}
