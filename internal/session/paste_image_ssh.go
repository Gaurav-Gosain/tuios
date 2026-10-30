package session

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Pasting an image into a pane of this daemon whose shell is on another
// machine: the person ran ssh (or mosh) in the pane.
//
// The pane's process is here, but the program that reads the pasted path, a
// shell, tmux or an agent, is over there, so a file here means nothing to it.
//
// Where the pane is comes from the process, never from what the pane prints.
// The foreground of the pane is read for an ssh or mosh client
// (findRemoteClient, the walk the ssh split makes):
//
//   - No client: the pane is here, and so is the file.
//   - A tunnel (ssh -N, or -f, -n or -W with no -t): no session over there
//     reads the pane's keys. The file is written here.
//   - A client whose line cannot be read (-F, an -o this file does not know,
//     a wrapper it does not trust): the pane is somewhere, and tuios cannot
//     say where. The paste is refused. A path typed into the wrong machine is
//     worse than none.
//   - Any other line, a remote command such as ssh -t host tmux included: the
//     pane is over there.
//
// The login and each [hosts] entry are resolved with ssh -G, the same program
// and the same user's config the links use, to a host name, a user, a port
// and the jump hosts or proxy command on the way (federation.SSHTarget).
// Exactly one entry must resolve to the same target. The same address behind
// another jump host is another machine. No match, or more than one, is
// refused, and nothing is written or typed.
//
// An OSC 7 report never picks the machine: any program in the pane can print
// one. It is read for one thing. When the shell over there names a machine
// that is another [hosts] entry, the pane runs ssh inside ssh, and the paste
// is refused rather than sent to the first hop. A report that only differs
// from the ssh destination (an address, a cloud host name, an alias) is
// what a remote shell usually says, and the login decides.
//
// With one match, the image is copied there the way drop-files copies a
// dropped file: a private drop folder on the host, then a transfer into it.
// The path there is the answer.

// resolveSSH resolves an ssh command line with ssh -G. A variable so the
// tests can stand in for ssh.
var resolveSSH = federation.ResolveSSH

// readHostSSH resolves a [hosts] entry with ssh -G, cached.
var readHostSSH = federation.ReadSSHConfig

// pasteRefusal is a paste into a pane on another machine that tuios cannot
// place there. detail is the sentence the person reads.
type pasteRefusal struct {
	detail string
	// noLink says no [hosts] entry is the machine, so adding one helps.
	noLink bool
}

func (e pasteRefusal) Error() string { return e.detail }

// paneRemoteHost is the [hosts] entry a pane of this daemon is logged in to
// with ssh or mosh. ok is false for a pane whose program runs here, and then
// the paste is written here. A pane on another machine that no single entry
// is gives a pasteRefusal.
func (d *Daemon) paneRemoteHost(pty *PTY) (host string, ok bool, err error) {
	login, read, seen, _ := findRemoteClient(pty.ShellPID())
	if !seen {
		return "", false, nil
	}
	if !read {
		return "", true, pasteRefusal{detail: "The pane runs an ssh command that tuios cannot read, so tuios cannot tell which machine it is on. Nothing was pasted."}
	}
	if login.tunnel {
		return "", false, nil
	}
	machine := destHost(login.dest)
	var args []string
	if !login.mosh {
		args = append(args, login.opts...)
	}
	want, resolved := resolveSSH(append(args, "--", login.dest))
	if !resolved || !want.Target.Complete() {
		return "", true, pasteRefusal{detail: "The pane runs on " + machine + ", and ssh cannot tell where that is. Nothing was pasted."}
	}

	type entry struct {
		name   string
		host   federation.Host
		target federation.SSHTarget
	}
	var entries []entry
	if d.federation != nil {
		table := d.federation.Table()
		for _, name := range table.Names() {
			h, err := table.Lookup(name)
			if err != nil || h.Addr == "" {
				continue
			}
			e := entry{name: name, host: h}
			if c, ok := readHostSSH(h); ok {
				e.target = c.Target
			}
			entries = append(entries, e)
		}
	}
	var matches []string
	for _, e := range entries {
		if e.target.Complete() && e.target == want.Target {
			matches = append(matches, e.name)
		}
	}

	// ssh inside ssh: the shell over there reports a machine that is another
	// entry, not the one the login reaches.
	if report := strings.ToLower(pty.place.Elsewhere()); report != "" && !reportIsLogin(report, machine, want.Target.HostName) {
		for _, e := range entries {
			if slices.Contains(matches, e.name) {
				continue
			}
			if report == strings.ToLower(e.name) || report == destHost(e.host.Addr) || report == e.target.HostName {
				return "", true, pasteRefusal{detail: "The pane runs ssh inside ssh, from " + machine + " to " + report + ". Paste the image in a pane that runs on " + report + "."}
			}
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], true, nil
	case 0:
		return "", true, pasteRefusal{detail: "The pane runs on " + machine + ", and tuios has no link to it. Add the machine with tuios hosts add.", noLink: true}
	default:
		return "", true, pasteRefusal{detail: "The pane runs on " + machine + ", and the hosts " + strings.Join(matches, " and ") + " are all that machine. Keep one of them in [hosts]."}
	}
}

// reportIsLogin reports whether an OSC 7 host names the login's machine: its
// destination, the host name ssh resolved, or that name's first label.
func reportIsLogin(report, dest, hostName string) bool {
	if report == dest || report == hostName {
		return true
	}
	first, _, dotted := strings.Cut(hostName, ".")
	return dotted && !strings.Contains(report, ".") && report == first
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
	j, err := d.transfers.start(Endpoint{Path: local}, Endpoint{Host: host, Path: dst}, transferOptions{Conflict: "keep-both", Private: true})
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
