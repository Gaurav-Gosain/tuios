package learnssh

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestMeasure measures a running tuios-learn: memory and CPU per session,
// the server's own, and the bytes a session sends. It runs only when
// TUIOS_LEARN_MEASURE_ADDR and TUIOS_LEARN_MEASURE_PID name the server.
//
//	TUIOS_LEARN_MEASURE_ADDR=127.0.0.1:2222 TUIOS_LEARN_MEASURE_PID=1234 \
//	  TUIOS_LEARN_MEASURE_N=20 go test ./internal/learnssh -run TestMeasure -v
func TestMeasure(t *testing.T) {
	addr := os.Getenv("TUIOS_LEARN_MEASURE_ADDR")
	pid, _ := strconv.Atoi(os.Getenv("TUIOS_LEARN_MEASURE_PID"))
	if addr == "" || pid == 0 {
		t.Skip("set TUIOS_LEARN_MEASURE_ADDR and TUIOS_LEARN_MEASURE_PID")
	}
	n := 1
	if v, _ := strconv.Atoi(os.Getenv("TUIOS_LEARN_MEASURE_N")); v > 0 {
		n = v
	}
	var bytesIn atomic.Int64
	var wg sync.WaitGroup
	clients := make([]*client, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := measureDial(t, addr, fmt.Sprintf("m%d", i), &bytesIn)
			clients[i] = c
			// A busy session: a lesson with a window running top, which
			// redraws every second.
			if !c.waitFor(15*time.Second, "Start the tour") {
				t.Errorf("client %d: no welcome", i)
				return
			}
			c.send("\r")
			c.waitFor(8*time.Second, "› Open a window")
			time.Sleep(800 * time.Millisecond)
			c.send("n")
			c.waitFor(5*time.Second, "› Start typing")
			c.send("i")
			c.waitFor(5*time.Second, "› Say hi")
			c.send("top\r")
			// The worst case a person can make: many windows at once.
			if spam, _ := strconv.Atoi(os.Getenv("TUIOS_LEARN_MEASURE_WINDOWS")); spam > 0 {
				c.send("\x02\x1b")
				time.Sleep(300 * time.Millisecond)
				for range spam {
					c.send("n")
					time.Sleep(60 * time.Millisecond)
				}
			}
		}()
		time.Sleep(150 * time.Millisecond)
	}
	wg.Wait()
	time.Sleep(5 * time.Second)

	kids := childrenOf(pid)
	t.Logf("sessions: %d, session processes: %d", n, len(kids))
	cpu0, srv0 := cpuTicks(kids), cpuTicks([]int{pid})
	b0 := bytesIn.Load()
	start := time.Now()
	time.Sleep(20 * time.Second)
	wall := time.Since(start).Seconds()
	cpu1, srv1 := cpuTicks(kids), cpuTicks([]int{pid})
	b1 := bytesIn.Load()

	var rss []int
	total := 0
	for _, k := range kids {
		r := rssKiB(k)
		rss = append(rss, r)
		total += r
	}
	maxRSS := 0
	for _, r := range rss {
		maxRSS = max(maxRSS, r)
	}
	const hz = 100.0
	t.Logf("session RSS: avg %.1f MiB, max %.1f MiB, total %.1f MiB",
		float64(total)/float64(max(len(kids), 1))/1024, float64(maxRSS)/1024, float64(total)/1024)
	pss := 0
	for _, k := range kids {
		pss += pssKiB(k)
	}
	t.Logf("session PSS (shared pages split): avg %.1f MiB, total %.1f MiB",
		float64(pss)/float64(max(len(kids), 1))/1024, float64(pss)/1024)
	t.Logf("server RSS: %.1f MiB", float64(rssKiB(pid))/1024)
	t.Logf("session CPU: %.2f%% of one core each, %.1f%% total",
		float64(cpu1-cpu0)/hz/wall*100/float64(max(len(kids), 1)), float64(cpu1-cpu0)/hz/wall*100)
	t.Logf("server CPU: %.2f%% of one core", float64(srv1-srv0)/hz/wall*100)
	t.Logf("output: %.1f KiB/s per session", float64(b1-b0)/wall/1024/float64(n))
	for _, c := range clients {
		if c != nil {
			c.sess.Close()
			c.conn.Close()
		}
	}
}

func measureDial(t *testing.T, addr, user string, count *atomic.Int64) *client {
	conn, err := gossh.Dial("tcp", addr, sshConfig(user, true))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sess, _ := conn.NewSession()
	_ = sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{})
	out, _ := sess.StdoutPipe()
	in, _ := sess.StdinPipe()
	c := &client{t: t, conn: conn, sess: sess, in: in, emu: vt.NewEmulator(120, 40), ended: make(chan struct{})}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := out.Read(buf)
			count.Add(int64(n))
			if n > 0 {
				c.mu.Lock()
				_, _ = c.emu.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				close(c.ended)
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := c.emu.Read(buf)
			if n > 0 {
				_, _ = in.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	_ = sess.Shell()
	return c
}

func childrenOf(ppid int) []int {
	var out []int
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		f := statFields(pid)
		if len(f) > 1 {
			if pp, _ := strconv.Atoi(f[1]); pp == ppid {
				out = append(out, pid)
			}
		}
	}
	return out
}

// statFields is /proc/pid/stat after the command name: field 0 is the
// state, 1 the parent, 11 and 12 utime and stime.
func statFields(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	s := string(b)
	return strings.Fields(s[strings.LastIndexByte(s, ')')+2:])
}

func cpuTicks(pids []int) int {
	sum := 0
	for _, p := range pids {
		f := statFields(p)
		if len(f) > 12 {
			u, _ := strconv.Atoi(f[11])
			s, _ := strconv.Atoi(f[12])
			sum += u + s
		}
	}
	return sum
}

func rssKiB(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			n, _ := strconv.Atoi(strings.Fields(v)[0])
			return n
		}
	}
	return 0
}

func pssKiB(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "Pss:"); ok {
			n, _ := strconv.Atoi(strings.Fields(v)[0])
			return n
		}
	}
	return 0
}
