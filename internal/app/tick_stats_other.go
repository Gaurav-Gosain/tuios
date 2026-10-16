//go:build !unix

package app

import tea "charm.land/bubbletea/v2"

// dumpTickStatsOnSignal does nothing here: there is no SIGUSR1.
func dumpTickStatsOnSignal(*tea.Program) {}
