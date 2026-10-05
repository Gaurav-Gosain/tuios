// Command fakessh is a compiled ssh stand-in for the ssh split tests. Built as
// a binary, it is what tuios sees for a real ssh client: a process whose
// executable is the client itself, not an interpreter running a script.
//
// Each run takes the next number in the ssh-runs folder beside the folder it
// lives in, writes os.Args[0] to N.argv0 and its arguments one per line to
// N.argv, prints FAKESSH-RUN-N-UP, and then runs each line typed into it with
// sh, as a remote shell would.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(255)
	}
	if len(os.Args) > 1 && os.Args[1] == "-G" {
		// ssh -G: the host name from -o HostName, or the destination.
		host := os.Args[len(os.Args)-1]
		for i := 1; i+1 < len(os.Args); i++ {
			if v, ok := strings.CutPrefix(os.Args[i+1], "HostName="); ok && os.Args[i] == "-o" {
				host = v
			}
		}
		fmt.Printf("hostname %s\n", host)
		return
	}
	runs := filepath.Join(filepath.Dir(filepath.Dir(exe)), "ssh-runs")
	if err := os.MkdirAll(runs, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(255)
	}
	done, _ := filepath.Glob(filepath.Join(runs, "*.argv"))
	n := len(done)
	base := filepath.Join(runs, fmt.Sprint(n))
	_ = os.WriteFile(base+".argv0", []byte(os.Args[0]+"\n"), 0o600)
	var b strings.Builder
	for _, a := range os.Args[1:] {
		b.WriteString(a + "\n")
	}
	_ = os.WriteFile(base+".tmp", []byte(b.String()), 0o600)
	_ = os.Rename(base+".tmp", base+".argv")
	fmt.Printf("FAKESSH-RUN-%d-UP\n", n)
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		cmd := exec.Command("sh", "-c", sc.Text())
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
		_ = cmd.Run()
	}
}
