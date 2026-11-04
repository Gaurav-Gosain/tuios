package shell

import (
	"os"
	"os/exec"
	"path/filepath"
)

// account returns the shell Windows Terminal opens: PowerShell 7 when it
// is installed, else Windows PowerShell, else cmd.exe.
func account() string {
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		if p := filepath.Join(pf, "PowerShell", "7", "pwsh.exe"); exists(p) {
			return p
		}
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	if p := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"); exists(p) {
		return p
	}
	if p := os.Getenv("ComSpec"); p != "" {
		return p
	}
	return filepath.Join(root, "System32", "cmd.exe")
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
