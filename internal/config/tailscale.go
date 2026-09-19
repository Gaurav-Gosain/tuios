package config

import "github.com/Gaurav-Gosain/tuios/internal/federation"

// The [tailscale] table: which machines on your tailnet are offered as
// addresses when you add a host.
//
//	[tailscale]
//	user = "ubuntu"
//	exclude = ["*-pad-*"]
//
// It changes what tuios suggests and nothing else. No machine is ever added on
// its own, no tailnet address is dialled by tuios itself, and a machine with no
// tailscale on it behaves exactly as it did before this table existed. A host
// added from this list is reached over ssh like every other host, which already
// works across a tailnet because a MagicDNS name resolves like any other name.
//
// It sits outside the option registry for the same reason [hosts] does: the
// per-machine logins are a map of named values rather than a scalar with a
// single settable path.
//
// See internal/federation's tailnet.go for what each field does to the list,
// and `tuios hosts tailnet` to see the answer for your own tailnet, including
// every machine that was left out and the reason it was.

// TailscaleConfig is the [tailscale] table.
type TailscaleConfig struct {
	// Enabled offers tailnet machines as addresses. Default true: the list is
	// read from the tailscaled already running on this machine, costs one
	// local call, and is empty on a machine that is not on a tailnet.
	Enabled *bool `toml:"enabled,omitempty"`
	// Addr is which form of address to offer: "dns" for the MagicDNS name,
	// "name" for the short name, "ip" for the 100.x address. Default "dns",
	// which is the form that works without depending on a search domain.
	Addr string `toml:"addr,omitempty"`
	// User is an ssh login put in front of every address, so a candidate reads
	// ubuntu@box.example.ts.net. Empty offers the address alone.
	User string `toml:"user,omitempty"`
	// Users are per-machine logins keyed by the machine's short name, for a
	// tailnet where the login differs from box to box. A machine named here
	// wins over User.
	//
	//	[tailscale.users]
	//	build = "ubuntu"
	Users map[string]string `toml:"users,omitempty"`
	// OS are the operating systems to offer. Default is the ones that can run
	// a tuios daemon, which is what keeps a phone out of the list. An empty
	// list offers every machine.
	OS []string `toml:"os,omitempty"`
	// Offline offers machines the control plane says are not connected.
	// Default false.
	Offline bool `toml:"offline,omitempty"`
	// Self offers this machine. Default false: `local` already means the
	// daemon you are talking to, and the host table refuses an entry for it.
	Self bool `toml:"self,omitempty"`
	// Shared offers machines other people shared into this tailnet. Default
	// false.
	Shared bool `toml:"shared,omitempty"`
	// Include and Exclude are glob patterns matched against both the short
	// name and the MagicDNS name. Exclude wins over Include, and an empty
	// Include means everything.
	Include []string `toml:"include,omitempty"`
	Exclude []string `toml:"exclude,omitempty"`
	// Max bounds the offered list. Zero uses the built-in default.
	Max int `toml:"max,omitempty"`
	// Socket overrides the path to the tailscaled local API socket, for a
	// machine where it is not in the usual place. Empty finds it the way the
	// tailscale command does.
	Socket string `toml:"socket,omitempty"`
}

// TailscaleEnabled reports whether tailnet machines are offered. Absent means
// yes: the call is local and cheap, and it answers nothing on a machine with
// no tailscale.
func (t TailscaleConfig) TailscaleEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// TailnetOptions turns the table into what internal/federation asks for.
//
// A field the user did not set keeps the built-in default rather than reading
// as "off", which is why this starts from DefaultTailnetOptions instead of
// building an empty struct.
func (t TailscaleConfig) TailnetOptions() federation.TailnetOptions {
	opt := federation.DefaultTailnetOptions()
	if t.Addr != "" {
		opt.Addr = t.Addr
	}
	opt.User = t.User
	opt.Users = t.Users
	if t.OS != nil {
		// A set but empty list is a decision: it means every machine. Only an
		// absent list keeps the default.
		opt.OS = t.OS
	}
	opt.Offline = t.Offline
	opt.Self = t.Self
	opt.Shared = t.Shared
	opt.Include = t.Include
	opt.Exclude = t.Exclude
	if t.Max > 0 {
		opt.Max = t.Max
	}
	opt.Socket = t.Socket
	return opt
}
