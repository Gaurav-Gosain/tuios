package herdrplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// Admin approvals: the machine-local record that the person gave a native
// plugin the admin grant.
//
// What it protects against: plugins.lock travels with the config folder, so
// a lock file from another machine, a hand edit, or an update that changes
// the plugin cannot give admin by itself. An approval names the plugin, the
// trust hash of its manifest and the content hash of the files its commands
// name (CodeHash). A change to any of them needs a new approval.
//
// Who may write it: tuios plugins install --grant admin asks the daemon to
// confirm that the caller is the person (attach-presence, the kernel check
// plugin.enable uses) before it writes, so a pane, a plugin process or a
// process with its pane variables stripped is refused there.
//
// What it does not protect against: any program that runs as the user can
// write this file itself, as it can write config.toml. The owner and mode
// checks below stop other users, not the user's own programs. The file must
// be owned by the user, not writable by others, in a folder of the user's
// that others cannot write.

// AdminApprovalsName is the approvals file in tuios's plugins state folder.
// The % keeps it apart from a plugin's state folder (PathComponent).
const AdminApprovalsName = "%admin-approvals.toml"

type adminApproval struct {
	ID       string `toml:"id"`
	Approved string `toml:"approved"`
	Code     string `toml:"code"`
}

type adminApprovals struct {
	Approval []adminApproval `toml:"approval"`
}

// AdminApprovalsPath is the approvals file of this machine.
func (d Dirs) AdminApprovalsPath() string {
	return filepath.Join(d.TuiosState, "plugins", AdminApprovalsName)
}

// readApprovals reads the approvals file, and nothing when it or its folder
// is not private to this user.
func readApprovals(path string) adminApprovals {
	var a adminApprovals
	if !privateToMe(path) || !privateToMe(filepath.Dir(path)) {
		return a
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = toml.Unmarshal(data, &a)
	}
	return a
}

// adminApproved reports whether path approves admin for p at trust hash
// approved and with the code it has now.
func adminApproved(path string, p *Plugin, approved string) bool {
	if approved == "" {
		return false
	}
	return slices.Contains(readApprovals(path).Approval, adminApproval{p.PluginID, approved, CodeHash(p)})
}

// ApproveAdmin records that the person gave p the admin grant at trust hash
// approved, with the code p has now. It replaces an older approval of the
// same id. The caller confirms the person first.
func ApproveAdmin(path string, p *Plugin, approved string) error {
	a := readApprovals(path)
	a.Approval = slices.DeleteFunc(a.Approval, func(x adminApproval) bool { return x.ID == p.PluginID })
	a.Approval = append(a.Approval, adminApproval{p.PluginID, approved, CodeHash(p)})
	data, err := toml.Marshal(a)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := WriteAtomic(path, data); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// CodeHash is sha256 over the files in the plugin folder that p's commands
// name: each argument of a startup, action, event or pane command that is a
// relative path to a file in the folder, such as bin/status or probe.sh.
// What those files load in turn is not covered.
func CodeHash(p *Plugin) string {
	var argvs [][]string
	for _, c := range p.Startup {
		argvs = append(argvs, c.Command)
	}
	for _, a := range p.Actions {
		argvs = append(argvs, a.Command)
	}
	for _, e := range p.Events {
		argvs = append(argvs, e.Command)
	}
	for _, x := range p.Panes {
		argvs = append(argvs, x.Command)
	}
	var names []string
	for _, argv := range argvs {
		for _, arg := range argv {
			name := filepath.FromSlash(arg)
			if !filepath.IsLocal(name) || slices.Contains(names, name) {
				continue
			}
			full := filepath.Join(p.PluginRoot, name)
			if info, err := os.Stat(full); err == nil && info.Mode().IsRegular() && Inside(full, p.PluginRoot) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(h, filepath.ToSlash(name))
		h.Write([]byte{0})
		if f, err := os.Open(filepath.Join(p.PluginRoot, name)); err == nil { //nolint:gosec // a file in the plugin folder, checked above
			_, _ = io.Copy(h, f)
			_ = f.Close()
		}
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
