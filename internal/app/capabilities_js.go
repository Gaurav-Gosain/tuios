//go:build js

package app

import "time"

func queryTerminalSize(_ *HostCapabilities) {}

func pollReadable(_ uintptr, _ time.Duration) (bool, error) { return false, nil }
