package session

import (
	"bufio"
	"encoding/json"

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
	return map[string]any{
		"type": "pane",
		"pane": hp.id,
	}, nil
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
