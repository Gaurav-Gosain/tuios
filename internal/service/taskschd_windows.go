package service

import (
	"encoding/binary"
	"fmt"
	"html"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// On Windows the daemon runs as a task of the user in Task Scheduler,
// \TUIOS\NAME. It needs no administrator. It starts when the user logs on,
// in their session, and runs while they are logged on. conhost --headless
// runs it without a window, which tuios.exe, a console program, would have.

func task(name string) string { return `\TUIOS\` + name }

// file is the task as Install registered it. Its presence says the task is
// installed without asking Task Scheduler.
func file(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "tuios", name+".task.xml")
}

func conhost() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "conhost.exe")
}

// definition is the task that runs the spec's daemon. Task Scheduler starts
// it again when it fails, and never runs two.
func definition(s Spec, account string) string {
	var args strings.Builder
	args.WriteString(`--headless "` + s.Binary + `" daemon --service`)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<!-- The TUIOS daemon. tuios daemon install-service wrote this file. -->
<!-- %[5]s%[6]s -->
<!-- %[7]s%[8]s -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>The TUIOS daemon. tuios daemon install-service wrote this task.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>
      <Arguments>%[3]s</Arguments>
      <WorkingDirectory>%[4]s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, html.EscapeString(account), html.EscapeString(conhost()), html.EscapeString(args.String()), html.EscapeString(s.Dir),
		socketMarker, html.EscapeString(s.Socket), binaryMarker, html.EscapeString(s.Binary))
}

// utf16File is s as schtasks reads a task: UTF-16 with its byte order mark.
func utf16File(s string) []byte {
	u := utf16.Encode([]rune("\ufeff" + strings.ReplaceAll(s, "\n", "\r\n")))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func schtasks(args ...string) error {
	out, err := childproc.Command("schtasks.exe", args...).CombinedOutput()
	if err != nil {
		return errfmt.Errorf("schtasks %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

func account() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// Render returns the task's file and its text.
func Render(s Spec) (string, string, error) {
	if err := s.check(); err != nil {
		return "", "", err
	}
	a, err := account()
	if err != nil {
		return "", "", err
	}
	return file(s.Name), definition(s, a), nil
}

// ManagerEnv is empty on Windows: the task runs with the user's own
// environment, and nothing is carried.
func ManagerEnv() (map[string]string, error) { return map[string]string{}, nil }

// Install registers the task and, with start, runs it.
func Install(s Spec, start bool) error {
	path, text, err := Render(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, utf16File(text), 0o600); err != nil {
		return err
	}
	if err := schtasks("/Create", "/TN", task(s.Name), "/XML", path, "/F"); err != nil {
		_ = os.Remove(path)
		return err
	}
	if start {
		_ = schtasks("/Run", "/TN", task(s.Name))
	}
	return nil
}

// Uninstall removes the task. Its daemon runs on.
func Uninstall(name string) error {
	if _, err := os.Stat(file(name)); err != nil {
		return nil
	}
	_ = schtasks("/Delete", "/TN", task(name), "/F")
	return os.Remove(file(name))
}

// Serves is false on Windows: a client starts the daemon itself, and the
// task matters only at logon.
func Serves(string, string) bool { return false }

// Start runs the task.
func Start(name string) error { return schtasks("/Run", "/TN", task(name)) }

// Get tells how the task stands.
func Get(name string) Status {
	st := Status{Name: name, Supported: true, Linger: true}
	if a, err := account(); err == nil {
		st.User = a
	}
	path := file(name)
	if _, err := os.Stat(path); err != nil {
		return st
	}
	st.File, st.Installed, st.Enabled = task(name), true, true
	st.Detail = "the task " + task(name)
	return st
}

// Lingering is always true on Windows: the task runs while the user is
// logged on, and there is no setting for more.
func Lingering(string) bool { return true }

// UsesLinger reports whether this system has the linger setting.
func UsesLinger() bool { return false }

// unescapeMarker reads a marker from the XML comment it is in.
func unescapeMarker(s string) string { return html.UnescapeString(s) }
