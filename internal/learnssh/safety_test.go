package learnssh

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func rawConn(t *testing.T, addr string) *gossh.Client {
	t.Helper()
	conn, err := gossh.Dial("tcp", addr, sshConfig("mallory", false))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestExecAndSubsystemAreRefused(t *testing.T) {
	addr := startServer(t, nil)
	conn := rawConn(t, addr)

	sess, _ := conn.NewSession()
	out, err := sess.CombinedOutput("id; cat /etc/passwd")
	if err == nil {
		t.Fatalf("exec ran, output %q", out)
	}
	if strings.Contains(string(out), "uid=") || strings.Contains(string(out), "root:") {
		t.Fatalf("exec reached the host: %q", out)
	}

	sess.Close()
	sess2, err := rawConn(t, addr).NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess2.RequestSubsystem("sftp"); err == nil {
		t.Fatal("sftp subsystem was accepted")
	}
}

func TestForwardingIsRefused(t *testing.T) {
	addr := startServer(t, nil)
	conn := rawConn(t, addr)

	// A local forward (ssh -L) opens a direct-tcpip channel.
	if c, err := conn.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("direct-tcpip was accepted")
	}
	// A remote forward (ssh -R) asks for tcpip-forward.
	if l, err := conn.Listen("tcp", "127.0.0.1:0"); err == nil {
		l.Close()
		t.Fatal("tcpip-forward was accepted")
	}

	// Agent forwarding: the server never opens an agent channel back.
	agentChans := conn.HandleChannelOpen("auth-agent@openssh.com")
	sess, _ := conn.NewSession()
	_ = sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{})
	_, _ = sess.SendRequest("auth-agent-req@openssh.com", true, nil)
	// X11 forwarding is refused outright.
	if ok, _ := sess.SendRequest("x11-req", true, gossh.Marshal(struct {
		Single        bool
		Proto, Cookie string
		Screen        uint32
	}{false, "MIT-MAGIC-COOKIE-1", "00", 0})); ok {
		t.Error("x11-req was accepted")
	}
	// An environment variable is taken and ignored.
	_ = sess.Setenv("LD_PRELOAD", "/tmp/evil.so")
	_ = sess.Shell()
	select {
	case ch := <-agentChans:
		ch.Reject(gossh.Prohibited, "no")
		t.Fatal("the server opened an agent channel")
	case <-time.After(2 * time.Second):
	}
	// The session process got a fixed environment, and nothing the client sent.
	for _, pid := range children() {
		env, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if strings.Contains(string(env), "LD_PRELOAD") {
			t.Fatalf("client env reached the session process: %q", env)
		}
	}
}

func TestOneSessionPerConnection(t *testing.T) {
	addr := startServer(t, nil)
	conn := rawConn(t, addr)
	s1, err := conn.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	if s2, err := conn.NewSession(); err == nil {
		s2.Close()
		t.Fatal("a second session channel was accepted")
	}
}

func TestNoPtyGetsANote(t *testing.T) {
	addr := startServer(t, nil)
	conn := rawConn(t, addr)
	sess, _ := conn.NewSession()
	out, _ := sess.StdoutPipe()
	_ = sess.Shell()
	b, _ := io.ReadAll(out)
	if !strings.Contains(string(b), "needs a terminal") {
		t.Fatalf("got %q", b)
	}
}

func TestConnectionRateLimit(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.ConnPerMinute, c.ConnBurst = 1, 2 })
	for i := range 2 {
		conn, err := gossh.Dial("tcp", addr, sshConfig("a", false))
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		conn.Close()
	}
	if conn, err := gossh.Dial("tcp", addr, sshConfig("a", false)); err == nil {
		conn.Close()
		t.Fatal("the third connection in a burst of two got in")
	}
}

func TestMaxSessions(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.MaxSessions, c.MaxPerIP = 1, 5 })
	a := dial(t, addr, "a", 100, 30)
	a.mustSee(10*time.Second, "Hi a!")
	b := dial(t, addr, "b", 100, 30)
	b.mustSee(5*time.Second, "The tour is full right now")
}

func TestMaxSessionsPerAddress(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.MaxSessions, c.MaxPerIP = 10, 1 })
	a := dial(t, addr, "a", 100, 30)
	a.mustSee(10*time.Second, "Hi a!")
	b := dial(t, addr, "b", 100, 30)
	b.mustSee(5*time.Second, "You have too many sessions open")
}

func TestIdleTimeout(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.Idle = 4 * time.Second })
	c := dial(t, addr, "idler", 100, 30)
	c.mustSee(10*time.Second, "Hi idler!")
	c.mustSee(15*time.Second, "You were away")
	c.frame("safety-idle")
	select {
	case <-c.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
}

func TestSessionTimeCap(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.MaxSession = 5 * time.Second })
	c := dial(t, addr, "stayer", 100, 30)
	c.mustSee(10*time.Second, "Hi stayer!")
	// Keep typing, so only the cap can end it.
	stop := time.After(20 * time.Second)
	for !c.waitFor(500*time.Millisecond, "Time is up") {
		select {
		case <-stop:
			t.Fatal("the cap never ended the session")
		default:
		}
		c.send("j")
	}
	c.frame("safety-timecap")
}

func TestMemoryLimit(t *testing.T) {
	addr := startServer(t, func(c *Config) { c.SessionMemMiB = 5 })
	c := dial(t, addr, "hog", 100, 30)
	c.mustSee(15*time.Second, "used too much memory")
}

func TestSmallTerminal(t *testing.T) {
	addr := startServer(t, nil)
	c := dial(t, addr, "tiny", 60, 20)
	c.mustSee(10*time.Second, "Your terminal is 60 by 20", "at least 80 by 24")
	c.frame("safety-small")
	c.mu.Lock()
	c.emu.Resize(100, 30)
	c.mu.Unlock()
	_ = c.sess.WindowChange(30, 100)
	c.mustSee(5*time.Second, "Hi tiny!")
}

// TestFakeShellHasNoHost types host-seeking commands into a pane and checks
// that only the pretend filesystem answers.
func TestFakeShellHasNoHost(t *testing.T) {
	addr := startServer(t, nil)
	c := dial(t, addr, "curious", 120, 40)
	c.mustSee(10*time.Second, "Start the tour")
	c.send("\r")
	c.mustSee(8*time.Second, "› Open a window")
	time.Sleep(time.Second)
	c.send("n")
	c.mustSee(5*time.Second, "› Start typing")
	c.send("i")
	c.mustSee(5*time.Second, "› Say hi")
	for _, line := range []string{
		"cat /etc/passwd", "ls /", "cd /proc && ls", "sh -c id", "bash -c 'id'",
		"curl http://127.0.0.1", "vim /etc/shadow", "echo x > /etc/x", "cd ~ && ls",
	} {
		c.send("clear\r")
		time.Sleep(150 * time.Millisecond)
		c.send(line + "\r")
		time.Sleep(400 * time.Millisecond)
		s := c.screen()
		for _, bad := range []string{"root:x:0", "uid=", "/bin/bash", "daemon:"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%q reached the host: screen has %q:\n%s", line, bad, s)
			}
		}
		c.send("q")
		c.send("\x1b:q\r")
	}
	c.frame("safety-fake-shell")
}

// TestSandbox runs the session sandbox in a child process and checks that
// files, exec and TCP are all out of reach after it.
func TestSandbox(t *testing.T) {
	if _, err := os.Stat("/sys/kernel/security/landlock"); err != nil {
		if b, _ := os.ReadFile("/sys/kernel/security/lsm"); !strings.Contains(string(b), "landlock") {
			t.Skip("no Landlock on this kernel")
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cmd := exec.Command(os.Args[0], "sandboxcheck", ln.Addr().String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox check: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestSelfCheck runs the start-up check the server makes before it serves.
func TestSelfCheck(t *testing.T) {
	if _, err := os.Stat("/sys/kernel/security/lsm"); err != nil {
		t.Skip("no LSM list")
	}
	if b, _ := os.ReadFile("/sys/kernel/security/lsm"); !strings.Contains(string(b), "landlock") {
		t.Skip("no Landlock on this kernel")
	}
	err := checkSandbox(os.Args[0])
	if raceEnabled {
		if err == nil {
			t.Fatal("a cgo build passed the check, but its sandbox misses threads")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

// sandboxCheck is the child half of TestSandbox.
func sandboxCheck() int {
	addr := os.Args[2]
	if err := sandbox(); err != nil {
		fmt.Println("sandbox:", err)
		return 1
	}
	fail := 0
	check := func(what string, err error) {
		if err == nil {
			fmt.Println("ALLOWED:", what)
			fail = 1
			return
		}
		fmt.Println("denied:", what, "-", err)
	}
	_, err := os.ReadFile("/etc/hostname")
	check("read /etc/hostname", err)
	check("write /tmp", os.WriteFile(os.TempDir()+"/tuios-learn-sandbox", []byte("x"), 0o600))
	_, err = syscall.ForkExec("/bin/true", []string{"true"}, &syscall.ProcAttr{Files: []uintptr{0, 1, 2}})
	check("exec /bin/true", err)
	_, err = os.ReadDir("/")
	check("list /", err)
	c, err := net.Dial("tcp", addr)
	if err == nil {
		c.Close()
	} else if !errors.Is(err, syscall.EACCES) {
		fmt.Println("note: connect failed with", err)
	}
	check("connect tcp", err)
	return fail
}

// children lists the processes this test process started.
func children() []int { return childrenOf(os.Getpid()) }

func TestInputRateLimit(t *testing.T) {
	a, b := net.Pipe()
	lc := &limitedConn{Conn: a, b: newBucket(8<<10, 8<<10, time.Now())}
	go func() {
		_, _ = b.Write(make([]byte, 40<<10))
		b.Close()
	}()
	start := time.Now()
	n, _ := io.Copy(io.Discard, lc)
	took := time.Since(start)
	if n != 40<<10 {
		t.Fatalf("read %d", n)
	}
	// 8 KiB at once, then 32 KiB at 8 KiB a second.
	if took < 3500*time.Millisecond {
		t.Fatalf("40 KiB took %s, want about 4 s", took)
	}
}
