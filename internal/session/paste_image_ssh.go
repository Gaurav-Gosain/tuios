package session

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pasting an image into a pane of this daemon whose shell is on another
// machine: the person ran ssh (or mosh) in the pane.
//
// The pane's process is here, but the program that reads the pasted path, a
// shell or an agent, is over there, so a file here means nothing to it. The
// daemon already knows when a pane is in that state: the shell over there
// reported a folder on another machine (OSC 7, placeRecord.Elsewhere), or the
// foreground of the pane is an ssh or mosh client (findRemoteLogin). The Files
// rail and the ssh split use the same two signals.
//
// When that machine is one of the [hosts] with a link that is up, the image
// is copied there the way drop-files copies a dropped file: a private drop
// folder on the host, then a transfer into it. The path there is the answer.
// When no [hosts] entry is that machine, nothing is written and nothing is
// typed: the path of a file here would be the wrong file, or none, over
// there.

// errPasteNoLink is a pane on a machine that no [hosts] entry names.
type errPasteNoLink struct{ machine string }

func (e errPasteNoLink) Error() string {
	return "the pane runs on " + e.machine + ", and tuios has no link to it"
}

// paneElsewhere names the machine a pane of this daemon runs its program on
// when that is not this machine, and the names it may go by there. It is
// false for a pane whose program is here.
func paneElsewhere(pty *PTY) (machine string, names []string, ok bool) {
	if host := pty.place.Elsewhere(); host != "" {
		machine = host
		names = append(names, strings.ToLower(host))
	}
	if login, found := findRemoteLogin(pty.ShellPID()); found {
		if dest := destHost(login.dest); dest != "" {
			if machine == "" {
				machine = dest
			}
			names = append(names, dest)
		}
	}
	return machine, names, machine != ""
}

// hostNamed is the [hosts] entry whose name or address is one of names. A
// name with no dot also matches the first label of a dotted address, which
// is what most machines call themselves.
func (d *Daemon) hostNamed(names []string) string {
	if d.federation == nil {
		return ""
	}
	table := d.federation.Table()
	for _, n := range names {
		for _, name := range table.Names() {
			h, err := table.Lookup(name)
			if err != nil {
				continue
			}
			for _, key := range []string{strings.ToLower(h.Name), destHost(h.Addr)} {
				if key == "" {
					continue
				}
				if key == n {
					return name
				}
				if first, _, dotted := strings.Cut(key, "."); dotted && !strings.Contains(n, ".") && first == n {
					return name
				}
			}
		}
	}
	return ""
}

// pasteToHost copies an image to host's drop folder and returns its path
// there. The far machine's link policy holds it like any drop: without files,
// the drop folder is refused before anything is written here.
func (d *Daemon) pasteToHost(ctx context.Context, host string, data []byte) (string, error) {
	var out struct {
		Dir string `json:"dir"`
	}
	if err := (fileEnd{d: d, host: host}).call(ctx, "file-drop-dir", map[string]any{"bytes": len(data)}, &out); err != nil {
		return "", err
	}
	if out.Dir == "" {
		return "", errors.New("the host named no drop folder")
	}
	// The transfer engine copies files, so the bytes are a file here for the
	// length of the copy, owner only, and removed after it.
	local, err := d.pastes.save(data)
	if err != nil {
		return "", err
	}
	defer d.pastes.remove(local)
	dst := strings.TrimSuffix(out.Dir, "/") + "/" + dropName(filepath.Base(local), map[string]bool{})
	j, err := d.transfers.start(Endpoint{Path: local}, Endpoint{Host: host, Path: dst}, false, "keep-both", true, nil)
	if err != nil {
		return "", err
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		switch row := j.row(); row.State {
		case transferDone:
			return cmp.Or(row.Final, dst), nil
		case transferFailed, transferCancelled:
			if row.Error == "" {
				row.Error = "the copy " + row.State
			}
			return "", &VerbCallError{Code: cmp.Or(row.Code, ErrVerbHostUnreachable), Message: row.Error}
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			params, _ := json.Marshal(map[string]string{"id": j.id})
			_, _ = transferControl("cancel")(d, nil, params)
			return "", ctx.Err()
		}
	}
}

// remove deletes one file this store wrote.
func (s *pasteStore) remove(path string) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		delete(s.written, path)
	}
}
