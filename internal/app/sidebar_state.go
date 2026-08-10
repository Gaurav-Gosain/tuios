package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/adrg/xdg"
)

// The sidebar keeps two pieces of state worth surviving a restart: the user's
// drag-defined session order and the per-session expand/collapse toggles. They
// are view preferences of this user's sidebar, not session data, so they live
// in a small state file of their own rather than in config.toml (which the
// settings page rewrites) or in the daemon (which serves many clients).

// sidebarStateDir returns the directory the sidebar state file lives in. A
// variable so tests can point it at a scratch directory.
var sidebarStateDir = func() string {
	return filepath.Join(xdg.StateHome, "tuios")
}

const sidebarStateFileName = "sidebar.json"

// sidebarStateFile is the on-disk shape. Collapsed records only explicit user
// toggles (true = collapsed); sessions without an entry keep the default of
// "expanded when current, collapsed otherwise".
type sidebarStateFile struct {
	Order     []string        `json:"order,omitempty"`
	Collapsed map[string]bool `json:"collapsed,omitempty"`
}

// loadSidebarState reads the persisted sidebar order and collapse toggles.
// Any failure leaves the defaults in place; a missing file is the ordinary
// first-run case, not an error worth surfacing.
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
	if len(st.Collapsed) > 0 {
		m.SidebarCollapsed = st.Collapsed
	}
}

// saveSidebarState writes the sidebar order and collapse toggles. Best effort:
// the state is a convenience, and a failed write must never interrupt the
// interaction that triggered it.
func (m *OS) saveSidebarState() {
	dir := sidebarStateDir()
	if os.MkdirAll(dir, 0o750) != nil {
		return
	}
	data, err := json.Marshal(sidebarStateFile{
		Order:     m.SidebarOrder,
		Collapsed: m.SidebarCollapsed,
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, sidebarStateFileName), data, 0o600)
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
