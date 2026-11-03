package session

import (
	"log"
	"sort"

	"github.com/google/uuid"
)

// An option set with no client attached has nothing to draw it, so set-option
// records it and promises it applies when a client attaches. The record alone
// never kept that promise: get-option read it back, and a client attaching
// later loaded its own config and never heard of the value.
//
// The options waiting for a client are kept apart from the session's record.
// The record holds every value ever set, and replaying all of it on each
// attach would put back a value the person changed since on the settings
// page. Only a value no client has applied waits here, and the first client
// that applies it takes it off.

// waitOption keeps a value for the next client that attaches. Called with
// the route order held.
func (s *Session) waitOption(path, value string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.optionsWaiting == nil {
		s.optionsWaiting = make(map[string]string)
	}
	s.optionsWaiting[path] = value
}

// dropWaitingOption forgets a waiting value, once a client was sent a newer
// one. Called with the route order held.
func (s *Session) dropWaitingOption(path string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(s.optionsWaiting, path)
}

// hasWaitingOptions says whether any value waits for a client.
func (s *Session) hasWaitingOptions() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.optionsWaiting) > 0
}

// takeWaitingOptions removes and returns the waiting values, sorted by path.
// Called with the route order held.
func (s *Session) takeWaitingOptions() (paths, values []string) {
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

// routeOption sends one option to an attached client the way set-option does
// live: the client applies it and writes it to its config file. It returns
// whether the client applied it, and why not when it did not.
func (d *Daemon) routeOption(tui *connState, path, value string) (bool, string) {
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: "set_config",
		ConfigPath:  path,
		ConfigValue: value,
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

// applyWaitingOptions routes the options set while no client was attached to
// the client that now shows the session. It runs after an attach reply, so
// the client is in the session's broadcast set and can take an unsolicited
// command. A value the client does not apply waits for the next attach,
// The route order is held throughout, so no newer value can be set between.
func (d *Daemon) applyWaitingOptions(sess *Session) {
	if !sess.hasWaitingOptions() {
		return
	}
	sess.optionApplyMu.Lock()
	defer sess.optionApplyMu.Unlock()
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		return
	}
	paths, values := sess.takeWaitingOptions()
	for i, path := range paths {
		if ok, why := d.routeOption(tui, path, values[i]); !ok {
			log.Printf("Session %s: the client did not apply the waiting option %s: %s",
				sess.Name(), path, why)
			// This one and the rest wait for the next client. A client that
			// did not answer one would only time out on each of the others.
			for j := i; j < len(paths); j++ {
				sess.waitOption(paths[j], values[j])
			}
			return
		}
	}
}
