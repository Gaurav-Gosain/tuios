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

type awakeKeeper struct {
	mode      atomic.Value
	kick      chan struct{}
	settleReq chan chan struct{}
	count     func() int
	publish   func(streamEvent)

	mu      sync.Mutex
	hold    awakeHolder
	held    bool
	working int
	failure string
	cancel  context.CancelFunc
	done    chan struct{}
	lost    chan error
}

func newAwakeKeeper(count func() int, publish func(streamEvent)) *awakeKeeper {
	k := &awakeKeeper{kick: make(chan struct{}, 1), settleReq: make(chan chan struct{}), count: count, publish: publish, lost: make(chan error, 1)}
	k.mode.Store(config.KeepAwakeOff)
	return k
}

func (k *awakeKeeper) Mode() string {
	m, _ := k.mode.Load().(string)
	return m
}

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

func (k *awakeKeeper) why() string {
	if k.Mode() == config.KeepAwakeOn {
		return "daemon.keep_awake is on"
	}
	return "An agent works"
}

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
		working = k.count()
	}
	return awakeStatus{Mode: k.Mode(), Held: k.held, Working: working, Method: awakeMethod(), Error: k.failure}
}

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

func (d *Daemon) verbGetKeepAwake(_ *connState, _ json.RawMessage) (any, *verbError) {
	d.awake.settle()
	return keepAwakeResult(d.awake.status()), nil
}

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
