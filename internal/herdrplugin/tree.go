package herdrplugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RunTree runs cmd and waits for it. When ctx ends, it kills the command
// and every process it started: the process group on unix, the job object
// on Windows. On Linux the command also dies with tuios. tuios plugins
// install runs build commands with it.
func RunTree(ctx context.Context, cmd *exec.Cmd) error {
	detach(cmd)
	deathSignal(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	g, err := track(cmd)
	if err != nil {
		g.kill()
		_ = cmd.Wait()
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			g.kill()
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	g.release()
	return err
}

// HoldLock takes an exclusive lock on the file p, waiting up to 10 seconds
// for another tuios plugins command. The kernel releases it when the
// process ends, so a killed install never blocks the next one. The file
// stays: removing it could let two processes lock two different files.
func HoldLock(p string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // a lock file tuios names
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		ok, err := tryLock(f)
		if ok || err != nil {
			if err != nil {
				_ = f.Close()
				return nil, err
			}
			return func() { _ = f.Close() }, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another tuios plugins command runs now. Wait for it to finish (%s)", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
