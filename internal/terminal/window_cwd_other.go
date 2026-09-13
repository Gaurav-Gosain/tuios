//go:build !linux && !darwin

package terminal

// shellCWD has no answer on a platform with neither procfs nor libproc.
//
// Saying so is the honest report rather than a guess: a caller that reads the
// empty string as the root directory, or a spoof check that reads "no answer"
// as "no disagreement", is the failure this signature exists to prevent.
func shellCWD(int) (string, bool) { return "", false }
