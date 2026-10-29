package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// verbDaemonUpgrade starts a live upgrade to another binary. See
// handoff.go. It answers after the fallible half, so a refusal reaches the
// caller with every part of the daemon as it was; the exec follows once the
// answer is written.
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
	// Whatever the caller's grants: the binary named runs as the daemon, which
	// holds every pane's terminal and the key that signs every pane's token.
	// A plugin never may. A pane may when the person says yes in a dialog at
	// an attached client (upgrade_confirm.go).
	notPerson := hintedVerbError(ErrVerbForbidden, "daemon-upgrade is for the person: the caller runs inside a pane of this daemon or is a plugin, and an upgrade is refused there whatever its grants", &VerbHint{
		Detail: "Nothing was changed. Run tuios daemon upgrade from a terminal outside tuios, or answer Yes in the dialog tuios shows when a pane asks.",
	})
	confirm := !d.mayActAsHuman(cs)
	if confirm && d.callerIsPlugin(cs) {
		return nil, notPerson
	}
	if p.Binary == "" || !filepath.IsAbs(p.Binary) {
		return nil, invalidParam("binary", "binary must be the absolute path of a tuios binary")
	}
	req := handoffRequest{binary: p.Binary}
	if confirm {
		// The dialog shows the binary's path and version, so the preflight
		// runs first, and the upgrade then takes only the file it ran.
		bin, err := preflightBinary(p.Binary)
		if err != nil {
			return nil, upgradeRefusal(err)
		}
		if !d.confirmUpgrade(cs, bin) {
			return nil, notPerson
		}
		req.binary, req.pinned = bin.path, &bin.file
	}
	clients := d.tuiClientCount()
	run, err := d.prepareHandoff(req)
	if err != nil {
		return nil, upgradeRefusal(err)
	}
	// commit returns only when the exec failed.
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
	}, nil
}

// upgradeRefusal is the refusal of an upgrade that changed nothing.
func upgradeRefusal(err error) *verbError {
	code := ErrVerbUpgradeRefused
	if errors.Is(err, errHandoffBusy) {
		code = ErrVerbBusy
	}
	return hintedVerbError(code, err.Error(), &VerbHint{
		Detail: "Nothing was changed. Every pane and client is as it was. " + restartAdvice(),
	})
}
