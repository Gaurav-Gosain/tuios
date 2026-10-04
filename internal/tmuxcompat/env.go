package tmuxcompat

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// show-environment and set-environment.
//
// tmux keeps a global environment and one per session, and gives both to
// every pane it starts. The shim keeps what set-environment sets in its
// runtime directory, one file per scope, readable only by the user, and
// gives it to the panes it opens (split-window, new-window, respawn-pane and
// new-session) through the pane holder. The global environment is the
// caller's own environment with those changes on top, as tmux's is the
// environment the server started with. A session's environment is what was
// set on it: tuios copies nothing into it on attach, so tmux's
// update-environment has no counterpart.

// envVar is one entry of an environment: a value, or a mark that the
// variable is removed from new panes (set-environment -r).
type envVar struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Remove bool   `json:"remove,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// envScope names an environment: "" for the global one, else the session.
func envScope(sv *sessionView) string {
	if sv == nil {
		return ""
	}
	return cmpOr(sv.id, sv.name)
}

// envFile is where an environment is kept, "" when the shim keeps them in
// memory.
func (s *Shim) envFile(scope string) string {
	if s.Dir == "" {
		return ""
	}
	name := "global"
	if scope != "" {
		name = "s-" + hex.EncodeToString([]byte(scope))
	}
	return filepath.Join(s.Dir, "env", name+".json")
}

// readEnv reads the entries set on one environment.
func (s *Shim) readEnv(scope string) ([]envVar, error) {
	path := s.envFile(scope)
	if path == "" {
		return s.memEnv[scope], nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []envVar
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return list, nil
}

// writeEnv stores the entries of one environment.
func (s *Shim) writeEnv(scope string, list []envVar) error {
	path := s.envFile(scope)
	if path == "" {
		if s.memEnv == nil {
			s.memEnv = map[string][]envVar{}
		}
		s.memEnv[scope] = list
		return nil
	}
	if err := EnsureDir(s.Dir); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// fullEnv is an environment as show-environment lists it: the global one is
// the caller's environment with the set entries on top.
func (s *Shim) fullEnv(scope string) ([]envVar, error) {
	set, err := s.readEnv(scope)
	if err != nil || scope != "" {
		return set, err
	}
	var out []envVar
	for _, kv := range s.Environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && !slices.ContainsFunc(set, func(e envVar) bool { return e.Name == k }) {
			out = append(out, envVar{Name: k, Value: v})
		}
	}
	out = append(out, set...)
	slices.SortFunc(out, func(a, b envVar) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// paneEnvFor is what the panes the shim opens in session sv get: the global
// entries, then the session's, as KEY=VALUE or -KEY to remove.
func (s *Shim) paneEnvFor(sv *sessionView) []string {
	var out []string
	scopes := []string{""}
	if sv != nil {
		scopes = append(scopes, envScope(sv))
	}
	for _, scope := range scopes {
		list, err := s.readEnv(scope)
		if err != nil {
			continue
		}
		for _, e := range list {
			if e.Remove {
				out = append(out, "-"+e.Name)
			} else {
				out = append(out, e.Name+"="+e.Value)
			}
		}
	}
	return out
}

// envTarget is the environment -g or -t names.
func (s *Shim) envTarget(p Parsed) (string, error) {
	if p.Has('g') {
		return "", nil
	}
	v, err := s.loadView()
	if err != nil {
		return "", err
	}
	tv, _ := p.Value('t')
	ref := strings.TrimSuffix(tv, ":")
	sv, err := v.targetSession(ref, ref != "", s.callerPane(v))
	if err != nil {
		return "", err
	}
	return envScope(sv), nil
}

// showEnvironment prints one variable or every one, as NAME=value, -NAME for
// a removed one, or with -s as shell commands.
func (s *Shim) showEnvironment(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) > 1 {
		return OutcomeError, nil, errors.New("show-environment: give at most one variable")
	}
	scope, err := s.envTarget(p)
	if err != nil {
		return OutcomeError, nil, err
	}
	list, err := s.fullEnv(scope)
	if err != nil {
		return OutcomeError, nil, err
	}
	show := func(e envVar) {
		switch {
		case p.Has('s') && e.Remove:
			s.println("unset " + e.Name + ";")
		case p.Has('s'):
			s.println(e.Name + "=\"" + escapeDoubleQuoted(e.Value) + "\"; export " + e.Name + ";")
		case e.Remove:
			s.println("-" + e.Name)
		default:
			s.println(e.Name + "=" + e.Value)
		}
	}
	if len(p.Args) == 1 {
		i := slices.IndexFunc(list, func(e envVar) bool { return e.Name == p.Args[0] })
		if i < 0 {
			return OutcomeError, nil, fmt.Errorf("unknown variable: %s", p.Args[0])
		}
		if list[i].Hidden == p.Has('h') {
			show(list[i])
		}
		return OutcomeOK, nil, nil
	}
	for _, e := range list {
		if e.Hidden == p.Has('h') {
			show(e)
		}
	}
	return OutcomeOK, nil, nil
}

// escapeDoubleQuoted escapes the characters POSIX reads inside double quotes
// ($ ` " and \), as tmux's show-environment -s does.
func escapeDoubleQuoted(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if strings.IndexByte("$`\"\\", v[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// setEnvironment sets, removes (-r) or unsets (-u) a variable of the global
// (-g) or a session's environment. -F expands the value as a format.
func (s *Shim) setEnvironment(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) == 0 {
		return OutcomeError, nil, errors.New("empty variable name")
	}
	if len(p.Args) > 2 {
		return OutcomeError, nil, errors.New("set-environment: give a name and at most one value")
	}
	varName := p.Args[0]
	if strings.Contains(varName, "=") {
		return OutcomeError, nil, errors.New("variable name contains =")
	}
	remove, unset := p.Has('r'), p.Has('u')
	if !remove && !unset && len(p.Args) < 2 {
		return OutcomeError, nil, errors.New("no value specified")
	}
	if (remove || unset) && len(p.Args) > 1 {
		return OutcomeError, nil, errors.New("can't specify a value with -r or -u")
	}
	scope, err := s.envTarget(p)
	if err != nil {
		return OutcomeError, nil, err
	}
	var detail []string
	value := ""
	if len(p.Args) > 1 {
		value = p.Args[1]
		if p.Has('F') {
			vars := s.sessionVars(nil)
			if v, err := s.loadView(); err == nil {
				if c := s.callerPane(v); c != nil {
					vars = s.paneVars(c)
				}
			}
			value, detail = s.expand(value, vars)
		}
	}
	list, err := s.readEnv(scope)
	if err != nil {
		return OutcomeError, nil, err
	}
	list = slices.DeleteFunc(list, func(e envVar) bool { return e.Name == varName })
	if !unset {
		list = append(list, envVar{Name: varName, Value: value, Remove: remove, Hidden: p.Has('h')})
	}
	if err := s.writeEnv(scope, list); err != nil {
		return OutcomeError, detail, fmt.Errorf("set-environment: %w", err)
	}
	return outcomeFor(detail), detail, nil
}
