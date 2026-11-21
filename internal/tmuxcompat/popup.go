package tmuxcompat

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"time"
)

type timeoutCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
}

const popupWait = 24 * time.Hour

func (s *Shim) displayPopup(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if p.Has('C') {
		return OutcomeUnsupported, nil, errors.New("display-popup -C: not supported, a tuios popup closes when its command exits")
	}
	var detail []string
	for _, c := range []byte("bcsSxy") {
		if _, ok := p.Value(c); ok {
			detail = append(detail, "display-popup -"+string(c)+" ignored: tuios places and draws the popup")
		}
	}
	if p.Has('B') {
		detail = append(detail, "display-popup -B ignored: tuios draws the popup's border")
	}
	if !p.Has('E') {
		detail = append(detail, "display-popup without -E: the popup closes when its command exits")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, detail, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, detail, err
	}

	var argv []string
	switch len(p.Args) {
	case 0:
		argv = []string{cmpOr(s.Shell, "/bin/sh")}
	case 1:
		argv = []string{"/bin/sh", "-c", p.Args[0]}
	default:
		argv = p.Args
	}
	if env := append(s.paneEnvFor(target.sess), p.Values('e')...); len(env) > 0 {
		pre := []string{"env"}
		for _, e := range env {
			if len(e) > 1 && e[0] == '-' {
				pre = append(pre, "-u", e[1:])
			} else {
				pre = append(pre, e)
			}
		}
		argv = append(append(pre, "--"), argv...)
	}

	params := map[string]any{
		"session":   target.sess.name,
		"workspace": target.Workspace,
		"command":   argv,
		"wait":      true,
	}
	if w, ok := p.Value('w'); ok {
		params["width"] = w
	}
	if h, ok := p.Value('h'); ok {
		params["height"] = h
	}
	if t, ok := p.Value('T'); ok {
		params["name"] = t
	}
	cwd := s.Cwd
	if d, ok := p.Value('d'); ok {
		cwd = d
		if !filepath.IsAbs(d) && s.Cwd != "" {
			cwd = filepath.Join(s.Cwd, d)
		}
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if tc, ok := s.Caller.(timeoutCaller); ok {
		_, err = tc.CallWithTimeout("popup", params, popupWait)
	} else {
		_, err = s.Caller.Call("popup", params)
	}
	if err != nil {
		return OutcomeError, detail, err
	}
	return outcomeFor(detail), detail, nil
}
