package app

import "github.com/Gaurav-Gosain/tuios/internal/sessiontree"

// CycleSessionInSpace switches to the next (delta 1) or previous (delta -1)
// session in the current session's space, in the rail's order and wrapping. A
// session in no space cycles through the other sessions in no space.
//
// With no space on this machine at all the key says so and stays put: every
// session is then in no space, and walking them would only repeat
// CycleSession under another key.
func (m *OS) CycleSessionInSpace(delta int) {
	sessions := localSessionNodes(m.BuildSessionTree().Sessions)
	space, ok := currentSpace(sessions)
	if !ok {
		m.ShowNotification("No spaces", "info", m.Settings.NotificationDuration)
		return
	}
	target := neighbourSession(sessionsInSpace(sessions, space), delta)
	if target == "" {
		where := "outside a space"
		if space != "" {
			where = "in " + space
		}
		m.ShowNotification("No other sessions "+where, "info", m.Settings.NotificationDuration)
		return
	}
	m.openSession("", target)
}

// currentSpace is the space of the current session among one machine's
// sessions. It reports false when none of them is in a space.
func currentSpace(sessions []sessiontree.Node) (string, bool) {
	current, filed := "", false
	for _, n := range sessions {
		space := sessiontree.SpaceOf(n)
		filed = filed || space != ""
		if n.IsCurrent {
			current = space
		}
	}
	return current, filed
}

// sessionsInSpace is the sessions filed under space, "" for those in none, in
// the order the rail draws them: grouped by space, and by repository inside
// each space.
func sessionsInSpace(sessions []sessiontree.Node, space string) []sessiontree.Node {
	var out []sessiontree.Node
	for _, n := range sessiontree.GroupBySpace(sessions, sessiontree.GroupByRepo) {
		if n.Kind == sessiontree.KindSession && sessiontree.SpaceOf(n) == space {
			out = append(out, n)
		}
	}
	return out
}
