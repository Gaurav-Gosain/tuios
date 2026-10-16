// Probe 3: exec plus Go init time of the real tuios binary (bounds the "exec + runtime init"
// part of the handoff pause).
//
//	cd .. && GOWORK=off GOFLAGS=-mod=mod GOPROXY=off go build -o p3bin ./p3 && ./p3bin [/path/to/tuios]
//
// Three measurements:
//  1. fork+exec from a parent: Start -> first stdout byte, Start -> exit (50 runs each)
//     for tuios --version and a minimal Go hello.
//  2. syscall.Exec in place from a Go process holding a big touched heap (as the daemon
//     would), timestamp just before Exec -> first stdout byte of the new image.
//  3. first exec of a freshly copied binary (new inode, as after an install).
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"
)

const helloSrc = `package main
import "fmt"
func main() { fmt.Println("hello") }
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "exec-in-place" {
		execInPlace()
		return
	}
	tuios := "/opt/homebrew/Cellar/tuios/0.8.5/bin/tuios"
	if len(os.Args) > 1 {
		tuios = os.Args[1]
	}
	dir, _ := os.MkdirTemp(filepath.Dir(must(os.Executable())), "p3work")
	defer os.RemoveAll(dir)
	hello := filepath.Join(dir, "hello")
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte(helloSrc), 0o644)
	b := exec.Command("go", "build", "-o", hello, filepath.Join(dir, "hello.go"))
	b.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=off")
	if out, err := b.CombinedOutput(); err != nil {
		panic(string(out))
	}
	fi, _ := os.Stat(tuios)
	hi, _ := os.Stat(hello)
	fmt.Printf("tuios: %s (%d bytes); hello: %d bytes\n", tuios, fi.Size(), hi.Size())

	fmt.Println("\n== 1. fork+exec, 50 warm runs (after 3 discarded warmups) ==")
	for _, c := range []struct {
		name string
		argv []string
	}{{"tuios --version", []string{tuios, "--version"}}, {"go hello", []string{hello}}} {
		var first, exit []time.Duration
		for i := 0; i < 53; i++ {
			f, e := timeRun(c.argv)
			if i >= 3 {
				first, exit = append(first, f), append(exit, e)
			}
		}
		fmt.Printf("  %-16s start->first byte p50=%v p95=%v max=%v | start->exit p50=%v p95=%v max=%v\n",
			c.name, pct(first, 50), pct(first, 95), pct(first, 100), pct(exit, 50), pct(exit, 95), pct(exit, 100))
	}

	fmt.Println("\n== 2. syscall.Exec in place from a process with a touched heap (20 runs each) ==")
	self := must(os.Executable())
	for _, mb := range []int{0, 200, 1000} {
		for _, target := range [][]string{{tuios, "--version"}, {hello}} {
			var d []time.Duration
			for i := 0; i < 21; i++ {
				v := timeExecInPlace(self, mb, target)
				if i > 0 {
					d = append(d, v)
				}
			}
			fmt.Printf("  heap=%4d MB -> %-16s pre-Exec->first byte p50=%v p95=%v max=%v\n", mb, filepath.Base(target[0]), pct(d, 50), pct(d, 95), pct(d, 100))
		}
	}

	fmt.Println("\n== 3. first exec of a freshly copied binary (new inode), 5 copies each ==")
	for _, src := range []string{tuios, hello} {
		var firsts, seconds []time.Duration
		for i := 0; i < 5; i++ {
			cp := filepath.Join(dir, fmt.Sprintf("copy-%d-%s", i, filepath.Base(src)))
			data, _ := os.ReadFile(src)
			os.WriteFile(cp, data, 0o755)
			argv := []string{cp}
			if src == tuios {
				argv = append(argv, "--version")
			}
			f1, _ := timeRun(argv)
			f2, _ := timeRun(argv)
			firsts, seconds = append(firsts, f1), append(seconds, f2)
		}
		fmt.Printf("  %-6s fresh copy: 1st exec first-byte p50=%v max=%v | 2nd exec p50=%v max=%v\n", filepath.Base(src), pct(firsts, 50), pct(firsts, 100), pct(seconds, 50), pct(seconds, 100))
	}
	fmt.Println("\nNEG CONTROL: a target that sleeps 300ms before printing must measure >= 300ms:")
	f, _ := timeRun([]string{"/bin/sh", "-c", "sleep 0.3; echo x"})
	fmt.Printf("  sh sleep 0.3: start->first byte=%v  %s\n", f, map[bool]string{true: "PASS", false: "FAIL"}[f >= 300*time.Millisecond])
}

func timeRun(argv []string) (first, exit time.Duration) {
	cmd := exec.Command(argv[0], argv[1:]...)
	pr, pw, _ := os.Pipe()
	cmd.Stdout = pw
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	pw.Close()
	buf := make([]byte, 1)
	if _, err := pr.Read(buf); err != nil {
		panic(err)
	}
	first = time.Since(t0)
	io.Copy(io.Discard, pr)
	cmd.Wait()
	exit = time.Since(t0)
	pr.Close()
	return
}

// timeExecInPlace starts `self exec-in-place MB target...`, which touches MB of heap, prints its
// pre-Exec unix-nano timestamp on fd 3 and calls syscall.Exec(target). We time first stdout byte.
func timeExecInPlace(self string, mb int, target []string) time.Duration {
	args := append([]string{"exec-in-place", strconv.Itoa(mb)}, target...)
	cmd := exec.Command(self, args...)
	outR, outW, _ := os.Pipe()
	tsR, tsW, _ := os.Pipe()
	cmd.Stdout = outW
	cmd.ExtraFiles = []*os.File{tsW}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	outW.Close()
	tsW.Close()
	line, err := bufio.NewReader(tsR).ReadString('\n')
	if err != nil {
		panic(fmt.Sprintf("ts read: %v %q", err, line))
	}
	buf := make([]byte, 1)
	if _, err := outR.Read(buf); err != nil {
		panic(err)
	}
	got := time.Now()
	io.Copy(io.Discard, outR)
	cmd.Wait()
	ns, _ := strconv.ParseInt(line[:len(line)-1], 10, 64)
	return got.Sub(time.Unix(0, ns))
}

var sink [][]byte

func execInPlace() {
	mb, _ := strconv.Atoi(os.Args[2])
	for i := 0; i < mb; i++ {
		b := make([]byte, 1<<20)
		for j := 0; j < len(b); j += 4096 {
			b[j] = 1
		}
		sink = append(sink, b)
	}
	ts := os.NewFile(3, "ts")
	target := os.Args[3:]
	fmt.Fprintf(ts, "%d\n", time.Now().UnixNano())
	err := syscall.Exec(target[0], target, os.Environ()) // fd 3 is CLOEXEC from ExtraFiles? no: ExtraFiles are inherited without CLOEXEC; harmless
	panic(err)
}

func pct(d []time.Duration, p int) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)*p/100].Round(10 * time.Microsecond)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
