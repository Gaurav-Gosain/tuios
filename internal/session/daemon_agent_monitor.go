//go:build !slim

package session

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// setupFederation builds the host table and the link manager. Nothing is dialed
// here; Start launches the supervisors.
//
// The manager is built even with no hosts configured, which is the default. It
// costs a struct and no goroutine, and it is what lets a host added later reach
// a running daemon: a nil manager would have to be built from the config
// reload, and the verbs read the pointer without a lock.
func (d *Daemon) setupFederation(hosts []federation.Host) {
	table, problems := federation.NewTable(hosts)
	for _, p := range problems {
		d.federationProblems = append(d.federationProblems, p.Error())
		log.Printf("[FEDERATION] %v", p)
	}
	dial := d.hostDial
	if dial == nil {
		// TUIOS_SSH names the ssh program to run. It exists for a machine where
		// ssh is not on the daemon's PATH, and it is what lets the link layer be
		// exercised end to end without an ssh server.
		dial = federation.SSHDialer(os.Getenv("TUIOS_SSH"))
	}
	d.federation = federation.New(table, federation.Options{
		Dial:            dial,
		ClientName:      "tuios-daemon",
		ClientVersion:   d.version,
		VerbProtocol:    VerbProtocolVersion,
		MinVerbProtocol: MinVerbProtocolVersion,
		// The name every host this one links to resolves its policy for this
		// machine from. See link_policy.go.
		Self: linkSelfName(d.hostedPaneHostName()),
		Log: func(format string, args ...any) {
			log.Printf("[FEDERATION] "+format, args...)
		},
		OnStatus: d.fleet.onStatus,
	})
}

// resolveAgentStallTimeout picks the stall heuristic's silence window: an
// explicit positive config wins, a negative config disables the heuristic, and a
// zero config falls back to the TUIOS_AGENT_STALL_SECONDS environment override
// (0 or less there disables it) and finally to the default.
func resolveAgentStallTimeout(cfg time.Duration) time.Duration {
	if cfg > 0 {
		return cfg
	}
	if cfg < 0 {
		return 0
	}
	if s := os.Getenv("TUIOS_AGENT_STALL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			if n <= 0 {
				return 0
			}
			return time.Duration(n) * time.Second
		}
	}
	return defaultAgentStallTimeout
}

// resolveAgentBinaries returns the extra agent binary names to merge with the
// built-in defaults: the config list, plus the comma-separated
// TUIOS_AGENT_BINARIES environment override.
func resolveAgentBinaries(cfg []string) []string {
	extra := append([]string(nil), cfg...)
	if s := os.Getenv("TUIOS_AGENT_BINARIES"); s != "" {
		for name := range strings.SplitSeq(s, ",") {
			if name = strings.TrimSpace(name); name != "" {
				extra = append(extra, name)
			}
		}
	}
	return extra
}

// resolveAgentDetectInterval picks the auto-detector's poll interval and, with
// it, whether auto-detection runs at all. An explicit enable/disable from config
// wins; then an explicit positive interval wins; a negative interval disables it;
// a zero interval falls back to the TUIOS_AGENT_DETECT_SECONDS environment
// override (0 or less there disables it), and finally to the default. A returned
// zero means auto-detection is off.
func resolveAgentDetectInterval(enabled *bool, cfg time.Duration) time.Duration {
	if enabled != nil && !*enabled {
		return 0
	}
	if enabled == nil {
		if v := strings.TrimSpace(os.Getenv("TUIOS_AGENT_AUTODETECT")); v != "" {
			switch strings.ToLower(v) {
			case "0", "false", "no", "off":
				return 0
			}
		}
	}
	if cfg > 0 {
		return cfg
	}
	if cfg < 0 {
		return 0
	}
	if s := os.Getenv("TUIOS_AGENT_DETECT_SECONDS"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			if n <= 0 {
				return 0
			}
			return time.Duration(n) * time.Second
		}
	}
	return defaultAgentDetectInterval
}

// agentMonitor periodically resolves the foreground process of every pane and
// marks or clears a running agent, so the status glyph appears without the user
// running set-agent-state. It is strictly subordinate to explicit reports and to
// the stall heuristic (see Session.applyAgentDetection). It exits when the daemon
// context is cancelled, and does nothing at all when auto-detection is disabled.
func (d *Daemon) agentMonitor() {
	if d.agentDetectInterval <= 0 {
		return
	}
	ticker := time.NewTicker(d.agentDetectInterval)
	defer ticker.Stop()
	quietTicks := detectQuietTicks(d.agentDetectInterval)

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			reg := d.agentMatcher.registry
			now := time.Now().UnixNano()
			for _, sess := range d.manager.AllSessions() {
				due := func(ptyID string) bool {
					pty := sess.GetPTY(ptyID)
					return pty == nil || pty.detectScanDue(now, quietTicks)
				}
				sess.scanAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identifyDetail, due)
				// The transcript joins ride this tick rather than one of their
				// own. It runs only for a pane already known to be running a
				// harness that has a transcript and that has no join yet, so a
				// session that is fully joined, or that runs no agent at all,
				// does nothing here.
				sess.maintainAgentTranscripts(reg, d.paneAgentIdentifier(sess))
			}
		}
	}
}

// paneAgentIdentifier reports the working directory and build version of the
// agent in a pane, which is what a searched transcript candidate is checked
// against before it is believed.
func (d *Daemon) paneAgentIdentifier(sess *Session) func(ptyID string) (string, string) {
	resolve := d.foregroundResolver(sess)
	return func(ptyID string) (string, string) {
		info, running := resolve(ptyID)
		if !running {
			return "", ""
		}
		// The agent's own process, not the wrapper above it: a transcript is
		// checked against the agent's directory and build, and a shell that
		// launched it has neither.
		if det, ok := d.agentMatcher.identifyDetail(info); ok {
			info = det.proc
		}
		return paneAgentIdentity(info)
	}
}

// foregroundResolver returns the resolve function the agent detector and the
// output-driven exit probe share: the foreground process of a pane's controlling
// terminal, or not-running when the PTY is gone or has exited.
func (d *Daemon) foregroundResolver(sess *Session) func(ptyID string) (foregroundInfo, bool) {
	return func(ptyID string) (foregroundInfo, bool) {
		pty := sess.GetPTY(ptyID)
		if pty == nil || pty.IsExited() {
			return foregroundInfo{}, false
		}
		// A pane whose process is on another machine is asked about there.
		// Reading a pid here would read nothing, because there is no process
		// on this machine, and every tier that starts from the foreground
		// process would give up.
		//
		// The answer arrives in the same struct the local read produces, so
		// nothing downstream knows or cares which machine looked: the rules,
		// the manifests and the user's configuration stay here, with the
		// window. See remotePane.Foreground.
		if info, running, remote := pty.remoteForeground(); remote {
			return info, running
		}
		shellPID := pty.ShellPID()
		info, running := foregroundProcess(shellPID)
		// Stamped after the resolve, and outside the running check, because the
		// shell pid is a fact about the pane rather than about what the pane is
		// running: foregroundProcess reports not-running whenever it cannot read
		// the foreground group, and the shell is alive either way. See
		// WindowState.ShellPID.
		info.shellPID = shellPID
		return info, running
	}
}

// stallMonitor periodically applies the agent-state output-stall heuristic to
// every live session, demoting panes that reported working but have gone quiet
// to idle after the screen tier has had a last look at them. It is the fallback
// for agents that never report their own state and is strictly secondary to
// explicit reports (see Session.applyStallHeuristic). It exits when the daemon
// context is cancelled, and does nothing at all when the heuristic is disabled.
func (d *Daemon) stallMonitor() {
	if d.agentStallTimeout <= 0 {
		return
	}
	// Tick often enough to demote within a fraction of the timeout, but never
	// spin: at least once a second, at most once every ten.
	interval := min(max(d.agentStallTimeout/4, time.Second), 10*time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			reg := d.agentMatcher.registry
			for _, sess := range d.manager.AllSessions() {
				sess.applyStallHeuristic(now, d.agentStallTimeout, func(ptyID string) int64 {
					if pty := sess.GetPTY(ptyID); pty != nil {
						return pty.LastOutput()
					}
					return 0
				}, func(ptyID string) bool {
					// The last look before the pane is called idle. A stalled pane
					// emits nothing, so the scan the output path would have run is
					// the one that never happens.
					return sess.scanStalledPane(ptyID, reg)
				})
			}
		}
	}
}

// detectQuietTicks is how many ticks a quiet pane waits between reads at the
// given poll interval: agentDetectQuietBound in ticks, and never less than one,
// so a poll slower than the bound reads every pane every tick.
func detectQuietTicks(interval time.Duration) int32 {
	if interval <= 0 {
		return 1
	}
	n := int32(agentDetectQuietBound / interval)
	if n < 1 {
		return 1
	}
	return n
}

// detectScanDue reports whether the detection poll should read this pane's
// foreground process on this tick, and records the read if so. A pane that has
// produced output since its last read is due at once: typing a command echoes
// it, a program starting or exiting prints, a title change is output. A pane
// never read before is due at once, since a pane opened on a silent program
// has no output to flag it. A pane that has been silent is due every
// quietTicks ticks. now is taken before the read, so output that lands while
// the read runs makes the next tick due.
func (p *PTY) detectScanDue(now int64, quietTicks int32) bool {
	last := p.lastDetectScan.Load()
	if last == 0 || p.lastOutput.Load() > last || p.detectSkips.Add(1) >= quietTicks {
		p.lastDetectScan.Store(now)
		p.detectSkips.Store(0)
		return true
	}
	return false
}
