//go:build !windows

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runLine runs an installed command line with PATH limited to dir-less
// /bin:/usr/bin, where there is no tuios, and no TUIOS_SOCKET.
func runLine(t *testing.T, cmdline, stdin string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", cmdline)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), 0
}

func TestForwarderHookExitsZeroWithoutTuiosOnPath(t *testing.T) {
	run := Runner{Program: "tuios-not-installed-here", Socket: SocketNC}
	out, code := runLine(t, run.command(ClaudeCode, 3), `{"hook_event_name":"Stop"}`)
	if code != 0 {
		t.Fatalf("hook exit = %d, want 0 (output %q)", code, out)
	}
}

func TestForwarderStatusLineRunsThenWithoutTuiosOnPath(t *testing.T) {
	run := Runner{Program: "tuios-not-installed-here", Socket: SocketNC}
	out, code := runLine(t, run.statusLineCommand("cat"), `{"model":"x"}`)
	if code != 0 || out != `{"model":"x"}` {
		t.Fatalf("with --then: exit %d output %q, want the then command's output", code, out)
	}
	out, code = runLine(t, run.statusLineCommand(""), `{"model":"x"}`)
	if code != 0 || out != "" {
		t.Fatalf("without --then: exit %d output %q, want exit 0 and no output", code, out)
	}
}

func TestForwarderLinesStillRecognisedWithGuard(t *testing.T) {
	run := Runner{Program: "/opt/tuios", Socket: SocketNC}
	prog, version, then, ok := managedStatusLine(run.statusLineCommand("ccstatusline"))
	if !ok || prog != "/opt/tuios" || version != StatusLineVersion || then != "ccstatusline" {
		t.Fatalf("managedStatusLine = %q %d %q %v", prog, version, then, ok)
	}
	if got := unshellWord(hookProgram(run.command(ClaudeCode, 3), ClaudeCode)); got != "/opt/tuios" {
		t.Fatalf("hookProgram = %q", got)
	}
}

// The positive half: a program that is there is still run.
func TestForwarderHookRunsTuiosWhenPresent(t *testing.T) {
	dir := t.TempDir()
	fake := dir + "/tuios"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho ran\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := Runner{Program: fake, Socket: SocketNC}
	out, code := runLine(t, run.command(ClaudeCode, 3), `{"hook_event_name":"Stop"}`)
	if code != 7 || out != "ran\n" {
		t.Fatalf("exit %d output %q, want the program's own", code, out)
	}
	out, code = runLine(t, run.statusLineCommand(""), `{"model":"x"}`)
	if code != 7 || out != "ran\n" {
		t.Fatalf("status line: exit %d output %q, want the program's own", code, out)
	}
}
