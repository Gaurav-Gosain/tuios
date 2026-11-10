//go:build !linux && !darwin

package session

// usageSupported says pane-usage can read processes here. It cannot.
const usageSupported = false

func procUsage(roots map[int]paneProc) (map[int]paneProc, map[int]*procStat) { return roots, nil }
