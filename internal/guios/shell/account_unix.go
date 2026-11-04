//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
)

func account() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	valid := func(sh string) string {
		if st, err := os.Stat(sh); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return sh
		}
		return ""
	}
	if out, err := exec.Command("/usr/bin/dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output(); err == nil {
		if _, sh, ok := strings.Cut(strings.TrimSpace(string(out)), ": "); ok {
			return valid(strings.TrimSpace(sh))
		}
	}
	// NSS, which knows the accounts of LDAP and SSSD too, then the file.
	if out, err := exec.Command("getent", "passwd", u.Username).Output(); err == nil {
		if f := strings.Split(strings.TrimSpace(string(out)), ":"); len(f) >= 7 && f[0] == u.Username {
			return valid(f[6])
		}
	}
	if b, err := os.ReadFile("/etc/passwd"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) >= 7 && f[0] == u.Username {
				return valid(f[6])
			}
		}
	}
	return ""
}
