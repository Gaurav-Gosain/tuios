package herdrplugin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/winpath"
)

const (
	leaseName        = ".in-use"
	registryLockName = ".plugins.lock"
)

func leaseInstall(herdrConfig, root string) (string, bool) {
	if herdrConfig == "" || root == "" {
		return "", false
	}
	installs, err := realPath(filepath.Join(herdrConfig, "plugins", "github-installations"))
	if err != nil {
		return "", false
	}
	root, err = realPath(root)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(installs, root)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 3 || parts[2] != "checkout" {
		return "", false
	}
	return filepath.Join(installs, parts[0], parts[1]), true
}

func realPath(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	return winpath.StripExtended(p), err
}

// HoldLease takes herdr's shared lock on the install that holds root, for as
// long as a command runs from it. release drops it. A root outside herdr's
// installs, or an install with no marker file, gets no lock and a release that
// does nothing: herdr never deletes an install without a marker.
//
// herdr picks an install and locks it while it holds the registry lock, so its
// cleanup cannot delete the install between the two steps. HoldLease takes
// the registry lock the same way. Both locks wait, as herdr's do.
//
// An error leaves the command without the lock. herdr runs its command in
// that case too, and logs the error.
func HoldLease(herdrConfig, root string) (release func(), err error) {
	release = func() {}
	install, ok := leaseInstall(herdrConfig, root)
	if !ok {
		return release, nil
	}
	reg, err := os.OpenFile(filepath.Join(herdrConfig, registryLockName), os.O_RDWR|os.O_CREATE, 0o666) //nolint:gosec // herdr's own lock file, opened as herdr opens it
	if err != nil {
		return release, err
	}
	defer func() { _ = reg.Close() }()
	if err := lockExclusive(reg); err != nil {
		return release, err
	}
	f, err := os.OpenFile(filepath.Join(install, leaseName), os.O_RDWR, 0) //nolint:gosec // the marker of a herdr install
	if errors.Is(err, fs.ErrNotExist) {
		return release, nil
	}
	if err != nil {
		return release, err
	}
	if err := lockShared(f); err != nil {
		_ = f.Close()
		return release, err
	}
	return func() { _ = f.Close() }, nil
}
