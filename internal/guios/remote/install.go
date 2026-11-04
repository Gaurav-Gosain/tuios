package remote

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"strings"
	"unicode/utf16"

	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"
	"github.com/Gaurav-Gosain/tuios/internal/guios/update"
)

// InstallCommand is the command that installs the server build of Kero,
// of version ("" the latest), on a host reached over SSH or WSL: the
// release's install script, which puts it in ~/.local, or on Windows in
// %LOCALAPPDATA%\Programs, and runs kerod as a service of the user's. It
// downloads the script whole before it runs it, so that a failed download
// fails the command. Batch: it asks nothing, as from the app.
//
// A host is of Windows when a connection found it so (Windows): a dial
// before tells, as the app's has.
func InstallCommand(a Addr, version string, batch bool) (*exec.Cmd, error) {
	if version != "" {
		version = strings.TrimPrefix(version, "v")
		if !update.ValidVersion(version) {
			return nil, errors.New(version + " is not a version of Kero")
		}
	}
	var cmd *exec.Cmd
	switch a.Scheme {
	case "ssh":
		run := "sh -c " + shellQuote(shInstall(version))
		if Windows(a) {
			run = encodedPowerShell(powershellInstall(version))
		}
		cmd = exec.Command(SSHCommand(), append(installSSH(a, batch), "--", a.Host, run)...)
		cmd.Env = sshEnv()
	case "wsl":
		wa := []string{}
		if a.Host != "" {
			wa = append(wa, "-d", a.Host)
		}
		cmd = exec.Command("wsl.exe", append(wa, "--exec", "sh", "-c", shInstall(version))...)
	default:
		return nil, errors.New("Kero installs itself on a host over SSH or WSL: install it there with the script of its releases")
	}
	hideWindow(cmd)
	return cmd, nil
}

// shInstall is the script of sh that installs Kero: install-server.sh, or
// on Windows, whose SSH server may run Git's sh, install-server.ps1.
func shInstall(version string) string {
	args := ""
	if version != "" {
		args = " --version " + version
	}
	return `case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exec ` + encodedPowerShell(powershellInstall(version)) + `;; esac; ` +
		`set -e; url=` + shellQuote(update.InstallScript(version)) + `; f="$(mktemp)"; trap 'rm -f "$f"' EXIT; ` +
		`if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" -o "$f"; ` +
		`elif command -v wget >/dev/null 2>&1; then wget -qO "$f" "$url"; ` +
		`else echo "installing Kero needs curl or wget" >&2; exit 1; fi; sh "$f"` + args
}

// powershellInstall is the script of PowerShell that installs Kero on
// Windows: install-server.ps1, which it runs from memory, as no policy of
// PowerShell's on scripts then stops it.
func powershellInstall(version string) string {
	args := ""
	if version != "" {
		args = " -Version " + shell.PSQuote(version)
	}
	return `$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'; ` +
		`[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12; ` +
		`$s = (New-Object Net.WebClient).DownloadString(` + shell.PSQuote(update.InstallScriptPS(version)) + `); ` +
		`& ([scriptblock]::Create($s))` + args
}

// encodedPowerShell is a command, for cmd and PowerShell alike, that runs
// a script of PowerShell: encoded, as neither shell then reads any of it.
func encodedPowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
}

// installSSH are the options of ssh for an install, which, but in batch,
// may ask to trust the host, or for a password.
func installSSH(a Addr, batch bool) []string {
	sa := []string{"-T", "-o", "ConnectTimeout=15"}
	if batch {
		sa = append(sa, "-o", "BatchMode=yes")
	}
	if a.Port != "" {
		sa = append(sa, "-p", a.Port)
	}
	if a.User != "" {
		sa = append(sa, "-l", a.User)
	}
	return sa
}

// FindShell finds whether a host reached over SSH runs commands with a
// shell of Windows, as Windows then reports, for InstallCommand: it runs
// sh there, which they have not. A connection finds it too. It may ask as
// an install does, and fails as it would.
func FindShell(a Addr, batch bool) error {
	if a.Scheme != "ssh" || Windows(a) {
		return nil
	}
	cmd := exec.Command(SSHCommand(), append(installSSH(a, batch), "--", a.Host, "sh -c 'exit 0'")...)
	cmd.Env = sshEnv()
	hideWindow(cmd)
	var stderr tail
	cmd.Stdin, cmd.Stderr = os.Stdin, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if sh := windowsShell(ee.ExitCode(), stderr.all()); sh != "" {
			shells.Store(a.String(), sh)
			return nil
		}
	}
	if err != nil && stderr.last() != "" {
		return errors.New(stderr.last())
	}
	return err
}
