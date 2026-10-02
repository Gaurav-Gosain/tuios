package session

// peerPlacer places the process on a connection: outside every pane, or in
// one, named by its window when that can be told.
type peerPlacer func(cs *connState) (fromPane bool, window string)

// peerPane says whether the process on cs runs inside a pane of this daemon,
// and which pane when that can be told. See peerPaneWindow.
func (d *Daemon) peerPane(cs *connState) (bool, string) {
	if place := d.peerPlaceOverride(); place != nil {
		return (*place)(cs)
	}
	return d.peerPaneWindow(cs)
}

// peerPaneWindow places the process on cs: outside every pane, or in one pane,
// found the way resolve-pane finds one (an ancestor that is a pane's shell,
// then the controlling terminal) and last by the TUIOS_PANE_ID in its
// environment. A process inside a pane that none of these place gets an empty
// window, which matches no target, so request-approval fails closed on it.
func (d *Daemon) peerPaneWindow(cs *connState) (bool, string) {
	if !d.connFromPane(cs) {
		return false, ""
	}
	if d.peerChanged(cs) {
		// The pid no longer names the process that connected.
		return true, ""
	}
	pid := cs.peerPID
	shells := d.localPaneShells()
	chain := []int{pid}
	for cur, depth := pid, 0; depth < paneOriginMaxDepth; depth++ {
		ppid, _, ok := readProcLineage(cur)
		if !ok || ppid <= 1 {
			break
		}
		chain = append(chain, ppid)
		cur = ppid
	}
	if m, ok := matchPaneByProcess(shells, 0, chain); ok {
		return true, m.pane.windowID
	}
	if _, tty, ok := readProcLineage(pid); ok && tty != 0 {
		for _, sh := range shells {
			if _, shTTY, ok := readProcLineage(sh.shellPID); ok && shTTY == tty {
				return true, sh.windowID
			}
		}
	}
	if id, ok := readProcEnvVar(pid, "TUIOS_PANE_ID"); ok && id != "" && d.holdsWindow(id) {
		return true, id
	}
	return true, ""
}

// peerPlaceOverride is the placer a test installs in place of the kernel's
// answer, or nil.
func (d *Daemon) peerPlaceOverride() *peerPlacer { return d.approvalPeer.Load() }
