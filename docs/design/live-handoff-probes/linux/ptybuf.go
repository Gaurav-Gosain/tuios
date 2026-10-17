package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

const childBytes = 200000

// fillByteAtATime writes 1 byte at a time to the non-blocking fd until EAGAIN.
func fillByteAtATime(fd int) int {
	b := []byte{'a'}
	n := 0
	for {
		_, err := unix.Write(fd, b)
		if errors.Is(err, unix.EAGAIN) {
			return n
		}
		must(err)
		n++
		if n > 1<<20 {
			return -1 // never hit EAGAIN within 1 MiB
		}
	}
}

func fillBulk(fd int, chunk []byte) (total int, writes int) {
	for {
		w, err := unix.Write(fd, chunk)
		if errors.Is(err, unix.EAGAIN) {
			return
		}
		must(err)
		total += w
		writes++
		if total > 1<<20 {
			return -1, writes
		}
	}
}

// drain reads the non-blocking fd until EAGAIN, retrying briefly so the
// flip-buffer worker can push queued bytes to the ldisc.
func drain(fd int) int {
	buf := make([]byte, 65536)
	total := 0
	idle := 0
	for idle < 20 {
		n, err := unix.Read(fd, buf)
		if n > 0 {
			total += n
			idle = 0
			continue
		}
		if errors.Is(err, unix.EAGAIN) {
			idle++
			time.Sleep(5 * time.Millisecond)
			continue
		}
		break
	}
	return total
}

type ptyMode struct {
	name string
	set  func(slave int)
}

func echoOff(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	must(err)
	t.Lflag &^= unix.ECHO
	must(unix.IoctlSetTermios(fd, unix.TCSETS, t))
}

func probePTYBuf() {
	fmt.Println("== probe 1: PTY kernel buffer (Linux) ==")
	modes := []ptyMode{
		{"cooked(default)", func(int) {}},
		{"raw", makeRaw},
	}

	fmt.Println("-- slave -> master (program output, master unread) --")
	for _, m := range modes {
		master, slave := openPTY()
		m.set(slave)
		must(unix.SetNonblock(slave, true))
		must(unix.SetNonblock(master, true))
		n1 := fillByteAtATime(slave)
		time.Sleep(50 * time.Millisecond)
		extra := fillByteAtATime(slave) // a second attempt after the flip worker ran
		got := drain(master)
		fmt.Printf("  %-16s byte-at-a-time: %d bytes before EAGAIN (+%d after 50ms retry), drained %d\n", m.name, n1, extra, got)
		check(got == n1+extra, "%s 1-byte: drained bytes equal written bytes (EAGAIN meant full, nothing lost)", m.name)
		again, err := unix.Write(slave, []byte{'z'})
		check(again == 1 && err == nil, "%s negative control: after drain a write succeeds again (err=%v)", m.name, err)
		drain(master)
		unix.Close(master)
		unix.Close(slave)

		master, slave = openPTY()
		m.set(slave)
		must(unix.SetNonblock(slave, true))
		must(unix.SetNonblock(master, true))
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'b'
		}
		bt, bw := fillBulk(slave, chunk)
		time.Sleep(50 * time.Millisecond)
		bt2, _ := fillBulk(slave, chunk)
		got = drain(master)
		fmt.Printf("  %-16s 4KB chunks:     %d bytes in %d writes before EAGAIN (+%d after 50ms retry), drained %d\n", m.name, bt, bw, bt2, got)
		check(got == bt+bt2, "%s 4KB: drained bytes equal written bytes", m.name)
		unix.Close(master)
		unix.Close(slave)
	}

	fmt.Println("-- master -> slave (input to program, slave unread) --")
	inModes := []ptyMode{
		{"cooked(echo on)", func(int) {}},
		{"cooked(echo off)", echoOff},
		{"raw", makeRaw},
	}
	fmt.Println("  (-1 means the kernel accepted >1 MiB without EAGAIN)")
	for _, m := range inModes {
		master, slave := openPTY()
		m.set(slave)
		must(unix.SetNonblock(slave, true))
		must(unix.SetNonblock(master, true))
		n1 := fillByteAtATime(master)
		time.Sleep(50 * time.Millisecond)
		extra := fillByteAtATime(master)
		readable := drain(slave)
		echoed := drain(master)
		fmt.Printf("  %-16s byte-at-a-time: %d bytes accepted before EAGAIN (+%d after retry); slave read %d, echoed back to master %d\n", m.name, n1, extra, readable, echoed)
		unix.Close(master)
		unix.Close(slave)

		master, slave = openPTY()
		m.set(slave)
		must(unix.SetNonblock(slave, true))
		must(unix.SetNonblock(master, true))
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'c'
		}
		bt, bw := fillBulk(master, chunk)
		time.Sleep(50 * time.Millisecond)
		readable = drain(slave)
		fmt.Printf("  %-16s 4KB chunks:     %d bytes accepted in %d writes before EAGAIN; slave read %d\n", m.name, bt, bw, readable)
		unix.Close(master)
		unix.Close(slave)
	}
	{
		master, slave := openPTY()
		echoOff(slave)
		must(unix.SetNonblock(slave, true))
		must(unix.SetNonblock(master, true))
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'd'
			if i%64 == 63 {
				chunk[i] = '\n'
			}
		}
		bt, bw := fillBulk(master, chunk)
		time.Sleep(50 * time.Millisecond)
		readable := drain(slave)
		fmt.Printf("  %-16s 4KB chunks with a newline every 64 bytes: %d bytes accepted in %d writes before EAGAIN; slave read %d\n", "cooked(echo off)", bt, bw, readable)
		check(readable == bt, "cooked with newlines: every accepted input byte is readable by the slave (%d of %d)", readable, bt)
		unix.Close(master)
		unix.Close(slave)
	}
	fmt.Println("  note: in canonical mode without a newline the slave's read() returns nothing (no complete line), so 'slave read 0' is expected there")

	fmt.Println("-- blocking child writes 200000 bytes, master unread for 1s --")
	blockingChild(true)
	fmt.Println("-- negative control: same child, master read concurrently from the start --")
	blockingChild(false)

	bad := make([]byte, childBytes)
	for i := range bad {
		bad[i] = pattern(i)
	}
	bad[123456] ^= 0xff
	check(verify(bad) == 123456, "negative control: verifier detects a single corrupted byte at 123456 (got %d)", verify(bad))
	finish("ptybuf")
}

func pattern(i int) byte { return byte(i % 251) }

func verify(b []byte) int {
	if len(b) != childBytes {
		return -2
	}
	for i, c := range b {
		if c != pattern(i) {
			return i
		}
	}
	return -1
}

func blockingChild(holdOff bool) {
	master, slave := openPTY()
	makeRaw(slave)
	cmd := exec.Command("/proc/self/exe", "ptybuf-child")
	cmd.Stdout = os.NewFile(uintptr(slave), "slave")
	cmd.Stderr = os.Stderr
	start := time.Now()
	must(cmd.Start())
	unix.Close(slave)
	got := make([]byte, 0, childBytes)
	buf := make([]byte, 65536)
	if holdOff {
		time.Sleep(time.Second)
		var ws unix.WaitStatus
		pid, _ := unix.Wait4(cmd.Process.Pid, &ws, unix.WNOHANG|unix.WNOWAIT, nil)
		_ = pid
		stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cmd.Process.Pid))
		wchan, _ := os.ReadFile(fmt.Sprintf("/proc/%d/wchan", cmd.Process.Pid))
		fmt.Printf("  after 1s unread: child state=%q wchan=%q\n", procState(string(stat)), string(wchan))
		check(procState(string(stat)) == "S", "child is still running and sleeping (blocked in write), not exited and not errored")
	}
	for len(got) < childBytes {
		n, err := unix.Read(master, buf)
		if n > 0 {
			got = append(got, buf[:n]...)
		}
		if err != nil {
			fmt.Printf("  read err after %d bytes: %v\n", len(got), err)
			break
		}
	}
	err := cmd.Wait()
	el := time.Since(start)
	fmt.Printf("  child exit=%v, bytes read=%d, elapsed=%v\n", err, len(got), el.Round(time.Millisecond))
	check(err == nil, "child exited 0 (its write never errored)")
	check(verify(got) == -1, "all %d bytes arrived intact and in order (verify=%d)", childBytes, verify(got))
	if !holdOff {
		check(el < 500*time.Millisecond, "with a reader the child finishes fast (%v), so the 1s hold above really was blocking", el.Round(time.Millisecond))
	}
	unix.Close(master)
}

func procState(stat string) string {
	// field 3 after the ")" of comm
	for i := len(stat) - 1; i >= 0; i-- {
		if stat[i] == ')' {
			if i+2 < len(stat) {
				return string(stat[i+2])
			}
		}
	}
	return "?"
}

func ptyBufChild() {
	b := make([]byte, childBytes)
	for i := range b {
		b[i] = pattern(i)
	}
	n, err := os.Stdout.Write(b)
	if err != nil || n != childBytes {
		fmt.Fprintf(os.Stderr, "child write n=%d err=%v\n", n, err)
		os.Exit(3)
	}
	os.Exit(0)
}
