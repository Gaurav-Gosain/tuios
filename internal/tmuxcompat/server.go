package tmuxcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// listClients prints one client for each session a tuios client is attached
// to. tuios does not name its clients' terminals, so client_tty is empty,
// and a tool that switches a client by its tty (switch-client -c) finds
// nothing to switch. The shim's own control-mode clients are not listed.
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
			return OutcomeError, nil, fmt.Errorf("can't find session: %s", tv)
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
		out, d := expand(format, vars)
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// option is one tmux option show-options answers, with the value that says
// how tuios behaves.
type option struct {
	name  string
	scope byte // 's' server, 'g' session, 'w' window
	value func(s *Shim) string
}

func fixed(v string) func(*Shim) string { return func(*Shim) string { return v } }

// options are the options show-options answers. Every other option is
// "invalid option", as for a name tmux does not know. The values describe
// tuios: workspaces count from 1, a pane's size follows the tuios client,
// and tuios owns the status line, so tmux's is off.
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
	{"window-size", 'w', fixed("latest")},
}

// historyLimit is the scrollback length tuios keeps, read from the daemon's
// appearance.scrollback_lines. It is empty when the daemon does not say.
func (s *Shim) historyLimit() string {
	raw, err := s.Caller.Call("get-option", map[string]any{"key": "appearance.scrollback_lines"})
	if err != nil {
		return ""
	}
	var res struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	var n int64
	if json.Unmarshal(res.Value, &n) == nil {
		return strconv.FormatInt(n, 10)
	}
	return ""
}

// showOptions prints options: one by name, or every one of a scope.
func (s *Shim) showOptions(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) > 1 {
		return OutcomeError, nil, fmt.Errorf("%s: give at most one option", name)
	}
	valueOnly := p.Has('v')
	if len(p.Args) == 1 {
		want := p.Args[0]
		i := slices.IndexFunc(options, func(o option) bool { return o.name == want })
		if i < 0 {
			if p.Has('q') {
				return OutcomeOK, nil, nil
			}
			return OutcomeError, nil, fmt.Errorf("invalid option: %s", want)
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

// quoteOption quotes a value the way show-options prints one with spaces.
func quoteOption(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"'") {
		return strconv.Quote(v)
	}
	return v
}

// newSession starts a tuios session. The shim does so only when it serves
// every session: a caller in a pane is held to its own. The session starts
// detached, since the shim attaches no terminal. Control mode is the one
// exception, where the control client attaches to it.
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
	if argv := s.paneCommand(p.Args, p.Values('e')); len(argv) > 0 {
		params["command"] = argv
	}
	raw, err := s.Caller.Call("new-session", params)
	if err != nil {
		var coded interface{ ErrorCode() string }
		if errors.As(err, &coded) && coded.ErrorCode() == "session_exists" {
			return OutcomeError, detail, fmt.Errorf("duplicate session: %s", sessName)
		}
		return OutcomeError, detail, fmt.Errorf("create session failed: %w", err)
	}
	var res struct {
		Session  string `json:"session"`
		WindowID string `json:"window_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Session == "" {
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

// valueOr is p's value for flag c, "" when it was not given.
func valueOr(p Parsed, c byte) string {
	v, _ := p.Value(c)
	return v
}
