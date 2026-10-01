package herdrcli

import (
	"slices"
	"strings"
)

// herdr's pane commands, from src/cli/pane.rs at v0.9.3.

func parsePane(sub string, args []string, getenv Env, _ string) (*Call, *UsageError) {
	switch sub {
	case "list":
		p := map[string]any{}
		err := walk(args, func(arg string, value valueFn) *UsageError {
			if arg != "--workspace" {
				return unknownOption(arg)
			}
			v, err := value(arg)
			p["workspace_id"] = v
			return err
		})
		if err != nil {
			return nil, err
		}
		return call("cli:pane:list", "pane.list", p), nil
	case "get":
		if len(args) != 1 {
			return nil, usage("usage: herdr pane get <pane_id>")
		}
		return call("cli:pane:get", "pane.get", map[string]any{"pane_id": args[0]}), nil
	case "current":
		pane, err := optionalPane(args, getenv)
		if err != nil {
			return nil, err
		}
		if pane == nil {
			pane = envPane(getenv)
		}
		p := map[string]any{}
		setOpt(p, "caller_pane_id", pane)
		return call("cli:pane:current", "pane.current", p), nil
	case "layout", "process-info", "edges":
		pane, err := optionalPane(args, getenv)
		if err != nil {
			return nil, err
		}
		p := map[string]any{}
		setOpt(p, "pane_id", pane)
		method := "pane." + strings.ReplaceAll(sub, "-", "_")
		return call("cli:pane:"+strings.ReplaceAll(sub, "-", "_"), method, p), nil
	case "neighbor":
		p, err := paneDirectionArgs(args, "usage: herdr pane neighbor --direction left|right|up|down [--pane ID|--current]", nil)
		if err != nil {
			return nil, err
		}
		return call("cli:pane:neighbor", "pane.neighbor", p), nil
	case "focus":
		p, err := paneDirectionArgs(args, "", nil)
		if err != nil {
			return nil, usage("usage: herdr pane focus --direction left|right|up|down [--pane ID|--current]")
		}
		return call("cli:pane:focus", "pane.focus_direction", p), nil
	case "resize":
		p, err := paneDirectionArgs(args, "usage: herdr pane resize --direction left|right|up|down [--amount FLOAT] [--pane ID|--current]", []string{"--amount"})
		if err != nil {
			return nil, err
		}
		return call("cli:pane:resize", "pane.resize", p), nil
	case "zoom":
		return paneZoom(args)
	case "rename":
		if len(args) < 2 {
			return nil, usage("usage: herdr pane rename <pane_id> <label>|--clear")
		}
		p := map[string]any{"pane_id": args[0], "label": nil}
		if len(args) != 2 || args[1] != "--clear" {
			p["label"] = strings.Join(args[1:], " ")
		}
		return call("cli:pane:rename", "pane.rename", p), nil
	case "read":
		return paneRead(args)
	case "input":
		return paneInput(args, getenv)
	case "split":
		return paneSplit(args, getenv)
	case "swap":
		return paneSwap(args)
	case "move":
		return paneMove(args)
	case "close":
		if len(args) != 1 {
			return nil, usage("usage: herdr pane close <pane_id>")
		}
		return call("cli:pane:close", "pane.close", map[string]any{"pane_id": args[0]}), nil
	case "send-text":
		if len(args) < 2 {
			return nil, usage("usage: herdr pane send-text <pane_id> <text>")
		}
		return okCall("cli:request", "pane.send_text", map[string]any{"pane_id": args[0], "text": strings.Join(args[1:], " ")}), nil
	case "send-keys":
		if len(args) < 2 {
			return nil, usage("usage: herdr pane send-keys <pane_id> <key> [key ...]")
		}
		return okCall("cli:request", "pane.send_keys", map[string]any{"pane_id": args[0], "keys": args[1:]}), nil
	case "run":
		if len(args) < 2 {
			return nil, usage("usage: herdr pane run <pane_id> <command>")
		}
		return okCall("cli:request", "pane.send_input", map[string]any{"pane_id": args[0], "text": strings.Join(args[1:], " "), "keys": []string{"Enter"}}), nil
	case "wait-output":
		return paneWaitOutput(args)
	case "report-agent", "report-agent-session":
		return paneReportAgent(sub, args)
	case "release-agent":
		return paneReleaseAgent(args)
	case "report-metadata":
		return paneReportMetadata(args)
	}
	return nil, &UsageError{Msg: groupHelp["pane"], Code: 2}
}

// optionalPane reads [--pane ID|--current], where --current names
// HERDR_PANE_ID.
func optionalPane(args []string, getenv Env) (any, *UsageError) {
	var pane any
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--pane":
			v, err := value(arg)
			pane = v
			return err
		case "--current":
			pane = envPane(getenv)
			return nil
		}
		return unknownOption(arg)
	})
	return pane, err
}

// paneDirectionArgs reads --direction D [--pane ID|--current], where
// --current leaves the pane to the server's focused one, and --amount when
// amount is allowed. A missing direction is the usage line.
func paneDirectionArgs(args []string, usageLine string, extra []string) (map[string]any, *UsageError) {
	p := map[string]any{}
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch {
		case arg == "--pane":
			v, err := value(arg)
			p["pane_id"] = v
			return err
		case arg == "--current":
			delete(p, "pane_id")
			return nil
		case arg == "--direction":
			v, err := value(arg)
			if err != nil {
				return err
			}
			d, err := paneDirection(v)
			p["direction"] = d
			return err
		case arg == "--amount" && slices.Contains(extra, arg):
			v, err := value(arg)
			if err != nil {
				return err
			}
			f, err := parseFloat("amount", v)
			p["amount"] = f
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["direction"]; !ok {
		return nil, usage(usageLine)
	}
	return p, nil
}

func paneZoom(args []string) (*Call, *UsageError) {
	p := map[string]any{"mode": "toggle"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		p["pane_id"] = args[0]
		args = args[1:]
	}
	seen := false
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--pane":
			v, err := value(arg)
			p["pane_id"] = v
			return err
		case "--current":
			delete(p, "pane_id")
			return nil
		case "--toggle", "--on", "--off":
			if seen {
				return usage("provide only one of --toggle, --on, or --off")
			}
			seen = true
			p["mode"] = strings.TrimPrefix(arg, "--")
			return nil
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	return call("cli:pane:zoom", "pane.zoom", p), nil
}

func paneRead(args []string) (*Call, *UsageError) {
	const usageLine = "usage: herdr pane read <pane_id> [--source visible|recent|recent-unwrapped|detection] [--lines N] [--format text|ansi] [--ansi] [--raw]"
	p := map[string]any{"source": "recent", "format": "text", "strip_ansi": true, "intent": "interactive"}
	err := walk(expandEquals(args, "--source", "--lines", "--format"), func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--source":
			v, err := value(arg)
			if err != nil {
				return err
			}
			s, err := readSource(v)
			p["source"] = s
			return err
		case "--lines":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--lines", v, 32)
			p["lines"] = n
			return err
		case "--format":
			v, err := value(arg)
			if err != nil {
				return err
			}
			f, err := readFormat(v)
			p["format"] = f
			return err
		case "--ansi":
			p["format"] = "ansi"
			return nil
		case "--raw":
			p["format"], p["strip_ansi"] = "ansi", false
			return nil
		}
		if strings.HasPrefix(arg, "-") {
			return unknownOption(arg)
		}
		if _, ok := p["pane_id"]; ok {
			return usage("unexpected argument: " + arg)
		}
		p["pane_id"] = arg
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["pane_id"]; !ok {
		return nil, usage(usageLine)
	}
	return &Call{ID: "cli:pane:read", Method: "pane.read", Params: p, Output: OutRead}, nil
}

func paneInput(args []string, getenv Env) (*Call, *UsageError) {
	const usageLine = "usage: herdr pane input [<pane_id>|--pane ID|--current] --right-click herdr|pane"
	p := map[string]any{}
	err := walk(expandEquals(args, "--pane", "--right-click"), func(arg string, value valueFn) *UsageError {
		_, have := p["pane_id"]
		switch arg {
		case "--pane":
			if have {
				return usage("provide only one pane selector")
			}
			v, err := value(arg)
			p["pane_id"] = v
			return err
		case "--current":
			if have {
				return usage("provide only one pane selector")
			}
			pane := envPane(getenv)
			if pane == nil {
				return usage("--current requires HERDR_PANE_ID")
			}
			p["pane_id"] = pane
			return nil
		case "--right-click":
			v, err := value(arg)
			if err != nil {
				return err
			}
			t, err := rightClick(v)
			p["right_click"] = t
			return err
		}
		if strings.HasPrefix(arg, "-") {
			return unknownOption(arg)
		}
		if have {
			return usage("unexpected argument: " + arg)
		}
		p["pane_id"] = arg
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p["pane_id"] == nil || p["right_click"] == nil {
		return nil, usage(usageLine)
	}
	return call("cli:pane:input:set", "pane.input.set", p), nil
}

func rightClick(v string) (string, *UsageError) {
	if v == "herdr" || v == "pane" {
		return v, nil
	}
	return "", usage("invalid right-click target: " + v)
}

func paneSplit(args []string, getenv Env) (*Call, *UsageError) {
	p := map[string]any{"focus": false, "right_click": "herdr", "env": map[string]string{}}
	setOpt(p, "target_pane_id", envPane(getenv))
	args = expandEquals(args, "--right-click")
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		p["target_pane_id"] = args[0]
		args = args[1:]
	}
	env := p["env"].(map[string]string)
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--pane":
			v, err := value(arg)
			p["target_pane_id"] = v
			return err
		case "--current":
			pane := envPane(getenv)
			if pane == nil {
				return usage("--current requires HERDR_PANE_ID")
			}
			p["target_pane_id"] = pane
			return nil
		case "--direction":
			v, err := value(arg)
			if err != nil {
				return err
			}
			d, err := splitDirection(v)
			p["direction"] = d
			return err
		case "--ratio":
			v, err := value(arg)
			if err != nil {
				return err
			}
			f, err := parseFloat("ratio", v)
			p["ratio"] = f
			return err
		case "--cwd":
			v, err := value(arg)
			p["cwd"] = v
			return err
		case "--right-click":
			v, err := value(arg)
			if err != nil {
				return err
			}
			t, err := rightClick(v)
			p["right_click"] = t
			return err
		case "--focus", "--no-focus":
			p["focus"] = arg == "--focus"
			return nil
		case "--env":
			v, err := value(arg)
			if err != nil {
				return err
			}
			k, val, err := envAssignment(v)
			env[k] = val
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["direction"]; !ok {
		return nil, usage("usage: herdr pane split [<pane_id>|--pane ID|--current] --direction right|down [--ratio FLOAT] [--cwd PATH] [--env KEY=VALUE] [--right-click herdr|pane] [--focus] [--no-focus]")
	}
	return call("cli:pane:split", "pane.split", p), nil
}

func paneSwap(args []string) (*Call, *UsageError) {
	p := map[string]any{}
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--pane":
			v, err := value(arg)
			p["pane_id"] = v
			return err
		case "--source-pane":
			v, err := value(arg)
			p["source_pane_id"] = v
			return err
		case "--target-pane":
			v, err := value(arg)
			p["target_pane_id"] = v
			return err
		case "--current":
			delete(p, "pane_id")
			return nil
		case "--direction":
			v, err := value(arg)
			if err != nil {
				return err
			}
			d, err := paneDirection(v)
			p["direction"] = d
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	_, directional := p["direction"]
	_, src := p["source_pane_id"]
	_, dst := p["target_pane_id"]
	_, pane := p["pane_id"]
	switch {
	case directional && !src && !dst:
		return call("cli:pane:swap", "pane.swap", p), nil
	case !directional && src && dst && !pane:
		return call("cli:pane:swap", "pane.swap", p), nil
	}
	return nil, usage("usage: herdr pane swap --direction left|right|up|down [--pane ID|--current]\n       herdr pane swap --source-pane ID --target-pane ID")
}

const paneMoveUsage = "usage: herdr pane move <pane_id> --tab <tab_id> --split right|down [--target-pane ID] [--ratio FLOAT] [--focus|--no-focus]\n       herdr pane move <pane_id> --new-tab [--workspace ID] [--label TEXT] [--focus|--no-focus]\n       herdr pane move <pane_id> --new-workspace [--label TEXT] [--tab-label TEXT] [--focus|--no-focus]"

func paneMove(args []string) (*Call, *UsageError) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usage(paneMoveUsage)
	}
	f := map[string]any{}
	focus := true
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--tab", "--workspace", "--target-pane", "--label", "--tab-label":
			v, err := value(arg)
			f[arg] = v
			return err
		case "--split":
			v, err := value(arg)
			if err != nil {
				return err
			}
			if v != "right" && v != "down" {
				return usage("invalid split direction: " + v + " (expected right or down)")
			}
			f[arg] = v
			return nil
		case "--ratio":
			v, err := value(arg)
			if err != nil {
				return err
			}
			r, err := parseFloat("ratio", v)
			f[arg] = r
			return err
		case "--new-tab", "--new-workspace":
			f[arg] = true
			return nil
		case "--focus", "--no-focus":
			focus = arg == "--focus"
			return nil
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	has := func(k string) bool { _, ok := f[k]; return ok }
	n := 0
	for _, k := range []string{"--tab", "--new-tab", "--new-workspace"} {
		if has(k) {
			n++
		}
	}
	if n != 1 {
		return nil, usage(paneMoveUsage)
	}
	dest := map[string]any{}
	switch {
	case has("--tab"):
		if !has("--split") || has("--workspace") || has("--label") || has("--tab-label") {
			return nil, usage(paneMoveUsage)
		}
		dest["type"], dest["tab_id"], dest["split"] = "tab", f["--tab"], f["--split"]
		setOpt(dest, "target_pane_id", f["--target-pane"])
		setOpt(dest, "ratio", f["--ratio"])
	case has("--new-tab"):
		if has("--split") || has("--target-pane") || has("--tab-label") {
			return nil, usage(paneMoveUsage)
		}
		dest["type"] = "new_tab"
		setOpt(dest, "workspace_id", f["--workspace"])
		setOpt(dest, "label", f["--label"])
	default:
		if has("--split") || has("--target-pane") || has("--workspace") {
			return nil, usage(paneMoveUsage)
		}
		dest["type"] = "new_workspace"
		setOpt(dest, "label", f["--label"])
		setOpt(dest, "tab_label", f["--tab-label"])
	}
	return call("cli:pane:move", "pane.move", map[string]any{"pane_id": args[0], "destination": dest, "focus": focus}), nil
}

func paneWaitOutput(args []string) (*Call, *UsageError) {
	const usageLine = "usage: herdr pane wait-output <pane_id> (--match TEXT | --regex PATTERN) [--source visible|recent|recent-unwrapped] [--lines N] [--timeout MS] [--raw]"
	p := map[string]any{"source": "recent", "strip_ansi": true}
	err := walk(expandEquals(args, "--match", "--regex", "--source", "--lines", "--timeout"), func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--match", "--regex":
			v, err := value(arg)
			if err != nil {
				return err
			}
			if _, ok := p["match"]; ok {
				return usage("--match and --regex are mutually exclusive")
			}
			kind := "substring"
			if arg == "--regex" {
				kind = "regex"
			}
			p["match"] = map[string]any{"type": kind, "value": v}
			return nil
		case "--source":
			v, err := value(arg)
			if err != nil {
				return err
			}
			s, err := readSource(v)
			p["source"] = s
			return err
		case "--lines":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--lines", v, 32)
			p["lines"] = n
			return err
		case "--timeout":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--timeout", v, 64)
			p["timeout_ms"] = n
			return err
		case "--raw":
			p["strip_ansi"] = false
			return nil
		}
		if strings.HasPrefix(arg, "-") {
			return unknownOption(arg)
		}
		if _, ok := p["pane_id"]; ok {
			return usage("unexpected argument: " + arg)
		}
		p["pane_id"] = arg
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["pane_id"]; !ok {
		return nil, usage(usageLine)
	}
	if _, ok := p["match"]; !ok {
		return nil, usage("missing required --match or --regex")
	}
	c := call("cli:pane:wait-output", "pane.wait_for_output", p)
	c.Wait = true
	return c, nil
}

// paneReportAgent is pane report-agent and pane report-agent-session. A
// resume command after "--" goes as resume_argv.
func paneReportAgent(sub string, args []string) (*Call, *UsageError) {
	withState := sub == "report-agent"
	usageLine := "usage: herdr pane report-agent <pane_id> --source ID --agent LABEL --state idle|working|blocked|unknown [--message TEXT] [--seq N] [--agent-session-id ID] [--agent-session-path PATH] [-- <resume-command...>]"
	values := []string{"--source", "--agent", "--seq", "--agent-session-id", "--agent-session-path"}
	if withState {
		values = append(values, "--state", "--message")
	} else {
		usageLine = "usage: herdr pane report-agent-session <pane_id> --source ID --agent LABEL [--seq N] [--agent-session-id ID] [--agent-session-path PATH] [--session-start-source SOURCE] [-- <resume-command...>]"
		values = append(values, "--session-start-source")
	}
	args, resume, hasResume := splitDashDash(args)
	p := map[string]any{}
	if hasResume {
		p["resume_argv"] = slices.Clone(resume)
	}
	err := walk(expandEquals(args, values...), func(arg string, value valueFn) *UsageError {
		if slices.Contains(values, arg) {
			v, err := value(arg)
			if err != nil {
				return err
			}
			key := flagKey(arg)
			switch arg {
			case "--seq":
				n, err := parseUint("--seq", v, 64)
				p[key] = n
				return err
			case "--state":
				s, err := paneAgentState(v)
				p[key] = s
				return err
			}
			p[key] = v
			return nil
		}
		if strings.HasPrefix(arg, "-") {
			return unknownOption(arg)
		}
		if _, ok := p["pane_id"]; ok {
			return usage("unexpected argument: " + arg)
		}
		p["pane_id"] = arg
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["pane_id"]; !ok {
		return nil, usage(usageLine)
	}
	if err := requireSource(p); err != nil {
		return nil, err
	}
	if _, ok := p["agent"]; !ok {
		return nil, usage("missing required --agent")
	}
	if _, ok := p["state"]; withState && !ok {
		return nil, usage("missing required --state")
	}
	c := okCall("cli:request", "pane."+strings.ReplaceAll(sub, "-", "_"), p)
	c.Report = true
	return c, nil
}

// requireSource checks --source, trimmed, as herdr does.
func requireSource(p map[string]any) *UsageError {
	s, _ := p["source"].(string)
	if s = strings.TrimSpace(s); s == "" {
		return usage("missing required --source")
	}
	p["source"] = s
	return nil
}

func paneReleaseAgent(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr pane release-agent <pane_id> --source ID --agent LABEL [--seq N]")
	}
	p := map[string]any{"pane_id": args[0]}
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--source", "--agent":
			v, err := value(arg)
			p[strings.TrimPrefix(arg, "--")] = v
			return err
		case "--seq":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--seq", v, 64)
			p["seq"] = n
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	if err := requireSource(p); err != nil {
		return nil, err
	}
	if _, ok := p["agent"]; !ok {
		return nil, usage("missing required --agent")
	}
	c := okCall("cli:request", "pane.release_agent", p)
	c.Report = true
	return c, nil
}

func paneReportMetadata(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr pane report-metadata <pane_id> --source ID [--agent LABEL] [--applies-to-source ID] [--title TEXT|--clear-title] [--display-agent TEXT|--clear-display-agent] [--state-label STATUS=TEXT] [--clear-state-labels] [--token NAME=VALUE] [--clear-token NAME] [--seq N] [--ttl-ms N]")
	}
	p := map[string]any{"pane_id": args[0], "clear_title": false, "clear_display_agent": false, "clear_state_labels": false}
	labels := map[string]string{}
	tokens := map[string]any{}
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--source", "--agent", "--applies-to-source", "--title", "--display-agent":
			v, err := value(arg)
			p[flagKey(arg)] = v
			return err
		case "--clear-title", "--clear-display-agent", "--clear-state-labels":
			p[flagKey(arg)] = true
			return nil
		case "--state-label":
			v, err := value(arg)
			if err != nil {
				return err
			}
			status, label, ok := strings.Cut(v, "=")
			if !ok {
				return usage("expected --state-label STATUS=TEXT")
			}
			status = strings.ToLower(strings.TrimSpace(status))
			if _, err := agentStatus(status); err != nil {
				return usage("unknown state label: " + status)
			}
			labels[status] = label
			return nil
		case "--token":
			v, err := value(arg)
			if err != nil {
				return err
			}
			k, val, err := tokenAssignment(v)
			tokens[k] = val
			return err
		case "--clear-token":
			v, err := value(arg)
			tokens[v] = nil
			return err
		case "--seq", "--ttl-ms":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint(arg, v, 64)
			p[flagKey(arg)] = n
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	if err := requireSource(p); err != nil {
		return nil, err
	}
	if s, ok := p["applies_to_source"].(string); ok && strings.TrimSpace(s) == "" {
		return nil, usage("missing value for --applies-to-source")
	}
	set := func(k string) bool { _, ok := p[k]; return ok }
	if set("title") && p["clear_title"] == true || set("display_agent") && p["clear_display_agent"] == true || len(labels) > 0 && p["clear_state_labels"] == true {
		return nil, usage("cannot set and clear the same metadata field")
	}
	if !set("title") && !set("display_agent") && len(labels) == 0 && len(tokens) == 0 &&
		p["clear_title"] == false && p["clear_display_agent"] == false && p["clear_state_labels"] == false {
		return nil, usage("missing metadata field to set or clear")
	}
	p["state_labels"], p["tokens"] = labels, tokens
	c := okCall("cli:request", "pane.report_metadata", p)
	c.Report = true
	return c, nil
}
