package session

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Keeping the machine awake while agents work.
//
// [daemon] keep_awake is off, on or agents. On holds the machine awake for as
// long as the daemon runs. Agents holds it only while an agent pane of this
// machine is working. The daemon does it, and not a client, because the
// daemon outlives every client: it works with no client attached, over ssh,
// and on a host.
//
// The hold is the system's own (awake_hold_*.go): a systemd inhibitor lock on
// idle and sleep on Linux, a caffeinate power assertion on macOS, and
// SetThreadExecutionState on Windows. Only sleep is held off. The display may
// still dim and lock.
//
// It costs nothing while nothing changes. A goroutine waits on a channel and
// wakes only when a pane's agent state changes, a pane or session closes, the
// agent switch moves or the mode changes. It then counts the working panes and
// takes or drops the hold. No timer runs. With the mode off, the event sink
// does not even wake the goroutine.
//
// Which panes count: a pane whose agent state is working, in a session of this
// daemon, whose program runs on this machine. A pane of a global session whose
// program runs on another machine (WindowState.Host) keeps nothing here awake,
// since the work happens on the other machine. With the agent features off no
// pane is working.
//
// Across a live upgrade the hold is dropped before the exec and taken again by
// the new image once its panes are in place (handoff.go, serve). The Linux and
// macOS holders also end on their own when the daemon goes: the holder reads
// a pipe whose write end only the daemon has, close-on-exec, so a daemon that
// dies or execs closes it and the holder exits.

// awakeKeeper owns the hold and the mode.
type awakeKeeper struct {
	// mode is the configured keep_awake: off, on or agents.
	mode atomic.Value // string
	// kick wakes the loop. It holds one wake: a second one sent before the
	// loop ran is the same news.
	kick chan struct{}
	// settleReq asks the loop to sync now and close the channel it sent.
	settleReq chan chan struct{}
	// count returns the number of working agent panes this machine runs.
	count func() int
	// publish sends a keep-awake event when the mode or the hold changes.
	publish func(streamEvent)

	mu      sync.Mutex
	hold    awakeHolder
	held    bool
	working int
	// failure is why the last attempt to hold failed, empty when it did not.
	failure string
	// cancel ends the running loop; nil while no loop runs.
	cancel context.CancelFunc
	done   chan struct{}
	// lost receives the holder's exit when it ends on its own.
	lost chan error
}

func newAwakeKeeper(count func() int, publish func(streamEvent)) *awakeKeeper {
	k := &awakeKeeper{kick: make(chan struct{}, 1), settleReq: make(chan chan struct{}), count: count, publish: publish, lost: make(chan error, 1)}
	k.mode.Store(config.KeepAwakeOff)
	return k
}

// Mode is the mode in force.
func (k *awakeKeeper) Mode() string {
	m, _ := k.mode.Load().(string)
	return m
}

// setMode changes the mode and reports whether it changed. An unknown value is
// off, so a typo never keeps a machine awake.
func (k *awakeKeeper) setMode(mode string) bool {
	mode = config.ResolveKeepAwake(mode)
	if k.Mode() == mode {
		return false
	}
	k.mode.Store(mode)
	k.mu.Lock()
	k.failure = ""
	k.mu.Unlock()
	k.poke()
	return true
}

// agentEvent wakes the loop for a change that can move the count. It is the
// only call on the event sink's path, and with the mode not agents it does
// nothing.
func (k *awakeKeeper) agentEvent() {
	if k == nil || k.Mode() != config.KeepAwakeAgents {
		return
	}
	k.poke()
}

func (k *awakeKeeper) poke() {
	select {
	case k.kick <- struct{}{}:
	default:
	}
}

// start runs the loop until ctx ends or stop is called. It takes the hold at
// once when the mode asks for it.
func (k *awakeKeeper) start(ctx context.Context) {
	k.mu.Lock()
	if k.cancel != nil {
		k.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	done := make(chan struct{})
	k.done = done
	k.mu.Unlock()
	k.poke()
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				k.apply(false, 0)
				return
			case <-k.kick:
				k.sync()
			case reply := <-k.settleReq:
				k.sync()
				close(reply)
			case err := <-k.lost:
				k.holderLost(err)
			}
		}
	}()
}

// stop ends the loop and drops the hold. It returns once the hold is gone.
func (k *awakeKeeper) stop() {
	if k == nil {
		return
	}
	k.mu.Lock()
	cancel, done := k.cancel, k.done
	k.cancel, k.done = nil, nil
	k.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// settle returns once the loop has synced the hold with the mode, when a loop
// runs.
func (k *awakeKeeper) settle() {
	k.mu.Lock()
	running := k.cancel != nil
	k.mu.Unlock()
	if !running {
		return
	}
	reply := make(chan struct{})
	select {
	case k.settleReq <- reply:
		<-reply
	case <-time.After(5 * time.Second):
	}
}

// sync counts the working panes and takes or drops the hold to match.
func (k *awakeKeeper) sync() {
	working := 0
	want := false
	switch k.Mode() {
	case config.KeepAwakeOn:
		want = true
	case config.KeepAwakeAgents:
		working = k.count()
		want = working > 0
	}
	k.apply(want, working)
}

// apply takes or drops the hold.
func (k *awakeKeeper) apply(want bool, working int) {
	k.mu.Lock()
	changed := working != k.working
	k.working = working
	if want == k.held || (want && k.failure != "") {
		k.mu.Unlock()
		if changed {
			k.announce()
		}
		return
	}
	if want {
		if err := k.hold.take(k.why(), k.lost); err != nil {
			k.failure = err.Error()
			k.mu.Unlock()
			log.Printf("Keep awake: the machine cannot be kept awake: %v", err)
			k.announce()
			return
		}
		k.held = true
		k.mu.Unlock()
		LogBasic("Keep awake: holding the machine awake (%s)", k.Mode())
	} else {
		k.hold.release()
		k.held = false
		k.mu.Unlock()
		LogBasic("Keep awake: the machine can sleep again")
	}
	k.announce()
}

// holderLost runs when the holder process ended while the hold was meant to
// be in force: the system refused the lock, or someone killed the holder. It
// is not taken again until the mode changes, so a holder that cannot run
// does not start again on every agent event.
func (k *awakeKeeper) holderLost(err error) {
	k.mu.Lock()
	if !k.held {
		k.mu.Unlock()
		return
	}
	k.hold.release()
	k.held = false
	msg := "the hold ended on its own"
	if err != nil {
		msg = err.Error()
	}
	k.failure = msg
	k.mu.Unlock()
	log.Printf("Keep awake: %s. Set daemon.keep_awake again to try again", msg)
	k.announce()
}

// why is the reason the system shows for the hold.
func (k *awakeKeeper) why() string {
	if k.Mode() == config.KeepAwakeOn {
		return "daemon.keep_awake is on"
	}
	return "An agent works"
}

// awakeStatus is what get-keep-awake reports.
type awakeStatus struct {
	Mode    string `json:"mode"`
	Held    bool   `json:"held"`
	Working int    `json:"working"`
	Method  string `json:"method"`
	Error   string `json:"error,omitempty"`
}

func (k *awakeKeeper) status() awakeStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	working := k.working
	if k.Mode() != config.KeepAwakeAgents {
		// The count is kept only in agents mode. Counted here, the answer
		// does not depend on the mode.
		working = k.count()
	}
	return awakeStatus{Mode: k.Mode(), Held: k.held, Working: working, Method: awakeMethod(), Error: k.failure}
}

// announce publishes the state as a keep-awake event.
func (k *awakeKeeper) announce() {
	if k.publish == nil {
		return
	}
	st := k.status()
	state := "released"
	if st.Held {
		state = "held"
	}
	ev := streamEvent{Type: EventKeepAwake, Mode: st.Mode, State: state, Working: &st.Working}
	if st.Error != "" {
		ev.Reason = st.Error
	}
	k.publish(ev)
}

// workingAgents counts the panes of this daemon whose agent works on this
// machine. With the agent features off none does.
func (d *Daemon) workingAgents() int {
	if d.agentsOff.Load() {
		return 0
	}
	n := 0
	for _, s := range d.manager.AllSessions() {
		n += s.workingLocalAgents()
	}
	return n
}

// workingLocalAgents counts the session's panes whose agent works and whose
// program runs on this machine.
func (s *Session) workingLocalAgents() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	n := 0
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.AgentState == AgentStateWorking && w.Host == "" {
			n++
		}
	}
	return n
}

// SetKeepAwake applies [daemon] keep_awake. It only keeps the machine awake
// and widens nothing a pane may do, so a file change applies it in both
// directions.
func (d *Daemon) SetKeepAwake(mode string) {
	if d.awake.setMode(mode) {
		LogBasic("Keep awake is %s", d.awake.Mode())
		d.awake.announce()
	}
}

// verbGetKeepAwake answers get-keep-awake.
func (d *Daemon) verbGetKeepAwake(_ *connState, _ json.RawMessage) (any, *verbError) {
	// An agent-state event the caller just read may not have reached the
	// keeper yet. Sync first, so the answer matches the panes.
	d.awake.settle()
	return keepAwakeResult(d.awake.status()), nil
}

// verbSetKeepAwake answers set-keep-awake: it applies the mode and saves it to
// config.toml, so the file, the settings page and every client agree.
func (d *Daemon) verbSetKeepAwake(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Mode string `json:"mode"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	mode := strings.TrimSpace(p.Mode)
	if !slices.Contains(config.KeepAwakeModes, mode) {
		return nil, hintedVerbError(ErrVerbInvalidParams, "mode must be off, on or agents, not "+echoName(p.Mode), &VerbHint{
			Param:    "mode",
			Accepted: config.KeepAwakeModes,
		})
	}
	d.SetKeepAwake(mode)
	// The loop takes or drops the hold on its own goroutine. Wait for it
	// here, so the answer says what is true now.
	d.awake.settle()
	out := keepAwakeResult(d.awake.status())
	if err := saveKeepAwake(mode); err != nil {
		out["saved"] = false
		out["save_error"] = err.Error()
		log.Printf("Keep awake: config.toml was not written: %v", err)
	} else {
		out["saved"] = true
	}
	return out, nil
}

// saveKeepAwake writes daemon.keep_awake to config.toml. The save writes only
// that key. The daemon's own config watch then reads the same value again,
// which changes nothing.
func saveKeepAwake(mode string) error {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return err
	}
	if cfg.Daemon.KeepAwake == mode {
		return nil
	}
	cfg.Daemon.KeepAwake = mode
	_, err = config.SaveUserConfig(cfg)
	return err
}

func keepAwakeResult(st awakeStatus) map[string]any {
	out := map[string]any{
		"type":    "keep_awake",
		"mode":    st.Mode,
		"held":    st.Held,
		"working": st.Working,
		"method":  st.Method,
	}
	if st.Error != "" {
		out["error"] = st.Error
	}
	return out
}
