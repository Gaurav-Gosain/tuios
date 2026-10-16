package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestDaemonServesPprof runs the daemon with --pprof and reads its heap
// profile. The daemon owns every pane's emulator and scrollback, so it is
// the process a memory question is about; --pprof was a persistent flag that
// "tuios daemon" accepted and ignored.
func TestDaemonServesPprof(t *testing.T) {
	base := t.TempDir()
	env := os.Environ()
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	daemon := exec.Command(tuiosBin, "daemon", "--pprof", addr)
	daemon.Env = env
	daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_, _ = daemon.Process.Wait()
	})
	killDaemon(t, base)

	deadline := time.Now().Add(15 * time.Second)
	for {
		heap, err := pprofHeapInUse(addr)
		if err == nil {
			if heap == 0 {
				t.Fatal("the daemon reports an in-use heap of 0 bytes")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never served /debug/pprof on %s: %v", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStartServerPassesPprofToTheDaemon starts the daemon with
// "tuios --pprof ADDR start-server" and reads the heap profile once
// start-server has returned. start-server spawns "tuios daemon" detached, so
// the flag has to be handed on: the process it was given to is gone by the
// time anyone asks for a profile.
func TestStartServerPassesPprofToTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	if out, err := tuiosCLI(t, base, "--pprof", addr, "start-server"); err != nil {
		t.Fatalf("start-server: %v: %s", err, out)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		heap, err := pprofHeapInUse(addr)
		if err == nil {
			if heap == 0 {
				t.Fatal("the daemon reports an in-use heap of 0 bytes")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the daemon start-server started never served /debug/pprof on %s: %v", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
