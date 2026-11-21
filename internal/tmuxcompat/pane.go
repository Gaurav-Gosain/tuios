package tmuxcompat

import (
	"bufio"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// RespawnRequest is what respawn-pane sends a pane's holder.
type RespawnRequest struct {
	// Window is the tuios window the request is for. The holder refuses a
	// request for any other, so two windows whose numbers collide cannot
	// respawn each other.
	Window string `json:"window"`
	// Command follows tmux: empty reruns the pane's first command, one word
	// is a shell command line, several are an argv.
	Command []string `json:"command,omitempty"`
	// Cwd is the directory to start in. Empty keeps the holder's.
	Cwd string `json:"cwd,omitempty"`
	// Env is extra KEY=VALUE for the new process.
	Env []string `json:"env,omitempty"`
}

type respawnReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// RequestRespawn asks the holder of a pane to replace its process.
func RequestRespawn(dir, windowID string, req RespawnRequest) error {
	path := paneSocket(dir, windowID)
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return errfmt.Errorf("pane %s was not opened through the tmux shim, so it has no holder to respawn it", PaneID(windowID))
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req.Window = windowID
	line, err := jsonx.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return errfmt.Errorf("send the request to pane %s: %w", PaneID(windowID), err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return errfmt.Errorf("pane %s did not answer: %w", PaneID(windowID), err)
	}
	var r respawnReply
	if err := jsonx.Unmarshal(reply, &r); err != nil {
		return errfmt.Errorf("pane %s answered %q", PaneID(windowID), strings.TrimSpace(string(reply)))
	}
	if !r.OK {
		return errfmt.Errorf("pane %s: %s", PaneID(windowID), r.Error)
	}
	return nil
}

// PaneOptions configures one holder.
type PaneOptions struct {
	// Dir is the shim's runtime directory.
	Dir string
	// Window is the tuios window the holder runs in (TUIOS_PANE_ID).
	Window string
	// Command is the pane's command, read as tmux reads it.
	Command []string
	// Env is extra KEY=VALUE for every process the holder starts.
	Env []string
	// Shell runs a pane with no command, as a login shell.
	Shell string
	// Authorize, when set, decides whether the process with peerPID, started
	// at peerStart (0 when unknown), may respawn this pane. The holder asks it for every request on its socket
	// before it acts, because a process can dial the socket directly and
	// skip the check the shim makes (mayRespawn). peerPID is 0 where the
	// kernel does not give it. The tuios tmux-pane command sets it to
	// RespawnAllowed.
	Authorize func(peerPID int, peerStart uint64) error
}

func paneEnv(base []string, dir, window string, extra []string) []string {
	set := map[string]string{
		"TMUX":      TmuxValue(dir, os.Getpid()),
		"TMUX_PANE": PaneID(window),
	}
	path := BinDir(dir)
	for _, kv := range base {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			if v != path && !strings.HasPrefix(v, path+string(os.PathListSeparator)) {
				path += string(os.PathListSeparator) + v
			} else {
				path = v
			}
		}
	}
	set["PATH"] = path
	out := make([]string, 0, len(base)+len(set)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "PATH"} {
		out = append(out, k+"="+set[k])
	}
	return mergeEnv(out, extra)
}

func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	keys := map[string]bool{}
	for _, kv := range extra {
		k, _, set := strings.Cut(kv, "=")
		if !set {
			k = strings.TrimPrefix(k, "-")
		}
		keys[k] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !keys[k] {
			out = append(out, kv)
		}
	}
	for _, kv := range extra {
		if strings.Contains(kv, "=") {
			out = append(out, kv)
		}
	}
	return out
}
