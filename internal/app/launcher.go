package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The launcher is its own overlay rather than a tier inside the command
// palette, because the two lists answer different questions.
//
// A palette row is a verb tuios performs: Split Vertical, Toggle Zoom, Switch
// Session. A launcher row is a thing you start. Ranking them against each other
// means one query straddles both ideas, and the palette had to carry a constant
// (paletteRunPenalty) whose whole job was to stop "new" from offering newgrp
// above New Window. That constant was the tell: two lists that need a handicap
// to share a box are two lists.
//
// The split also buys the rows things the palette could not give them. Every
// program carried the same "[Run]" tag, which said nothing; the count in the
// footer said "N of M commands" about a few thousand executables; and a program
// wants a second verb (type it out, below) that no command row has any use for.
//
// The palette keeps one row that opens this overlay, so nothing is lost for a
// user who only remembers one box.

// LauncherItem is one row: a program, and where the live query matched its
// name.
type LauncherItem struct {
	Entry applist.Entry
	// Match holds the byte offsets in the entry's Name that the query matched,
	// filled in by the filter so the renderer can underline them without
	// running the matcher a second time.
	Match []int
}

// OpenLauncher shows the launcher and returns the command that rescans the
// programs it lists.
//
// The scan runs off the Update goroutine and its rows arrive later, so the
// launcher is open and typeable before it finishes; whatever the last scan
// found is already on screen in the meantime.
func (m *OS) OpenLauncher() tea.Cmd {
	m.ShowLauncher = true
	m.LauncherQuery = ""
	m.LauncherSelected = 0
	m.LauncherScroll = 0
	m.rebuildLauncherItems()
	return tea.Batch(m.ScanPathApps(), m.LauncherIconWork())
}

// LauncherIconWork asks for the icons the rows now on screen need. It is nil
// when there is nothing new to decode, which is the usual answer once a list
// has been looked at, so moving the selection costs nothing.
//
// It is called by whatever changed which rows are visible, rather than by the
// renderer, because the renderer cannot start work and this must not run on a
// timer.
func (m *OS) LauncherIconWork() tea.Cmd {
	if !m.ShowLauncher {
		return nil
	}
	return m.LauncherIconCmd(m.LauncherVisibleIcons())
}

// CloseLauncher hides the launcher and drops its rows.
//
// A few thousand entries is the largest thing this overlay holds and nothing
// reads them while it is shut. They are rebuilt from the scan cache on the next
// open, so dropping them costs only that rebuild.
func (m *OS) CloseLauncher() {
	m.ShowLauncher = false
	m.LauncherQuery = ""
	m.LauncherSelected = 0
	m.LauncherScroll = 0
	m.LauncherItems = nil
	// A drawn icon outlives the panel that placed it, so it is taken down here
	// rather than left for the next frame to paint over.
	m.clearLauncherIcons()
}

// rebuildLauncherItems builds one row per known program, in the order an empty
// query shows them. Called when the launcher opens and when a scan lands, never
// per frame.
//
// The history ordering is applied here rather than in the filter because the
// filter runs on every frame the launcher is drawn and this list is several
// thousand rows long. Launch history only moves when something is launched,
// which closes the launcher, so once per open is exactly often enough.
func (m *OS) rebuildLauncherItems() {
	entries := m.knownPathApps()
	items := make([]LauncherItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, LauncherItem{Entry: e})
	}
	m.LauncherItems = orderByHistory(items, m.launchHistory)
}

// launcherItems returns the rows, building them if this OS reached the list
// without opening the overlay.
func (m *OS) launcherItems() []LauncherItem {
	if m.LauncherItems == nil {
		m.rebuildLauncherItems()
	}
	return m.LauncherItems
}

// FilterLauncherItems returns the entries matching query, best match first.
//
// Ranking is the shared scored matcher plus each program's launch history, so
// the second time someone runs a thing it is already at the top. The history
// boost is capped at applist.MaxBoost, which is below what one matched
// character is worth, so it lifts a near-tie and cannot drag a program the
// query barely matches above one it matches well.
//
// The label is matched on its own and the entry's other names are a weaker
// fallback, appended behind every label hit rather than scored beside them.
// That is what lets "browser" find Firefox through its keywords and "vscode"
// find Code without either of them outranking a program actually called that.
func FilterLauncherItems(items []LauncherItem, query string, hist *applist.Frecency) []LauncherItem {
	if query == "" {
		// With nothing typed the whole list is on offer and it is already in the
		// order history put it in (see rebuildLauncherItems). Returning it as it
		// stands is what keeps drawing an unfiltered launcher free of a pass
		// over several thousand rows per frame.
		return items
	}
	var m fuzzy.Matcher
	hits := m.FilterIndex(query, len(items), func(i int) string {
		return items[i].Entry.Label()
	})

	if hist != nil {
		boosted := false
		for i := range hits {
			if b := hist.Boost(items[hits[i].Index].Entry.Name); b != 0 {
				hits[i].Score += b
				boosted = true
			}
		}
		if boosted {
			fuzzy.Sort(hits)
		}
	}

	named := make([]bool, len(items))
	out := make([]LauncherItem, 0, len(hits))
	for _, h := range hits {
		named[h.Index] = true
		item := items[h.Index]
		item.Match = h.Positions
		out = append(out, item)
	}

	for i, item := range items {
		if len(out)-len(hits) >= maxAliasHits {
			break
		}
		if named[i] || !matchesAlias(item.Entry, query) {
			continue
		}
		// The positions index the label, and this row did not match its label,
		// so there is nothing to highlight.
		item.Match = nil
		out = append(out, item)
	}
	return out
}

// maxAliasHits caps the tail of rows admitted on a keyword rather than a name.
//
// A short query is a subsequence of a great many keywords ("Internet", "WWW",
// "Utility"), and those rows arrive unranked behind every scored hit. Without a
// cap, typing two characters buries the answer under a hundred entries that
// merely contain those letters somewhere in their metadata, and the count in
// the footer stops meaning anything.
const maxAliasHits = 12

// matchesAlias reports whether query matches any of an entry's other names.
func matchesAlias(e applist.Entry, query string) bool {
	for _, a := range e.Aliases() {
		if fuzzy.Match(query, a) {
			return true
		}
	}
	return false
}

// orderByHistory puts the programs with launch history in front, strongest
// first, and leaves everything else in the order the sources listed them.
//
// This is what an empty launcher should open on: the things this person
// actually runs, rather than the alphabetical head of /usr/bin.
func orderByHistory(items []LauncherItem, hist *applist.Frecency) []LauncherItem {
	if hist == nil {
		return items
	}
	var known, rest []LauncherItem
	for _, it := range items {
		if hist.Boost(it.Entry.Name) > 0 {
			known = append(known, it)
			continue
		}
		rest = append(rest, it)
	}
	if len(known) == 0 {
		return items
	}
	// A stable sort keeps source order between programs with equal history, so
	// the list does not shuffle under the cursor.
	stableSortByBoost(known, hist)
	return append(known, rest...)
}

// filteredLauncherItems returns the rows matching the current query.
func (m *OS) filteredLauncherItems() []LauncherItem {
	return FilterLauncherItems(m.launcherItems(), m.LauncherQuery, m.launchHistory)
}

// LauncherMove moves the selection by delta and keeps the scroll window in
// view. Shared by the keyboard arrows and the mouse wheel.
func (m *OS) LauncherMove(delta int) {
	n := len(m.filteredLauncherItems())
	if n == 0 {
		m.LauncherSelected = 0
		return
	}
	m.LauncherSelected = clampInt(m.LauncherSelected+delta, 0, n-1)
	_, visible, _ := m.launcherLayout()
	if m.LauncherSelected < m.LauncherScroll {
		m.LauncherScroll = m.LauncherSelected
	}
	if m.LauncherSelected >= m.LauncherScroll+visible {
		m.LauncherScroll = m.LauncherSelected - visible + 1
	}
}

// LauncherRefilter resets the selection after the query changes.
func (m *OS) LauncherRefilter() {
	m.LauncherSelected = 0
	m.LauncherScroll = 0
}

// launcherSelection returns the entry under the cursor.
func (m *OS) launcherSelection(idx int) (applist.Entry, bool) {
	filtered := m.filteredLauncherItems()
	if idx < 0 || idx >= len(filtered) {
		return applist.Entry{}, false
	}
	return filtered[idx].Entry, true
}

// launcherTarget resolves the row a verb was pressed on and closes the
// launcher, or explains why there is nothing to act on and leaves it up.
//
// Closing on no selection is what made both verbs look broken. The list is
// filled by a scan that runs off the Update goroutine, so on the first open of
// a session there is nothing selected yet however precisely the query was
// typed; pressing the key then threw the query away, dismissed the panel and
// said nothing, which reads as the key not working. The wait it needs is the
// scan, not the pane's shell.
//
// Staying open is the whole repair: the query survives, the rows arrive behind
// it, and the same keypress works a moment later. The notification is there
// because a key that does nothing visible is indistinguishable from a key that
// is not bound.
func (m *OS) launcherTarget(idx int) (applist.Entry, bool) {
	e, ok := m.launcherSelection(idx)
	if ok {
		m.CloseLauncher()
		return e, true
	}
	m.ShowNotification(m.launcherEmptyReason(), "info", config.NotificationDuration)
	return applist.Entry{}, false
}

// launcherEmptyReason says why there is nothing to act on, which is two
// different things: the scan has not landed, or it has and nothing matches.
//
// The wording deliberately does not repeat the panel's own empty line. A
// notification that reads the same as text already on screen cannot be asserted
// on, and a test that waits for it passes against a build that never raised it.
// Saying "no program matches" before the first scan is simply wrong, and it is
// the answer that would send someone looking for a program they do have.
func (m *OS) launcherEmptyReason() string {
	if len(m.LauncherItems) == 0 {
		return "Still finding the programs on this machine"
	}
	return "Nothing to launch: no program matches " + m.LauncherQuery
}

// LauncherRun starts the selected program in a new pane and closes the
// launcher. This is Enter, and the mouse click.
func (m *OS) LauncherRun(idx int) tea.Cmd {
	e, ok := m.launcherTarget(idx)
	if !ok {
		return nil
	}
	return m.RunProgram(e)
}

// LauncherType opens a new pane with the selected program's command line typed
// at the shell's prompt, entered by the user rather than by tuios. This is Tab.
//
// Run and type are the same choice made per invocation, not a setting: the same
// person wants htop to just start and ffmpeg to be waiting on the prompt so
// they can add arguments to it. Neither is the correct default for the other,
// so both live on the row and the user picks with the key they press.
//
// Tab is the type-it-out key because Tab is already the completion key. Asking
// for a command line to finish is what completion means, and unlike a modifier
// on Enter it is expressible under every terminal keyboard encoding.
func (m *OS) LauncherType(idx int) tea.Cmd {
	e, ok := m.launcherTarget(idx)
	if !ok {
		return nil
	}
	return m.TypeProgram(e)
}
