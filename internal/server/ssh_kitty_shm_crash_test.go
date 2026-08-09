package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// TestSSHKittyShmDoesNotKillSession exercises the remote-client graphics path
// that the captured crash lives on: a guest pane emits kitty a=T frames over
// shared-memory transport (t=s, dataLen=0, i=1), and the SSH-attached client
// re-encodes them to inline for the remote terminal (RemoteClient=true, set by
// createEphemeralTUIOSInstance). The session must keep running and never be torn
// down by a graphics frame - the point of the render-path panic barrier.
//
// Ephemeral mode is used so the test is fully isolated: no daemon, no saved
// session state is read or written. The re-encode path is identical to daemon
// mode. No browser is launched; the frames are synthesized and emitted as cat's
// output of a file of raw kitty APC sequences that reference a real /dev/shm
// object created by the test.
func TestSSHKittyShmDoesNotKillSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH integration test in short mode")
	}
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no /dev/shm on this platform")
	}

	// A real shm object sized like a browser pane (~1810x800 px RGBA), so the
	// client re-encodes multi-MB frames just like the capture.
	const pxW, pxH = 1810, 800
	shmName := fmt.Sprintf("tuios-crash-%d", os.Getpid())
	shmPath := "/dev/shm/" + shmName
	shmData := make([]byte, pxW*pxH*4)
	for i := range shmData {
		shmData[i] = byte(i)
	}
	if err := os.WriteFile(shmPath, shmData, 0o600); err != nil {
		t.Skipf("cannot write /dev/shm object: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(shmPath) })

	// A file of raw kitty a=T shm frames, each the captured control command:
	// transmit+place, shared-memory medium, image id 1, no inline data, carrying
	// only the base64 shm name.
	nameB64 := base64.StdEncoding.EncodeToString([]byte(shmName))
	frame := fmt.Sprintf("\x1b_Ga=T,t=s,i=1,f=32,s=%d,v=%d;%s\x1b\\", pxW, pxH, nameB64)
	framesPath := filepath.Join(t.TempDir(), "frames.bin")
	var framesBuf []byte
	for i := 0; i < 120; i++ {
		framesBuf = append(framesBuf, frame...)
	}
	if err := os.WriteFile(framesPath, framesBuf, 0o600); err != nil {
		t.Fatalf("write frames file: %v", err)
	}

	// Ephemeral SSH server: self-contained, touches no daemon state.
	port := freePort(t)
	hostKey := filepath.Join(t.TempDir(), "test_host_key")
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- StartSSHServer(ctx, &SSHServerConfig{
			Host:      "127.0.0.1",
			Port:      port,
			KeyPath:   hostKey,
			Ephemeral: true,
			Version:   "test",
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(5 * time.Second):
		}
	})

	clientCfg := &gossh.ClientConfig{
		User:            "tester",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	var client *gossh.Client
	deadline := time.Now().Add(8 * time.Second)
	for {
		c, err := gossh.Dial("tcp", addr, clientCfg)
		if err == nil {
			client = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("could not connect to SSH server: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	_ = sess.Setenv("TERM_PROGRAM", "ghostty")
	modes := gossh.TerminalModes{gossh.ECHO: 0}
	if err := sess.RequestPty("xterm-kitty", 42, 183, modes); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	// Drain stdout so the SSH channel never blocks on our side; note if it EOFs.
	streamClosed := make(chan struct{})
	go func() {
		buf := make([]byte, 64*1024)
		for {
			if _, rerr := stdout.Read(buf); rerr != nil {
				close(streamClosed)
				return
			}
		}
	}()

	// Let the TUI paint, then drive the guest pane to emit the shm frames the
	// way a browser pane does, by cat-ing the frames file in the shell.
	time.Sleep(1500 * time.Millisecond)
	_, _ = io.WriteString(stdin, "cat "+framesPath+"\n")

	// The session must survive. On a build where a graphics frame can tear the
	// tea program down, the session ends within a few frames (~3s in the
	// capture); watch for 5s.
	sessionEnded := make(chan error, 1)
	go func() { sessionEnded <- sess.Wait() }()
	select {
	case err := <-sessionEnded:
		t.Fatalf("SSH session torn down while processing kitty shm frames (err=%v)", err)
	case <-streamClosed:
		t.Fatal("SSH stdout closed while processing kitty shm frames - session torn down")
	case <-time.After(5 * time.Second):
		// Survived.
	}
}
