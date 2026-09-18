//go:build !linux && !darwin

package session

// processCwd has no answer on a platform with neither procfs nor libproc.
//
// Saying so is the honest report rather than a guess: a caller that reads the
// empty string as the root directory would spawn a shell in / rather than
// where the daemon runs.
func processCwd(int) (string, bool) { return "", false }
