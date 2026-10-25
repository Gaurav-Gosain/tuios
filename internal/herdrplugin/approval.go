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

// Approvals: the machine-local record of what the person said about each
// native plugin.
//
// A native plugin runs only when this machine holds an approval of its trust
// hash (TrustHash: its source, what it runs and the grants it asks for). The
// person gives one with tuios plugins install, with tuios plugins approve, or
// in the dialog an attached client shows (plugin_approval.go in session). A
// changed manifest has another trust hash, so it asks again. A denial is
// kept too, so the dialog does not ask again for the same trust hash.
//
// What it protects against: plugins.lock travels with the config folder, so
// a lock file from another machine, a plugin linked or dropped into a
// plugin folder, a hand edit, or an update that changes the plugin cannot
// make it run by itself. An approval of admin also names the content hash of
// the files its commands name (CodeHash), so a changed program needs a new
// approval of admin.
//
// Who may write it: the daemon writes it for an answer that only the person
// can give (mayActAsHuman). The CLI writes it after it asks the person, and
// for admin it first asks every daemon of the user that runs whether the
// caller is outside its panes and plugins (confirmPerson in cmd/tuios).
//
// What it does not protect against: a process that leaves its pane with
// setsid, one that comes back in over ssh, one that points XDG_RUNTIME_DIR
// at a folder with no daemon, and anything that writes this file directly.
// Every program that runs as the user can do the last, as it can write
// config.toml. The owner and mode checks below stop other users, not the
// user's own programs: the file must be owned by the user and not writable
// by others, in a folder of the user's that others cannot write.

// ApprovalsName is the approvals file in tuios's plugins state folder. The
// % keeps it apart from a plugin's state folder (PathComponent).
const ApprovalsName = "%approvals.toml"

// The approval states a native plugin can be in, as Plugin.Approval says.
const (
	// ApprovalAllowed: the person allowed this trust hash.
	ApprovalAllowed = "allowed"
	// ApprovalWaiting: nobody answered for this trust hash on this machine.
	ApprovalWaiting = "waiting"
	// ApprovalDenied: the person denied this trust hash.
	ApprovalDenied = "denied"
)

type approval struct {
	ID     string   `toml:"id"`
	Trust  string   `toml:"trust"`
	Grants []string `toml:"grants,omitempty"`
	// Code is CodeHash when Grants holds admin, else empty.
	Code   string `toml:"code,omitempty"`
	Denied bool   `toml:"denied,omitempty"`
}

type approvals struct {
	Approval []approval `toml:"approval"`
}

// ApprovalsPath is the approvals file of this machine.
func (d Dirs) ApprovalsPath() string {
	return filepath.Join(d.TuiosState, "plugins", ApprovalsName)
}

// readApprovals reads the approvals file, and nothing when it or its folder
// is not private to this user.
func readApprovals(path string) approvals {
	var a approvals
	if !privateToMe(path) || !privateToMe(filepath.Dir(path)) {
		return a
	}
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // the approvals file in the state folder
		_ = toml.Unmarshal(data, &a)
	}
	return a
}

// approvalOf is the answer path holds for p at trust hash trust, or nil.
func approvalOf(path string, p *Plugin, trust string) *approval {
	if trust == "" {
		return nil
	}
	a := readApprovals(path)
	for i := range a.Approval {
		if x := &a.Approval[i]; x.ID == p.PluginID && x.Trust == trust {
			return x
		}
	}
	return nil
}

// Approve records that the person allowed p at trust hash trust, with the
// grants it asks for. admin must be true for a plugin that asks for admin:
// the person typed it. It replaces any older answer for the same id.
func Approve(path string, p *Plugin, trust string, admin bool) error {
	x := approval{ID: p.PluginID, Trust: trust, Grants: slices.Clone(p.Grants)}
	if slices.Contains(x.Grants, "admin") {
		if !admin {
			x.Grants = slices.DeleteFunc(x.Grants, func(g string) bool { return g == "admin" })
		} else {
			x.Code = CodeHash(p)
		}
	}
	return writeApproval(path, x)
}

// Deny records that the person denied p at trust hash trust.
func Deny(path string, p *Plugin, trust string) error {
	return writeApproval(path, approval{ID: p.PluginID, Trust: trust, Denied: true})
}

func writeApproval(path string, x approval) error {
	a := readApprovals(path)
	a.Approval = slices.DeleteFunc(a.Approval, func(y approval) bool { return y.ID == x.ID })
	a.Approval = append(a.Approval, x)
	return saveApprovals(path, a)
}

// Forget drops every answer for the plugin id, for tuios plugins
// uninstall. A plugin installed again later is asked about again.
func Forget(path, id string) error {
	a := readApprovals(path)
	n := len(a.Approval)
	a.Approval = slices.DeleteFunc(a.Approval, func(y approval) bool { return y.ID == id })
	if len(a.Approval) == n {
		return nil
	}
	return saveApprovals(path, a)
}

func saveApprovals(path string, a approvals) error {
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

// TrustOf is the trust hash a native plugin is approved by: the one
// plugins.lock recorded at install, or for a plugin that is not installed,
// the hash of its folder as its source.
func TrustOf(p *Plugin, lock *LockEntry) string {
	if lock != nil {
		return lock.Approved
	}
	return TrustHash(LocalPrefix+canonical(p.PluginRoot), p, p.Grants)
}

// applyApproval sets what native plugin p holds from the answer on this
// machine, and says why it does not run when it may not. grants are what
// the person approved at install (plugins.lock), or the manifest's.
func applyApproval(d Dirs, p *Plugin, lock *LockEntry) *Error {
	grants := p.Grants
	if lock != nil {
		grants = lock.Grants
	}
	p.Held = []string{}
	p.Trust = TrustOf(p, lock)
	x := approvalOf(d.ApprovalsPath(), p, p.Trust)
	switch {
	case x == nil:
		p.Approval = ApprovalWaiting
		return errf("plugin_needs_approval", "%s waits for your approval on this machine. Allow it in tuios, or run tuios plugins approve %s", p.PluginID, p.PluginID)
	case x.Denied:
		p.Approval = ApprovalDenied
		return errf("plugin_denied", "you denied %s on this machine. To allow it, run tuios plugins approve %s", p.PluginID, p.PluginID)
	case slices.Contains(grants, "admin") && (!slices.Contains(x.Grants, "admin") || x.Code != CodeHash(p)):
		p.Approval = ApprovalWaiting
		return errf("plugin_admin_not_approved", "%s holds the admin grant only when you approve it on this machine. Run tuios plugins approve %s --grant admin, from a terminal outside tuios", p.PluginID, p.PluginID)
	}
	p.Approval = ApprovalAllowed
	p.Held = slices.Clone(grants)
	return nil
}

// CodeHash is sha256 over the files in the plugin folder that p's commands
// name: each argument of a startup, action, event or pane command that is a
// relative path to a file in the folder, such as bin/status or probe.sh.
//
// Not covered: what those files load (a sourced lib.sh, a Python module,
// node_modules), an argument that is not a plain file name (sh -c
// "./probe.sh --flag", go run ., python -m pkg), programs on PATH, config
// and data files, and files written at run time. The hash is checked when
// tuios reads the plugins, before a command starts. A command that already
// runs keeps running when one of its files changes.
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
