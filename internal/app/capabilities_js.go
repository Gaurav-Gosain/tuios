//go:build js

package app

import "time"

// The browser build has no host terminal to probe. The page's renderer is the
// host, and it reports its size through the WindowSizeMsg the page sends, so
// every probe here answers "not a terminal" and capability detection falls
// back to its defaults.

func queryTerminalSize(_ *HostCapabilities) {}

func pollReadable(_ uintptr, _ time.Duration) (bool, error) { return false, nil }
