//go:build !linux && !darwin

package session

import "os"

// readParentAndOwner has no source on this platform, so no process is ever
// trusted as a pane's ssh client and an ssh split is an ordinary split.
func readParentAndOwner(int) (int, int, bool) { return 0, 0, false }

func readArgvExact(int) []string { return nil }

func readEnvVarOf(int, string) (string, bool) { return "", false }

func fileOwner(os.FileInfo) (int, bool) { return 0, false }
