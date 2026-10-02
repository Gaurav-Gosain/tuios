//go:build !slim

package session

// verbLeftOut is false in the full build, which has every verb. The tables
// that name verbs (scopes, grants, examples) are checked against all of
// them.
func verbLeftOut(string) bool { return false }

// examplesTrimmed is false in the full build, which keeps every example.
func examplesTrimmed(string) bool { return false }

// servedWaitConditions is every wait-for condition in the full build.
func servedWaitConditions() []string { return waitConditions }
