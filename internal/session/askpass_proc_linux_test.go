package session

import (
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestAskpassWatchWithoutPidfd is a prompt on a kernel that refuses
// pidfd_open, as before Linux 5.3 or under a seccomp profile that does not
// know the call. The watch stays open while the program runs, and closes
// once it is gone.
//
// Negative control: with the fallback to pollExit cut, the channel closes at
// once and every prompt would end as cancelled before the person sees it.
func TestAskpassWatchWithoutPidfd(t *testing.T) {
	oldOpen, oldPoll := pidfdOpen, askpassPoll
	pidfdOpen = func(int, int) (int, error) { return -1, unix.ENOSYS }
	askpassPoll = 20 * time.Millisecond
	t.Cleanup(func() { pidfdOpen, askpassPoll = oldOpen, oldPoll })

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep command")
	}
	stop := make(chan struct{})
	defer close(stop)
	gone := askpassWatch(cmd.Process.Pid, stop)
	select {
	case <-gone:
		t.Fatal("the watch closed while the program still ran")
	case <-time.After(300 * time.Millisecond):
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Fatal("the watch did not close after the program exited")
	}
}
