package service

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// unitFile is the unit's file name.
func unitFile(name string) string { return name + ".service" }

// ownUnitDir is the folder of user units under this process's config home:
// XDG_CONFIG_HOME, or HOME/.config. tuios writes, finds and removes its unit
// there only, so a process with a home of its own never touches the units of
// the person's login.
func ownUnitDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user")
	}
	return filepath.Join(ownHome(), ".config", "systemd", "user")
}

// unitDirs are the folders a user unit of tuios may be in. With the
// manager-home knob of a test, they are the account's folder and this
// process's, as before.
func unitDirs() []string {
	if !useManagerHome() {
		return []string{ownUnitDir()}
	}
	dirs := []string{filepath.Join(passwdHome(), ".config", "systemd", "user")}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		if d := filepath.Join(x, "systemd", "user"); d != dirs[0] {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// findUnit is the path of the unit, or "" when none is installed.
func findUnit(name string) string {
	for _, dir := range unitDirs() {
		p := filepath.Join(dir, unitFile(name))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// enabled reports whether the unit at path is enabled: its link is in
// default.target.wants beside it.
func enabled(path string) bool {
	_, err := os.Lstat(filepath.Join(filepath.Dir(path), "default.target.wants", filepath.Base(path)))
	return err == nil
}

// managerRuntimeDir is where the user's service manager listens:
// /run/user/UID. systemctl finds it through XDG_RUNTIME_DIR. A process that
// moved that variable, as a test or an isolated server does, finds no
// manager, and that is right: it must not change the person's services.
// Only the manager-home knob of a test points systemctl back at it.
func managerRuntimeDir() string {
	dir := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

func systemctl(args ...string) (string, error) {
	cmd := childproc.Command("systemctl", append([]string{"--user"}, args...)...)
	if dir := managerRuntimeDir(); useManagerHome() && dir != "" && os.Getenv("XDG_RUNTIME_DIR") != dir {
		cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+dir)
	}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil && s != "" {
		return s, errfmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), s)
	}
	return s, err
}

// ManagerEnv is the environment of the user's service manager. It fails
// when there is no systemd user manager to talk to.
func ManagerEnv() (map[string]string, error) {
	// A process with a home of its own asks the person's manager nothing.
	if err := checkHome(); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, ErrUnsupported
	}
	out, err := systemctl("show-environment")
	if err != nil {
		return nil, errfmt.Errorf("no systemd user manager answers: %w", err)
	}
	env := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}

// managerUnitDir is the folder the manager reads user units from.
func managerUnitDir(env map[string]string) string {
	if x := env["XDG_CONFIG_HOME"]; x != "" {
		return filepath.Join(x, "systemd", "user")
	}
	home := env["HOME"]
	if home == "" {
		home = passwdHome()
	}
	return filepath.Join(home, ".config", "systemd", "user")
}

// systemdQuote quotes one word of ExecStart= or Environment=.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

// unit is the unit that runs the spec's daemon.
func unit(s Spec) string {
	argv := LoginCommand(s.Shell, s.Binary)
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = systemdQuote(a)
	}
	var env strings.Builder
	for _, kv := range s.Env {
		env.WriteString("Environment=")
		env.WriteString(systemdQuote(kv))
		env.WriteString("\n")
	}
	dir := ""
	if s.Dir != "" {
		// WorkingDirectory= takes the path as it is, with no quotes.
		dir = "WorkingDirectory=" + strings.ReplaceAll(s.Dir, "%", "%%") + "\n"
	}
	return `# The TUIOS daemon. tuios daemon install-service wrote this file.
` + socketMarker + s.Socket + `
` + binaryMarker + s.Binary + `
[Unit]
Description=TUIOS daemon (` + s.Name + `)

[Service]
Type=simple
ExecStart=` + strings.Join(quoted, " ") + `
` + dir + env.String() + `# The daemon follows the display of the client that attaches.
UnsetEnvironment=DISPLAY WAYLAND_DISPLAY XAUTHORITY
# The daemon stops its panes itself. Other processes in this unit, such as
# a program started with nohup in a pane or the keeper of a live upgrade,
# must outlive it.
KillMode=process
# An OOM kill of one pane's process leaves the daemon and the other panes
# running. The daemon also starts each pane in a scope of its own.
OOMPolicy=continue
# kill-server exits 0 and the daemon stays stopped. A crash restarts it.
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}

// managerDir is the folder the user's manager reads units from, when it is
// the folder of this process's config home too. A process with a home of
// its own, or a config home the manager does not read, gets an error, so
// it writes no unit the manager would miss and changes no service of the
// person.
func managerDir() (string, error) {
	if err := checkHome(); err != nil {
		return "", err
	}
	env, err := ManagerEnv()
	if err != nil {
		return "", err
	}
	dir := managerUnitDir(env)
	if useManagerHome() {
		return dir, nil
	}
	if own := ownUnitDir(); !samePath(own, dir) {
		return "", errfmt.Errorf("the systemd user manager reads units from %s, and this tuios keeps its config in %s, so the manager would not find the service. Unset XDG_CONFIG_HOME, or set it as the manager has it", dir, own)
	}
	return dir, nil
}

// Render returns the file a service of s is written to, and its text. It
// asks the service manager where its units go.
func Render(s Spec) (string, string, error) {
	if err := s.check(); err != nil {
		return "", "", err
	}
	dir, err := managerDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, unitFile(s.Name)), unit(s), nil
}

// Install writes the unit, enables it, and with start starts it. A unit that
// does not start is removed again, so Installed never reports a broken one.
func Install(s Spec, start bool) error {
	path, text, err := Render(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // systemd reads a unit file; it holds no secret
		return err
	}
	undo := func(err error) error {
		_, _ = systemctl("disable", unitFile(s.Name))
		_ = os.Remove(path)
		_, _ = systemctl("daemon-reload")
		return err
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return undo(err)
	}
	if _, err := systemctl("enable", unitFile(s.Name)); err != nil {
		return undo(err)
	}
	if start {
		if _, err := systemctl("start", unitFile(s.Name)); err != nil {
			return undo(err)
		}
	}
	return nil
}

// Uninstall disables the unit and removes its file. It does not stop the
// daemon: that would end the programs in its panes.
func Uninstall(name string) error {
	path := findUnit(name)
	if path == "" {
		return nil
	}
	// The manager is told only when it reads this folder. Otherwise a unit
	// of the same name in the person's own folder would be disabled.
	_, managed := managerDir()
	if managed == nil {
		_, _ = systemctl("disable", unitFile(name))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if managed == nil {
		_, _ = systemctl("daemon-reload")
	}
	return nil
}

// Serves reports whether an enabled service of this name serves socket. It
// reads files only, so a client can ask on every daemon start.
func Serves(name, socket string) bool {
	path := findUnit(name)
	if path == "" || !enabled(path) {
		return false
	}
	sock, _ := readMarkers(path)
	return sameSocket(sock, socket)
}

// Start starts the service.
func Start(name string) error {
	if _, err := managerDir(); err != nil {
		return err
	}
	_, err := systemctl("start", unitFile(name))
	return err
}

// Get tells how the service stands.
func Get(name string) Status {
	st := Status{Name: name, Linger: true}
	if u, err := user.Current(); err == nil {
		st.User = u.Username
		st.Linger = Lingering(u.Username)
	}
	_, err := exec.LookPath("systemctl")
	st.Supported = err == nil
	path := findUnit(name)
	if path == "" {
		return st
	}
	st.File, st.Installed, st.Enabled = path, true, enabled(path)
	st.Socket, st.Binary = readMarkers(path)
	// The manager's state is of its own unit of this name, which is not this
	// file when the manager does not read this folder.
	if _, err := managerDir(); err != nil {
		return st
	}
	if out, _ := systemctl("is-active", unitFile(name)); out != "" {
		st.Detail = out
		st.Running = out == "active"
	}
	if out, _ := systemctl("show", "--property=MainPID", "--value", unitFile(name)); out != "" {
		st.PID, _ = strconv.Atoi(out)
	}
	return st
}

// Lingering reports whether systemd keeps the services of the user running
// with no login, and starts them at boot.
func Lingering(username string) bool {
	_, err := os.Stat(filepath.Join("/var/lib/systemd/linger", username))
	return err == nil
}

// UsesLinger reports whether this system has the linger setting.
func UsesLinger() bool { return true }

// unescapeMarker reads a marker as written: a unit escapes nothing.
func unescapeMarker(s string) string { return s }
