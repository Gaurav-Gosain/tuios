//go:build ghostty

package session

// libghostty-vt does not count the history rows it lets go of, so a pane on it
// gives its history rows no ids and pane-history refuses it. The pure Go
// emulator gives ids, and the example succeeds there.
func init() {
	exampleOutcomes["pane-history#0"] = exampleOutcome{
		errCode: ErrVerbNoRowIDs,
		why:     "the libghostty-vt emulator gives its history rows no ids",
	}
}
