// Probe 2: readiness on a darwin PTY master (opened by creack/pty v1.1.24, blocking fd)
// and a prototype of the spec's read gate (section 2.4).
//
//	cd .. && GOWORK=off GOFLAGS=-mod=mod GOPROXY=off go run ./p2
package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type mech string

const (
	mPoll   mech = "poll"
	mSelect mech = "select"
	mKqueue mech = "kqueue"
)

// waitResult reports, per fd in the order asked, whether it is ready and a description.
type waitResult struct {
	n     int
	ready []bool
	desc  []string
	err   error
}

func waitReady(m mech, fds []int, timeoutMs int) waitResult {
	res := waitResult{ready: make([]bool, len(fds)), desc: make([]string, len(fds))}
	switch m {
	case mPoll:
		pf := make([]unix.PollFd, len(fds))
		for i, fd := range fds {
			pf[i] = unix.PollFd{Fd: int32(fd), Events: unix.POLLIN}
		}
		res.n, res.err = unix.Poll(pf, timeoutMs)
		for i := range pf {
			r := pf[i].Revents
			res.ready[i] = r != 0
			res.desc[i] = pollBits(r)
		}
	case mSelect:
		var rs unix.FdSet
		maxfd := 0
		for _, fd := range fds {
			rs.Set(fd)
			maxfd = max(maxfd, fd)
		}
		var tv *unix.Timeval
		if timeoutMs >= 0 {
			t := unix.NsecToTimeval(int64(timeoutMs) * 1e6)
			tv = &t
		}
		res.n, res.err = unix.Select(maxfd+1, &rs, nil, nil, tv)
		for i, fd := range fds {
			res.ready[i] = res.err == nil && rs.IsSet(fd)
			res.desc[i] = fmt.Sprintf("readable=%v", res.ready[i])
		}
	case mKqueue:
		kq, err := unix.Kqueue()
		if err != nil {
			res.err = err
			return res
		}
		defer unix.Close(kq)
		ch := make([]unix.Kevent_t, len(fds))
		for i, fd := range fds {
			unix.SetKevent(&ch[i], fd, unix.EVFILT_READ, unix.EV_ADD)
		}
		if _, err := unix.Kevent(kq, ch, nil, nil); err != nil {
			res.err = fmt.Errorf("register: %w", err)
			return res
		}
		evs := make([]unix.Kevent_t, len(fds))
		var ts *unix.Timespec
		if timeoutMs >= 0 {
			t := unix.NsecToTimespec(int64(timeoutMs) * 1e6)
			ts = &t
		}
		res.n, res.err = unix.Kevent(kq, nil, evs, ts)
		for i := range res.desc {
			res.desc[i] = "not-ready"
		}
		for _, e := range evs[:max(res.n, 0)] {
			for i, fd := range fds {
				if int(e.Ident) == fd {
					res.ready[i] = true
					res.desc[i] = fmt.Sprintf("flags=0x%x EV_EOF=%v EV_ERROR=%v data=%d fflags=0x%x", e.Flags, e.Flags&unix.EV_EOF != 0, e.Flags&unix.EV_ERROR != 0, e.Data, e.Fflags)
				}
			}
		}
	}
	return res
}

func pollBits(r int16) string {
	if r == 0 {
		return "revents=0"
	}
	s := fmt.Sprintf("revents=0x%x", r)
	for _, b := range []struct {
		bit  int16
		name string
	}{{unix.POLLIN, "POLLIN"}, {unix.POLLRDNORM, "POLLRDNORM"}, {unix.POLLHUP, "POLLHUP"}, {unix.POLLERR, "POLLERR"}, {unix.POLLNVAL, "POLLNVAL"}, {unix.POLLPRI, "POLLPRI"}} {
		if r&b.bit != 0 {
			s += " " + b.name
		}
	}
	return s
}

func openPTY() (master, slave *os.File) {
	m, s, err := pty.Open()
	if err != nil {
		panic(err)
	}
	return m, s
}

// rawFd returns the master's fd without calling Fd() (which would force blocking mode;
// creack's darwin master is already blocking either way).
func rawFd(f *os.File) int {
	var fd int
	sc, _ := f.SyscallConn()
	sc.Control(func(u uintptr) { fd = int(u) })
	return fd
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func readOnce(fd int) string {
	buf := make([]byte, 256)
	n, err := unix.Read(fd, buf)
	return fmt.Sprintf("read n=%d err=%v data=%q", n, err, buf[:max(n, 0)])
}

func partA() map[mech]bool {
	works := map[mech]bool{}
	fmt.Println("=== Part A: readiness on a creack/pty darwin master ===")
	{
		m, s := openPTY()
		fl, _ := unix.FcntlInt(uintptr(rawFd(m)), unix.F_GETFL, 0)
		fmt.Printf("master fd O_NONBLOCK=%v (creack opens it blocking)\n", fl&unix.O_NONBLOCK != 0)
		_, derr := m.Stat()
		fmt.Printf("SetReadDeadline on master: %v\n", m.SetReadDeadline(time.Now().Add(time.Millisecond)))
		_ = derr
		m.Close()
		s.Close()
	}
	for _, mc := range []mech{mPoll, mSelect, mKqueue} {
		fmt.Printf("--- %s ---\n", mc)
		// (a) no data -> timeout
		m, s := openPTY()
		mfd := rawFd(m)
		t0 := time.Now()
		r := waitReady(mc, []int{mfd}, 200)
		okA := r.err == nil && r.n == 0
		fmt.Printf("(a) no data, 200ms timeout: n=%d err=%v %s elapsed=%v  %s\n", r.n, r.err, r.desc[0], time.Since(t0).Round(time.Millisecond), verdict(okA))
		// (b) slave writes -> readable
		s.Write([]byte("hello"))
		r = waitReady(mc, []int{mfd}, 1000)
		okB := r.err == nil && r.n > 0 && r.ready[0]
		fmt.Printf("(b) slave wrote 5 bytes: n=%d err=%v %s  %s\n", r.n, r.err, r.desc[0], verdict(okB))
		if okB {
			fmt.Printf("    %s\n", readOnce(mfd))
		}
		works[mc] = okA && okB
		// (c1) slave closed, no child, no pending data
		s.Close()
		r = waitReady(mc, []int{mfd}, 300)
		fmt.Printf("(c1) all slave fds closed (no child), no data: n=%d err=%v %s\n", r.n, r.err, r.desc[0])
		if r.n > 0 {
			fmt.Printf("    %s\n", readOnce(mfd))
		}
		m.Close()

		// (c2) child holds slave, writes, exits; daemon copy of slave closed
		m, s = openPTY()
		mfd = rawFd(m)
		cmd := exec.Command("/bin/sh", "-c", "printf bye; exit 0")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		s.Close()
		exited := make(chan struct{})
		go func() { cmd.Wait(); close(exited) }()
		select {
		case <-exited:
			fmt.Printf("(c2) child exited before master was read\n")
		case <-time.After(1 * time.Second):
			fmt.Printf("(c2) child (wrote 'bye', called exit) has NOT been reaped after 1s while the master is unread: exit blocks on tty output drain\n")
		}
		r = waitReady(mc, []int{mfd}, 300)
		fmt.Printf("(c2) readiness with output pending: n=%d err=%v %s\n", r.n, r.err, r.desc[0])
		if r.n > 0 {
			fmt.Printf("    1st %s\n", readOnce(mfd))
		}
		select {
		case <-exited:
			fmt.Printf("    child reaped after master read\n")
		case <-time.After(3 * time.Second):
			fmt.Printf("    child still not reaped 3s after master read\n")
		}
		r = waitReady(mc, []int{mfd}, 300)
		fmt.Printf("(c2) after child exit, slave fully closed, no data: n=%d err=%v %s\n", r.n, r.err, r.desc[0])
		if r.n > 0 {
			fmt.Printf("    2nd %s\n", readOnce(mfd))
		}
		m.Close()
	}
	// POLLNVAL check on a live master, and negative control on a closed fd number.
	m, s := openPTY()
	pf := []unix.PollFd{{Fd: int32(rawFd(m)), Events: unix.POLLIN}}
	unix.Poll(pf, 0)
	fmt.Printf("POLLNVAL on live master (idle): %s  %s\n", pollBits(pf[0].Revents), verdict(pf[0].Revents&unix.POLLNVAL == 0))
	closedFd := rawFd(m)
	m.Close()
	s.Close()
	pf = []unix.PollFd{{Fd: int32(closedFd), Events: unix.POLLIN}}
	unix.Poll(pf, 0)
	fmt.Printf("NEG CONTROL POLLNVAL on closed fd %d: %s  %s\n", closedFd, pollBits(pf[0].Revents), verdict(pf[0].Revents&unix.POLLNVAL != 0))
	return works
}

// ---------------- Part B: read gate ----------------

type gate struct {
	mech     mech
	mfd      int
	pr, pw   int
	mu       sync.Mutex
	quiesced bool
	resume   chan struct{}
	parked   chan struct{} // reader signals once it has observed quiesce
	ungated  bool          // negative control: ignore the gate entirely
	dropNext atomic.Bool   // negative control: lose one chunk (simulates a chunk lost at exec)

	out   bytes.Buffer
	sink  hash.Hash // when set, bytes go to the hash instead of out (no realloc in the critical section)
	count atomic.Int64
	done  chan error
}

func newGate(m mech, mfd int) *gate {
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		panic(err)
	}
	unix.SetNonblock(p[0], true)
	unix.SetNonblock(p[1], true)
	return &gate{mech: m, mfd: mfd, pr: p[0], pw: p[1], done: make(chan error, 1)}
}

func (g *gate) run() {
	buf := make([]byte, 16*1024)
	drain := make([]byte, 64)
	for {
		r := g.waitFn()
		if r.err != nil && !errors.Is(r.err, unix.EINTR) {
			g.done <- r.err
			return
		}
		if r.ready[1] {
			for {
				if n, _ := unix.Read(g.pr, drain); n <= 0 {
					break
				}
			}
		}
		if !g.ungated {
			g.mu.Lock()
			if g.quiesced {
				res, parked := g.resume, g.parked
				g.parked = nil
				g.mu.Unlock()
				if parked != nil {
					close(parked)
				}
				<-res
				continue
			}
		}
		if r.ready[0] {
			n, err := unix.Read(g.mfd, buf)
			if n > 0 {
				if g.dropNext.CompareAndSwap(true, false) {
					// lost chunk
				} else {
					if g.sink != nil {
						g.sink.Write(buf[:n])
					} else {
						g.out.Write(buf[:n])
					}
					g.count.Add(int64(n))
				}
			}
			if err != nil || n == 0 {
				if !g.ungated {
					g.mu.Unlock()
				}
				g.done <- err
				return
			}
		}
		if !g.ungated {
			g.mu.Unlock()
		}
	}
}

func (g *gate) waitFn() waitResult { return waitReady(g.mech, []int{g.mfd, g.pr}, -1) }

// Quiesce returns once the reader can no longer consume a byte. It also returns a
// channel closed when the reader has actually parked.
func (g *gate) Quiesce() (lockLat time.Duration, parked chan struct{}) {
	t0 := time.Now()
	g.mu.Lock()
	g.quiesced = true
	g.resume = make(chan struct{})
	g.parked = make(chan struct{})
	parked = g.parked
	unix.Write(g.pw, []byte{1})
	g.mu.Unlock()
	return time.Since(t0), parked
}

func (g *gate) Resume() {
	g.mu.Lock()
	g.quiesced = false
	close(g.resume)
	g.mu.Unlock()
}

func setRaw(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		panic(err)
	}
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, t); err != nil {
		panic(err)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	x := uint32(2463534242)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

type setup struct {
	m, s    *os.File
	g       *gate
	want    []byte
	written chan struct{}
	cont    chan struct{}
}

func start(mc mech, size int, ungated bool) *setup {
	m, s := openPTY()
	setRaw(rawFd(s))
	g := newGate(mc, rawFd(m))
	g.ungated = ungated
	st := &setup{m: m, s: s, g: g, want: pattern(size), written: make(chan struct{}), cont: make(chan struct{})}
	go g.run()
	go func() {
		for off := 0; off < len(st.want); off += 1024 {
			if off == 10*1024 {
				<-st.cont // pause so the quiesce lands mid-stream deterministically
			}
			end := min(off+1024, len(st.want))
			if _, err := s.Write(st.want[off:end]); err != nil {
				panic(err)
			}
		}
		close(st.written)
		s.Close() // reader then sees EIO after draining
	}()
	return st
}

func waitCount(g *gate, n int64, d time.Duration) bool {
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if g.count.Load() >= n {
			return true
		}
		time.Sleep(200 * time.Microsecond)
	}
	return false
}

// singleQuiesce: quiesce at >=10 KB, hold 300 ms, check no reads and bytes pending in the kernel, resume, compare.
func singleQuiesce(mc mech, ungated, lossy bool) (stable, pending, exact bool, info string) {
	const size = 50 * 1024
	st := start(mc, size, ungated)
	g := st.g
	waitCount(g, 10*1024, 5*time.Second)
	lat, parked := g.Quiesce()
	close(st.cont)
	if !ungated {
		select {
		case <-parked:
		case <-time.After(2 * time.Second):
		}
	}
	c0 := g.count.Load()
	time.Sleep(300 * time.Millisecond)
	c1 := g.count.Load()
	fionread, _ := unix.IoctlGetInt(g.mfd, fionreadReq)
	kqPending := pendingKq(g.mfd)
	writerDone := false
	select {
	case <-st.written:
		writerDone = true
	default:
	}
	if lossy {
		g.dropNext.Store(true)
	}
	if !ungated {
		g.Resume()
	}
	err := <-g.done
	stable = c0 == c1
	pending = kqPending > 0 && !writerDone
	exact = bytes.Equal(g.out.Bytes(), st.want)
	info = fmt.Sprintf("quiesce lock latency=%v count@quiesce=%d count+300ms=%d kernelFIONREAD=%d kqueuePending=%d writerBlocked=%v final=%d/%d readerEnd=%v",
		lat, c0, c1, fionread, kqPending, !writerDone, g.out.Len(), size, err)
	st.m.Close()
	return
}

func latencyRun(mc mech, iters int) {
	m, sl := openPTY()
	setRaw(rawFd(sl))
	g := newGate(mc, rawFd(m))
	g.sink = sha256.New()
	go g.run()
	written := sha256.New()
	var writtenN int64
	stop := make(chan struct{})
	wdone := make(chan struct{})
	go func() {
		chunk := pattern(1 << 20)
		k := 0
		for {
			select {
			case <-stop:
				sl.Close()
				close(wdone)
				return
			default:
			}
			off := (k * 997) % (len(chunk) - 1024)
			b := chunk[off : off+1024]
			if _, err := sl.Write(b); err != nil {
				panic(err)
			}
			written.Write(b)
			writtenN += int64(len(b))
			k++
		}
	}()
	var lock, park []time.Duration
	violations := 0
	for i := 0; i < iters; i++ {
		time.Sleep(time.Duration(500+i%7*300) * time.Microsecond)
		t0 := time.Now()
		l, parked := g.Quiesce()
		select {
		case <-parked:
		case err := <-g.done:
			fmt.Printf("  reader ended early: %v\n", err)
			return
		}
		park = append(park, time.Since(t0))
		lock = append(lock, l)
		c0 := g.count.Load()
		time.Sleep(2 * time.Millisecond)
		if g.count.Load() != c0 {
			violations++
		}
		g.Resume()
	}
	close(stop)
	<-wdone
	err := <-g.done
	exact := bytes.Equal(g.sink.Sum(nil), written.Sum(nil)) && g.count.Load() == writtenN
	fmt.Printf("  %s: %d quiesce/resume cycles over a continuous %d-byte stream (sha256 compared): lock-acquire p50=%v p99=%v max=%v | reader-parked p50=%v p99=%v max=%v | reads-during-quiesce=%d | byte-exact=%v (%d/%d, reader end: %v)  %s\n",
		mc, iters, writtenN, pct(lock, 50), pct(lock, 99), pct(lock, 100), pct(park, 50), pct(park, 99), pct(park, 100), violations, exact, g.count.Load(), writtenN, err,
		verdict(exact && violations == 0))
	m.Close()
}

func pendingKq(fd int) int64 {
	kq, _ := unix.Kqueue()
	defer unix.Close(kq)
	var ch [1]unix.Kevent_t
	unix.SetKevent(&ch[0], fd, unix.EVFILT_READ, unix.EV_ADD)
	ev := make([]unix.Kevent_t, 1)
	ts := unix.Timespec{}
	n, _ := unix.Kevent(kq, ch[:], ev, &ts)
	if n <= 0 {
		return 0
	}
	return ev[0].Data
}

func pct(d []time.Duration, p int) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := (len(s) - 1) * p / 100
	return s[i]
}

func main() {
	works := partA()
	fmt.Println("\n=== Part B: read gate (readiness wait outside readMu, quiesce = lock+flag+self-pipe) ===")
	for _, mc := range []mech{mPoll, mSelect, mKqueue} {
		if !works[mc] {
			fmt.Printf("  %s: skipped, readiness did not work in Part A\n", mc)
			continue
		}
		stable, pending, exact, info := singleQuiesce(mc, false, false)
		fmt.Printf("  %s gated, 50 KB: no reads while quiesced=%v, bytes left in kernel=%v, byte-exact after resume=%v  %s\n    %s\n", mc, stable, pending, exact, verdict(stable && pending && exact), info)
		latencyRun(mc, 200)
	}
	fmt.Println("\n=== Part B negative controls (detector must flag these) ===")
	stable, _, exact, info := singleQuiesce(mKqueue, true, false)
	fmt.Printf("  NEG ungated reader: reads continued during 'quiesce'=%v  %s (byte-exact=%v)\n    %s\n", !stable, verdict(!stable), exact, info)
	_, _, exact, info = singleQuiesce(mKqueue, false, true)
	fmt.Printf("  NEG lossy reader (drops one chunk after resume): byte-exact check fails=%v  %s\n    %s\n", !exact, verdict(!exact), info)
}

// FIONREAD on darwin (_IOR('f', 127, int)); x/sys does not export it.
const fionreadReq = 0x4004667f
