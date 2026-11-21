package tmuxcompat

import (
	"errors"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func neighbour(p *pane, dir byte) *pane {
	on := p.sess.panesOn(p.Workspace)
	minX, minY, maxX, maxY := bounds(on)
	var near, wrap int
	var far func(q *pane) int
	var over func(q *pane) bool
	overX := func(q *pane) bool { return q.X < p.X+p.Width && p.X < q.X+q.Width }
	overY := func(q *pane) bool { return q.Y < p.Y+p.Height && p.Y < q.Y+q.Height }
	switch dir {
	case 'L':
		near, wrap, far, over = p.X, maxX, func(q *pane) int { return q.X + q.Width }, overY
		if near <= minX {
			near = wrap
		}
	case 'R':
		near, wrap, far, over = p.X+p.Width, minX, func(q *pane) int { return q.X }, overY
		if near >= maxX {
			near = wrap
		}
	case 'U':
		near, wrap, far, over = p.Y, maxY, func(q *pane) int { return q.Y + q.Height }, overX
		if near <= minY {
			near = wrap
		}
	default:
		near, wrap, far, over = p.Y+p.Height, minY, func(q *pane) int { return q.Y }, overX
		if near >= maxY {
			near = wrap
		}
	}
	var found []*pane
	for _, q := range on {
		if q != p && abs(far(q)-near) <= 1 && over(q) {
			found = append(found, q)
		}
	}
	if len(found) == 0 {
		return nil
	}
	for _, id := range p.sess.wsHistory[p.Workspace] {
		for _, q := range found {
			if q.ID == id {
				return q
			}
		}
	}
	return found[0]
}

func (s *Shim) selectDirection(target *pane, dir byte) error {
	next := neighbour(target, dir)
	if next == nil {
		return nil
	}
	_, err := s.Caller.Call("focus-window", map[string]any{"session": next.sess.name, "window": next.ID})
	return err
}

func (s *Shim) lastPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	sv, ws, err := v.resolveWindow(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	active := sv.active(ws)
	var last *pane
	for _, id := range sv.wsHistory[ws] {
		if q := sv.byWindowID(id); q != nil && q.Workspace == ws && (active == nil || q.ID != active.ID) {
			last = q
			break
		}
	}
	if last == nil {
		return OutcomeError, nil, errors.New("no last pane")
	}
	if _, err := s.Caller.Call("focus-window", map[string]any{"session": sv.name, "window": last.ID}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

func (s *Shim) stepWindow(name string, args []string, step int) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	sessRef := strings.TrimSuffix(tv, ":")
	sv, err := v.targetSession(sessRef, sessRef != "", s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	used := sv.windowsInUse()
	i := slices.Index(used, sv.current)
	word := "next"
	if step < 0 {
		word = "previous"
	}
	if len(used) == 0 || len(used) == 1 && i >= 0 {
		return OutcomeError, nil, errfmt.Errorf("no %s window", word)
	}
	var ws int
	switch {
	case i < 0 && step > 0:
		ws = used[0]
		for _, n := range used {
			if n > sv.current {
				ws = n
				break
			}
		}
	case i < 0:
		ws = used[len(used)-1]
		for _, n := range slices.Backward(used) {
			if n < sv.current {
				ws = n
				break
			}
		}
	default:
		ws = used[(i+step+len(used))%len(used)]
	}
	if _, err := s.Caller.Call("select-workspace", map[string]any{"session": sv.name, "workspace": ws}); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}

func (s *Shim) nextWindow(name string, args []string) (string, []string, error) {
	return s.stepWindow(name, args, 1)
}

func (s *Shim) previousWindow(name string, args []string) (string, []string, error) {
	return s.stepWindow(name, args, -1)
}

func (s *Shim) breakPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	sv0, _ := p.Value('s')
	src, err := v.resolvePane(sv0, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	sv := src.sess
	if len(sv.panesOn(src.Workspace)) == 1 {
		return OutcomeError, nil, errors.New("can't break with only one pane")
	}
	ws := 0
	if tv, ok := p.Value('t'); ok && tv != "" {
		_, win, _, _, _ := splitTarget(tv)
		n, err := sv.workspaceOf(win)
		if err != nil {
			return OutcomeError, nil, err
		}
		if sv.wsCount[n] > 0 {
			return OutcomeError, nil, errfmt.Errorf("index in use: %d", n)
		}
		ws = n
	} else {
		for _, n := range sv.workspace {
			if sv.wsCount[n] == 0 {
				ws = n
				break
			}
		}
		if ws == 0 {
			return OutcomeError, nil, errors.New("break pane failed: every tuios workspace already holds panes")
		}
	}
	if _, err := s.Caller.Call("move-window", map[string]any{"session": sv.name, "window": src.ID, "workspace": ws, "follow": !p.Has('d')}); err != nil {
		return OutcomeError, nil, err
	}
	if n, ok := p.Value('n'); ok {
		if _, err := s.Caller.Call("set-workspace-name", map[string]any{"session": sv.name, "workspace": ws, "name": n}); err != nil {
			return OutcomeError, nil, errfmt.Errorf("name window failed: %w", err)
		}
	}
	if !p.Has('P') {
		return OutcomeOK, nil, nil
	}
	format := cmpOr(valueOr(p, 'F'), "#{session_name}:#{window_index}.#{pane_index}")
	detail := s.printNew(src.ID, format, "")
	return outcomeFor(detail), detail, nil
}

func (s *Shim) joinPane(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	caller := s.callerPane(v)
	srcRef, _ := p.Value('s')
	dstRef, _ := p.Value('t')
	var src, dst *pane
	if srcRef == "" {
		return OutcomeError, nil, errors.New("no source pane: give one with -s")
	}
	if src, err = v.resolvePane(srcRef, caller); err != nil {
		return OutcomeError, nil, err
	}
	if dst, err = v.resolvePane(dstRef, caller); err != nil {
		return OutcomeError, nil, err
	}
	if src.ID == dst.ID {
		return OutcomeError, nil, errors.New("source and target panes must be different")
	}
	if src.sess != dst.sess {
		if _, err := s.Caller.Call("move-window", map[string]any{"session": src.sess.name, "window": src.ID, "to_session": dst.sess.name, "workspace": dst.Workspace, "follow": false}); err != nil {
			return OutcomeError, nil, err
		}
		if !p.Has('d') {
			if _, err := s.Caller.Call("focus-window", map[string]any{"session": dst.sess.name, "window": src.ID}); err != nil {
				return OutcomeError, nil, err
			}
		}
		return OutcomeOK, nil, nil
	}
	if src.Workspace == dst.Workspace {
		return OutcomeOK, nil, nil
	}
	if _, err := s.Caller.Call("move-window", map[string]any{"session": src.sess.name, "window": src.ID, "workspace": dst.Workspace, "follow": false}); err != nil {
		return OutcomeError, nil, err
	}
	if !p.Has('d') {
		if _, err := s.Caller.Call("focus-window", map[string]any{"session": src.sess.name, "window": src.ID}); err != nil {
			return OutcomeError, nil, err
		}
	}
	return OutcomeOK, nil, nil
}

func (s *Shim) swapPane(name string, args []string) (string, []string, error) {
	if _, err := parseFlags(name, specs[name], args); err != nil {
		return OutcomeUnsupported, nil, err
	}
	return OutcomeUnsupported, nil, errors.New("swap-pane: not supported, tuios places panes itself")
}

func (s *Shim) renameSession(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("rename-session: give exactly one new name")
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	sessRef := strings.TrimSuffix(tv, ":")
	sv, err := v.targetSession(sessRef, sessRef != "", s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	newName := p.Args[0]
	if v.isSession(newName) && newName != sv.name {
		return OutcomeError, nil, errfmt.Errorf("duplicate session: %s", newName)
	}
	if _, err := s.Caller.Call("rename-session", map[string]any{"session": sv.name, "name": newName}); err != nil {
		return OutcomeError, nil, err
	}
	if !s.AllSessions && sv.name == s.Session {
		s.Session = newName
	}
	return OutcomeOK, nil, nil
}
