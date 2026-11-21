package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func (d *Daemon) verbDaemonUpgrade(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Binary string `json:"binary"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "daemon-upgrade is refused over a link", &VerbHint{
			Detail: "Nothing was changed. Run tuios daemon upgrade on the machine whose daemon it is.",
		})
	}
	notPerson := func(why string) *verbError {
		return hintedVerbError(ErrVerbForbidden, "daemon-upgrade is for the person, and an upgrade asked for from a pane needs their Yes in a dialog at an attached client. Nothing was changed. "+why+".", &VerbHint{
			Detail: "The dialog opens on every attached client and shows the binary, its version and the pane that asked. From a terminal outside tuios, tuios daemon upgrade needs no dialog.",
		})
	}
	confirm := !d.mayActAsHuman(cs)
	if confirm && d.callerIsPlugin(cs) {
		return nil, hintedVerbError(ErrVerbForbidden, "daemon-upgrade is for the person. A plugin cannot upgrade the daemon, and no dialog is shown for one. Nothing was changed. Run tuios daemon upgrade from a terminal outside tuios", &VerbHint{
			Detail: "From a pane, the upgrade asks the person in a dialog at an attached client instead.",
		})
	}
	if p.Binary == "" || !filepath.IsAbs(p.Binary) {
		return nil, invalidParam("binary", "binary must be the absolute path of a tuios binary")
	}
	req := handoffRequest{binary: p.Binary}
	if confirm {
		pin, err := statFileID(p.Binary)
		if err != nil {
			return nil, upgradeRefusal(errfmt.Errorf("%s: %w", p.Binary, err))
		}
		if why := d.upgradeDialogRefusal(); why != "" {
			LogBasic("Live upgrade from a pane refused: %s", why)
			return nil, notPerson(why)
		}
		bin, err := preflightBinary(p.Binary, preflightOpts{copy: req.copyRoute})
		if err != nil {
			return nil, upgradeRefusal(err)
		}
		bin.close()
		if bin.file != pin {
			return nil, upgradeRefusal(errfmt.Errorf("%s was replaced while it was being checked. Run the upgrade again when the install has finished", bin.path))
		}
		shown := exitedRemoteLabels(d.manager.AllSessions())
		if why := d.confirmUpgrade(cs, bin, shown); why != "" {
			return nil, notPerson(why)
		}
		req.binary, req.pinned = bin.path, &bin.file
		req.shownExited = append([]string{}, shown...)
	}
	clients := d.tuiClientCount()
	run, err := d.prepareHandoff(req)
	if err != nil {
		return nil, upgradeRefusal(err)
	}
	cs.takeover = func(*bufio.Reader) { LogError("Live upgrade: %v", run.commit()) }
	return map[string]any{
		"type":           "daemon_upgrading",
		"pid":            os.Getpid(),
		"instance":       d.instance,
		"from":           d.version,
		"to":             run.bin.accepts.Version,
		"binary":         run.bin.path,
		"handoff":        HandoffVersion,
		"panes":          run.panes,
		"manifest_bytes": run.manifestBytes,
		"clients":        clients,
		"preflight_ms":   int64(run.stats.PreflightMS),
		"closed_remote":  run.closedRemote,
	}, nil
}

func upgradeRefusal(err error) *verbError {
	code := ErrVerbUpgradeRefused
	if errors.Is(err, errHandoffBusy) {
		code = ErrVerbBusy
	}
	return hintedVerbError(code, err.Error(), &VerbHint{
		Detail: "Nothing was changed. Every pane and client is as it was. " + restartAdvice(),
	})
}
