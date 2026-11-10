package proto

// A remote client, the app or the CLI on another machine, reaches kerod
// through one connection: an SSH or WSL session running `kero __bridge` on
// the host, or one of kerod's network listeners (see package remote). It
// opens with a Hello of kind "mux", which a MuxWelcome answers, then
// carries streams (yamux): each stream is a connection as a local client
// opens one to the socket, a control or an attach connection, starting
// with its own Hello.
//
// The mux layer changes far less often than the protocol it carries, so
// that a client still talks to a host of another protocol, to tell the
// user so and to update it: MuxVersion is its version, and the host
// streams (Hello kinds "host.restart" and "host.update") are answered
// whatever the protocol.

// MuxVersion is the version of the mux layer: a MuxWelcome of another
// one ends the connection.
const MuxVersion = 1

// Kinds of Hello beyond "control" and "attach".
const (
	// KindMux opens a remote connection (MuxHello).
	KindMux = "mux"
	// KindRestart asks the host, over a stream of a remote connection, to
	// restart kerod into the Kero installed: its sessions start again, as
	// after a reboot. A HostReply answers it.
	KindRestart = "host.restart"
	// KindUpdate asks a server build of Kero on the host to update itself
	// to App's version (Hello.App), signed as the app's own updates are. A
	// HostReply answers it; a restart then runs the new version.
	KindUpdate = "host.update"
)

// MuxHello opens a remote connection.
type MuxHello struct {
	Kind    string `json:"kind"` // KindMux
	Mux     int    `json:"mux"`
	Version int    `json:"version"` // the client's protocol
	App     string `json:"app,omitempty"`
	// Compress lists what the client reads, best first: "deflate".
	Compress []string `json:"compress,omitempty"`
	// Client names the client, as `kero clients` shows it: "Kero 2.1.0 on
	// laptop".
	Client string `json:"client,omitempty"`
}

// MuxWelcome answers a MuxHello. Both sides then compress what they write
// as Compress says, and carry streams.
type MuxWelcome struct {
	Mux      int      `json:"mux"`
	Compress string   `json:"compress,omitempty"`
	Host     HostInfo `json:"host"`
	Error    string   `json:"error,omitempty"`
}

// HostInfo describes the machine a remote connection reaches.
type HostInfo struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	User     string `json:"user,omitempty"`
	Home     string `json:"home,omitempty"`
	// Kero is the version of Kero that answered, the bridge's or kerod's,
	// and Version the protocol it speaks; kerod's own come with each
	// stream's Welcome, as it may run an older Kero until it restarts.
	Kero    string `json:"kero,omitempty"`
	Version int    `json:"version"`
	// Server tells a server build of Kero, and Updates one that
	// install-server.sh installed, which updates itself (KindUpdate); a
	// desktop app on the host updates as apps do.
	Server  bool `json:"server,omitempty"`
	Updates bool `json:"updates,omitempty"`
	// Peer is who the host takes the client for: "alice@example.com", or
	// the account a bridge runs as.
	Peer string `json:"peer,omitempty"`
}

// HostRequest is the Hello of a host stream: KindRestart or KindUpdate.
type HostRequest struct {
	Kind string `json:"kind"`
	// App is the version to update to.
	App string `json:"app,omitempty"`
}

// HostReply answers a HostRequest.
type HostReply struct {
	OK    bool   `json:"ok"`
	App   string `json:"app,omitempty"` // the version installed
	Error string `json:"error,omitempty"`
}

// ClientInfo is a client connected to kerod, as `kero clients` lists it.
type ClientInfo struct {
	Kind  string `json:"kind"` // control, attach
	Peer  string `json:"peer"` // who: "uid 501", "alice@example.com"
	Via   string `json:"via"`  // unix, ssh, wsl, tailnet, tailscale-serve
	Since int64  `json:"since"`
	// Client names the app or CLI, when it said.
	Client string `json:"client,omitempty"`
	// Session is the session an attach connection shows.
	Session string `json:"session,omitempty"`
}

// Params and results of the methods for remote clients, which see the
// host's files through kerod.
type (
	// FSListParams list a directory of the host: Dir, absolute, or ~ and
	// ~/… for the home, "" for the home.
	FSListParams struct {
		Dir    string `json:"dir"`
		Hidden bool   `json:"hidden,omitempty"` // dotfiles too
		// Ignored marks the entries git ignores (FSEntry.Ignored).
		Ignored bool `json:"ignored,omitempty"`
	}
	FSList struct {
		Dir     string    `json:"dir"` // absolute, cleaned
		Parent  string    `json:"parent,omitempty"`
		Home    string    `json:"home"`
		Entries []FSEntry `json:"entries"`
	}
	FSEntry struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir,omitempty"`
		// Repo tells a directory that holds a git repository.
		Repo bool `json:"repo,omitempty"`
		// Ignored tells an entry git ignores, as FSListParams.Ignored
		// asks.
		Ignored bool `json:"ignored,omitempty"`
	}
	// FSStatParams ask about a path printed in a pane: relative to Base,
	// the pane's directory, unless absolute; ~ and ~/… are the home's.
	FSStatParams struct {
		Path string `json:"path"`
		Base string `json:"base,omitempty"`
	}
	FSStat struct {
		Path   string `json:"path"` // absolute
		Exists bool   `json:"exists"`
		Dir    bool   `json:"dir,omitempty"`
		Size   int64  `json:"size,omitempty"`
	}
	// FSReadParams read a file of the host, of at most Max bytes.
	FSReadParams struct {
		Path string `json:"path"`
		Max  int64  `json:"max,omitempty"`
	}
	FSRead struct {
		Data []byte `json:"data"` // base64 in JSON
	}
	// FSWriteParams write Text to a file of the host, as the user edited
	// it, when its content still hashes to Hash (git.Hash), as it was
	// read, or whatever it is with Force: the result is the hash of what
	// was written.
	FSWriteParams struct {
		Path  string `json:"path"`
		Hash  string `json:"hash"`
		Text  string `json:"text"`
		Force bool   `json:"force,omitempty"`
	}
	// FSCreateParams make a file of the host, empty, or a folder (Dir),
	// with the folders it is in; none that is there already.
	FSCreateParams struct {
		Path string `json:"path"`
		Dir  bool   `json:"dir,omitempty"`
	}
	// FSRenameParams move a file or folder of the host to To, which must
	// be free.
	FSRenameParams struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	// FSDeleteParams delete a file or folder of the host, and what is in
	// it, for good: the app moves its own machine's to the trash instead.
	FSDeleteParams struct {
		Path string `json:"path"`
	}
	// PasteFileParams put a file the user pasted or dropped into a pane on
	// the host, in a directory of kero's, for the program to read by its
	// path.
	PasteFileParams struct {
		Name string `json:"name"`
		Data []byte `json:"data"`
	}
	PasteFile struct {
		Path string `json:"path"`
	}
)
