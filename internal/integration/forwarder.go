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
	"github.com/Gaurav-Gosain/tuios/internal/shquote"
)

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

const (
	forwarderShortWait = 2
	forwarderHoldWait  = 330
)

const forwarderMark = "TUIOS-HOOK-OK"

func connector(tool string) string {
	switch tool {
	case SocketNC:
		return `nc -U -w "$w" "$TUIOS_SOCKET"`
	case SocketSocat:
		return `socat -t "$w" -T "$w" - "UNIX-CONNECT:$TUIOS_SOCKET"`
	}
	return ""
}

func (run Runner) command(harnessID string, version int) string {
	plain := HookCommand(run.Program, harnessID, version)
	conn := connector(run.Socket)
	if conn == "" || runtime.GOOS == "windows" {
		return plain
	}
	script := `p=$(cat); if [ -n "$TUIOS_SOCKET" ] && [ -n "$p" ]; then ` +
		`case $p in (*PermissionRequest*) w=` + strconv.Itoa(forwarderHoldWait) + `;; (*) w=` + strconv.Itoa(forwarderShortWait) + `;; esac; ` +
		`o=$({ printf "{\"id\":1,\"verb\":\"agent-hook-event\",\"params\":{\"harness\":\"%s\",\"forwarder\":true,\"env\":{" ` + harnessID + `; ` +
		forwardEnvLoop() +
		`printf "},\"payload_json\":"; printf "%s" "$p"; printf "}}\n"; } | ` + conn + ` 2>/dev/null; echo .); ` +
		`o=${o%.}; case $o in (` + forwarderMark + `?*) printf "%s" "${o#` + forwarderMark + `?}"; exit 0;; esac; fi; ` +
		`printf "%s" "$p" | exec ` + plain
	return "sh -c " + shellWord(script)
}

const statusLineArg0 = "tuios-statusline"

func (run Runner) statusLineCommand(then string) string {
	conn := connector(run.Socket)
	if conn == "" || runtime.GOOS == "windows" {
		return StatusLineCommand(run.Program, then)
	}
	plain := shellWord(run.Program) + " " + statusLineVerb + " " + ClaudeCode + " " + managedMarker + " " + strconv.Itoa(StatusLineVersion)
	script := `p=$(cat); if [ -n "$TUIOS_SOCKET" ] && [ -n "$p" ]; then w=` + strconv.Itoa(forwarderShortWait) + `; ` +
		`o=$({ printf "{\"id\":1,\"verb\":\"agent-statusline-event\",\"params\":{\"harness\":\"%s\",\"forwarder\":true,\"env\":{" ` + ClaudeCode + `; ` +
		forwardEnvLoop() +
		`printf "},\"payload_json\":"; printf "%s" "$p"; printf "}}\n"; } | ` + conn + ` 2>/dev/null; echo .); ` +
		`case $o in (` + forwarderMark + `*) ;; (*) printf "%s" "$p" | ` + plain + ` >/dev/null 2>&1;; esac; ` +
		`if [ -n "$1" ]; then printf "%s" "$p" | sh -c "$1"; exit $?; fi; exit 0; fi; ` +
		`if [ -n "$1" ]; then printf "%s" "$p" | ` + plain + ` --then "$1"; exit $?; fi; ` +
		`printf "%s" "$p" | exec ` + plain
	cmd := "sh -c " + shquote.Word(script) + " " + statusLineArg0
	if then != "" {
		cmd += " " + shquote.Word(then)
	}
	return cmd
}

func forwardEnvLoop() string {
	return `s=; for v in ` + strings.Join(HookEnv, " ") + `; do eval "x=\${$v-}"; case $x in ("") ;; (*[!A-Za-z0-9._:/@+=-]*) ;; (*) printf "%s\"%s\":\"%s\"" "$s" "$v" "$x"; s=,;; esac; done; `
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

func probeSocketTool(tool string) bool {
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
