package tmuxcompat

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

func (s *Shim) listClients(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	sessions := v.sessions
	if tv, ok := p.Value('t'); ok && tv != "" {
		sv, found := v.sessionOf(strings.TrimSuffix(tv, ":"))
		if !found {
			return OutcomeError, nil, errfmt.Errorf("can't find session: %s", tv)
		}
		sessions = []*sessionView{sv}
	}
	format, ok := p.Value('F')
	if !ok {
		format = "#{client_name}: #{session_name} [#{client_width}x#{client_height} #{client_termname}]"
	}
	var detail []string
	for _, sv := range sessions {
		if !sv.attached {
			continue
		}
		vars := s.sessionVars(sv)
		if a := sv.active(sv.current); a != nil {
			vars = s.paneVars(a)
		}
		vars["client_name"] = "tuios-" + sv.name
		vars["client_session"] = sv.name
		vars["client_control_mode"] = "0"
		vars["client_activity"] = strconv.FormatInt(sv.activity, 10)
		vars["client_tty"] = ""
		vars["client_width"] = strconv.Itoa(sv.width)
		vars["client_height"] = strconv.Itoa(sv.height)
		vars["client_termname"] = "tuios"
		vars["client_flags"] = "attached,focused"
		out, d := s.expand(format, vars)
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

func (s *Shim) detachClient(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) > 0 {
		return OutcomeUnsupported, nil, errors.New("detach-client: the shim does not run a command on detach")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	params := map[string]any{}
	if sv, ok := p.Value('s'); ok && sv != "" {
		ref := strings.TrimSuffix(sv, ":")
		target, found := v.sessionOf(ref)
		if !found {
			return OutcomeError, nil, errfmt.Errorf("can't find session: %s", ref)
		}
		params["session"] = target.name
	} else {
		ref := ""
		if tv, ok := p.Value('t'); ok && tv != "" {
			sessName, isClient := strings.CutPrefix(tv, "tuios-")
			if !isClient {
				return OutcomeError, nil, errfmt.Errorf("can't find client: %s", tv)
			}
			ref = sessName
		}
		switch {
		case ref != "":
			target, found := v.sessionOf(ref)
			if !found {
				return OutcomeError, nil, errfmt.Errorf("can't find client: tuios-%s", ref)
			}
			params["session"] = target.name
		case s.callerPane(v) == nil:
			if v.def == nil {
				return OutcomeError, nil, errors.New("no current client")
			}
			params["session"] = v.def.name
		}
		if p.Has('a') {
			params["all_other"] = true
		}
	}
	if _, err := s.Caller.Call("detach-client", params); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

type option struct {
	name  string
	scope byte
	value func(s *Shim) string
}

func fixed(v string) func(*Shim) string { return func(*Shim) string { return v } }

var options = []option{
	{"default-terminal", 's', fixed("xterm-256color")},
	{"escape-time", 's', fixed("0")},
	{"exit-empty", 's', fixed("on")},
	{"focus-events", 's', fixed("on")},
	{"set-clipboard", 's', fixed("external")},
	{"base-index", 'g', fixed("1")},
	{"default-shell", 'g', func(s *Shim) string { return s.Shell }},
	{"destroy-unattached", 'g', fixed("off")},
	{"detach-on-destroy", 'g', fixed("on")},
	{"history-limit", 'g', (*Shim).historyLimit},
	{"mouse", 'g', fixed("on")},
	{"renumber-windows", 'g', fixed("off")},
	{"status", 'g', fixed("off")},
	{"aggressive-resize", 'w', fixed("off")},
	{"allow-rename", 'w', fixed("on")},
	{"automatic-rename", 'w', fixed("on")},
	{"pane-base-index", 'w', fixed("0")},
	{"remain-on-exit", 'w', fixed("off")},
	{"window-size", 'w', (*Shim).windowSize},
}

func (s *Shim) windowSize() string {
	var v string
	if !s.daemonOption(map[string]any{"session": s.Session, "key": "daemon.window_size"}, &v) || v == "" {
		return config.WindowSizeLatest
	}
	return v
}

func (s *Shim) daemonOption(params map[string]any, v any) bool {
	raw, err := s.Caller.Call("get-option", params)
	if err != nil {
		return false
	}
	var res struct {
		Value json.RawMessage `json:"value"`
	}
	return jsonx.Unmarshal(raw, &res) == nil && jsonx.Unmarshal(res.Value, v) == nil
}

func (s *Shim) setWindowSize(value string) (string, error) {
	if !slices.Contains(config.WindowSizeModes, value) {
		return OutcomeError, errfmt.Errorf("window-size: %s is not supported, use smallest, largest or latest", value)
	}
	if _, err := s.Caller.Call("set-option", map[string]any{"session": s.Session, "key": "daemon.window_size", "value": value}); err != nil {
		return OutcomeError, errfmt.Errorf("window-size: %w", err)
	}
	return OutcomeOK, nil
}

func optionAssignment(cmd string, args []string) (name, value string, ok bool) {
	p, err := parseFlags(cmd, specs[cmd], args)
	if err != nil || p.Has('u') || len(p.Args) < 2 {
		return "", "", false
	}
	return p.Args[0], p.Args[1], true
}

func (s *Shim) historyLimit() string {
	var v any
	if s.daemonOption(map[string]any{"session": s.Session, "key": "appearance.scrollback_lines"}, &v) {
		switch n := v.(type) {
		case float64:
			if n > 0 {
				return strconv.FormatInt(int64(n), 10)
			}
		case string:
			if i, err := strconv.Atoi(n); err == nil && i > 0 {
				return n
			}
		}
	}
	return strconv.Itoa(config.DefaultScrollbackLines)
}

func (s *Shim) showOptions(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) > 1 {
		return OutcomeError, nil, errfmt.Errorf("%s: give at most one option", name)
	}
	valueOnly := p.Has('v')
	if len(p.Args) == 1 {
		want := p.Args[0]
		i := slices.IndexFunc(options, func(o option) bool { return o.name == want })
		if i < 0 {
			if p.Has('q') {
				return OutcomeOK, nil, nil
			}
			return OutcomeError, nil, errfmt.Errorf("invalid option: %s", want)
		}
		val := options[i].value(s)
		if valueOnly {
			s.println(val)
		} else {
			s.println(want + " " + quoteOption(val))
		}
		return OutcomeOK, nil, nil
	}
	scope := byte('g')
	switch {
	case name == "show-window-options" || p.Has('w'):
		scope = 'w'
	case p.Has('s'):
		scope = 's'
	}
	for _, o := range options {
		if o.scope != scope {
			continue
		}
		val := o.value(s)
		if valueOnly {
			s.println(val)
		} else {
			s.println(o.name + " " + quoteOption(val))
		}
	}
	return OutcomeOK, nil, nil
}

func quoteOption(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"'") {
		return strconv.Quote(v)
	}
	return v
}

func (s *Shim) newSession(name string, args []string) (string, []string, error) {
	if !s.AllSessions {
		return OutcomeUnsupported, nil, errors.New("new-session: refused, the tuios tmux shim in a pane does not start sessions")
	}
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if _, ok := p.Value('t'); ok {
		return OutcomeUnsupported, nil, errors.New("new-session: -t (a session group) is not supported by the tuios tmux shim")
	}
	if !p.Has('d') && !s.control {
		return OutcomeError, nil, errors.New("new-session: add -d. The tuios tmux shim does not attach a terminal. Run tuios attach to see the session")
	}
	var detail []string
	if _, ok := p.Value('x'); ok {
		detail = append(detail, "new-session -x and -y ignored: a tuios client sets the size")
	} else if _, ok := p.Value('y'); ok {
		detail = append(detail, "new-session -x and -y ignored: a tuios client sets the size")
	}
	sessName, _ := p.Value('s')
	params := map[string]any{}
	if sessName != "" {
		params["name"] = sessName
	}
	if n, ok := p.Value('n'); ok {
		params["window_name"] = n
	}
	cwd := s.Cwd
	if c, ok := p.Value('c'); ok {
		cwd = c
		if !filepath.IsAbs(c) && s.Cwd != "" {
			cwd = filepath.Join(s.Cwd, c)
		}
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if argv := s.paneCommand(p.Args, append(s.paneEnvFor(nil), p.Values('e')...)); len(argv) > 0 {
		params["command"] = argv
	}
	raw, err := s.Caller.Call("new-session", params)
	if err != nil {
		var coded interface{ ErrorCode() string }
		if errors.As(err, &coded) && coded.ErrorCode() == "session_exists" {
			return OutcomeError, detail, errfmt.Errorf("duplicate session: %s", sessName)
		}
		return OutcomeError, detail, errfmt.Errorf("create session failed: %w", err)
	}
	var res struct {
		Session  string `json:"session"`
		WindowID string `json:"window_id"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil || res.Session == "" {
		return OutcomeError, detail, errors.New("create session failed: new-session returned no session")
	}
	s.created = res.Session
	if !p.Has('P') {
		return outcomeFor(detail), detail, nil
	}
	format := cmpOr(valueOr(p, 'F'), "#{session_name}:")
	if res.WindowID == "" {
		s.println(res.Session + ":")
		return OutcomePartial, append(detail, "-P: the new session has no window"), nil
	}
	detail = append(detail, s.printNew(res.WindowID, format, cwd)...)
	return outcomeFor(detail), detail, nil
}

func valueOr(p Parsed, c byte) string {
	v, _ := p.Value(c)
	return v
}
