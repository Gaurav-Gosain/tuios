//go:build !unix

package app

import tea "charm.land/bubbletea/v2"

// App icons come from freedesktop desktop entries and a freedesktop icon
// theme, neither of which exists off unix. The launcher itself works
// everywhere; on these platforms it simply has no icon column, which is the
// same state a unix host with no graphics support lands in.

// launcherIconCols is the icon column's width in cells, zero here because there
// is no column.
const launcherIconCols = 0

// launcherIcons is the icon store. There is nothing to store.
type launcherIcons struct{}

// launcherIconPlacement is one icon's screen position.
type launcherIconPlacement struct {
	Name string
	X, Y int
}

// launcherIconsMsg carries decoded icons. None are ever decoded here, but
// Update still has to name the type.
type launcherIconsMsg struct{}

func (m *OS) launcherGraphicsReady() bool { return false }

func (m *OS) LauncherIconCmd([]string) tea.Cmd { return nil }

func (m *OS) applyLauncherIcons(launcherIconsMsg) {}

func (m *OS) flushLauncherIconsForFrame() {}

func (m *OS) clearLauncherIcons() {}
