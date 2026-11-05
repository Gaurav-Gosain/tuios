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
// name, as several shells in one folder: the first has no mark, the next
// get "2", "3", in the daemon's order (QA-010). The mark is drawn after the
// name, quiet, and is no part of it: the name stays the one the TUI shows
// and `tuios attach` takes (council round 5, S2). The detail line says
// what else tells them apart.
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
			out[s.ID] = strconv.Itoa(n)
		}
	}
	return out
}

// railName is a session's name in the sidebar: the one the TUI shows.
func (a *App) railName(s *proto.Session) string {
	return s.DisplayName(a.set.SessionName)
}

// railTitle is a session row's title: its name, or for a row nested
// under its repository's session, its branch, as the TUI rail nests a
// worktree ("└ feature/login"). A fan's attempts differ in their branch's
// end ("fan/try-two-ways-2"), which a long session name loses to a cut.
func (a *App) railTitle(s *proto.Session, nested bool) string {
	if k := s.Checkout; nested && k != nil && k.Branch != "" {
		return k.Branch
	}
	return a.railName(s)
}

// railLetter is a fan attempt's letter for its session, "" for a session
// that is no attempt of a fan.
func (a *App) railLetter(s *proto.Session) string {
	if s.Name == "" || a.hostOf(s.ID).remote() {
		return ""
	}
	return a.ck.letters[s.Name]
}

// railMark is the quiet mark after the name of a session that shows the
// same name as another, "" for none.
func (a *App) railMark(s *proto.Session) string {
	return a.sbLabels[s.ID]
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
