package session

import (
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/google/uuid"
)

// This file makes the configuration surface reachable and, more to the point,
// findable. set-option would take any string at all: a misspelled path was
// recorded, reported as set, and did nothing, and there was no verb that would
// say which paths exist. The whole sidebar and dock surface was in that state,
// because the runtime setter only ever understood six appearance paths.
//
// The registry in internal/config is the one source for what exists, what type
// it is, and what values it takes. Both verbs here read it, so a caller that
// asks what is settable and a caller that sets something cannot disagree.

// verbListOptions reports the settable configuration paths.
//
// This is the discovery half of the pair. An agent asked to turn the sidebar on
// had no way to learn that the path is appearance.sidebar.enabled short of
// reading the docs, and guessing was indistinguishable from succeeding.
func (d *Daemon) verbListOptions(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Section string `json:"section"`
		Prefix  string `json:"prefix"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	// The session is optional here: the set of options is a property of the
	// build, not of a session, so listing them works before any session exists.
	// A session only adds which of them this session has been told to override.
	var sess *Session
	if s := d.findTargetSession(p.Session); s != nil {
		sess = s
	} else if p.Session != "" {
		return nil, mustResolve(d, p.Session)
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
			"default":     opt.Default,
		}
		if len(opt.Accepted) > 0 {
			row["accepted"] = opt.Accepted
		}
		if opt.Max > 0 {
			row["min"] = opt.Min
			row["max"] = opt.Max
		}
		if opt.Deprecated != "" {
			row["deprecated"] = opt.Deprecated
		}
		// The override, when this session carries one. It is reported separately
		// from the default because the two answer different questions: what this
		// session was told, and what it would do untold.
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
			Detail:    "section groups the options for display; prefix matches the start of a path.",
		})
	}

	return map[string]any{
		"type":     "option_list",
		"options":  out,
		"sections": sortedKeys(sections),
		"total":    len(out),
	}, nil
}

// sortedKeys returns a set's members in sorted order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	// The list is short and fixed; a sort keeps the output stable between calls
	// so a caller can diff two of them.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// mustResolve re-runs session resolution for its error, for a caller that has
// already established the session is missing.
func mustResolve(d *Daemon, name string) *verbError {
	_, verr := d.resolveVerbSession(name)
	if verr != nil {
		return verr
	}
	return newVerbError(ErrVerbInternal, "session "+name+" resolved on the second attempt")
}

// resolveOptionPath looks a key up in the registry, accepting the bare spelling
// of an appearance option as well as the full path.
//
// The runtime setter has always taken both ("border_style" and
// "appearance.border_style"), the CLI's completions offer both, and callers use
// both. Validation that only knew the long form would have made the short one
// an error for the first time, which is a break dressed up as a fix. It returns
// the option and the full path it resolved to, so everything downstream records
// one spelling.
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

// verbSetOption records a session option and applies it live where a client is
// attached.
//
// The validation is the change. The path is checked against the registry and
// the value against that option's type and accepted set, so a call that could
// never have an effect fails saying why instead of being recorded and reported
// as set. Deliberately still permissive in one direction: a path the registry
// does not know is refused, but the recorded value is kept for the paths it
// does, because the record is what get-option reads and what a client picks up
// when it attaches later.
func (d *Daemon) verbSetOption(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Key     string `json:"key"`
		Value   string `json:"value"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Key == "" {
		return nil, invalidParam("key", `key is required, e.g. "appearance.sidebar.enabled"`)
	}

	opt, path, known := resolveOptionPath(p.Key)
	if !known {
		return nil, hintedVerbError(ErrVerbOptionNotFound, "no such option "+echoName(p.Key), &VerbHint{
			Param:      "key",
			Verb:       "list-options",
			Command:    "tuios list-options",
			DidYouMean: closestMatch(p.Key, config.OptionPaths()),
			// Every path, not a sample: a caller that mistyped one can pick the
			// right one from the failure rather than making a second call to find
			// out what it should have said.
			Available: config.OptionPaths(),
			Detail:    "call list-options for each path with its type, default and accepted values.",
		})
	}
	// Validate by attempting the set against a throwaway config. Doing it this
	// way rather than duplicating the type rules here means the check and the
	// apply can never disagree about what a value means.
	probe := config.DefaultConfig()
	if err := config.SetOptionValue(probe, path, p.Value); err != nil {
		hint := &VerbHint{
			Param:    "value",
			Accepted: opt.Accepted,
			Detail:   "type " + opt.Type + ", default " + echoName(opt.Default) + ". " + opt.Description,
		}
		// A theme has no Accepted set to fall back on and its names are one
		// separator apart from each other, so the near miss is the whole of what
		// a caller needs: catppuccin-mocha for catppuccin_mocha was reported as
		// a set that worked, and is now reported with the name that would have.
		if opt.Theme {
			hint.Verb = "list-themes"
			hint.Command = "tuios list-themes --filter " + p.Value
			hint.DidYouMean = closestMatch(p.Value, theme.AvailableThemes())
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), hint)
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	// Record the option in daemon-owned state so get-option can read it back.
	sess.SetOption(path, p.Value)

	// When a TUI is attached, also route the change so the live renderer applies
	// it. A routing failure is not fatal: the option is still recorded, so
	// applied reflects only whether the live apply succeeded.
	applied := false
	var reason string
	if tui := d.findTUIClient(sess.ID); tui != nil {
		res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
			CommandType: "set_config",
			ConfigPath:  path,
			ConfigValue: p.Value,
		}, routedVerbTimeout)
		applied = err == nil && res != nil && res.Success
		switch {
		case applied:
		case err != nil:
			reason = "the attached client did not answer: " + err.Error()
		case res != nil && res.Message != "":
			reason = res.Message
		default:
			reason = "the attached client refused the change"
		}
	} else {
		// applied false used to mean both "nobody was attached" and "that key
		// means nothing", which a caller could not tell apart and so could not
		// act on. The key is now validated above, and this says which of the two
		// remaining cases it is.
		reason = "no client is attached, so nothing is drawing it yet; the value is recorded and applies when one attaches"
	}

	out := map[string]any{
		"type":    "option_set",
		"key":     path,
		"value":   p.Value,
		"applied": applied,
	}
	if reason != "" {
		out["reason"] = reason
	}
	if opt.Deprecated != "" {
		out["deprecated"] = opt.Deprecated
	}
	return out, nil
}

// verbGetOption reads an option, preferring what this session was told and
// falling back to what the option does untold.
//
// It used to read only the session's own overrides, so the ordinary question
// (what is the dockbar position) failed on every session that had never set it,
// which is most of them. A caller cannot act on "not set" when what it wanted
// was the value in effect.
func (d *Daemon) verbGetOption(_ *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}

	opt, path, known := resolveOptionPath(p.Key)
	if value, ok := sess.GetOption(path); ok {
		out := map[string]any{"type": "option", "key": path, "value": value, "source": "session"}
		if known {
			out["default"] = opt.Default
			out["option_type"] = opt.Type
		}
		return out, nil
	}
	if known {
		return map[string]any{
			"type":        "option",
			"key":         path,
			"value":       opt.Default,
			"source":      "default",
			"default":     opt.Default,
			"option_type": opt.Type,
		}, nil
	}

	// Unknown to the registry and never set here. Both halves of that are worth
	// saying: a caller with a typo and a caller reading a key from a newer build
	// need different remedies.
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
