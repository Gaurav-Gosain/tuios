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
// value, so there is no single path the set-option verb could write.
//
// It is not edited by hand any more, though it still can be. `tuios hosts add`,
// `tuios hosts remove` and the Hosts section of the settings page write it, and
// the daemon follows the file, so a change takes effect with no restart. See
// hosts_edit.go for the write and internal/session's daemon_hosts.go for the
// reload.
//
// Discovery of machines is refused on purpose (design document, section 3): a
// host exists because the user named it, and a name resolves exactly or not at
// all. What is offered instead is discovery of what to type: the ssh_config
// Host aliases, as candidates for an addr. See internal/federation's
// sshalias.go.

// HostConfig is one [hosts.NAME] table.
type HostConfig struct {
	// Addr is anything ssh understands, ssh_config aliases included. A host
	// with no addr is ignored, and the daemon logs why.
	Addr string `toml:"addr"`
	// ConnectTimeout is how many seconds one dial may take before the host is
	// called unreachable. Zero uses the built-in default. It is also handed to
	// ssh, so a machine that is powered off is reported rather than waited on.
	ConnectTimeout int `toml:"connect_timeout,omitempty"`
	// Command is the tuios binary on the far side. Empty means the link finds
	// one itself: on the PATH, at the known install paths, or through the
	// login shell. Set it to run a given binary instead; nothing is then
	// looked for.
	Command string `toml:"command,omitempty"`
	// SSHOptions are extra arguments passed to ssh before the address, for a
	// host that needs a flag ssh_config cannot carry.
	SSHOptions []string `toml:"ssh_options,omitempty"`
}
