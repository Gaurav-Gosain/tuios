package tmuxcompat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// The pane holder.
//
// tmux's respawn-pane replaces a pane's process and keeps the pane: its id,
// its place in the layout. Claude Code relies on it: it opens each teammate's
// pane running `cat` as a placeholder and then respawns it with the teammate's
// command. A tuios window's process cannot be swapped from outside, so every
// pane the shim opens runs `tuios tmux-pane`, a small holder that runs the
// pane's command as its child and, on a respawn request, ends that child and
// starts the new command in its place. The command runs in a process group of
// its own that holds the terminal's foreground, as a shell's job does, so
// agent detection reads the command and not the holder.
//
// The request arrives on a unix socket in the shim's runtime directory, which
// is private to the user (0700). The holder takes a request only from there,
// so only the user's own processes can respawn a pane: the same processes that
// could type into it with the tuios CLI.

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
		return fmt.Errorf("pane %s was not opened through the tmux shim, so it has no holder to respawn it", PaneID(windowID))
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req.Window = windowID
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("send the request to pane %s: %w", PaneID(windowID), err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("pane %s did not answer: %w", PaneID(windowID), err)
	}
	var r respawnReply
	if err := json.Unmarshal(reply, &r); err != nil {
		return fmt.Errorf("pane %s answered %q", PaneID(windowID), strings.TrimSpace(string(reply)))
	}
	if !r.OK {
		return fmt.Errorf("pane %s: %s", PaneID(windowID), r.Error)
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
}

// paneArgv turns a tmux command into an argv. It returns the argv and the
// name argv[0] is shown as, which for a login shell is "-name".
func paneArgv(cmd []string, shell string) (path string, argv []string) {
	switch len(cmd) {
	case 0:
		if shell == "" {
			shell = "/bin/sh"
		}
		base := shell
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		return shell, []string{"-" + base}
	case 1:
		return "/bin/sh", []string{"sh", "-c", cmd[0]}
	}
	return cmd[0], cmd
}

// paneEnv is the environment of a holder's processes: the holder's own, with
// what makes the shim the pane's tmux, then extra on top.
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

// mergeEnv returns base with every KEY=VALUE of extra set, replacing any
// entry of base for the same key.
func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	keys := map[string]bool{}
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		keys[k] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !keys[k] {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}
