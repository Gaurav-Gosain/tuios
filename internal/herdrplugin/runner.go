package herdrplugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Bounds on what plugins run, as herdr has them where herdr has one.
const (
	// OutputCap is how much of each of stdout and stderr a log keeps.
	OutputCap = 64 * 1024
	// LogLimit is how many runs the log keeps, oldest dropped first.
	LogLimit = 200
	// MaxInFlight is how many commands run at once, all plugins together.
	MaxInFlight = 32
	// MaxPerPlugin is how many commands of one plugin run at once. herdr has
	// no such bound. It keeps one plugin's event storm from taking every
	// slot.
	MaxPerPlugin = 8
	// DefaultTimeout is how long an action or event command may run before
	// its process group is killed. A startup command has no timeout: it is
	// often a service that runs for as long as the plugin is on.
	DefaultTimeout = 10 * time.Minute
	// waitDelay is how long the runner reads a command's output after the
	// command exits, when a process it left behind holds the output open.
	waitDelay = 2 * time.Second
)

// Log statuses.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// LogEntry is one run, in herdr's PluginCommandLogInfo shape.
type LogEntry struct {
	LogID      string   `json:"log_id"`
	PluginID   string   `json:"plugin_id"`
	ActionID   string   `json:"action_id,omitempty"`
	Event      string   `json:"event,omitempty"`
	Command    []string `json:"command"`
	Status     string   `json:"status"`
	StartedMS  int64    `json:"started_unix_ms"`
	FinishedMS *int64   `json:"finished_unix_ms,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	Stdout     *string  `json:"stdout,omitempty"`
	Stderr     *string  `json:"stderr,omitempty"`
	Error      *string  `json:"error,omitempty"`
}

// Job is one command to run for a plugin.
type Job struct {
	Plugin   *Plugin
	ActionID string
	Event    string
	Command  []string
	// Env is KEY=VALUE pairs set on top of the daemon's environment.
	Env []string
	// Timeout kills the process group after this long. Zero is no timeout.
	Timeout time.Duration
	// Release, when set, is called once: when the run ends and nothing it
	// started is left, or when Start refuses the job. It drops herdr's lease
	// on the plugin's install (HoldLease).
	Release func()
}

// Runner runs plugin commands and keeps their log.
type Runner struct {
	mu       sync.Mutex
	logs     []LogEntry
	next     int
	inFlight int
	perPlug  map[string]int
	procs    map[string]map[*exec.Cmd]*procGroup
	stopped  bool
	wg       sync.WaitGroup
	// owners are the plugins whose commands lead each process session, by
	// the session id, which is the leader's pid. With a reaper the leader
	// is the reaper, which lives until nothing the command started is left,
	// so what a command leaves behind is still its plugin's. native counts
	// the native plugins among them.
	owners map[int]*Plugin
	native atomic.Int32
	// Reaper, when set, is the argv that runs a command under a reaper:
	// the tuios binary and ReaperArg (reaper.go). Without it the command
	// leads its own session, and its entry goes when it exits.
	Reaper []string
	// OnPID, when set, is told each process the runner starts and ends, so
	// the daemon can tell a plugin's process from a person's.
	OnPID func(pid int, running bool)
	// Hold, when set, is held from a command's start until its output is
	// read: the daemon does not hand off while a pipe it reads is open. A
	// command it refuses is not started.
	Hold func(pluginID string) (release func(), ok bool)
}

// NewRunner returns an empty runner.
func NewRunner() *Runner {
	return &Runner{perPlug: map[string]int{}, procs: map[string]map[*exec.Cmd]*procGroup{}, owners: map[int]*Plugin{}}
}

// ResolveProgram finds a command's program as herdr does: a relative path
// with a separator is taken from the plugin root, a bare name is looked up
// on PATH, and an absolute path is used as written. The program must exist,
// so a missing build fails here with a message that names it. On Windows a
// path is resolved as exec.Command resolves it, with the PATHEXT
// extensions, as herdr and Windows itself do.
func ResolveProgram(program, root string) (string, *Error) {
	switch {
	case filepath.IsAbs(program):
	case strings.ContainsRune(program, '/') || (filepath.Separator == '\\' && strings.ContainsRune(program, '\\')):
		program = filepath.Join(root, program)
	default:
		found, err := exec.LookPath(program)
		if err != nil {
			return "", errf("plugin_command_not_found", "%s is not on PATH", program)
		}
		return found, nil
	}
	if found, ok := withExtension(program); ok {
		return found, nil
	}
	info, err := os.Stat(program)
	if err != nil {
		return "", errf("plugin_command_not_found", "%s does not exist. If the plugin has a build step, run tuios plugins build", program)
	}
	if info.IsDir() {
		return "", errf("plugin_command_not_found", "%s is a folder, not a program", program)
	}
	return program, nil
}

// Start runs a job in the background and returns its log entry, status
// running. The process has no terminal: a session of its own, stdin closed,
// stdout and stderr kept up to OutputCap each. A start refused by a bound
// is logged as failed and returned as an error.
func (r *Runner) Start(j Job) (LogEntry, *Error) {
	lease := func() {}
	if j.Release != nil {
		lease = sync.OnceFunc(j.Release)
	}
	now := time.Now().UnixMilli()
	r.mu.Lock()
	r.next++
	e := LogEntry{LogID: fmt.Sprintf("plugin-log-%d", r.next), PluginID: j.Plugin.PluginID, ActionID: j.ActionID, Event: j.Event, Command: j.Command, Status: StatusRunning, StartedMS: now}
	refuse := func(code, msg string) (LogEntry, *Error) {
		e.Status, e.FinishedMS, e.Error = StatusFailed, &now, &msg
		empty := ""
		e.Stdout, e.Stderr = &empty, &empty
		r.pushLocked(e)
		r.mu.Unlock()
		lease()
		return e, errf(code, "%s", msg)
	}
	switch {
	case r.stopped:
		return refuse("plugin_host_stopped", "the plugin host is stopping")
	case r.inFlight >= MaxInFlight:
		return refuse("plugin_command_limit_reached", fmt.Sprintf("maximum concurrent plugin commands reached (%d)", MaxInFlight))
	case j.Plugin.Format == FormatTuios && !kernelPlacesCallers:
		return refuse("plugin_unsupported_platform", "tuios cannot hold a native plugin to its grants on "+runtime.GOOS+" yet, so it does not run its commands")
	case r.perPlug[j.Plugin.PluginID] >= MaxPerPlugin:
		return refuse("plugin_command_limit_reached", fmt.Sprintf("plugin %s already runs %d commands", j.Plugin.PluginID, MaxPerPlugin))
	}
	program, perr := ResolveProgram(j.Command[0], j.Plugin.PluginRoot)
	if perr != nil {
		return refuse(perr.Code, perr.Msg)
	}
	release := func() {}
	if r.Hold != nil {
		rel, ok := r.Hold(j.Plugin.PluginID)
		if !ok {
			return refuse("plugin_host_upgrading", "the daemon is being upgraded. Try again in a moment")
		}
		release = rel
		failed := refuse
		refuse = func(code, msg string) (LogEntry, *Error) {
			release()
			return failed(code, msg)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	argv := append([]string{program}, j.Command[1:]...)
	if len(r.Reaper) > 0 {
		argv = append(slices.Clone(r.Reaper), argv...)
	}
	//tuios:allow-exec detach sets CREATE_NO_WINDOW with the flags the job needs
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // an enabled plugin's own manifest names the command
	cmd.Dir = j.Plugin.PluginRoot
	cmd.Env = append(cleanEnv(os.Environ()), j.Env...)
	if len(r.Reaper) > 0 {
		cmd.Env = reaperEnv(cmd.Env)
	}
	cmd.Stdin = nil
	var stdout, stderr capBuffer
	stdout.cap, stderr.cap = OutputCap, OutputCap
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A process the command leaves behind can hold its output open. Wait
	// then stops reading a moment after the command exits, so the run ends
	// and frees its slot.
	cmd.WaitDelay = waitDelay
	detach(cmd)
	// The reaper says on fd 3 when the command itself exits.
	var status, statusW *os.File
	if len(r.Reaper) > 0 {
		var err error
		if status, statusW, err = os.Pipe(); err != nil {
			cancel()
			return refuse("plugin_command_failed", err.Error())
		}
		cmd.ExtraFiles = []*os.File{statusW}
	}
	err := cmd.Start()
	if statusW != nil {
		_ = statusW.Close()
	}
	if err != nil {
		if status != nil {
			_ = status.Close()
		}
		cancel()
		return refuse("plugin_command_failed", err.Error())
	}
	group, terr := track(cmd)
	if terr != nil {
		group.kill()
		_ = cmd.Wait()
		group.release()
		cancel()
		return refuse("plugin_command_failed", terr.Error())
	}
	r.inFlight++
	r.perPlug[j.Plugin.PluginID]++
	if r.procs[j.Plugin.PluginID] == nil {
		r.procs[j.Plugin.PluginID] = map[*exec.Cmd]*procGroup{}
	}
	r.procs[j.Plugin.PluginID][cmd] = group
	// Still under r.mu, which Owner takes: the command cannot be asked
	// about before it is entered here.
	r.setOwner(cmd.Process.Pid, j.Plugin)
	r.pushLocked(e)
	r.wg.Add(1)
	onPID := r.OnPID
	r.mu.Unlock()
	if onPID != nil {
		onPID(cmd.Process.Pid, true)
	}

	// The session's leader is reaped in a goroutine of its own. The owner
	// entry goes before the pid is free again (waitExited), so it never
	// names a process that got the pid later.
	pid := cmd.Process.Pid
	reaped := make(chan error, 1)
	go func() {
		waitExited(pid)
		r.mu.Lock()
		r.dropOwner(pid)
		r.mu.Unlock()
		reaped <- cmd.Wait()
	}()

	go func() {
		defer r.wg.Done()
		defer cancel()
		if j.Timeout > 0 {
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(j.Timeout):
					group.kill()
				}
			}()
		}
		code, text, done, failed := -1, "", false, false
		var werr error
		if status != nil {
			// The command exits first. What it left keeps the reaper and
			// the output open, as it did the command's: read the output a
			// moment longer, then end the run and free its slot.
			b, _ := io.ReadAll(status)
			_ = status.Close()
			if c, t, ok := strings.Cut(strings.TrimSpace(string(b)), "\t"); ok {
				code, _ = strconv.Atoi(c)
				text = t
				select {
				case werr = <-reaped:
					done = true
				case <-time.After(waitDelay):
				}
			}
		}
		if text == "" {
			werr, done = <-reaped, true
			code = cmd.ProcessState.ExitCode()
			if werr != nil {
				text, failed = werr.Error(), true
			}
		}
		// Stop the timeout before anything else: once the leader is
		// reaped, its process group id may name another group.
		cancel()
		end := time.Now().UnixMilli()
		out, errOut := stdout.String(), stderr.String()
		r.mu.Lock()
		r.inFlight--
		r.perPlug[j.Plugin.PluginID]--
		for i := range r.logs {
			if r.logs[i].LogID != e.LogID {
				continue
			}
			l := &r.logs[i]
			l.FinishedMS, l.Stdout, l.Stderr = &end, &out, &errOut
			if code >= 0 {
				l.ExitCode = &code
			}
			l.Status = StatusSucceeded
			if code != 0 || failed {
				l.Status = StatusFailed
				if code < 0 {
					msg := text
					l.Error = &msg
				}
			}
		}
		r.mu.Unlock()
		if !done {
			werr = <-reaped
		}
		_ = werr
		if onPID != nil {
			onPID(pid, false)
		}
		r.mu.Lock()
		delete(r.procs[j.Plugin.PluginID], cmd)
		group.release()
		r.mu.Unlock()
		release()
		lease()
	}()
	return e, nil
}

// setOwner enters the plugin of session sid. r.mu is held.
func (r *Runner) setOwner(sid int, p *Plugin) {
	r.dropOwner(sid)
	r.owners[sid] = p
	if p.Format == FormatTuios {
		r.native.Add(1)
	}
}

// dropOwner forgets session sid. r.mu is held.
func (r *Runner) dropOwner(sid int) {
	if p, ok := r.owners[sid]; ok {
		delete(r.owners, sid)
		if p.Format == FormatTuios {
			r.native.Add(-1)
		}
	}
}

// Owner is the plugin whose command leads process session sid, or nil. The
// caller reads sid from the kernel (procinfo.SessionID). A process that
// starts a session of its own leaves its plugin.
func (r *Runner) Owner(sid int) *Plugin {
	if sid <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owners[sid]
}

// NativeRunning reports whether a native plugin's processes may be running,
// so a call has to be checked against its grants.
func (r *Runner) NativeRunning() bool { return r.native.Load() > 0 }

func (r *Runner) pushLocked(e LogEntry) {
	r.logs = append(r.logs, e)
	if n := len(r.logs) - LogLimit; n > 0 {
		r.logs = append([]LogEntry(nil), r.logs[n:]...)
	}
}

// Logs is the last limit runs, oldest first, of one plugin or of all when
// pluginID is "". A limit of 0 is 50, and a limit is clamped to 1..200, as
// herdr's plugin.log.list does.
func (r *Runner) Logs(pluginID string, limit int) []LogEntry {
	if limit == 0 {
		limit = 50
	}
	limit = min(max(limit, 1), LogLimit)
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []LogEntry
	for i := len(r.logs) - 1; i >= 0 && len(out) < limit; i-- {
		if pluginID == "" || r.logs[i].PluginID == pluginID {
			out = append(out, r.logs[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// StopPlugin kills every process group a plugin's commands run in. A
// disabled plugin runs nothing.
func (r *Runner) StopPlugin(pluginID string) {
	r.mu.Lock()
	var groups []*procGroup
	for _, g := range r.procs[pluginID] {
		groups = append(groups, g)
	}
	r.mu.Unlock()
	for _, g := range groups {
		g.kill()
	}
}

// StopAll kills every plugin process, refuses new ones, and waits up to
// wait for them to be reaped.
func (r *Runner) StopAll(wait time.Duration) {
	r.mu.Lock()
	r.stopped = true
	var ids []string
	for id := range r.procs {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.StopPlugin(id)
	}
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait):
	}
}

// cleanEnv drops the variables that would name a pane or a plugin the
// command does not belong to. The daemon's own environment can carry them
// when the daemon was started from a pane.
func cleanEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "HERDR_") || k == "TUIOS_PANE_ID" || k == "TUIOS_WINDOW_ID" || k == "TUIOS_PANE_TOKEN" || k == "TUIOS_SESSION" || k == "TUIOS_SOCKET" || k == "TUIOS_PLUGIN_ID" || k == "TUIOS_PLUGIN_TOKEN" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// capBuffer keeps the first cap bytes written to it and marks the rest as
// cut, as herdr's read_capped_plugin_output does.
type capBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.cap - b.buf.Len()
	if room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	if len(p) > room {
		b.truncated = true
	}
	return len(p), nil
}

func (b *capBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.ToValidUTF8(b.buf.String(), "�")
	if b.truncated {
		s += fmt.Sprintf("\n[tuios truncated plugin output after %d bytes]", b.cap)
	}
	return s
}

var _ io.Writer = (*capBuffer)(nil)
