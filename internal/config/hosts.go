package config

// The [hosts] table names the other machines whose daemons this one may ask
// for listings. It is federation stage 1's whole configuration surface.
//
//	[hosts.build]
//	addr = "gaurav@buildbox"
//
//	[hosts.work]
//	addr = "workstation.local"
//	connect_timeout = 5
//
// It sits outside the option registry for the reason [hooks], [keybindings] and
// [dock.custom] do: it is a map of named tables, not a scalar with a settable
// value, so there is no single path the set-option verb or the settings panel
// could write. Editing it is a file edit, and the daemon reads it at start.
//
// Discovery is refused on purpose (design document, section 3): a host exists
// because the user named it, and a name resolves exactly or not at all.

// HostConfig is one [hosts.NAME] table.
type HostConfig struct {
	// Addr is anything ssh understands, ssh_config aliases included. A host
	// with no addr is ignored, and the daemon logs why.
	Addr string `toml:"addr"`
	// ConnectTimeout is how many seconds one dial may take before the host is
	// called unreachable. Zero uses the built-in default. It is also handed to
	// ssh, so a machine that is powered off is reported rather than waited on.
	ConnectTimeout int `toml:"connect_timeout,omitempty"`
	// Command is the tuios binary on the far side. Empty means "tuios". Set it
	// when a per-user install is not on the non-interactive PATH.
	Command string `toml:"command,omitempty"`
	// SSHOptions are extra arguments passed to ssh before the address, for a
	// host that needs a flag ssh_config cannot carry.
	SSHOptions []string `toml:"ssh_options,omitempty"`
}
