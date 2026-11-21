package session

import (
	"log"
	"sort"

	"github.com/google/uuid"
)

type waitingOption struct {
	value  string
	noSave bool
}

func (s *Session) waitOption(path, value string, noSave bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.optionsWaiting == nil {
		s.optionsWaiting = make(map[string]waitingOption)
	}
	s.optionsWaiting[path] = waitingOption{value: value, noSave: noSave}
}

func (s *Session) dropWaitingOption(path string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(s.optionsWaiting, path)
}

func (s *Session) hasWaitingOptions() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.optionsWaiting) > 0
}

func (cs *connState) holdsOptions() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.holdOptions
}

func (s *Session) takeWaitingOptions() (paths []string, values []waitingOption) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for p := range s.optionsWaiting {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		values = append(values, s.optionsWaiting[p])
	}
	s.optionsWaiting = nil
	return paths, values
}

func (d *Daemon) routeOption(tui *connState, path, value string, noSave bool) (bool, string) {
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType:  "set_config",
		ConfigPath:   path,
		ConfigValue:  value,
		ConfigNoSave: noSave,
	}, routedVerbTimeout)
	switch {
	case err == nil && res != nil && res.Success:
		return true, ""
	case err != nil:
		return false, "the attached client did not answer: " + err.Error()
	case res != nil && res.Message != "":
		return false, res.Message
	default:
		return false, "the attached client refused the change"
	}
}

func (d *Daemon) applyWaitingOptions(sess *Session) {
	sess.optionApplyMu.Lock()
	defer sess.optionApplyMu.Unlock()
	if !sess.hasWaitingOptions() {
		return
	}
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return
	}
	paths, values := sess.takeWaitingOptions()
	holds := tui.holdsOptions()
	for i, path := range paths {
		w := values[i]
		if w.noSave && !holds {
			sess.waitOption(path, w.value, w.noSave)
			continue
		}
		if ok, why := d.routeOption(tui, path, w.value, w.noSave); !ok {
			log.Printf("Session %s: the client did not apply the waiting option %s: %s",
				sess.Name(), path, why)
			for j := i; j < len(paths); j++ {
				sess.waitOption(paths[j], values[j].value, values[j].noSave)
			}
			return
		}
	}
}
