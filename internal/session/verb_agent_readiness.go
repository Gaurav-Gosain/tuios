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
	// UsageLimit says a pane of this harness here is errored with a usage
	// limit, so a new agent of it would stop the same way.
	UsageLimit bool `json:"usage_limit,omitempty"`
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

// readinessParams are what agent-readiness takes.
type readinessParams struct {
	Harnesses []string          `json:"harnesses"`
	Env       map[string]string `json:"env"`
	// RepoURL and ReposRoot ask whether this machine has a checkout of a
	// repository, found the way start-agent finds one (pool_place.go).
	RepoURL   string `json:"repo_url"`
	ReposRoot string `json:"repos_root"`
	// PeerInstance is the calling daemon's instance. The answer's back is
	// this machine's name for that daemon, when a link to it is up.
	PeerInstance string `json:"peer_instance"`
}

// readinessAnswer is agent-readiness's result.
type readinessAnswer struct {
	Agents []AgentReadiness `json:"agents"`
	// Repo is found, none or many, with repo_url; empty without.
	Repo string `json:"repo,omitempty"`
	// Load is this machine's load now, with the agents that are starting.
	Load *poolLoad `json:"load,omitempty"`
	// Cap is [tasks] machine_cap, 0 for no cap.
	Cap int `json:"cap"`
	// Back is this machine's [hosts] name for the daemon peer_instance
	// names, when its link is up.
	Back string `json:"back,omitempty"`
}

func (d *Daemon) verbAgentReadiness(cs *connState, params json.RawMessage) (any, *verbError) {
	var p readinessParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	return d.readiness(cs, p)
}

// readiness answers agent-readiness for this machine.
func (d *Daemon) readiness(cs *connState, p readinessParams) (readinessAnswer, *verbError) {
	_, pathList, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return readinessAnswer{}, verr
	}
	ans := readinessAnswer{Agents: []AgentReadiness{}, Cap: int(d.machineCap.Load())}
	if p.RepoURL != "" {
		repo, verr := d.repoPresence(p.RepoURL, p.ReposRoot)
		if verr != nil {
			return readinessAnswer{}, verr
		}
		ans.Repo = repo
	}
	load := d.poolLoadNow()
	ans.Load = &load
	if p.PeerInstance != "" {
		ans.Back = d.backName(p.PeerInstance)
	}
	reg := d.agentMatcher.registry
	if reg == nil {
		return ans, nil
	}
	ids := p.Harnesses
	if len(ids) == 0 {
		ids = reg.IDs()
	}
	if len(ids) > 64 {
		return readinessAnswer{}, invalidParam("harnesses", "name at most 64 harnesses")
	}
	limited := d.usageLimitedHarnesses()
	out := make([]AgentReadiness, 0, len(ids))
	var wg sync.WaitGroup
	for _, id := range ids {
		m, cmd, ok := reg.Resolve(id)
		if !ok || m == nil {
			continue
		}
		r := AgentReadiness{Harness: m.ID, Name: m.DisplayName, Command: cmd, UsageLimit: limited[m.ID]}
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
		wg.Go(func() { out[i].SignedIn = signedIn(d.ctx, m.ID, path, launch) })
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool { return out[a].Harness < out[b].Harness })
	ans.Agents = out
	return ans, nil
}

// signedIn is the harness's answer, from the cache when it is fresh. ctx is
// the daemon's: a check still running when the daemon stops is killed, and
// its answer is not kept.
func signedIn(ctx context.Context, id, path string, l *harness.Launch) string {
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

	answer := runSignedInCheck(ctx, path, l)
	readinessCache.Lock()
	e.answer, e.at = answer, time.Now()
	if ctx.Err() != nil {
		// Cut short by the stop: nothing was learned.
		delete(readinessCache.m, key)
	}
	readinessCache.Unlock()
	close(e.done)
	return answer
}

// runSignedInCheck runs the check once, under parent: the harness's program
// is killed when parent ends, so a daemon that stops leaves no check running
// after it. A check left running wrote the harness's own files (Claude Code's
// ~/.claude.json) after its daemon was gone.
func runSignedInCheck(parent context.Context, path string, l *harness.Launch) string {
	ctx, cancel := context.WithTimeout(parent, readinessCheckTimeout)
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
