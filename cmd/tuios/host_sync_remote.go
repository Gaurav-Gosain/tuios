package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The reading side of `tuios hosts sync`: the probe of a host's binary and
// the reading of its daemon. The scripts and the runner that carries them are
// in internal/hostsync.

// probeHostBinary runs the probe script and fills in what it found.
func probeHostBinary(ctx context.Context, t *syncTarget) error {
	arg, plain := hostsync.CommandArg(t.host.Command)
	if !plain {
		t.note("The host has a command set in the config that is not one path. Sync looks for tuios the way the link does without one.")
	}
	out, stderr, err := t.runner.Run(ctx, hostsync.ProbeScript(), []string{arg}, nil)
	if err != nil {
		return hostsync.RemoteRunError("could not read the host", stderr, err)
	}
	f := hostsync.ParseFacts(out)
	goos, goarch, err := hostsync.GoPlatform(f.Get("os"), f.Get("arch"))
	t.res.OS, t.res.Arch = goos, goarch
	if t.res.OS == "" {
		t.res.OS = strings.ToLower(f.Get("os"))
		t.res.Arch = f.Get("arch")
	}
	if err != nil {
		return err
	}
	t.home = f.Get("home")
	t.configured = f.Get("configured")
	t.res.Path = f.Get("path")
	t.symlink = f.Has("symlink")
	t.dirWritable = f.Has("dirw")
	t.sha = f.Get("sha")
	t.res.Service = f.Get("svc")
	if vers := f["ver"]; len(vers) > 0 {
		t.res.Before, t.res.BeforeBackend = hostsync.ParseVersionLine(vers[0])
	}
	if t.res.Path != "" && t.res.Before == "" {
		t.note("The tuios on the host did not report a version.")
	}
	return nil
}

// daemonCaller runs one verb on the machine's daemon.
type daemonCaller func(ctx context.Context, verb string, params any) (json.RawMessage, error)

// readHostDaemon reads the daemon on a host over a link this process opens,
// the way 'tuios hosts test' does. The link runs the host's own tuios, so
// nothing here starts a daemon there.
func readHostDaemon(ctx context.Context, t *syncTarget) {
	table, _ := federation.NewTable([]federation.Host{t.host})
	m := federation.New(table, federation.Options{
		Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
		ClientName:      "tuios-hosts-sync",
		ClientVersion:   version,
		VerbProtocol:    session.VerbProtocolVersion,
		MinVerbProtocol: session.MinVerbProtocolVersion,
	})
	dialCtx, cancel := context.WithTimeout(ctx, linkSettleBudget(t.host))
	defer cancel()
	m.Start(dialCtx)
	defer m.Stop()
	reports := m.Reports(dialCtx)
	if len(reports) == 0 {
		t.res.Daemon.State = daemonUnknown
		t.daemonNote = "The link to the host did not report a state."
		return
	}
	r := reports[0]
	switch r.Status {
	case federation.StatusUp:
		t.res.Daemon.State = daemonRunning
		t.res.Daemon.Version = r.DaemonVersion
		t.res.Daemon.PID = r.PID
		t.res.Daemon.SessionCount = r.Sessions
		call := func(ctx context.Context, verb string, params any) (json.RawMessage, error) {
			return m.Call(ctx, t.name, verb, params)
		}
		readDaemonSessions(ctx, t, call)
	case federation.StatusNoDaemon, federation.StatusNoBinary:
		t.res.Daemon.State = daemonStopped
	case federation.StatusIncompatible:
		t.res.Daemon.State = daemonRunning
		t.res.Daemon.Version = r.DaemonVersion
		t.res.Daemon.PID = r.PID
		t.res.Daemon.SessionCount = r.Sessions
		t.res.Daemon.Unread = true
		t.note("The daemon speaks a control protocol this build does not serve, so its sessions could not be read.")
	default:
		t.res.Daemon.State = daemonUnknown
		reason := r.Reason
		if reason == "" {
			reason = "The link did not come up."
		}
		t.daemonNote = "The daemon could not be read. " + reason
	}
}

// linkSettleBudget is how long the link to one host may take to come up.
func linkSettleBudget(h federation.Host) time.Duration {
	timeout := h.ConnectTimeout
	if timeout <= 0 {
		timeout = federation.DefaultConnectTimeout
	}
	return timeout + 10*time.Second
}

// readLocalDaemon reads this machine's daemon over its socket.
func readLocalDaemon(ctx context.Context, t *syncTarget) {
	if !session.IsDaemonRunning() {
		t.res.Daemon.State = daemonStopped
		return
	}
	t.res.Daemon.State = daemonRunning
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		if mismatch, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
			t.res.Daemon.Version = mismatch.DaemonVersion
			t.res.Daemon.PID = mismatch.DaemonPID
			t.res.Daemon.SessionCount = mismatch.Sessions
			t.res.Daemon.Unread = true
			t.note("The daemon speaks a control protocol this build does not serve, so its sessions could not be read.")
			return
		}
		t.res.Daemon.State = daemonUnknown
		t.note("The daemon could not be read: " + err.Error())
		return
	}
	defer func() { _ = client.Close() }()
	call := func(_ context.Context, verb string, params any) (json.RawMessage, error) {
		return client.Call(verb, params)
	}
	raw, err := call(ctx, "hello", map[string]any{"client": "tuios-hosts-sync", "version": version, "protocol": session.VerbProtocolVersion})
	if err == nil {
		var hs struct {
			DaemonVersion string `json:"daemon_version"`
			PID           int    `json:"pid"`
			Sessions      int    `json:"sessions"`
		}
		if json.Unmarshal(raw, &hs) == nil {
			t.res.Daemon.Version = hs.DaemonVersion
			t.res.Daemon.PID = hs.PID
			t.res.Daemon.SessionCount = hs.Sessions
		}
	}
	readDaemonSessions(ctx, t, call)
}

// readDaemonSessions lists the daemon's sessions and the panes in each that
// run a program. A pane counts as running one when its terminal's foreground
// process is not the pane's own shell, or when the pane's own process is not
// a shell at all.
func readDaemonSessions(ctx context.Context, t *syncTarget, call daemonCaller) {
	raw, err := call(ctx, "list-sessions", nil)
	if err != nil {
		t.res.Daemon.Unread = true
		t.note("The daemon did not list its sessions: " + plainLine(err.Error()))
		return
	}
	var list struct {
		Sessions []struct {
			Name string `json:"name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.res.Daemon.Unread = true
		return
	}
	type paneRef struct {
		session int
		window  string
		facts   paneFacts
	}
	var refs []paneRef
	var pids []string
	sessions := make([]syncSession, 0, len(list.Sessions))
	for _, s := range list.Sessions {
		ss := syncSession{Name: plainLine(s.Name)}
		raw, err := call(ctx, "list-windows", map[string]any{"session": s.Name})
		if err == nil {
			var wl struct {
				Windows []struct {
					DisplayName string `json:"display_name"`
					WindowID    string `json:"window_id"`
					PID         int    `json:"pid"`
					AtPrompt    *bool  `json:"at_prompt"`
					Running     string `json:"running_cmdline"`
					AgentState  string `json:"agent_state"`
					Resumable   string `json:"agent_resumable"`
				} `json:"windows"`
			}
			if json.Unmarshal(raw, &wl) == nil {
				ss.Panes = len(wl.Windows)
				live := map[string]string{}
				for _, w := range wl.Windows {
					name := plainLine(w.DisplayName)
					if name == "" {
						name = shortWindowID(w.WindowID)
					}
					if w.Resumable != "" {
						ss.Resume = append(ss.Resume, syncAgent{Window: name, Harness: plainLine(w.Resumable)})
					} else if w.AgentState != "" && w.AgentState != "none" {
						live[w.WindowID] = name
					}
					f := paneFacts{pid: w.PID, running: plainLine(w.Running)}
					if w.AtPrompt != nil {
						f.atPrompt, f.promptKnown = *w.AtPrompt, true
					}
					refs = append(refs, paneRef{session: len(sessions), window: name, facts: f})
					if w.PID > 0 {
						pids = append(pids, strconv.Itoa(w.PID))
					}
				}
				// A daemon of another build may not mark the panes, so the
				// saved files answer for it.
				if t.local && t.res.Daemon.Version != version {
					ss.Resume = append(ss.Resume, resumableAgents(s.Name, live)...)
				}
			}
		}
		sessions = append(sessions, ss)
	}
	procs := map[int]paneProc{}
	if len(pids) > 0 {
		out, _, err := t.runner.Run(ctx, hostsync.PaneScript(), pids, nil)
		if err == nil {
			procs = parsePaneProcs(out)
		}
	}
	for _, r := range refs {
		if program, busy := paneBusy(r.facts, procs); busy {
			sessions[r.session].Busy = append(sessions[r.session].Busy, syncPane{Window: r.window, Program: program})
		}
	}
	t.res.Daemon.Sessions = sessions
	t.res.Daemon.SessionCount = len(sessions)
}

// paneFacts is what the daemon said about one pane.
type paneFacts struct {
	pid         int
	running     string
	atPrompt    bool
	promptKnown bool
}

// paneProc is what ps said about one pane's process.
type paneProc struct {
	tpgid      int
	comm       string
	foreground string
}

func parsePaneProcs(out string) map[int]paneProc {
	procs := map[int]paneProc{}
	for _, line := range hostsync.ParseFacts(out)["pane"] {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		tpgid, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		procs[pid] = paneProc{tpgid: tpgid, comm: strings.TrimSpace(parts[2]), foreground: strings.TrimSpace(parts[3])}
	}
	return procs
}

// shellNames are the programs a pane runs at its prompt.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"mksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true,
	"pwsh": true, "ash": true, "yash": true, "oksh": true,
}

func programName(comm string) string {
	return strings.TrimPrefix(path.Base(strings.TrimSpace(comm)), "-")
}

// paneBusy decides whether a pane runs a program, and which.
func paneBusy(f paneFacts, procs map[int]paneProc) (string, bool) {
	if p, ok := procs[f.pid]; ok && f.pid > 0 {
		own := programName(p.comm)
		if p.tpgid > 0 && p.tpgid != f.pid {
			if fg := programName(p.foreground); fg != "" {
				return fg, true
			}
			return "a program", true
		}
		if !shellNames[own] {
			return own, true
		}
		return "", false
	}
	// No process to read: what the shell integration said, when it said it.
	if f.running != "" {
		return f.running, true
	}
	if f.promptKnown && !f.atPrompt {
		return "a program", true
	}
	return "", false
}
