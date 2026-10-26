//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// daemonAppEnvFileFlag hands the launcher a file with the starting client's
// environment. open(1) does not pass the caller's environment, and argv would
// show it to every user in ps.
const daemonAppEnvFileFlag = "--env-file"

// isDaemonAppLauncher reports whether this process is the executable of the
// TUIOS Daemon app rather than the tuios CLI. LaunchServices passes the full
// path of the executable as argv[0].
func isDaemonAppLauncher() bool {
	return len(os.Args) > 0 && filepath.Base(os.Args[0]) == daemonAppExecutable
}

// runDaemonAppLauncher is the whole life of the app's executable: it execs the
// tuios the bundle names, in this process, so the daemon keeps the pid that
// LaunchServices started as the app. It returns only when the exec fails.
//
// This code is frozen in every installed bundle: a bundle is never replaced,
// so a later tuios cannot change what an older copy does. Keep the argument
// and env-file formats as they are.
func runDaemonAppLauncher() int {
	if logPath := daemonAppLogPath(); logPath != "" {
		_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
		if fh, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_ = syscall.Dup2(int(fh.Fd()), 1)
			_ = syscall.Dup2(int(fh.Fd()), 2)
			_ = fh.Close()
		}
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(os.Stderr, "%s tuios-daemon-app: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
		return 1
	}

	exe, err := os.Executable()
	if err != nil {
		return fail("find the app: %v", err)
	}
	contents := filepath.Dir(filepath.Dir(exe))
	target, err := daemonAppTarget(filepath.Dir(contents))
	if err != nil {
		return fail("read %s: %v", daemonAppTargetFile, err)
	}

	var envFile string
	var args []string
	rest := os.Args[1:]
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case strings.HasPrefix(a, "-psn_"):
			// Old LaunchServices adds a process serial number.
		case a == daemonAppEnvFileFlag && i+1 < len(rest) && envFile == "" && len(args) == 0:
			envFile = rest[i+1]
			i++
		default:
			args = append(args, a)
		}
	}
	if len(args) == 0 {
		args = []string{"daemon"}
	}
	argv := append([]string{"tuios"}, args...)

	if envFile != "" {
		env, err := readDaemonAppEnvFile(envFile)
		if err != nil {
			return fail("read the environment file: %v", err)
		}
		err = syscall.Exec(target, argv, env)
		return fail("start %s: %v", target, err)
	}

	// No client started this daemon: the login item or a person opened the
	// app. LaunchServices gives an app a minimal environment, and the panes
	// inherit the daemon's. A login shell reads the profile (where Homebrew's
	// PATH is set) and then execs tuios, still in this process.
	if shell := os.Getenv("SHELL"); filepath.IsAbs(shell) {
		switch filepath.Base(shell) {
		case "sh", "bash", "zsh", "dash", "ksh":
			// The command string ends option parsing, so the tuios
			// arguments after it are only operands: $0 and "$@".
			_ = syscall.Exec(shell, append([]string{shell, "-l", "-c", `exec "$0" "$@"`, target}, args...), os.Environ())
		}
	}
	env := os.Environ()
	path := strings.Join([]string{filepath.Dir(target), "/opt/homebrew/bin", "/usr/local/bin", os.Getenv("PATH")}, ":")
	env = append(env, "PATH="+path)
	err = syscall.Exec(target, argv, env)
	return fail("start %s: %v", target, err)
}

// readDaemonAppEnvFile reads the NUL-separated environment a client wrote and
// removes the file. It accepts only a regular file of this user that no one
// else can read, since the file holds the client's whole environment.
func readDaemonAppEnvFile(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is not a private file of this user", path)
	}
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return nil, err
	}
	var env []string
	for kv := range bytes.SplitSeq(data, []byte{0}) {
		if len(kv) > 0 {
			env = append(env, string(kv))
		}
	}
	return env, nil
}

// startDaemonThroughApp starts the daemon through the TUIOS Daemon app when it
// is installed, so the daemon is the app's responsibility and not the
// terminal's. It reports false when the app is not used, and the caller then
// starts the daemon directly, as it always has.
func startDaemonThroughApp(daemonArgs []string) (bool, error) {
	if os.Getenv(daemonAppOffEnv) == "1" {
		return false, nil
	}
	app := defaultDaemonAppPath()
	if app == "" || !isDaemonApp(app) {
		return false, nil
	}
	envFile, err := writeDaemonAppEnvFile()
	if err != nil {
		return false, nil
	}
	args := append([]string{"-g", "-n", "-a", app, "--args", daemonAppEnvFileFlag, envFile, "daemon"}, daemonArgs...)
	if out, err := exec.Command("/usr/bin/open", args...).CombinedOutput(); err != nil {
		_ = os.Remove(envFile)
		fmt.Fprintf(os.Stderr, "Could not start the daemon through %s: %v %s\nStarting it directly.\n", app, err, strings.TrimSpace(string(out)))
		return false, nil
	}
	deadline := time.Now().Add(daemonStartTimeout)
	for time.Now().Before(deadline) {
		if session.IsDaemonRunning() {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.Remove(envFile)
	return true, errors.New("the daemon did not start through " + app + " within " + daemonStartTimeout.String() +
		". Look in " + daemonAppLogPath() + ", or set " + daemonAppOffEnv + "=1 to start it directly")
}

// writeDaemonAppEnvFile writes this process's environment to a private file
// for the launcher, so a daemon started through the app has the same
// environment as one started directly.
func writeDaemonAppEnvFile() (string, error) {
	fh, err := os.CreateTemp("", "tuios-daemon-env-")
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	for _, kv := range os.Environ() {
		buf.WriteString(kv)
		buf.WriteByte(0)
	}
	if _, err := fh.Write(buf.Bytes()); err != nil {
		_ = fh.Close()
		_ = os.Remove(fh.Name())
		return "", err
	}
	if err := fh.Close(); err != nil {
		_ = os.Remove(fh.Name())
		return "", err
	}
	return fh.Name(), nil
}
