//go:build !linux && !darwin

package ptyspawn

// ProcessCwd has no answer on a platform with neither procfs nor libproc.
//
// Saying so is the honest report rather than a guess: a caller that reads the
// empty string as the root directory would spawn a shell in / rather than
// where the daemon runs, and a spoof check that reads "no answer" as "no
// disagreement" would pass a pane it should refuse.
func ProcessCwd(int) (string, bool) { return "", false }
