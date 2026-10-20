package herdrplugin

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// Admin approvals: the machine-local record that the person gave a native
// plugin the admin grant.
//
// plugins.lock travels with the config folder and any process of the user
// can write it, so its grants alone never give admin. A native plugin holds
// admin only when this file, in the state folder, approves the exact trust
// hash in its lock entry. tuios plugins install --grant admin writes it,
// and the CLI refuses that from inside a pane. The file is written with mode
// 0600 in a 0700 folder, and a file that others may write is ignored. Until
// the first-run prompt exists, this is the only way to approve admin.

// AdminApprovalsName is the approvals file in tuios's plugins state folder.
// The % keeps it apart from a plugin's state folder (PathComponent).
const AdminApprovalsName = "%admin-approvals.toml"

type adminApproval struct {
	ID       string `toml:"id"`
	Approved string `toml:"approved"`
}

type adminApprovals struct {
	Approval []adminApproval `toml:"approval"`
}

// AdminApprovalsPath is the approvals file of this machine.
func (d Dirs) AdminApprovalsPath() string {
	return filepath.Join(d.TuiosState, "plugins", AdminApprovalsName)
}

// adminApproved reports whether path approves admin for id at trust hash
// approved.
func adminApproved(path, id, approved string) bool {
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return false
	}
	var a adminApprovals
	if data, err := os.ReadFile(path); err != nil || toml.Unmarshal(data, &a) != nil {
		return false
	}
	return slices.ContainsFunc(a.Approval, func(x adminApproval) bool {
		return x.ID == id && x.Approved == approved && approved != ""
	})
}

// ApproveAdmin records that the person gave plugin id the admin grant at
// trust hash approved. It replaces an older approval of the same id.
func ApproveAdmin(path, id, approved string) error {
	var a adminApprovals
	if data, err := os.ReadFile(path); err == nil {
		_ = toml.Unmarshal(data, &a)
	}
	a.Approval = slices.DeleteFunc(a.Approval, func(x adminApproval) bool {
		return x.ID == id
	})
	a.Approval = append(a.Approval, adminApproval{id, approved})
	data, err := toml.Marshal(a)
	if err != nil {
		return err
	}
	if err := WriteAtomic(path, data); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
