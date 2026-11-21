//go:build !unix

package app

import tea "charm.land/bubbletea/v2"

const launcherIconCols = 0

type launcherIcons struct{}

type launcherIconPlacement struct {
	Name string
	X, Y int
}

type launcherIconsMsg struct{}

func (m *OS) launcherGraphicsReady() bool { return false }

func (m *OS) LauncherIconCmd([]string) tea.Cmd { return nil }

func (m *OS) applyLauncherIcons(launcherIconsMsg) {}

func (m *OS) flushLauncherIconsForFrame() {}

func (m *OS) clearLauncherIcons() {}

func (*launcherIcons) shotPieces() []shotPiece { return nil }
