package herdrcli

import (
	"slices"
	"strings"
)

// herdr's tab, workspace, agent, worktree, notification, api and server
// commands, from src/cli at v0.9.3.

// valueFlags reads flags that each take one value into p under the flag's
// name less "--" with "-" made "_", and --focus and --no-focus into focus.
// --env KEY=VALUE goes into env when env is not nil.
func valueFlags(args []string, allowed []string, p map[string]any, env map[string]string) *UsageError {
	return walk(args, func(arg string, value valueFn) *UsageError {
		switch {
		case slices.Contains(allowed, arg):
			v, err := value(arg)
			p[flagKey(arg)] = v
			return err
		case arg == "--focus" || arg == "--no-focus":
			p["focus"] = arg == "--focus"
			return nil
		case arg == "--env" && env != nil:
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
}

// renameKey maps a flag's name to the param herdr sends for it.
func renameKey(p map[string]any, from, to string) {
	if v, ok := p[from]; ok {
		delete(p, from)
		p[to] = v
	}
}

func oneID(args []string, usageLine string) (string, *UsageError) {
	if len(args) != 1 {
		return "", usage(usageLine)
	}
	return args[0], nil
}

func parseTab(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
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
		return call("cli:tab:list", "tab.list", p), nil
	case "create":
		env := map[string]string{}
		p := map[string]any{"focus": false}
		if err := valueFlags(args, []string{"--workspace", "--cwd", "--label"}, p, env); err != nil {
			return nil, err
		}
		renameKey(p, "workspace", "workspace_id")
		p["env"] = env
		return call("cli:tab:create", "tab.create", p), nil
	case "get", "focus", "close":
		id, err := oneID(args, "usage: herdr tab "+sub+" <tab_id>")
		if err != nil {
			return nil, err
		}
		return call("cli:tab:"+sub, "tab."+sub, map[string]any{"tab_id": id}), nil
	case "rename":
		if len(args) < 2 {
			return nil, usage("usage: herdr tab rename <tab_id> <label>")
		}
		return call("cli:tab:rename", "tab.rename", map[string]any{"tab_id": args[0], "label": strings.Join(args[1:], " ")}), nil
	}
	return nil, &UsageError{Msg: groupHelp["tab"], Code: 2}
}

func parseWorkspace(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
	switch sub {
	case "list":
		if len(args) != 0 {
			return nil, usage("usage: herdr workspace list")
		}
		return call("cli:workspace:list", "workspace.list", nil), nil
	case "create":
		env := map[string]string{}
		p := map[string]any{"focus": false}
		if err := valueFlags(args, []string{"--cwd", "--label"}, p, env); err != nil {
			return nil, err
		}
		p["env"] = env
		return call("cli:workspace:create", "workspace.create", p), nil
	case "get", "focus":
		id, err := oneID(args, "usage: herdr workspace "+sub+" <workspace_id>")
		if err != nil {
			return nil, err
		}
		return call("cli:workspace:"+sub, "workspace."+sub, map[string]any{"workspace_id": id}), nil
	case "rename":
		if len(args) < 2 {
			return nil, usage("usage: herdr workspace rename <workspace_id> <label>")
		}
		return call("cli:workspace:rename", "workspace.rename", map[string]any{"workspace_id": args[0], "label": strings.Join(args[1:], " ")}), nil
	case "close":
		switch {
		case len(args) == 1:
			return call("cli:workspace:close", "workspace.close", map[string]any{"workspace_id": args[0], "close_group": false}), nil
		case len(args) == 2 && args[1] == "--group":
			return call("cli:workspace:close", "workspace.close", map[string]any{"workspace_id": args[0], "close_group": true}), nil
		}
		return nil, usage("usage: herdr workspace close <workspace_id> [--group]")
	case "report-metadata":
		return workspaceReportMetadata(args)
	}
	return nil, &UsageError{Msg: groupHelp["workspace"], Code: 2}
}

func workspaceReportMetadata(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr workspace report-metadata <workspace_id> --source ID [--token NAME=VALUE] [--clear-token NAME] [--seq N] [--ttl-ms N]")
	}
	p := map[string]any{"workspace_id": args[0]}
	tokens := map[string]any{}
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--source":
			v, err := value(arg)
			p["source"] = v
			return err
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
	if s, _ := p["source"].(string); strings.TrimSpace(s) == "" {
		return nil, usage("missing required --source")
	}
	if len(tokens) == 0 {
		return nil, usage("missing token to set or clear")
	}
	p["tokens"] = tokens
	return okCall("cli:request", "workspace.report_metadata", p), nil
}

func parseAgent(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
	switch sub {
	case "list":
		if len(args) != 0 {
			return nil, usage("usage: herdr agent list")
		}
		return call("cli:agent:list", "agent.list", nil), nil
	case "get", "focus":
		id, err := oneID(args, "usage: herdr agent "+sub+" <target>")
		if err != nil {
			return nil, err
		}
		return call("cli:agent:"+sub, "agent."+sub, map[string]any{"target": id}), nil
	case "read":
		return agentRead(args)
	case "send-keys":
		if len(args) < 2 {
			return nil, usage("usage: herdr agent send-keys <target> <key> [key ...]")
		}
		return call("cli:agent:send-keys", "agent.send_keys", map[string]any{"target": args[0], "keys": args[1:]}), nil
	case "prompt":
		return agentPrompt(args)
	case "rename":
		if len(args) != 2 {
			return nil, usage("usage: herdr agent rename <target> <name>|--clear")
		}
		p := map[string]any{"target": args[0], "name": nil}
		if args[1] != "--clear" {
			p["name"] = args[1]
		}
		return call("cli:agent:rename", "agent.rename", p), nil
	case "wait":
		return agentWait(args)
	case "start":
		return agentStart(args)
	case "attach":
		return local("cli:agent:attach", "herdr agent attach attaches herdr's client protocol, which tuios does not serve. Run tuios attach, or focus the agent with herdr agent focus"), nil
	case "explain":
		return agentExplain(args)
	}
	return nil, &UsageError{Msg: groupHelp["agent"], Code: 2}
}

func agentRead(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr agent read <target> [--source visible|recent|recent-unwrapped] [--lines N] [--format text|ansi] [--ansi]")
	}
	p := map[string]any{"target": args[0], "source": "recent", "format": "text", "strip_ansi": true}
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
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
			p["format"], p["strip_ansi"] = f, f != "ansi"
			return err
		case "--ansi":
			p["format"], p["strip_ansi"] = "ansi", false
			return nil
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	return &Call{ID: "cli:agent:read", Method: "agent.read", Params: p, Output: OutRead}, nil
}

// untilTimeout reads [--until STATUS]... [--timeout MS] into p, and --wait
// when wait is not nil.
func untilTimeout(args []string, p map[string]any, wait *bool) *UsageError {
	var until []string
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--until":
			v, err := value(arg)
			if err != nil {
				return usage("--until requires at least one status")
			}
			s, err := agentStatus(v)
			until = append(until, s)
			return err
		case "--timeout":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--timeout", v, 64)
			p["timeout_ms"] = n
			return err
		case "--wait":
			if wait != nil {
				*wait = true
				return nil
			}
		}
		return unknownOption(arg)
	})
	if until == nil {
		until = []string{}
	}
	p["until"] = until
	return err
}

func agentWait(args []string) (*Call, *UsageError) {
	const usageLine = "usage: herdr agent wait <target> [--until STATUS]... [--timeout MS]"
	if len(args) == 0 {
		return nil, usage(usageLine)
	}
	for _, a := range args[1:] {
		if a == "help" {
			return nil, &UsageError{Msg: usageLine, Code: 0}
		}
	}
	p := map[string]any{"target": args[0]}
	if err := untilTimeout(args[1:], p, nil); err != nil {
		return nil, err
	}
	c := call("cli:agent:wait", "agent.wait", p)
	c.Wait = true
	return c, nil
}

func agentPrompt(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr agent prompt <target> <text> [--wait] [--until STATUS]... [--timeout MS]")
	}
	if len(args) == 1 {
		return nil, usage("agent prompt requires text")
	}
	wait := false
	opts := map[string]any{}
	if err := untilTimeout(args[2:], opts, &wait); err != nil {
		return nil, err
	}
	if len(opts["until"].([]string)) > 0 && !wait {
		return nil, usage("--until requires --wait")
	}
	if _, ok := opts["timeout_ms"]; ok && !wait {
		return nil, usage("--timeout requires --wait")
	}
	p := map[string]any{"target": args[0], "text": args[1]}
	if wait {
		p["wait"] = opts
	}
	c := call("cli:agent:prompt", "agent.prompt", p)
	c.Wait = wait
	return c, nil
}

func agentStart(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, usage("usage: herdr agent start <name> --kind KIND --pane ID [--timeout MS] [-- <agent-args...>]")
	}
	flags, rest, _ := splitDashDash(args[1:])
	p := map[string]any{"name": args[0]}
	err := walk(flags, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--kind":
			v, err := value(arg)
			p["kind"] = v
			return err
		case "--pane":
			v, err := value(arg)
			p["pane_id"] = v
			return err
		case "--timeout":
			v, err := value(arg)
			if err != nil {
				return err
			}
			n, err := parseUint("--timeout", v, 64)
			p["timeout_ms"] = n
			return err
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	if _, ok := p["kind"]; !ok {
		return nil, usage("missing required --kind")
	}
	if _, ok := p["pane_id"]; !ok {
		return nil, usage("missing required --pane")
	}
	if kind, _ := p["kind"].(string); !herdrAgentKind(kind) {
		return nil, usage("unsupported interactive agent kind: " + kind)
	}
	if len(rest) > 0 {
		p["args"] = slices.Clone(rest)
	}
	c := call("cli:agent:start", "agent.start", p)
	c.Output, c.Wait = OutAgentStart, true
	return c, nil
}

func agentExplain(args []string) (*Call, *UsageError) {
	target := ""
	file := false
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--file", "--agent", "--format":
			v, err := value(arg)
			if err != nil {
				return err
			}
			if arg == "--format" && v != "json" && v != "text" {
				return usage("invalid --format: " + v + " (expected text or json)")
			}
			file = file || arg == "--file"
			return nil
		case "--json", "--verbose", "-v":
			return nil
		case "help", "--help", "-h":
			return &UsageError{Msg: "usage: herdr agent explain <target> [--json|--verbose]\nusage: herdr agent explain --file PATH --agent LABEL [--json|--verbose]", Code: 0}
		}
		if strings.HasPrefix(arg, "-") {
			return unknownOption(arg)
		}
		if target != "" {
			return usage("usage: herdr agent explain <target> [--json]")
		}
		target = arg
		return nil
	})
	if err != nil {
		return nil, err
	}
	if file {
		return local("cli:agent:explain", "herdr agent explain --file reads herdr's agent manifests. Use tuios explain-agent-detect"), nil
	}
	if target == "" {
		return nil, usage("usage: herdr agent explain <target> [--json]\nusage: herdr agent explain --file PATH --agent LABEL [--json]")
	}
	return call("cli:agent:explain", "agent.explain", map[string]any{"target": target}), nil
}

func parseWorktree(sub string, args []string, getenv Env, cwd string) (*Call, *UsageError) {
	var allowed []string
	usageLine := ""
	switch sub {
	case "list":
		allowed = []string{"--workspace", "--cwd"}
		usageLine = "usage: herdr worktree list [--workspace ID | --cwd PATH] [--trust-repository]"
	case "create":
		allowed = []string{"--workspace", "--cwd", "--branch", "--base", "--path", "--label"}
		usageLine = "usage: herdr worktree create [--workspace ID | --cwd PATH] [--branch NAME] [--base REF] [--path PATH] [--label TEXT] [--focus] [--no-focus] [--trust-repository]"
	case "open":
		allowed = []string{"--workspace", "--cwd", "--path", "--branch", "--label"}
		usageLine = "usage: herdr worktree open [--workspace ID | --cwd PATH] (--path PATH | --branch NAME) [--label TEXT] [--focus] [--no-focus] [--trust-repository]"
	case "remove":
		allowed = []string{"--workspace"}
		usageLine = "usage: herdr worktree remove --workspace ID [--force] [--trust-repository]"
	default:
		return nil, &UsageError{Msg: groupHelp["worktree"], Code: 2}
	}
	p := map[string]any{"trust_repository": false}
	if sub == "create" || sub == "open" {
		p["focus"] = false
	}
	err := walk(args, func(arg string, value valueFn) *UsageError {
		switch {
		case slices.Contains(allowed, arg):
			v, err := value(arg)
			if err != nil {
				return err
			}
			if arg == "--cwd" || arg == "--path" {
				v = absPath(v, getenv, cwd)
			}
			p[strings.TrimPrefix(arg, "--")] = v
			return nil
		case arg == "--trust-repository":
			p["trust_repository"] = true
			return nil
		case arg == "--json":
			return nil
		case (arg == "--focus" || arg == "--no-focus") && (sub == "create" || sub == "open"):
			p["focus"] = arg == "--focus"
			return nil
		case arg == "--force" && sub == "remove":
			p["force"] = true
			return nil
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	renameKey(p, "workspace", "workspace_id")
	_, ws := p["workspace_id"]
	_, dir := p["cwd"]
	if ws && dir {
		return nil, usage(usageLine)
	}
	switch sub {
	case "open":
		_, path := p["path"]
		_, branch := p["branch"]
		if path == branch {
			return nil, usage(usageLine)
		}
	case "remove":
		if !ws {
			return nil, usage(usageLine)
		}
		if _, ok := p["force"]; !ok {
			p["force"] = false
		}
	}
	return call("cli:worktree:"+sub, "worktree."+sub, p), nil
}

func parseNotification(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
	const usageLine = "usage: herdr notification show <title> [--body TEXT] [--position top-left|top-right|bottom-left|bottom-right] [--sound none|done|request]"
	if sub != "show" {
		return nil, &UsageError{Msg: groupHelp["notification"], Code: 2}
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return nil, usage(usageLine)
	}
	p := map[string]any{"title": args[0], "sound": "none"}
	err := walk(args[1:], func(arg string, value valueFn) *UsageError {
		switch arg {
		case "--body":
			v, err := value(arg)
			p["body"] = v
			return err
		case "--position":
			v, err := value(arg)
			if err != nil {
				return err
			}
			switch v {
			case "top-left", "top-right", "bottom-left", "bottom-right":
				p["position"] = v
				return nil
			}
			return usage("invalid position: " + v + " (expected top-left, top-right, bottom-left, or bottom-right)")
		case "--sound":
			v, err := value(arg)
			if err != nil {
				return err
			}
			switch v {
			case "none", "done", "request":
				p["sound"] = v
				return nil
			}
			return usage("invalid sound: " + v + " (expected none, done, or request)")
		}
		return unknownOption(arg)
	})
	if err != nil {
		return nil, err
	}
	return call("cli:notification:show", "notification.show", p), nil
}

func parseAPI(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
	switch sub {
	case "snapshot":
		if len(args) != 0 {
			return nil, usage("usage: herdr api snapshot")
		}
		return call("cli:api:snapshot", "session.snapshot", nil), nil
	case "schema":
		return local("cli:api:schema", "herdr api schema prints herdr's own schema file, which tuios does not ship. tuios follows herdr "+Version+": see herdr's docs/next/api/herdr-api.schema.json at that tag"), nil
	}
	return nil, &UsageError{Msg: groupHelp["api"], Code: 2}
}

func parseServer(sub string, args []string, _ Env, _ string) (*Call, *UsageError) {
	switch sub {
	case "reload-config", "reload-agent-manifests":
		if len(args) != 0 {
			return nil, usage("usage: herdr server " + sub)
		}
		return call("cli:server:"+sub, "server."+strings.ReplaceAll(sub, "-", "_"), nil), nil
	case "agent-manifests":
		return call("cli:server:agent-manifests", "server.agent_manifests", nil), nil
	case "stop", "live-handoff", "update-agent-manifests":
		return local("cli:server:"+sub, "herdr server "+sub+" acts on herdr's own server. tuios's daemon is not stopped or changed this way. Use tuios kill-server"), nil
	}
	return nil, &UsageError{Msg: groupHelp["server"], Code: 2}
}

// herdrAgentKinds are the agent names herdr 0.9.3 starts (lookup_agent in
// src/detect/mod.rs). herdr refuses any other kind before it sends anything.
var herdrAgentKinds = []string{
	"pi", "claude", "claude-code", "codex", "gemini", "cursor", "cursor-agent",
	"devin", "devin-cli", "devin cli", "agy", "antigravity", "antigravity-cli",
	"cline", ".cline", "omp", "mastracode", "mastra-code", "mastra code",
	"opencode", "opencode2", "open-code", "copilot", "github-copilot", "ghcs",
	"kimi", "kimi-code", "kimi code", "kiro", "kiro-cli", "droid", "amp",
	"amp-local", "grok", "grok-build", "hermes", "hermes-agent", "kilo",
	"kilo-code", "kilo code", "qodercli", "qoderclicn", "qoder", "qodercn",
	"qwen", "qwen-code", "qwen code", "letta", "letta-code", "letta code",
	"maki", "muse", "muse-code", "muse-cli",
}

// herdrAgentKind reports whether herdr starts an agent of this kind: a name
// above, in any case, with any directory before it.
func herdrAgentKind(kind string) bool {
	name := strings.ToLower(strings.TrimSpace(kind))
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	if rest, ok := strings.CutPrefix(name, "muse-bin-"); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
		return true
	}
	return slices.Contains(herdrAgentKinds, name)
}
