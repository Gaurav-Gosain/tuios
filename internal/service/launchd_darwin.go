package service

import (
	"fmt"
	"html"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// label is the agent's launchd label.
func label(name string) string { return "dev.tuios." + name }

func plistPath(name string) string {
	return filepath.Join(passwdHome(), "Library", "LaunchAgents", label(name)+".plist")
}

func passwdHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func launchctl(args ...string) error {
	out, err := childproc.Command("/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return errfmt.Errorf("launchctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// plist is the agent that runs the spec's daemon at login, and again after
// a crash. A clean exit, as after kill-server, leaves it stopped.
func plist(s Spec) string {
	argv := LoginCommand(s.Shell, s.Binary)
	keepAlive := "\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n"
	if s.App != "" {
		// open(1) returns at once, so launchd cannot see the daemon end.
		argv = []string{"/usr/bin/open", "-g", "-a", s.App, "--args", "daemon", "--service"}
		keepAlive = ""
	}
	var args, env strings.Builder
	for _, a := range argv {
		args.WriteString("\t\t<string>" + html.EscapeString(a) + "</string>\n")
	}
	if len(s.Env) > 0 {
		env.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, kv := range s.Env {
			k, v, _ := strings.Cut(kv, "=")
			env.WriteString("\t\t<key>" + html.EscapeString(k) + "</key>\n\t\t<string>" + html.EscapeString(v) + "</string>\n")
		}
		env.WriteString("\t</dict>\n")
	}
	dir := ""
	if s.Dir != "" {
		dir = "\t<key>WorkingDirectory</key>\n\t<string>" + html.EscapeString(s.Dir) + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- The TUIOS daemon. tuios daemon install-service wrote this file. -->
<!-- ` + socketMarker + html.EscapeString(s.Socket) + ` -->
<!-- ` + binaryMarker + html.EscapeString(s.Binary) + ` -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label(s.Name) + `</string>
	<key>ProgramArguments</key>
	<array>
` + args.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
` + keepAlive + `	<key>ProcessType</key>
	<string>Interactive</string>
` + dir + env.String() + `</dict>
</plist>
`
}

// Render returns the agent's file and its text.
func Render(s Spec) (string, string, error) {
	if err := s.check(); err != nil {
		return "", "", err
	}
	return plistPath(s.Name), plist(s), nil
}

// ManagerEnv is empty on macOS: launchd gives an agent no variables of the
// login, so every carried one differs.
func ManagerEnv() (map[string]string, error) { return map[string]string{}, nil }

// Install writes the agent and loads it. Loading runs it once (RunAtLoad).
// A daemon that already runs makes that one exit 0, so start changes
// nothing here.
func Install(s Spec, _ bool) error {
	path, text, err := Render(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = launchctl("bootout", domain()+"/"+label(s.Name))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // launchd reads the agent; it holds no secret
		return err
	}
	if err := launchctl("bootstrap", domain(), path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Uninstall removes the agent. bootout of an agent whose program runs would
// end the daemon, so the file goes and the agent stays loaded until logout.
func Uninstall(name string) error {
	path := plistPath(name)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return os.Remove(path)
}

// Serves reports whether an installed agent of this name serves socket.
func Serves(name, socket string) bool {
	sock, _ := readMarkers(plistPath(name))
	return sameSocket(sock, socket)
}

// Start starts the agent's program.
func Start(name string) error {
	if err := launchctl("kickstart", domain()+"/"+label(name)); err == nil {
		return nil
	}
	return launchctl("bootstrap", domain(), plistPath(name))
}

// Get tells how the agent stands.
func Get(name string) Status {
	st := Status{Name: name, Supported: true, Linger: true}
	if u, err := user.Current(); err == nil {
		st.User = u.Username
	}
	path := plistPath(name)
	if _, err := os.Stat(path); err != nil {
		return st
	}
	st.File, st.Installed, st.Enabled = path, true, true
	st.Socket, st.Binary = readMarkers(path)
	out, err := childproc.Command("/bin/launchctl", "print", domain()+"/"+label(name)).Output()
	if err == nil {
		for l := range strings.SplitSeq(string(out), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), " = "); ok && k == "state" {
				st.Detail = v
				st.Running = v == "running"
			}
			if k, v, ok := strings.Cut(strings.TrimSpace(l), " = "); ok && k == "pid" {
				st.PID, _ = strconv.Atoi(v)
			}
		}
	} else {
		st.Detail = "not loaded"
	}
	return st
}

// Lingering is always true on macOS: an agent runs while its user is logged
// in, and there is no setting for more.
func Lingering(string) bool { return true }

// UsesLinger reports whether this system has the linger setting.
func UsesLinger() bool { return false }

// unescapeMarker reads a marker from the XML comment it is in.
func unescapeMarker(s string) string { return html.UnescapeString(s) }
