package herdrcli

// herdr's plugin commands, from src/cli/plugin.rs at v0.9.3. Each sends the
// plugin.* method herdr's sends, which tuios's plugin host answers (see
// internal/session/herdr_plugins.go). enable, disable, link and unlink are
// sent too: the daemon refuses them from a pane, so a plugin or an agent
// that runs one fails cleanly. install and uninstall clone from the
// network, which tuios does not do.

func parsePlugin(sub string, args []string, getenv Env, cwd string) (*Call, *UsageError) {
	switch sub {
	case "list":
		p := map[string]any{}
		if err := walk(args, func(arg string, value valueFn) *UsageError {
			switch arg {
			case "--json":
				return nil
			case "--plugin":
				v, err := value(arg)
				p["plugin_id"] = v
				return err
			}
			return unknownOption(arg)
		}); err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.list", p), nil
	case "config-dir":
		id, err := oneID(args, "usage: herdr plugin config-dir <plugin_id>")
		if err != nil {
			return nil, err
		}
		return &Call{ID: "cli:plugin", Output: OutPluginConfigDir, Text: id}, nil
	case "enable", "disable":
		id, err := oneID(args, "usage: herdr plugin "+sub+" <plugin_id>")
		if err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin."+sub, map[string]any{"plugin_id": id}), nil
	case "unlink":
		id, err := oneID(args, "usage: herdr plugin unlink <plugin_id>")
		if err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.unlink", map[string]any{"plugin_id": id}), nil
	case "link":
		if len(args) == 0 {
			return nil, usage("usage: herdr plugin link <path> [--disabled]")
		}
		p := map[string]any{"path": absPath(args[0], getenv, cwd), "enabled": true}
		for _, a := range args[1:] {
			switch a {
			case "--disabled":
				p["enabled"] = false
			case "--enabled":
				p["enabled"] = true
			default:
				return nil, unknownOption(a)
			}
		}
		return call("cli:plugin", "plugin.link", p), nil
	case "install", "uninstall":
		return local("cli:plugin", "herdr plugin "+sub+" downloads from the network, which tuios does not do. Clone the plugin and run tuios plugins link DIR from a terminal outside tuios"), nil
	case "action":
		return parsePluginAction(args)
	case "log", "logs":
		p := map[string]any{}
		i := 0
		if len(args) > 0 && args[0] == "list" {
			i = 1
		}
		if err := walk(args[i:], func(arg string, value valueFn) *UsageError {
			switch arg {
			case "--plugin":
				v, err := value(arg)
				p["plugin_id"] = v
				return err
			case "--limit":
				v, err := value(arg)
				if err != nil {
					return err
				}
				n, perr := parseUint(arg, v, 64)
				if perr != nil {
					return usage("invalid --limit value: " + v)
				}
				p["limit"] = n
				return nil
			}
			return unknownOption(arg)
		}); err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.log.list", p), nil
	case "pane":
		return parsePluginPane(args, getenv, cwd)
	}
	return nil, &UsageError{Msg: groupHelp["plugin"], Code: 2}
}

func parsePluginAction(args []string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, &UsageError{Msg: pluginActionHelp, Code: 2}
	}
	switch args[0] {
	case "list":
		p := map[string]any{}
		if err := walk(args[1:], func(arg string, value valueFn) *UsageError {
			if arg != "--plugin" {
				return unknownOption(arg)
			}
			v, err := value(arg)
			p["plugin_id"] = v
			return err
		}); err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.action.list", p), nil
	case "invoke":
		if len(args) < 2 {
			return nil, usage("usage: herdr plugin action invoke <action_id> [--plugin ID]")
		}
		p := map[string]any{"action_id": args[1], "context": map[string]any{"invocation_source": "cli"}}
		if err := walk(args[2:], func(arg string, value valueFn) *UsageError {
			if arg != "--plugin" {
				return unknownOption(arg)
			}
			v, err := value(arg)
			p["plugin_id"] = v
			return err
		}); err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.action.invoke", p), nil
	case "help", "--help", "-h":
		return nil, &UsageError{Msg: pluginActionHelp, Code: 0}
	}
	return nil, &UsageError{Msg: pluginActionHelp, Code: 2}
}

func parsePluginPane(args []string, getenv Env, cwd string) (*Call, *UsageError) {
	if len(args) == 0 {
		return nil, &UsageError{Msg: pluginPaneHelp, Code: 2}
	}
	switch args[0] {
	case "open":
		p := map[string]any{"focus": true}
		env := map[string]string{}
		err := walk(args[1:], func(arg string, value valueFn) *UsageError {
			switch arg {
			case "--plugin", "--entrypoint", "--workspace", "--target-pane", "--cwd":
				v, err := value(arg)
				if err != nil {
					return err
				}
				key := map[string]string{"--plugin": "plugin_id", "--entrypoint": "entrypoint", "--workspace": "workspace_id", "--target-pane": "target_pane_id", "--cwd": "cwd"}[arg]
				if arg == "--cwd" {
					v = absPath(v, getenv, cwd)
				}
				p[key] = v
				return nil
			case "--placement":
				v, err := value(arg)
				if err != nil {
					return err
				}
				switch v {
				case "overlay", "popup", "split", "tab", "zoomed":
				case "fullscreen":
					v = "zoomed"
				default:
					return usage("invalid placement: " + v + " (expected overlay, popup, split, tab, zoomed, or fullscreen)")
				}
				p["placement"] = v
				return nil
			case "--width", "--height":
				v, err := value(arg)
				if err != nil {
					return err
				}
				s, serr := popupSize(v)
				if serr != nil {
					return usage(arg + " " + serr.Error())
				}
				p[arg[2:]] = s
				return nil
			case "--direction":
				v, err := value(arg)
				if err != nil {
					return err
				}
				d, derr := splitDirection(v)
				p["direction"] = d
				return derr
			case "--env":
				v, err := value(arg)
				if err != nil {
					return err
				}
				k, val, eerr := envAssignment(v)
				env[k] = val
				return eerr
			case "--focus", "--no-focus":
				p["focus"] = arg == "--focus"
				return nil
			}
			return unknownOption(arg)
		})
		if err != nil {
			return nil, err
		}
		if _, ok := p["plugin_id"]; !ok {
			return nil, usage("missing required --plugin")
		}
		if _, ok := p["entrypoint"]; !ok {
			return nil, usage("missing required --entrypoint")
		}
		p["env"] = env
		return call("cli:plugin", "plugin.pane.open", p), nil
	case "focus", "close":
		id, err := oneID(args[1:], "usage: herdr plugin pane "+args[0]+" <pane_id>")
		if err != nil {
			return nil, err
		}
		return call("cli:plugin", "plugin.pane."+args[0], map[string]any{"pane_id": id}), nil
	case "help", "--help", "-h":
		return nil, &UsageError{Msg: pluginPaneHelp, Code: 0}
	}
	return nil, &UsageError{Msg: pluginPaneHelp, Code: 2}
}

// popupSize reads --width and --height as herdr's PopupSize::parse_cli
// does, and gives the value herdr sends: a number of cells, or "N%".
func popupSize(v string) (any, error) {
	if len(v) > 0 && v[len(v)-1] == '%' {
		n, err := parseUint("", v[:len(v)-1], 8)
		if err != nil {
			return nil, errUsage("must be a number of cells or a percentage like 80%")
		}
		if n < 1 || n > 100 {
			return nil, errUsage("percentage must be between 1% and 100%")
		}
		return v, nil
	}
	n, err := parseUint("", v, 16)
	if err != nil {
		return nil, errUsage("must be a number of cells or a percentage like 80%")
	}
	return n, nil
}

type errUsage string

func (e errUsage) Error() string { return string(e) }

const pluginActionHelp = `herdr plugin action commands:
  herdr plugin action list [--plugin ID]
  herdr plugin action invoke <action_id> [--plugin ID]`

const pluginPaneHelp = `herdr plugin pane commands:
  herdr plugin pane open --plugin ID --entrypoint ID [--placement overlay|popup|split|tab|zoomed|fullscreen] [--width SIZE] [--height SIZE] [--workspace ID] [--target-pane PANE] [--direction right|down] [--cwd PATH] [--env KEY=VALUE]... [--focus|--no-focus]
  herdr plugin pane focus <pane_id>
  herdr plugin pane close <pane_id>`
