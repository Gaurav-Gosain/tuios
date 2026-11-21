//go:build !windows

package federation

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

// SharedMasterDir is where a client that can ask the person for a secret
// keeps the ssh master connections it opened: tuios-gpui opens the first
// connection to a machine itself, with its passphrase, password or second
// factor prompts in its own window, as a master socket in this folder. The
// daemon's links never prompt (BatchMode), so this is how a machine that
// needs a password or a code is reached at all, and no secret is stored.
//
// It is the folder "cm" beside the daemon's socket, as the stash store's
// folder is: <sockpath.Dir>/cm, which is $XDG_RUNTIME_DIR/tuios/cm (found at
// /run/user/<uid>/tuios/cm when the variable is unset), or /tmp/tuios-<uid>/cm,
// and <that folder>/servers/<name>/cm for a named server. Each master
// is named by ssh's %C, a hash of the local host, the remote host, the port
// and the user, so the client and the daemon name the same socket for the
// same destination without agreeing on more.
func SharedMasterDir() string {
	return filepath.Join(sockpath.Dir(), "cm")
}

func reuseOptions(h Host) []string {
	dir := SharedMasterDir()
	if len(dir)+1+masterNameLen > sockpath.MaxLen() {
		return nil
	}
	if !privateDir(filepath.Dir(dir)) || !privateDir(dir) {
		return nil
	}
	c, ok := ReadSSHConfig(h)
	if !ok {
		return nil
	}
	if c.ControlPath != "" && c.ControlPath != "none" {
		return nil
	}
	if c.ForwardAgent != "" && !strings.EqualFold(c.ForwardAgent, "no") {
		return nil
	}
	return reuseArgs(dir)
}

func freshOptions() []string {
	return []string{"-o", "ControlPath=none"}
}

func privateDir(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return false
	}
	return st.Mode().Perm()&0o077 == 0
}

const masterNameLen = 40

func reuseArgs(dir string) []string {
	return []string{
		"-o", "ControlMaster=no",
		"-o", "ControlPath=" + dir + "/%C",
	}
}
