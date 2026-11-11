package pathroot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// isHomeForm reports whether p starts with a ~ that means the home folder: "~"
// alone, or "~" and a separator. "~user" and "~x" are names, not the home.
// The back slash counts only where it is the separator.
func isHomeForm(p string) bool {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return true
	}
	return filepath.Separator == '\\' && strings.HasPrefix(p, `~\`)
}

// ExpandHomeStrict replaces a leading ~ with the home folder and cleans the
// result. A path with no leading ~ is returned as it is. When the path
// needs the home folder and the system has none, it returns p and an error.
func ExpandHomeStrict(p string) (string, error) {
	if !isHomeForm(p) {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p, err
	}
	if home == "" {
		return p, errors.New("the home folder is not known")
	}
	return filepath.Join(home, p[1:]), nil
}

// ExpandHome is ExpandHomeStrict for a caller that keeps the path as given
// when there is no home folder.
func ExpandHome(p string) string {
	out, _ := ExpandHomeStrict(p)
	return out
}

// ContractHome writes a path under home as ~ or ~/rest, the way a shell
// prompt shows it. Either separator after home counts, and the rest is written
// with "/". With no home, or with "/" as home, the path is returned as it is.
func ContractHome(p, home string) string {
	if home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	for _, sep := range []string{"/", string(filepath.Separator)} {
		if rest, ok := strings.CutPrefix(p, home+sep); ok {
			return "~/" + filepath.ToSlash(rest)
		}
	}
	return p
}

// ContractUserHome is ContractHome with this user's home folder.
func ContractUserHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return ContractHome(p, home)
}
