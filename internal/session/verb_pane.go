package session

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pathroot"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// ErrVerbUnknownPane reports a pane id that this daemon is not running. It is
// its own code because the remedy differs from a bad parameter: the pane was
// real and is gone, so the caller should drop it rather than correct it.
const ErrVerbUnknownPane = "unknown_pane"

func (d *Daemon) verbOpenPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var spec hostedPaneSpec
	if verr := decodeParams(params, &spec); verr != nil {
		return nil, verr
	}
	if spec.Resume != nil {
		return d.resumeHostedPane(cs, spec.Resume)
	}

	var grace time.Duration
	if spec.Resumable {
		grace = d.linkPolicy(cs).HostedGrace
	}
	hp, err := d.registerHostedPane(spec, grace)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not start a pane on this machine: "+err.Error())
	}
	if spec.KittyMedia {
		hp.kittyLink = d.newKittyLinkFar(cs)
	}

	LogBasic("Client %s opened pane %s on this machine", cs.clientID, hp.id)
	cs.takeover = func(br *bufio.Reader) {
		d.relayHostedPane(cs, br, hp, 0)
		LogBasic("Pane %s left its connection", shortID(hp.id))
	}
	out := map[string]any{
		"type": "pane",
		"pane": hp.id,
	}
	if hp.callsToken != "" {
		out["calls_token"] = hp.callsToken
	}
	if hp.resumeToken != "" {
		out["resume_token"] = hp.resumeToken
		out["grace"] = int64(hp.grace / time.Second)
	}
	if hp.kittyLink != nil {
		out["kitty_media_token"] = hp.kittyLink.token
	}
	out["pixels"] = true
	return out, nil
}

func (d *Daemon) resumeHostedPane(cs *connState, r *hostedResume) (any, *verbError) {
	if r.Pane == "" {
		return nil, invalidParam("resume", "resume needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(r.Pane)
	if hp == nil || hp.ended() {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(r.Pane)+". Its process exited, or it was not reattached within its grace.")
	}
	if hp.resumeToken == "" || subtle.ConstantTimeCompare([]byte(hp.resumeToken), []byte(r.Token)) != 1 {
		return nil, newVerbError(ErrVerbForbidden, "a reattach needs the resume token the open-pane reply carried, and this is not it")
	}
	gap := hp.resumeGap(r.Offset)
	LogBasic("Client %s reattached pane %s", cs.clientID, shortID(hp.id))
	cs.takeover = func(br *bufio.Reader) {
		d.relayHostedPane(cs, br, hp, r.Offset)
		LogBasic("Pane %s left its connection", shortID(hp.id))
	}
	return map[string]any{
		"type":    "pane",
		"pane":    hp.id,
		"resumed": true,
		"gap":     gap,
		"grace":   int64(hp.grace / time.Second),
	}, nil
}

func (d *Daemon) verbClosePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane string `json:"pane"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "close-pane needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	hp.end()
	return map[string]any{"pane": p.Pane, "closed": true}, nil
}

func (d *Daemon) verbResizePane(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane   string `json:"pane"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		XPixel int    `json:"xpixel"`
		YPixel int    `json:"ypixel"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Pane == "" {
		return nil, invalidParam("pane", "resize-pane needs the pane id that open-pane returned.")
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	if err := hp.resize(p.Width, p.Height, p.XPixel, p.YPixel); err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not resize the pane: "+err.Error())
	}
	return map[string]any{"pane": p.Pane, "width": p.Width, "height": p.Height}, nil
}

func checkWindowHost(d *Daemon, host *string) *verbError {
	if *host == "" {
		return nil
	}
	if *host == federation.LocalHostName {
		*host = ""
		return nil
	}
	if name := d.hostedPaneHostName(); name != "" && *host == name {
		*host = ""
		return nil
	}
	return d.checkHostParam(*host)
}

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

func (d *Daemon) verbReadDir(cs *connState, params json.RawMessage) (any, *verbError) {
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
	dir := pathroot.ExpandHome(filepath.Clean(p.Dir))
	if verr := d.checkLinkRead(cs, dir); verr != nil {
		return nil, verr
	}
	return listDir(dir, p.Max), nil
}
