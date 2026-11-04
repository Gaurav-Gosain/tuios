package remote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"
)

// agentMark starts the line the login shell prints its SSH agent on,
// after whatever its profile printed.
const agentMark = "__kero_ssh_auth_sock="

// shellAgent is the SSH agent the user's login shell names, as
// 1Password's when a profile sets SSH_AUTH_SOCK to it: ssh in a terminal
// uses that agent, and an app opened from the Dock or a launcher never
// inherits it. "" for a process a terminal started, which has the shell's
// environment already, and when the shell names no agent that is there.
var shellAgent = sync.OnceValue(func() string {
	if runtime.GOOS == "windows" || os.Getenv("TERM") != "" {
		return ""
	}
	sh := shell.Login()
	switch filepath.Base(sh) {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "mksh", "yash":
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Interactive as a terminal's, for the profiles that set it in .zshrc.
	cmd := exec.CommandContext(ctx, sh, "-l", "-i", "-c", `printf '\n`+agentMark+`%s\n' "$SSH_AUTH_SOCK"`)
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	s := agentLine(out)
	if s == "" {
		return ""
	}
	if _, err := os.Stat(s); err != nil {
		return ""
	}
	return s
})

// agentLine reads the agent off the shell's output, the last line that
// agentMark starts, where a profile may print anything before it.
func agentLine(out []byte) string {
	i := bytes.LastIndex(out, []byte("\n"+agentMark))
	if i < 0 {
		return ""
	}
	sock, _, _ := bytes.Cut(out[i+1+len(agentMark):], []byte("\n"))
	return string(sock)
}

// sshEnv is the environment ssh runs in: this process's, with the login
// shell's SSH agent where it names another.
func sshEnv() []string {
	env := os.Environ()
	if s := shellAgent(); s != "" && s != os.Getenv("SSH_AUTH_SOCK") {
		env = append(env, "SSH_AUTH_SOCK="+s)
	}
	return env
}
