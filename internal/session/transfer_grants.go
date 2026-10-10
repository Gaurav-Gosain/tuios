package session

import (
	"path/filepath"
	"strings"
)

// The files grant: a pane that may copy files, and no more.
//
// The transfer verbs are the person's (scopeDeny), so a pane needs admin to
// call them, which every pane holds under mode open. Under strict a person
// can give a pane files instead (tuios set-pane-grants --grants files, or
// files in [agents.permissions] grants). checkGrants lets a pane with files
// call the verbs in transferGrantVerbs, and the handlers hold it to two
// rules:
//
//   - transfer-start: each end on this machine is inside the pane's own
//     folder (its session's worktree, else its working folder), after links,
//     and each end on a host is under that host's home. A copy out of the
//     project into the person's other folders, or into a host's system
//     folders, needs the person.
//   - The other verbs see and act on only the copies the pane started. A
//     pane never lists, pauses or cancels the person's copies or another
//     pane's.
//
// Every copy started from a pane carries the pane in its row, whatever the
// pane holds, so the person sees which pane asked for it.

// transferGrantVerbs are the verbs the files grant reaches.
var transferGrantVerbs = map[string]bool{
	"transfer-start":  true,
	"transfer-list":   true,
	"transfer-pause":  true,
	"transfer-resume": true,
	"transfer-cancel": true,
	"transfer-answer": true,
	"transfer-clear":  true,
}

// transferGrantPane is the pane the transfer verbs on cs are held to: the
// caller's pane when it holds files and not admin, else "" for no hold.
func (d *Daemon) transferGrantPane(cs *connState) string {
	if cs == nil {
		return ""
	}
	pa := cs.paneView.Load()
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return ""
	}
	return pa.window
}

// transferGrantStart tags a copy with the pane that starts it, and holds a
// pane with the files grant to its folder and the hosts' homes.
func (d *Daemon) transferGrantStart(cs *connState, src, dst *Endpoint, o *transferOptions) *verbError {
	if cs == nil {
		return nil
	}
	pa := cs.paneView.Load()
	if pa == nil {
		// Under mode open checkGrants does not place the caller, since every
		// pane holds admin. The copy still says which pane it is from.
		pa = d.paneAuthority(cs)
	}
	if pa == nil {
		return nil
	}
	if pa.window != "" && pa.window != unplacedWindow && !pa.hosted {
		o.Pane, o.PaneSession = pa.window, pa.session
	}
	if pa.grants.Has(GrantAdmin) {
		return nil
	}
	deny := func(why string) *verbError {
		LogBasic("Pane %s (%s) refused transfer-start: %s", shortWindowID(pa.window), pa.grants.String(), why)
		return grantForbidden("transfer-start", pa, why)
	}
	if !pa.grants.Has(GrantFiles) {
		return deny("copying files needs the files grant")
	}
	root := d.paneFolder(pa)
	for _, e := range []*Endpoint{src, dst} {
		if e.Host != "" {
			if !underTilde(e.Path) {
				return deny(echoName(e.String()) + " is not under the home folder on " + e.Host + ", and the files grant reaches only that")
			}
			continue
		}
		if root == "" {
			return deny("the pane's folder could not be found")
		}
		if !pathUnder(realPath(e.Path), realPath(root)) {
			return deny(echoName(e.Path) + " is outside the pane's folder " + echoName(root) + ", and the files grant reaches only that")
		}
	}
	return nil
}

// underTilde reports whether a path on a host is its home folder or under it:
// it starts with ~ and no part of it climbs out.
func underTilde(p string) bool {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// paneFolder is where a pane's work is rooted: its session's worktree, else
// the pane's working folder, as the approvals read it (paneRoot).
func (d *Daemon) paneFolder(pa *paneAuth) string {
	sess := d.sessionHoldingWindow(pa.window)
	if sess == nil {
		return ""
	}
	st := sess.GetState()
	w, ok := findWindowState(st, pa.window)
	if !ok {
		return ""
	}
	root := paneRoot(st, w)
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}
