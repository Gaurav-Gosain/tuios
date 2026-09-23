package session

import (
	"bufio"
	"encoding/json"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The two verbs a machine serves so another machine's session can hold a
// window here. See hosted_pane.go for what a hosted pane is and why it is
// shaped this way.
//
// They are split across two connections on purpose. open-pane takes over its
// own connection and never speaks JSON on it again, because a pane is a stream
// and framing every keystroke would buy nothing; resize-pane arrives
// separately on the link's control stream, because the pane's connection has
// no room left to carry anything out of band. That is the same division
// open-host-connection already uses, and it is why neither verb needs a new
// binary message type.

// ErrVerbUnknownPane reports a pane id that this daemon is not running. It is
// its own code because the remedy differs from a bad parameter: the pane was
// real and is gone, so the caller should drop it rather than correct it.
const ErrVerbUnknownPane = "unknown_pane"

// verbOpenPane spawns a process here and hands this connection to the relay.
// From the reply onward the connection is the pty: every byte written to it
// reaches the process and every byte the process writes comes back.
func (d *Daemon) verbOpenPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var spec hostedPaneSpec
	if verr := decodeParams(params, &spec); verr != nil {
		return nil, verr
	}

	hp, err := d.registerHostedPane(spec)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not start a pane on this machine: "+err.Error())
	}

	LogBasic("Client %s opened pane %s on this machine", cs.clientID, hp.id)
	cs.takeover = func(br *bufio.Reader) {
		d.relayHostedPane(cs, br, hp)
		LogBasic("Pane %s ended", hp.id)
	}
	out := map[string]any{
		"type": "pane",
		"pane": hp.id,
	}
	if hp.callsToken != "" {
		out["calls_token"] = hp.callsToken
	}
	return out, nil
}

// verbResizePane changes a hosted pane's size. The size is decided by the
// layout on the machine that owns the window, which is the only place that
// knows what rectangle the pane is being drawn into.
func (d *Daemon) verbResizePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane   string `json:"pane"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "resize-pane needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		// A pane that ended between the write and the resize is the ordinary
		// case, not a fault: the process exited and the relay dropped it. The
		// caller closes the window on its own side.
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	if err := hp.resize(p.Width, p.Height); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not resize the pane: "+err.Error())
	}
	return map[string]any{"pane": p.Pane, "width": p.Width, "height": p.Height}, nil
}

// checkWindowHost validates the host a window was asked for and normalises
// this machine's several spellings to the empty one.
//
// The name is resolved before anything is spawned, so a typo comes back as a
// parameter error listing the machines that do exist rather than as a link
// failure with a timeout in front of it. "local" is accepted because that is
// what the rail and the listings call this machine, and it is the default.
func checkWindowHost(d *Daemon, host *string) *verbError {
	if *host == "" {
		return nil
	}
	if *host == federation.LocalHostName {
		*host = ""
		return nil
	}
	if name := d.hostedPaneHostName(); name != "" && *host == name {
		// The machine's own name is this machine. Putting a window "on" it
		// through a link that loops back would be a second daemon's worth of
		// relay for a pane that belongs here.
		*host = ""
		return nil
	}
	return d.checkHostParam(*host)
}

// verbPaneCwd answers where a hosted pane's process is.
//
// The machine running the process is the only one that can say. The daemon
// that owns the window reads a pid that means nothing here, and the shell may
// never announce over OSC 7: bash and zsh mostly do not, which is the case the
// rail's file section already had to be taught for panes of its own.
func (d *Daemon) verbPaneCwd(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane string `json:"pane"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "pane-cwd needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	cwd, _ := hp.processCwd()
	return map[string]any{"pane": p.Pane, "cwd": cwd}, nil
}

// verbPaneAgent reports what a pane this machine runs for another machine is
// running, so the daemon that owns the window can detect an agent in it.
//
// Without this, a pane whose process is here is invisible to detection on both
// sides. The owning daemon reads a shell pid of zero, because there is no
// process on its machine, so every tier that starts from the foreground
// process gives up: no harness is identified, and the transcript, title and
// screen tiers all gate on having one. This side never looks either, because a
// hosted pane is not a window of any session here and the detector walks
// sessions.
//
// So the two halves are split the way pane-cwd splits them. This side reads
// the process, which only it can do. The other side decides what the process
// means, which only it should do: the rules, the manifests and the user's
// configuration all live with the window.
//
// It carries no authority the link did not already have. A configured host can
// be asked for a shell, and naming the process already running in one is
// strictly less than that.
func (d *Daemon) verbPaneAgent(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane string `json:"pane"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "pane-agent needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	info, running := hp.foreground()
	return map[string]any{
		"pane":      p.Pane,
		"running":   running,
		"comm":      info.comm,
		"argv":      info.argv,
		"exe":       info.exe,
		"pid":       info.pid,
		"shell_pid": info.shellPID,
	}, nil
}

// verbReadDir lists a directory on this machine.
//
// It is the far half of the rail's file section for a pane whose process runs
// here. The section asks the daemon that owns the pane rather than reading a
// filesystem itself, for the reason it was taught once already: the machine
// with the process is the machine with the files, and any other answer is a
// listing of the wrong disk under the right path.
//
// It carries no authority the link did not already have. A configured host can
// be asked for a shell, and reading the names in a directory is strictly less
// than that.
func (d *Daemon) verbReadDir(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Dir string `json:"dir"`
		Max int    `json:"max"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Dir == "" {
		return nil, invalidParam("dir", "read-dir needs a directory to list.")
	}
	return listDir(filepath.Clean(p.Dir), p.Max), nil
}
