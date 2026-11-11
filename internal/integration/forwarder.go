package integration

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// A hook command that does not start tuios.
//
// A harness runs its hook command for every event, and `tuios agent-hook`
// costs a whole tuios start each time. Where the machine has a program that
// talks to a unix socket, the installed command is a line of sh instead: it
// sends the event to the daemon's agent-hook-event verb over TUIOS_SOCKET and
// prints what the daemon answers, which is what tuios agent-hook would have
// printed. The line falls back to tuios agent-hook when there is no socket,
// when the daemon does not answer with the forwarder's mark (a daemon older
// than the verb answers with a JSON error), or when the payload is empty. The
// program is chosen when the integration is installed (DetectHookSocket), not
// for each event.

// Runner is how installed hooks run tuios: the program, and the socket tool a
// hook command forwards events through, "" to start the program for each
// event.
type Runner struct {
	Program string
	Socket  string
}

// The socket tools a forwarder can use, by the name DetectHookSocket reports.
const (
	SocketNC    = "nc"
	SocketSocat = "socat"
)

// Idle timeouts of a forwarder's connection, in seconds. A hook gives up after
// half a second (agenthook.Deadline) and the daemon answers by then, so a
// short one only matters for a daemon that hangs. An event that may wait on
// the Inbox for the person (a PermissionRequest) gets the long one, past the
// daemon's 300 second hold.
const (
	forwarderShortWait = 2
	forwarderHoldWait  = 330
)

// forwarderMark is the line a daemon starts a forwarder's reply with. See
// session.forwardedReplyMark.
const forwarderMark = "TUIOS-HOOK-OK"

// connector is the sh words that send stdin to the daemon socket with idle
// timeout $w, for a socket tool.
func connector(tool string) string {
	switch tool {
	case SocketNC:
		return `nc -U -w "$w" "$TUIOS_SOCKET"`
	case SocketSocat:
		return `socat -t "$w" -T "$w" - "UNIX-CONNECT:$TUIOS_SOCKET"`
	}
	return ""
}

// command is the hook command run installs for a harness: the forwarder line,
// or tuios agent-hook itself when there is no socket tool.
func (run Runner) command(harnessID string, version int) string {
	plain := HookCommand(run.Program, harnessID, version)
	conn := connector(run.Socket)
	if conn == "" || runtime.GOOS == "windows" {
		return plain
	}
	var envJSON strings.Builder
	for i, name := range HookEnv {
		if i > 0 {
			envJSON.WriteString(" ")
		}
		envJSON.WriteString(name)
	}
	// The script is one sh -c argument, quoted by shellWord. The payload goes into the JSON line as JSON (payload_json), on one line:
	// JSON has no raw newline inside a string, so removing newlines keeps it
	// the same value. A variable whose value needs JSON escaping is left out;
	// the daemon reads it from the process when it can. The reply's trailing
	// newlines are kept by the dot after it. Every case pattern opens with
	// its own parenthesis, so a pattern inside $(...) does not end it.
	script := `p=$(cat); if [ -n "$TUIOS_SOCKET" ] && [ -n "$p" ]; then ` +
		`case $p in (*PermissionRequest*) w=` + strconv.Itoa(forwarderHoldWait) + `;; (*) w=` + strconv.Itoa(forwarderShortWait) + `;; esac; ` +
		`o=$({ printf "{\"id\":1,\"verb\":\"agent-hook-event\",\"params\":{\"harness\":\"%s\",\"forwarder\":true,\"env\":{" ` + harnessID + `; ` +
		`s=; for v in ` + envJSON.String() + `; do eval "x=\${$v-}"; case $x in ("") ;; (*[!A-Za-z0-9._:/@+=-]*) ;; (*) printf "%s\"%s\":\"%s\"" "$s" "$v" "$x"; s=,;; esac; done; ` +
		`printf "},\"payload_json\":"; printf "%s" "$p" | tr -d "\r\n"; printf "}}\n"; } | ` + conn + ` 2>/dev/null; echo .); ` +
		`o=${o%.}; case $o in (` + forwarderMark + `?*) printf "%s" "${o#` + forwarderMark + `?}"; exit 0;; esac; fi; ` +
		`printf "%s" "$p" | exec ` + plain
	return "sh -c " + shellWord(script)
}

// DetectHookSocket reports the socket tool a forwarder line can use on this
// machine, "" for none. It runs each candidate the way the line runs it,
// against a socket of its own that answers only after the tool has closed
// its sending half, so a tool that ends the connection at the end of its
// input, or does not wait for the answer, is not taken.
func DetectHookSocket(lookPath func(string) (string, error)) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("sh"); err != nil {
		return ""
	}
	for _, tool := range []string{SocketNC, SocketSocat} {
		if _, err := lookPath(tool); err != nil {
			continue
		}
		if probeSocketTool(tool) {
			return tool
		}
	}
	return ""
}

// probeSocketTool runs one tool against a socket that reads its input to the
// end, waits, and answers.
func probeSocketTool(tool string) bool {
	// A unix socket path is short on macOS: the default temp folder is long.
	dir, err := os.MkdirTemp("/tmp", "tuios-probe")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		return false
	}
	defer func() { _ = ln.Close() }()
	const want = forwarderMark + "\nprobe\n"
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.Copy(io.Discard, conn); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = conn.Write([]byte(want))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := childproc.CommandContext(ctx, "sh", "-c", "w="+strconv.Itoa(forwarderShortWait)+"; "+connector(tool))
	cmd.Env = append(os.Environ(), "TUIOS_SOCKET="+path)
	cmd.Stdin = strings.NewReader("{}\n")
	out, err := cmd.Output()
	return err == nil && bytes.Equal(out, []byte(want))
}
