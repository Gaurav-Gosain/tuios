package config

// ShellConfig is the [shell] table: what tuios does to the shells it starts
// in panes. The daemon reads it when it spawns a pane, so a change applies to
// the next pane.
type ShellConfig struct {
	// Integration is whether tuios turns on OSC 133 prompt marks in the bash
	// and zsh shells it starts: auto (the default) or off. tuios edits no
	// file of yours: it starts the shell so that it reads your startup files
	// and then the marks. See internal/shellinteg.
	Integration string `toml:"integration"`
}

const (
	// ShellIntegrationAuto turns the marks on for a shell tuios knows how to
	// set up, and leaves any other shell alone.
	ShellIntegrationAuto = "auto"
	// ShellIntegrationOff starts every shell as it is.
	ShellIntegrationOff = "off"
)

// ShellIntegrationModes are the accepted shell.integration values.
var ShellIntegrationModes = []string{ShellIntegrationAuto, ShellIntegrationOff}

// ShellIntegrationOn reports whether mode turns the marks on. Empty, and any
// value that is not off, is auto: validation warns about a typo, and the
// default is the safe reading of one.
func ShellIntegrationOn(mode string) bool {
	return mode != ShellIntegrationOff
}
