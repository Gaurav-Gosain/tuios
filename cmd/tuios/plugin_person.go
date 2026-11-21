//go:build unix

package main

import (
	"net"
	"path/filepath"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

func liveDaemonSockets() []string {
	var bases []string
	for _, b := range []string{sockpath.BaseDir(), sockpath.OtherDir()} {
		if b != "" && !slices.Contains(bases, filepath.Clean(b)) {
			bases = append(bases, filepath.Clean(b))
		}
	}
	var live []string
	for _, base := range bases {
		cands := []string{filepath.Join(base, "tuios.sock")}
		named, _ := filepath.Glob(filepath.Join(base, serverscope.SocketDirName, "*", "tuios.sock"))
		for _, sock := range append(cands, named...) {
			if conn, err := net.DialTimeout("unix", sock, time.Second); err == nil {
				_ = conn.Close()
				live = append(live, sock)
			}
		}
	}
	return live
}
