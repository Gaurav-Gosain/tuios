//go:build linux || darwin

package procinfo

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStartTimePinsAProcess(t *testing.T) {
	a, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("the start time of this process cannot be read")
	}
	if b, _ := StartTime(os.Getpid()); a != b {
		t.Errorf("the start time changed: %d, then %d", a, b)
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, ok := StartTime(cmd.Process.Pid); ok {
		t.Error("a process that exited has a start time")
	}
}

func TestPeerPIDIsTheDialer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := net.Dial("unix", sock)
		if err == nil {
			defer c.Close()
			var b [1]byte
			_, _ = c.Read(b[:])
		}
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := PeerPID(conn); got != os.Getpid() {
		t.Errorf("PeerPID = %d, want %d", got, os.Getpid())
	}
}
