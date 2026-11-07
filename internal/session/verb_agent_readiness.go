package session

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// agent-readiness: whether each harness can start an agent on this machine.
//
// The New agent sheet showed "claude ready" from a PATH lookup in the client
// process, so it said nothing true about another machine, and nothing about
// a harness that is installed and signed out. The daemon of each machine now
// answers for itself: the harness's program on the PATH the caller sent (or
// the daemon's own), and, for a harness whose manifest names a check
// ([launch] signed_in_args), whether it says it is signed in.
//
// The check runs the harness's own program with the manifest's arguments,
// never a program a manifest names. Its output is matched and dropped: Claude
// Code's holds the account's email. It is bounded by readinessCheckTimeout and
// its answer kept for readinessTTL, so a sheet open on five machines starts at
// most one check per harness a minute on each.

// readinessCheckTimeout bounds one signed-in check.
const readinessCheckTimeout = 2 * time.Second

// readinessTTL is how long an answer is kept.
const readinessTTL = time.Minute

// readinessOutputMax bounds the output a check is read for.
const readinessOutputMax = 64 << 10

// Signed-in answers.
const (
	SignedInYes     = "yes"
	SignedInNo      = "no"
	SignedInUnknown = "unknown"
)

// AgentReadiness is one harness on this machine.
type AgentReadiness struct {
	Harness string `json:"harness"`
	Name    string `json:"name"`
	Command string `json:"command"`
	// Installed says the program is on the PATH.
	Installed bool `json:"installed"`
	// SignedIn is yes, no or unknown (the check failed or timed out), and
	// empty for a harness with no check.
	SignedIn string `json:"signed_in,omitempty"`
}

type readinessEntry struct {
	at     time.Time
	answer string
	// done is closed when the check ends; a second caller waits on it
	// rather than start its own.
	done chan struct{}
}

var readinessCache struct {
	sync.Mutex
	m map[string]*readinessEntry
}

func (d *Daemon) verbAgentReadiness(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Harnesses []string          `json:"harnesses"`
		Env       map[string]string `json:"env"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	_, pathList, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	reg := d.agentMatcher.registry
	if reg == nil {
		return map[string]any{"agents": []AgentReadiness{}}, nil
	}
	ids := p.Harnesses
	if len(ids) == 0 {
		ids = reg.IDs()
	}
	if len(ids) > 64 {
		return nil, invalidParam("harnesses", "name at most 64 harnesses")
	}
	out := make([]AgentReadiness, 0, len(ids))
	var wg sync.WaitGroup
	for _, id := range ids {
		m, cmd, ok := reg.Resolve(id)
		if !ok || m == nil {
			continue
		}
		r := AgentReadiness{Harness: m.ID, Name: m.DisplayName, Command: cmd}
		var path string
		var err error
		if pathList != "" {
			path, err = lookPathIn(cmd, pathList)
		} else {
			path, err = exec.LookPath(cmd)
		}
		r.Installed = err == nil
		out = append(out, r)
		if !r.Installed || len(m.Launch.SignedInArgs) == 0 {
			continue
		}
		i, launch := len(out)-1, &m.Launch
		wg.Go(func() { out[i].SignedIn = signedIn(m.ID, path, launch) })
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool { return out[a].Harness < out[b].Harness })
	return map[string]any{"agents": out}, nil
}

// signedIn is the harness's answer, from the cache when it is fresh.
func signedIn(id, path string, l *harness.Launch) string {
	key := id + "\x00" + path
	readinessCache.Lock()
	if readinessCache.m == nil {
		readinessCache.m = map[string]*readinessEntry{}
	}
	e := readinessCache.m[key]
	if e != nil && (e.answer == "" || time.Since(e.at) < readinessTTL) {
		readinessCache.Unlock()
		<-e.done
		return e.answer
	}
	e = &readinessEntry{done: make(chan struct{})}
	readinessCache.m[key] = e
	readinessCache.Unlock()

	answer := runSignedInCheck(path, l)
	readinessCache.Lock()
	e.answer, e.at = answer, time.Now()
	readinessCache.Unlock()
	close(e.done)
	return answer
}

// runSignedInCheck runs the check once.
func runSignedInCheck(path string, l *harness.Launch) string {
	ctx, cancel := context.WithTimeout(context.Background(), readinessCheckTimeout)
	defer cancel()
	cmd := childproc.CommandContext(ctx, path, l.SignedInArgs...)
	cmd.WaitDelay = 500 * time.Millisecond
	var buf capBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		// A signed-out harness may say so with a failing exit; a check
		// that never ran or ran out of time says nothing.
		var exit *exec.ExitError
		if ctx.Err() != nil || !errors.As(err, &exit) {
			return SignedInUnknown
		}
	}
	ok, _ := l.SignedIn(buf.b)
	if ok {
		return SignedInYes
	}
	return SignedInNo
}

// capBuffer keeps the first readinessOutputMax bytes written to it.
type capBuffer struct{ b []byte }

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := readinessOutputMax - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(len(p), room)]...)
	}
	return len(p), nil
}
