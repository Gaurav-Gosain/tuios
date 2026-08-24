package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/adrg/xdg"
)

// The sidebar keeps the view preferences worth surviving a restart: the user's
// drag-defined session order, the rail width, and the per-window accents and
// unread bits. They are preferences of this user's sidebar, not session data,
// so they live in a small state file of their own rather than in config.toml
// (which the settings page rewrites) or in the daemon (which serves many
// clients).

// sidebarStateDir returns the directory the sidebar state file lives in. A
// variable so tests can point it at a scratch directory.
var sidebarStateDir = func() string {
	return filepath.Join(xdg.StateHome, "tuios")
}

const sidebarStateFileName = "sidebar.json"

// sidebarStateFile is the on-disk shape. The tree's per-session "collapsed"
// map is gone with the tree; a file still carrying it parses fine, because
// encoding/json drops keys the struct no longer names.
type sidebarStateFile struct {
	Order []string `json:"order,omitempty"`
	// Width is the user's drag-defined full-rail width, overriding the config
	// default at runtime. Zero means never dragged, so the config width stands.
	Width int `json:"width,omitempty"`
	// Accents is the per-window ANSI slot index, by window ID, and AccentColors
	// is the per-window picked colour as #rrggbb. Window IDs outlive a detach,
	// so an accent set today is still on the row tomorrow.
	//
	// Two fields rather than one union-typed field because this file is shared
	// with whatever tuios binary the user runs next. A slot written as an int
	// still loads into a build that predates the colour picker, and that build
	// ignores the colours instead of failing to parse the whole file and
	// dropping the order, the collapse state and the width with it. A window
	// appears in exactly one of the two maps: SetWindowAccent replaces, so the
	// split happens on the way out and cannot leave both set.
	Accents      map[string]int    `json:"accents,omitempty"`
	AccentColors map[string]string `json:"accent_colors,omitempty"`
	// AgentSeen holds the window IDs of finished panes already looked at, so a
	// pane reviewed before a detach does not come back demanding attention.
	AgentSeen map[string]bool `json:"agent_seen,omitempty"`
	// AgentsFilter and AgentsSort are the agents section's two header controls.
	// Absent means the default ("all" and "priority"), so a file written before
	// they existed needs no migration.
	AgentsFilter string `json:"agents_filter,omitempty"`
	AgentsSort   string `json:"agents_sort,omitempty"`
	// Collapsed is the rail folded to its glyph strip. Absent means expanded,
	// which is what every file written before the toggle existed says.
	Collapsed bool `json:"collapsed,omitempty"`
	// Socket is the daemon socket the window IDs in this file were written
	// against. Window IDs are only unique within one daemon, and this file is
	// keyed by the XDG state directory, so two daemons on different sockets
	// sharing one state directory each hold IDs the other cannot account for.
	// Pruning is what makes that dangerous: without this, each would read the
	// other's live panes as dead and delete their colours. An empty or
	// mismatched value means this file is not ours to prune.
	Socket string `json:"socket,omitempty"`
}

// loadSidebarState reads the persisted sidebar preferences. Any failure leaves
// the defaults in place; a missing file is the ordinary first-run case, not an
// error worth surfacing.
func (m *OS) loadSidebarState() {
	data, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		return
	}
	var st sidebarStateFile
	if json.Unmarshal(data, &st) != nil {
		return
	}
	if len(st.Order) > 0 {
		m.SidebarOrder = st.Order
	}
	if a := accentsFromFile(st); len(a) > 0 {
		m.SidebarAccents = a
	}
	if len(st.AgentSeen) > 0 {
		m.SidebarAgentSeen = st.AgentSeen
	}
	m.SidebarAgentFilter, m.SidebarAgentSort = st.AgentsFilter, st.AgentsSort
	m.SidebarCollapsed = st.Collapsed
	m.sidebarStateSocket = st.Socket
	// A stored drag width wins over the config default; GetSidebarWidth still
	// folds it against the breakpoints and pane floor, so an out-of-range value
	// cannot starve the panes.
	if st.Width >= config.SidebarGlyphWidth {
		m.SidebarWidthPref = st.Width
	}
}

// saveSidebarState writes the sidebar preferences. Best effort: the state is a
// convenience, and a failed write must never interrupt the interaction that
// triggered it.
func (m *OS) saveSidebarState() {
	dir := sidebarStateDir()
	if os.MkdirAll(dir, 0o750) != nil {
		return
	}
	slots, colors := accentsToFile(m.SidebarAccents)
	data, err := json.Marshal(sidebarStateFile{
		Order:        m.SidebarOrder,
		Width:        m.sidebarWidthPreference(),
		Accents:      slots,
		AccentColors: colors,
		AgentSeen:    m.SidebarAgentSeen,
		AgentsFilter: m.SidebarAgentFilter,
		AgentsSort:   m.SidebarAgentSort,
		Collapsed:    m.SidebarCollapsed,
		Socket:       m.sidebarStateSocket,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, sidebarStateFileName), data, 0o600)
}

// pruneWindowKeyedState drops the accents and unread bits of windows that are
// gone. Both maps are keyed by window ID and both are persisted, so an entry a
// closed pane leaves behind outlives the pane in the state file and is reloaded
// every start; the rail's signature folds every unread bit on every frame, so
// the map's size is also a per-frame cost.
// ownsSidebarState reports whether the state file's window IDs were written by
// the daemon this client is talking to, which is the only case where an ID
// missing from the listing means the pane is gone rather than that it belongs
// to somebody else. A file with no socket recorded is claimed on the spot,
// since a file this client is about to write is one it may as well own.
func (m *OS) ownsSidebarState() bool {
	socket, err := session.GetSocketPath()
	if err != nil || socket == "" {
		return false
	}
	if m.sidebarStateSocket == "" {
		m.sidebarStateSocket = socket
		m.saveSidebarState()
		return true
	}
	return m.sidebarStateSocket == socket
}

func (m *OS) pruneWindowKeyedState() {
	if len(m.SidebarAccents) == 0 && len(m.SidebarAgentSeen) == 0 {
		return
	}
	known, ok := m.knownWindowIDs()
	if !ok {
		return
	}
	changed := false
	for id := range m.SidebarAccents {
		if !known[id] {
			delete(m.SidebarAccents, id)
			changed = true
		}
	}
	for id := range m.SidebarAgentSeen {
		if !known[id] {
			delete(m.SidebarAgentSeen, id)
			changed = true
		}
	}
	if changed {
		m.saveSidebarState()
	}
}

// knownWindowIDs is every window this client can legitimately name: its own
// live ones plus the panes of every session in the daemon's cached listing,
// because a foreign session's done pane is ranked and coloured out of the same
// two maps.
//
// The second return is whether the listing could account for all of them. It
// says no when there is no daemon to ask, when a session was listed with a
// window count but no summaries (an older daemon), and when the attached
// session is missing from the listing, which is what a cache from before the
// switch looks like. Pruning against a listing that cannot say what exists
// would take live panes' colours with it, so the caller stands down instead.
func (m *OS) knownWindowIDs() (map[string]bool, bool) {
	if m.DaemonClient == nil || !m.ownsSidebarState() {
		return nil, false
	}
	known := make(map[string]bool, len(m.Windows))
	for _, w := range m.Windows {
		if w != nil {
			known[w.ID] = true
		}
	}
	attached := false
	for _, s := range m.DaemonClient.CachedSessions() {
		if len(s.Windows) != s.WindowCount {
			return nil, false
		}
		if s.Name == m.SessionName {
			attached = true
		}
		for _, w := range s.Windows {
			known[w.ID] = true
		}
	}
	if !attached {
		return nil, false
	}
	return known, true
}

// accentsFromFile reads both accent maps into one. Slots are read first so a
// file written before the colour picker existed loads exactly as it did: an
// index stays an index, resolves against the live theme the way it always has,
// and index 0 still means bright black. A colour entry wins over a slot for the
// same window, which is what a file half-written by an older binary would look
// like.
func accentsFromFile(st sidebarStateFile) map[string]Accent {
	out := make(map[string]Accent, len(st.Accents)+len(st.AccentColors))
	for id, idx := range st.Accents {
		if idx < 0 || idx >= accentSwatchCount {
			continue // out of range: no slot to mean, so no accent
		}
		out[id] = SlotAccent(idx)
	}
	for id, hex := range st.AccentColors {
		if c, ok := parseHexColor(hex); ok {
			out[id] = RGBAccent(c)
		}
	}
	return out
}

// accentsToFile splits the accents back into the two on-disk maps.
func accentsToFile(accents map[string]Accent) (map[string]int, map[string]string) {
	if len(accents) == 0 {
		return nil, nil
	}
	var slots map[string]int
	var colors map[string]string
	for id, a := range accents {
		if a.IsSlot() {
			if slots == nil {
				slots = make(map[string]int, len(accents))
			}
			slots[id] = a.Slot
			continue
		}
		if colors == nil {
			colors = make(map[string]string, len(accents))
		}
		colors[id] = a.Hex()
	}
	return slots, colors
}

// orderByKey rearranges items so those whose key appears in order come first,
// in order's sequence; the rest follow in their given (natural) order. This is
// how the user's drag-defined session order overlays the daemon's
// creation-order list: reordered sessions take their chosen slots, and a
// session the user never dragged (including a brand-new one) appends where the
// daemon put it instead of jumping around.
func orderByKey[T any](items []T, key func(T) string, order []string) []T {
	if len(order) == 0 || len(items) < 2 {
		return items
	}
	rank := make(map[string]int, len(order))
	for i, k := range order {
		if _, ok := rank[k]; !ok {
			rank[k] = i
		}
	}
	out := append([]T(nil), items...)
	sort.SliceStable(out, func(a, b int) bool {
		ra, oka := rank[key(out[a])]
		rb, okb := rank[key(out[b])]
		switch {
		case oka && okb:
			return ra < rb
		case oka:
			return true
		default:
			// Two unranked items keep their relative order (SliceStable), so a
			// session the user never touched cannot drift.
			return false
		}
	})
	return out
}
