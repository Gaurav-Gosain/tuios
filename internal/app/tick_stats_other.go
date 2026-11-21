//go:build !unix

package app

import tea "charm.land/bubbletea/v2"

func dumpTickStatsOnSignal(*tea.Program) {}
