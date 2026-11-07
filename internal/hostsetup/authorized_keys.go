package hostsetup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"charm.land/ssh"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
)

// AuthorizedKeysHas reports whether the file holds pub on any line, whatever
// the options and comment of that line. A missing file holds nothing.
func AuthorizedKeysHas(path string, pub ssh.PublicKey) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the file the person names
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := pub.Marshal()
	for len(data) > 0 {
		k, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			// The parser stops at the first line it cannot read. Skip that
			// line and go on.
			i := strings.IndexByte(string(data), '\n')
			if i < 0 {
				break
			}
			data = data[i+1:]
			continue
		}
		if string(k.Marshal()) == string(want) {
			return true, nil
		}
		data = rest
	}
	return false, nil
}

// CheckAuthorizedKeysModes checks the authorized keys file the way sshd's
// StrictModes does, so the pairing does not end with a key that cannot log
// in. sshd refuses the file when it, its folder, or any folder above it up to
// the home folder is writable by others or owned by another user. A file
// outside the home folder is checked with its folder only, as sshd cannot
// be asked which root it walks to.
//
// Writable by anyone, or owned by another user, is refused: anyone could
// have put a key there already. Writable by the group is a warning, because
// Debian's sshd accepts a group that holds only the user.
func CheckAuthorizedKeysModes(path string, stderr io.Writer) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	check := []string{path, filepath.Dir(path)}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if rel, ok := pathroot.Within(home, filepath.Dir(path)); ok && rel != "." {
			for dir := filepath.Dir(filepath.Dir(path)); ; dir = filepath.Dir(dir) {
				check = append(check, dir)
				if dir == home || dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}
	uid := os.Getuid()
	for _, p := range check {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		mode := fi.Mode().Perm()
		if mode&0o002 != 0 {
			return errfmt.Errorf("anyone can write to %s (mode %04o). sshd does not use keys under it. Run chmod o-w %s", p, mode, p)
		}
		if owner, ok := fileOwner(fi); ok && owner != 0 && int(owner) != uid {
			return errfmt.Errorf("another user owns %s. sshd does not use keys under it. Make it yours or use another --authorized-keys file", p)
		}
		if mode&0o020 != 0 {
			fmt.Fprintf(stderr, "Warning: the group can write to %s (mode %04o). sshd does not use keys under it unless the group holds only you. Run chmod g-w %s\n", p, mode, p)
		}
	}
	return nil
}
