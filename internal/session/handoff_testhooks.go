package session

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Test-only verbs for the live handoff, registered only when the daemon starts
// with TUIOS_TEST_HANDOFF=1. The end-to-end tests drive the primitives through
// them before the user-facing command exists, and drive the controls through
// them after: a hold between writing the manifest and the exec, and a fork in
// the span where the descriptors are exposed. No build is meant to ship with
// the variable set, and a daemon without it does not know these verbs.

// handoffTestEnv enables the verbs.
const handoffTestEnv = "TUIOS_TEST_HANDOFF"

// registerHandoffTestVerbs adds the verbs, once, when the variable asks. It
// runs from NewDaemon: the registry is built by an init of its own, which an
// init here could run before.
func registerHandoffTestVerbs() {
	if os.Getenv(handoffTestEnv) != "1" {
		return
	}
	handoffTestOnce.Do(addHandoffTestVerbs)
}

var handoffTestOnce sync.Once

func addHandoffTestVerbs() {
	reg := verbRegistry()
	reg["test-handoff"] = verbEntry{
		description: "Test only: hand every pane to a new image of this binary, in place.",
		params: []verbParam{
			{Name: "binary", Type: "string", Description: "The binary to exec. Omit for this one."},
			{Name: "hold_ms", Type: "int", Description: "Wait this long after the manifest is written, before the clients are closed."},
			{Name: "fork_in_window", Type: "bool", Description: "Start a long-lived child while the descriptors are exposed."},
			{Name: "swap_in_window", Type: "[]string", Description: "Two folders to swap by renames while the descriptors are exposed."},
			{Name: "swap_after_check", Type: "[]string", Description: "Two folders to swap by renames between the preflight's checks and its run."},
		},
		handler: (*Daemon).verbTestHandoff,
	}
	reg["test-handoff-stats"] = verbEntry{
		description: "Test only: what the handoff that made this image measured.",
		handler:     (*Daemon).verbTestHandoffStats,
	}
	reg["test-quiesce"] = verbEntry{
		description: "Test only: quiesce one pane and resume it, cycles times.",
		params: []verbParam{
			sessionParam, windowParam,
			{Name: "cycles", Type: "int", Description: "How many times."},
			{Name: "hold_ms", Type: "int", Description: "How long each quiesce lasts."},
		},
		handler: (*Daemon).verbTestQuiesce,
	}
}

func (d *Daemon) verbTestHandoff(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Binary         string   `json:"binary"`
		HoldMS         int      `json:"hold_ms"`
		ForkInWindow   bool     `json:"fork_in_window"`
		SwapInWindow   []string `json:"swap_in_window"`
		SwapAfterCheck []string `json:"swap_after_check"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, err.Error())
		}
		p.Binary = exe
	}
	req := handoffRequest{binary: p.Binary, holdAfterWrite: time.Duration(p.HoldMS) * time.Millisecond}
	if p.ForkInWindow {
		req.inWindow = func() {
			//tuios:allow-exec a test-only child that must not inherit the exposed descriptors
			go func() { _ = exec.Command("sleep", "20.4242").Start() }()
			time.Sleep(300 * time.Millisecond)
		}
	}
	for _, swap := range []struct {
		name  string
		pair  []string
		field *func()
	}{{"swap_in_window", p.SwapInWindow, &req.inWindow}, {"swap_after_check", p.SwapAfterCheck, &req.afterCheck}} {
		if swap.pair == nil {
			continue
		}
		if len(swap.pair) != 2 {
			return nil, invalidParam(swap.name, swap.name+" must be two folder paths")
		}
		*swap.field = swapFolders(swap.pair[0], swap.pair[1])
	}
	run, err := d.prepareHandoff(req)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	cs.takeover = func(*bufio.Reader) { _ = run.commit() }
	return map[string]any{"pid": os.Getpid(), "panes": run.panes, "manifest_bytes": run.manifestBytes}, nil
}

// swapFolders swaps folders a and b with three renames.
func swapFolders(a, b string) func() {
	return func() {
		for _, mv := range [][2]string{{a, a + ".swap"}, {b, a}, {a + ".swap", b}} {
			if err := os.Rename(mv[0], mv[1]); err != nil {
				LogError("test-handoff: swap %s and %s: %v", a, b, err)
				return
			}
		}
	}
}

func (d *Daemon) verbTestHandoffStats(_ *connState, _ json.RawMessage) (any, *verbError) {
	st := d.lastHandoff.Load()
	if st == nil {
		return map[string]any{"handoff": false}, nil
	}
	return map[string]any{"handoff": true, "stats": st}, nil
}

func (d *Daemon) verbTestQuiesce(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Cycles  int    `json:"cycles"`
		HoldMS  int    `json:"hold_ms"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	var read int64
	for range max(p.Cycles, 1) {
		ctx, cancel := context.WithTimeout(d.ctx, handoffQuiesceTimeout)
		err := pty.Quiesce(ctx)
		cancel()
		if err != nil {
			pty.Resume()
			return nil, newVerbError(ErrVerbInternal, err.Error())
		}
		before := pty.outputSeqNow()
		time.Sleep(time.Duration(p.HoldMS) * time.Millisecond)
		read += pty.outputSeqNow() - before
		pty.Resume()
	}
	return map[string]any{"cycles": max(p.Cycles, 1), "read_while_quiesced": read}, nil
}

// outputSeqNow is the number of bytes the pane has produced.
func (p *PTY) outputSeqNow() int64 {
	p.outputMu.RLock()
	defer p.outputMu.RUnlock()
	return p.outputSeq
}
