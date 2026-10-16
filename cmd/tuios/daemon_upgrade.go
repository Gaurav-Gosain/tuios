package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// adoptFDFlag is the daemon command's flag naming the descriptor a live
// upgrade's manifest is on.
const adoptFDFlag = "adopt-fd"

// handoffArgs is the command line, after the program, a live upgrade execs
// the next daemon image with: this daemon's own, which carries its server and
// its log level, plus the manifest's descriptor. A flag left by an earlier
// upgrade is dropped.
func handoffArgs(manifestFD int) []string {
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "--"+adoptFDFlag) {
			continue
		}
		args = append(args, a)
	}
	return append(args, "--"+adoptFDFlag+"="+strconv.Itoa(manifestFD))
}

// runAdoptingDaemon runs the daemon image a live upgrade execed: it takes
// over the panes, sockets and lock its predecessor handed down, rather than
// starting fresh. The socket is live, so the running-daemon check a fresh
// start makes would find this very process.
func runAdoptingDaemon(manifestFD int) error {
	return session.NewDaemon(foregroundDaemonConfig()).RunAdopted(manifestFD)
}
