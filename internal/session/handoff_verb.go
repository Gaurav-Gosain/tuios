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
	if p.Binary == "" || !filepath.IsAbs(p.Binary) {
		return nil, invalidParam("binary", "binary must be the absolute path of a tuios binary")
	}
	clients := d.tuiClientCount()
	run, err := d.prepareHandoff(handoffRequest{binary: p.Binary})
	if err != nil {
		code := ErrVerbUpgradeRefused
		if errors.Is(err, errHandoffBusy) {
			code = ErrVerbBusy
		}
		return nil, hintedVerbError(code, err.Error(), &VerbHint{
			Detail: "Nothing was changed: every pane and client is as it was. 'tuios kill-server' restarts the daemon instead, which ends running programs.",
		})
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
