package tmuxcompat

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

type envVar struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Remove bool   `json:"remove,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

func envScope(sv *sessionView) string {
	if sv == nil {
		return ""
	}
	return cmpOr(sv.id, sv.name)
}

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
	if err := jsonx.Unmarshal(data, &list); err != nil {
		return nil, errfmt.Errorf("read %s: %w", path, err)
	}
	return list, nil
}

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
	data, err := jsonx.Marshal(list)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o600, false)
}

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
	sortx.SortFunc(out, func(a, b envVar) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

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
			return OutcomeError, nil, errfmt.Errorf("unknown variable: %s", p.Args[0])
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
		return OutcomeError, detail, errfmt.Errorf("set-environment: %w", err)
	}
	return outcomeFor(detail), detail, nil
}
