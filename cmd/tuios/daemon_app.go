package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/spf13/cobra"
)

const (
	daemonAppExecutable = "tuios-daemon-app"
	daemonAppBundleID   = "dev.tuios.daemon"
	daemonAppName       = "TUIOS Daemon"
	daemonAppTargetFile = "Resources/tuios-path"
	daemonAppOffEnv     = "TUIOS_NO_DAEMON_APP"
)

const daemonAppInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + daemonAppBundleID + `</string>
	<key>CFBundleName</key>
	<string>` + daemonAppName + `</string>
	<key>CFBundleDisplayName</key>
	<string>` + daemonAppName + `</string>
	<key>CFBundleExecutable</key>
	<string>` + daemonAppExecutable + `</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSLocalNetworkUsageDescription</key>
	<string>The TUIOS daemon runs your terminal sessions. Programs in your panes connect to devices on your local network.</string>
	<key>NSMicrophoneUsageDescription</key>
	<string>Programs in your TUIOS panes, such as voice input in coding agents, use the microphone.</string>
</dict>
</plist>
`

func defaultDaemonAppPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Applications", daemonAppName+".app")
}

func daemonAppAgentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "LaunchAgents", daemonAppBundleID+".plist")
}

func daemonAppLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", "tuios-daemon-app.log")
}

func isDaemonApp(app string) bool {
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	return err == nil && strings.Contains(string(data), "<string>"+daemonAppBundleID+"</string>")
}

func daemonAppTarget(app string) (string, error) {
	data, err := os.ReadFile(filepath.Join(app, "Contents", daemonAppTargetFile))
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(string(data))
	if !filepath.IsAbs(target) {
		return "", errfmt.Errorf("%s does not hold an absolute path", daemonAppTargetFile)
	}
	return target, nil
}

func stableTuiosPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("tuios"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			if r, err := filepath.EvalSymlinks(abs); err == nil && r == resolved {
				return abs, nil
			}
		}
	}
	return exe, nil
}

func newDaemonAppCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon-app",
		Short: "Run the daemon as a macOS app, so panes keep local network access",
		Long: `Run the daemon as a macOS app, so panes keep local network access.

On macOS, a program needs permission to connect to devices on your local
network. The permission belongs to an app. A daemon that a terminal started
loses that app when the terminal quits. Then programs in your panes get
"No route to host" for local addresses.

'tuios daemon-app install' writes a small app, TUIOS Daemon, that starts the
daemon. Allow TUIOS Daemon one time in System Settings > Privacy & Security >
Local Network. When the app is installed, tuios starts each new daemon through it.

Do not install the app again after you allow it. The permission is tied to
the app's signature. An update of tuios does not change the app.`,
		Hidden:      runtime.GOOS != "darwin",
		Annotations: map[string]string{platformAnnotation: "darwin"},
	}
	cmd.AddCommand(newDaemonAppInstallCommand(), newDaemonAppUninstallCommand(), newDaemonAppStatusCommand())
	return cmd
}

func errDaemonAppNotDarwin() error {
	return errors.New("tuios daemon-app works only on macOS")
}

func newDaemonAppInstallCommand() *cobra.Command {
	var appPath, binary string
	var force, login bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write and sign the TUIOS Daemon app",
		Example: `  tuios daemon-app install
  tuios daemon-app install --login`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errDaemonAppNotDarwin()
			}
			if appPath == "" {
				appPath = defaultDaemonAppPath()
			}
			if binary == "" {
				b, err := stableTuiosPath()
				if err != nil {
					return errfmt.Errorf("find the tuios binary: %w", err)
				}
				binary = b
			}
			if !filepath.IsAbs(binary) {
				return errfmt.Errorf("--binary must be an absolute path, not %q", binary)
			}
			if _, err := os.Stat(binary); err != nil {
				return errfmt.Errorf("--binary: %w", err)
			}
			if err := installDaemonApp(appPath, binary, force); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Wrote %s. It starts %s.\n", appPath, binary)
			if login {
				agent := daemonAppAgentPath()
				if err := writeDaemonAppAgent(agent, appPath); err != nil {
					return err
				}
				fmt.Fprintf(out, "Wrote %s. To start the app at login, run:\n", agent)
				fmt.Fprintf(out, "  launchctl bootstrap gui/%d %s\n", os.Getuid(), shquote.Quote(agent))
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Next steps:")
			fmt.Fprintln(out, "  1. Stop the daemon that runs now. Run this in a terminal outside tuios:")
			fmt.Fprintln(out, "       tuios kill-server")
			fmt.Fprintln(out, "  2. Run tuios. It starts the daemon through the app.")
			fmt.Fprintln(out, "  3. In a pane, connect to a device on your local network.")
			fmt.Fprintln(out, "     When macOS asks about TUIOS Daemon, click Allow.")
			fmt.Fprintln(out, "     If you see no question, turn on TUIOS Daemon in")
			fmt.Fprintln(out, "     System Settings > Privacy & Security > Local Network.")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Do not install the app again after you allow it. A new signature removes the permission.")
			return nil
		},
	}
	cmd.Flags().StringVar(&appPath, "path", "", "Where to write the app (default ~/Applications/TUIOS Daemon.app)")
	cmd.Flags().StringVar(&binary, "binary", "", "The tuios the app starts (default the tuios on PATH)")
	cmd.Flags().BoolVar(&force, "force", false, "Replace an app that is already there. This removes its Local Network permission")
	cmd.Flags().BoolVar(&login, "login", false, "Also write a LaunchAgent that opens the app at login")
	return cmd
}

func installDaemonApp(app, binary string, force bool) error {
	if _, err := os.Stat(app); err == nil {
		if !force {
			return &diagnosticError{
				What:  fmt.Sprintf("%s already exists.", app),
				Cause: "a new app has a new signature, and macOS removes the Local Network permission of the old one.",
				Fix:   "tuios daemon-app install --force",
			}
		}
		if !isDaemonApp(app) {
			return errfmt.Errorf("%s is not a TUIOS Daemon app. Remove it yourself", app)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return errfmt.Errorf("find this binary: %w", err)
	}
	parent := filepath.Dir(app)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".tuios-daemon-app-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	staged := filepath.Join(staging, filepath.Base(app))
	contents := filepath.Join(staged, "Contents")
	for _, dir := range []string{"MacOS", "Resources"} {
		if err := os.MkdirAll(filepath.Join(contents, dir), 0o700); err != nil {
			return err
		}
	}
	files := map[string]string{
		"Info.plist":        daemonAppInfoPlist,
		"PkgInfo":           "APPL????",
		daemonAppTargetFile: binary + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(contents, name), []byte(body), 0o600); err != nil {
			return err
		}
	}
	if err := copyExecutable(exe, filepath.Join(contents, "MacOS", daemonAppExecutable)); err != nil {
		return errfmt.Errorf("copy %s into the app: %w", exe, err)
	}
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", "--identifier", daemonAppBundleID, staged).CombinedOutput(); err != nil {
		return errfmt.Errorf("sign the app: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if force {
		if err := os.RemoveAll(app); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, app); err != nil {
		return err
	}
	if out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", app).CombinedOutput(); err != nil {
		return errfmt.Errorf("check the signature of %s: %w: %s", app, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // the bundle executable must be executable; only this user can read it
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func writeDaemonAppAgent(agent, app string) error {
	if err := os.MkdirAll(filepath.Dir(agent), 0o700); err != nil {
		return err
	}
	var esc strings.Builder
	for _, r := range app {
		switch r {
		case '&':
			esc.WriteString("&amp;")
		case '<':
			esc.WriteString("&lt;")
		case '>':
			esc.WriteString("&gt;")
		default:
			esc.WriteRune(r)
		}
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + daemonAppBundleID + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/open</string>
		<string>-g</string>
		<string>-a</string>
		<string>` + esc.String() + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
	return os.WriteFile(agent, []byte(plist), 0o600)
}

func newDaemonAppUninstallCommand() *cobra.Command {
	var appPath string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the TUIOS Daemon app and its login item",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errDaemonAppNotDarwin()
			}
			if appPath == "" {
				appPath = defaultDaemonAppPath()
			}
			out := cmd.OutOrStdout()
			if _, err := os.Stat(appPath); err == nil {
				if !isDaemonApp(appPath) {
					return errfmt.Errorf("%s is not a TUIOS Daemon app. Remove it yourself", appPath)
				}
				if err := os.RemoveAll(appPath); err != nil {
					return err
				}
				fmt.Fprintf(out, "Removed %s.\n", appPath)
			}
			if agent := daemonAppAgentPath(); agent != "" {
				if _, err := os.Stat(agent); err == nil {
					fmt.Fprintf(out, "To stop the login item, run:\n  launchctl bootout gui/%d/%s\n", os.Getuid(), daemonAppBundleID)
					if err := os.Remove(agent); err != nil {
						return err
					}
					fmt.Fprintf(out, "Removed %s.\n", agent)
				}
			}
			fmt.Fprintln(out, "A daemon that runs now continues. New daemons start directly.")
			fmt.Fprintln(out, "The TUIOS Daemon entry can stay in System Settings > Privacy & Security > Local Network.")
			return nil
		},
	}
	cmd.Flags().StringVar(&appPath, "path", "", "The app to remove (default ~/Applications/TUIOS Daemon.app)")
	return cmd
}

func newDaemonAppStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the TUIOS Daemon app, its login item, and how new daemons start",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errDaemonAppNotDarwin()
			}
			out := cmd.OutOrStdout()
			app := defaultDaemonAppPath()
			if !isDaemonApp(app) {
				fmt.Fprintf(out, "App:     not installed (%s)\n", app)
				fmt.Fprintln(out, "Start:   new daemons start directly from the terminal")
				return nil
			}
			fmt.Fprintf(out, "App:     %s\n", app)
			if target, err := daemonAppTarget(app); err != nil {
				fmt.Fprintf(out, "Runs:    unknown (%v)\n", err)
			} else if _, err := os.Stat(target); err != nil {
				fmt.Fprintf(out, "Runs:    %s, which is missing. Install the app again with --force --binary PATH\n", target)
			} else {
				fmt.Fprintf(out, "Runs:    %s\n", target)
			}
			if err := exec.Command("/usr/bin/codesign", "--verify", "--strict", app).Run(); err != nil {
				fmt.Fprintln(out, "Sign:    the signature is not valid")
			} else {
				fmt.Fprintln(out, "Sign:    valid")
			}
			if _, err := os.Stat(daemonAppAgentPath()); err == nil {
				fmt.Fprintln(out, "Login:   opens at login")
			} else {
				fmt.Fprintln(out, "Login:   off")
			}
			if os.Getenv(daemonAppOffEnv) == "1" {
				fmt.Fprintf(out, "Start:   new daemons start directly, because %s=1\n", daemonAppOffEnv)
			} else {
				fmt.Fprintln(out, "Start:   new daemons start through the app")
			}
			fmt.Fprintf(out, "Log:     %s\n", daemonAppLogPath())
			return nil
		},
	}
}
