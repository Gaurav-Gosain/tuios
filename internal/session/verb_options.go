package session

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const optionDaemonLogLevel = "daemon.log_level"

const optionAgentsEnabled = "agents.enabled"

func (d *Daemon) verbListOptions(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Section string `json:"section"`
		Prefix  string `json:"prefix"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	var sess *Session
	if s := d.findTargetSession(cs, p.Session); s != nil {
		sess = s
	} else if p.Session != "" {
		return nil, mustResolve(d, cs, p.Session)
	}

	sections := map[string]bool{}
	out := make([]map[string]any, 0, len(config.Options()))
	for _, opt := range config.Options() {
		sections[opt.Section] = true
		if p.Section != "" && opt.Section != p.Section {
			continue
		}
		if p.Prefix != "" && !strings.HasPrefix(opt.Path, p.Prefix) {
			continue
		}
		row := map[string]any{
			"path":        opt.Path,
			"type":        opt.Type,
			"section":     opt.Section,
			"description": opt.Description,
			"default":     config.EffectiveDefault(opt),
		}
		if len(opt.Accepted) > 0 {
			row["accepted"] = opt.Accepted
		}
		if opt.Max > 0 {
			row["min"] = opt.Min
			row["max"] = opt.Max
		}
		if opt.Unit != "" {
			row["unit"] = opt.Unit
		}
		if opt.Auto {
			row["auto"] = true
		}
		if opt.Deprecated != "" {
			row["deprecated"] = opt.Deprecated
		}
		if sess != nil {
			if v, ok := sess.GetOption(opt.Path); ok {
				row["session_value"] = v
			}
		}
		out = append(out, row)
	}

	if len(out) == 0 && (p.Section != "" || p.Prefix != "") {
		return nil, hintedVerbError(ErrVerbOptionNotFound, "no options match that filter", &VerbHint{
			Param:     "section",
			Available: sortedKeys(sections),
			Detail:    "section groups the options for display. prefix matches the start of a path.",
		})
	}

	return map[string]any{
		"type":     "option_list",
		"options":  out,
		"sections": sortedKeys(sections),
		"total":    len(out),
	}, nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func mustResolve(d *Daemon, cs *connState, name string) *verbError {
	_, verr := d.resolveVerbSession(cs, name)
	if verr != nil {
		return verr
	}
	return newVerbError(ErrVerbInternal, "session "+name+" resolved on the second attempt")
}

func resolveOptionPath(key string) (config.Option, string, bool) {
	if opt, ok := config.LookupOption(key); ok {
		return opt, key, true
	}
	if !strings.Contains(key, ".") {
		full := "appearance." + key
		if opt, ok := config.LookupOption(full); ok {
			return opt, full, true
		}
	}
	return config.Option{}, key, false
}

func (d *Daemon) verbSetOption(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Key     string `json:"key"`
		Value   string `json:"value"`
		NoSave  bool   `json:"no_save"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Key == "" {
		return nil, invalidParam("key", `key is required, e.g. "appearance.sidebar.enabled"`)
	}

	opt, path, known := resolveOptionPath(p.Key)
	if msg, retired := config.RetiredOption(p.Key); !known && retired {
		return nil, invalidParam("key", msg)
	}
	if !known {
		return nil, hintedVerbError(ErrVerbOptionNotFound, "no such option "+echoName(p.Key), &VerbHint{
			Param:      "key",
			Verb:       "list-options",
			Command:    "tuios list-options",
			DidYouMean: closestMatch(p.Key, config.OptionPaths()),
			Available:  config.OptionPaths(),
			Detail:     "call list-options for each path with its type, default and accepted values.",
		})
	}
	if path == optionAgentsEnabled && d.paneAuthority(cs) != nil {
		return nil, hintedVerbError(ErrVerbForbidden, "only the person can change agents.enabled. A process in a pane cannot", &VerbHint{
			Param:  "key",
			Detail: "Nothing was changed. The person sets it on the settings page or in config.toml.",
		})
	}

	if path == optionAgentsEnabled && p.NoSave {
		return nil, invalidParam("no_save", "agents.enabled is always saved, because the daemon reads it from the config file. Nothing was changed")
	}

	probe := config.DefaultConfig()
	if err := config.SetOptionValue(probe, path, p.Value); err != nil {
		hint := &VerbHint{
			Param:    "value",
			Accepted: opt.Accepted,
			Detail:   "type " + opt.Type + ", default " + echoName(opt.Default) + ". " + opt.Description,
		}
		if opt.Theme {
			hint.Verb = "list-themes"
			hint.Command = "tuios list-themes --filter " + p.Value
			hint.DidYouMean = closestMatch(p.Value, theme.AvailableThemes())
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), hint)
	}

	if path == optionDaemonLogLevel {
		previous := GetDebugLevel()
		SetDebugLevel(ParseDebugLevel(p.Value))
		LogBasic("Client %s set the log level to %s (was %s)", cs.clientID, GetDebugLevel(), previous)
		return map[string]any{
			"type":     "option_set",
			"key":      path,
			"value":    p.Value,
			"applied":  true,
			"scope":    "daemon",
			"previous": previous.String(),
			"reason":   "the log level applies to the daemon at once. It is not saved to the config file",
			"no_save":  p.NoSave,
		}, nil
	}

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	if p.NoSave && path != optionWindowSize {
		if tui := d.findTUIClient(sess.ID); tui != nil && !tui.holdsOptions() {
			return nil, hintedVerbError(ErrVerbInvalidParams, "the attached client is too old to apply a value without saving it. Nothing was changed", &VerbHint{
				Param:  "no_save",
				Detail: "Restart the client with the tuios version of the daemon, then set the value again.",
			})
		}
	}

	sess.SetOption(path, p.Value)

	if path == optionWindowSize {
		d.recalculateAndBroadcastSize(sess.ID, "")
		out := map[string]any{
			"type":    "option_set",
			"key":     path,
			"value":   windowSizePolicy(p.Value),
			"applied": true,
			"scope":   "session",
			"reason":  "the session uses this size policy at once. It is not saved to the config file",
			"no_save": p.NoSave,
		}
		if eff := sess.WindowSizePolicy(); eff != "" && eff != windowSizePolicy(p.Value) && d.getSessionClientCount(sess.ID) > 0 {
			out["in_force"] = eff
			out["reason"] = "an attached client is too old for this policy, so the session uses " + eff + " until that client detaches"
		}
		return out, nil
	}

	applied := false
	var reason string
	sess.optionApplyMu.Lock()
	if tui := d.findTUIClient(sess.ID); tui != nil {
		sess.dropWaitingOption(path)
		applied, reason = d.routeOption(tui, path, p.Value, p.NoSave)
	} else {
		sess.waitOption(path, p.Value, p.NoSave)
		reason = "no client is attached, so nothing is drawing it yet. " +
			"The next client that attaches applies it and saves it to the config file"
		if p.NoSave {
			reason = "no client is attached, so nothing is drawing it yet. " +
				"The next client that attaches applies it and does not save it"
		}
	}
	sess.optionApplyMu.Unlock()

	out := map[string]any{
		"type":    "option_set",
		"key":     path,
		"value":   p.Value,
		"applied": applied,
		"no_save": p.NoSave,
	}
	if reason != "" {
		out["reason"] = reason
	}
	if opt.Deprecated != "" {
		out["deprecated"] = opt.Deprecated
	}
	return out, nil
}

func (d *Daemon) verbGetOption(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Key     string `json:"key"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Key == "" {
		return nil, invalidParam("key", `key is required, e.g. "appearance.sidebar.enabled"`)
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}

	opt, path, known := resolveOptionPath(p.Key)
	if path == optionWindowSize {
		if _, ok := sess.GetOption(path); !ok {
			return map[string]any{
				"type": "option", "key": path, "value": d.windowSize,
				"source": "config", "default": config.EffectiveDefault(opt), "option_type": opt.Type,
			}, nil
		}
	}
	if path == optionAgentsEnabled {
		value := strconv.FormatBool(d.agentsEnabled())
		source := "default"
		if held, ok := sess.GetOption(path); ok && held == value {
			source = "session"
		} else if value != config.EffectiveDefault(opt) {
			source = "config"
		}
		return map[string]any{
			"type": "option", "key": path, "value": value,
			"source": source, "default": config.EffectiveDefault(opt), "option_type": opt.Type,
		}, nil
	}
	if value, source, ok := d.optionsFor(cs, sess).lookup(path); ok {
		out := map[string]any{"type": "option", "key": path, "value": value, "source": source}
		if known {
			out["default"] = config.EffectiveDefault(opt)
			out["option_type"] = opt.Type
		}
		return out, nil
	}

	available := sess.OptionKeys()
	return nil, hintedVerbError(ErrVerbOptionNotFound, "no such option "+echoName(p.Key), &VerbHint{
		Param:      "key",
		Verb:       "list-options",
		Command:    "tuios list-options",
		DidYouMean: closestMatch(p.Key, append(config.OptionPaths(), available...)),
		Available:  append(config.OptionPaths(), available...),
		Detail:     "the key is in neither this build's option registry nor this session's overrides.",
	})
}
