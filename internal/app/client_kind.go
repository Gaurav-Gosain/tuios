package app

// ClientKind names where the person looking at the screen is sitting. It is
// the one fact the three entry points differ on, and everything below is
// derived from it.
//
// A client is built with a kind and NewOS derives the rest: whether the
// settings page may write the config file, whether the process shares a desktop
// with the user, how graphics reach the terminal. A flag derived from the kind
// cannot be set on one client and forgotten on another.
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

func (m *OS) previewsLive() bool { return !m.Client.Remote() || m.LearnMode }

func (o *OSOptions) applyClientKind() {
	switch o.Client {
	case ClientSSH:
		o.IsSSHMode = true
		o.GraphicsRemoteClient = true
	case ClientBrowser:
		o.BrowserClient = true
		o.ForceGraphicsEnabled = true
	}
	if o.Client.Remote() {
		o.ConfigReadOnly = true
		o.RemoteClient = true
	}
}
