package session

import (
	"os"
	"path/filepath"
	"strings"
)

var transferGrantVerbs = map[string]bool{
	"transfer-start":  true,
	"transfer-list":   true,
	"transfer-pause":  true,
	"transfer-resume": true,
	"transfer-cancel": true,
	"transfer-answer": true,
	"transfer-clear":  true,
}

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

func (d *Daemon) transferGrantStart(cs *connState, src, dst *Endpoint, o *transferOptions) *verbError {
	if cs == nil {
		return nil
	}
	pa := cs.paneView.Load()
	if pa == nil {
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
			if e.Path != "" && !underTilde(e.Path) {
				return deny(echoName(e.String()) + " is not under the home folder on " + e.Host + ", and the files grant reaches only that")
			}
			continue
		}
		if root == "" {
			return deny(noFolderReason)
		}
		if !pathUnder(realPath(e.Path), root) {
			return deny(echoName(e.Path) + " is outside the pane's folder " + echoName(root) + ", and the files grant reaches only that")
		}
	}
	o.PaneRoot = root
	return nil
}

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

func (d *Daemon) paneFolder(pa *paneAuth) string {
	return d.windowFolder(pa.window)
}

func (d *Daemon) windowFolder(windowID string) string {
	sess := d.sessionHoldingWindow(windowID)
	if sess == nil {
		return ""
	}
	dir := ""
	if wt := sess.Worktree(); wt != nil && wt.Managed && wt.Path != "" {
		dir = filepath.Clean(wt.Path)
	} else {
		dir = sess.windowStartDir(windowID)
	}
	if dir == "" {
		return ""
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || realPath(dir) != dir {
		return ""
	}
	if tooWideFolder(dir) || !ownedByDaemonUser(dir) {
		return ""
	}
	return dir
}

func tooWideFolder(dir string) bool {
	if filepath.Dir(dir) == dir {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return true
	}
	real := realPath(dir)
	return pathUnder(realPath(filepath.Clean(home)), real)
}

const noFolderReason = "the pane has no folder the files grant can reach. It reaches the folder a pane started in, or its session's tuios worktree, " +
	"and never the home folder, a folder above it, or a folder another user owns. Start the pane in a project folder or a worktree, then give it files"

func (d *Daemon) childWindowBound(cs *connState, verb, cwd string) (bound string, bounded bool, verr *verbError) {
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return "", false, nil
	}
	bound = d.paneFolder(pa)
	if cwd != "" && (bound == "" || !pathUnder(realPath(cwd), bound)) {
		where := "this pane has no folder of its own"
		if bound != "" {
			where = "this pane's folder is " + echoName(bound)
		}
		return "", true, grantForbidden(verb, pa, echoName(cwd)+" is outside the folder of the pane that asks, and "+where+
			". A pane without admin starts windows only inside its own folder")
	}
	return bound, true, nil
}

func (d *Daemon) addWindowFor(cs *connState, verb string, sess *Session, opts NewWindowOptions, onExit func(ptyID string)) (WindowState, *verbError, error) {
	if opts.Host == "" {
		bound, bounded, verr := d.childWindowBound(cs, verb, opts.Cwd)
		if verr != nil {
			return WindowState{}, verr, nil
		}
		opts.Bounded, opts.Bound = bounded, bound
		if opts.Grants == nil {
			opts.Grants = d.childDefaultGrants(cs)
		}
		if !opts.modeChecked {
			argv, verr := d.clampChildMode(cs, verb, "", opts.Command, opts.Env, false)
			if verr != nil {
				return WindowState{}, verr, nil
			}
			opts.Command = argv
		}
	}
	win, err := sess.AddDaemonWindowWith(opts, onExit)
	return win, nil, err
}
