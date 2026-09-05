package app

// ClientKind names where the person looking at the screen is sitting. It is
// the one fact the three entry points differ on, and everything below that
// used to be a boolean each of them set by hand.
//
// A client is built with a kind and NewOS derives the rest: whether the
// settings page may write the config file, whether the process shares a desktop
// with the user, how graphics reach the terminal. A flag derived from the kind
// cannot be set on one client and forgotten on another, which is how the same
// server shipped read-only for the browser and writable over SSH once.
//
// The zero value is a client nobody named. The tests and the benchmarks build
// those, and they get no derivation and no watcher: exactly the struct the
// options describe.
type ClientKind int

const (
	// ClientUnknown is a model built without saying who is looking at it.
	ClientUnknown ClientKind = iota
	// ClientLocal is a terminal on this machine: bare tuios, attach, new, tape.
	ClientLocal
	// ClientSSH is a terminal at the far end of a `tuios ssh` connection.
	ClientSSH
	// ClientBrowser is a tab served by tuios-web.
	ClientBrowser
)

// String is the name a bug report and a log line use.
func (k ClientKind) String() string {
	switch k {
	case ClientLocal:
		return "local"
	case ClientSSH:
		return "ssh"
	case ClientBrowser:
		return "web"
	default:
		return "unknown"
	}
}

// Remote reports whether the user is at the far end of a network, so the
// process must not touch its own desktop on their behalf.
func (k ClientKind) Remote() bool {
	return k == ClientSSH || k == ClientBrowser
}

// applyClientKind fills the per-client flags a kind implies. Each is OR-ed
// with what the caller set, so a test that sets one flag directly keeps it and
// a caller that names a kind cannot forget one.
func (o *OSOptions) applyClientKind() {
	switch o.Client {
	case ClientSSH:
		o.IsSSHMode = true
		// The terminal is reached over the network and does not share the
		// server's filesystem, so file-medium kitty transmissions are
		// re-encoded as direct data.
		o.GraphicsRemoteClient = true
	case ClientBrowser:
		o.BrowserClient = true
		// stdin is not a TTY in a web server, so capability detection cannot
		// see the browser's image addon. The browser's inline mode reads
		// file-medium transmissions server-side itself.
		o.ForceGraphicsEnabled = true
	}
	if o.Client.Remote() {
		// The config file belongs to the server operator, and several clients
		// each hold a stale snapshot of it. Settings still apply live to the
		// session, they are just not written back.
		o.ConfigReadOnly = true
		// Nothing here may touch the host's own desktop: a clipboard helper or
		// a file viewer opened here acts on the operator's machine.
		o.RemoteClient = true
	}
}
