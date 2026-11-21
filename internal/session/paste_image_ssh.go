package session

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

var resolveSSH = federation.ResolveSSH

var readHostSSH = federation.ReadSSHConfig

type pasteRefusal struct {
	detail string
	noLink bool
}

func (e pasteRefusal) Error() string { return e.detail }

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
	want, entries, matches, resolved := d.loginHosts(login)
	if !resolved {
		return "", true, pasteRefusal{detail: "The pane runs on " + machine + ", and ssh cannot tell where that is. Nothing was pasted."}
	}

	if report := strings.ToLower(pty.place.Elsewhere()); report != "" && !reportIsLogin(report, machine, want.HostName) {
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

type hostEntry struct {
	name   string
	host   federation.Host
	target federation.SSHTarget
}

func (d *Daemon) loginHosts(login remoteLogin) (want federation.SSHTarget, entries []hostEntry, matches []string, ok bool) {
	var args []string
	if !login.mosh {
		args = append(args, login.opts...)
	}
	c, resolved := resolveSSH(append(args, "--", login.dest))
	if !resolved || !c.Target.Complete() {
		return federation.SSHTarget{}, nil, nil, false
	}
	if d.federation != nil {
		table := d.federation.Table()
		for _, name := range table.Names() {
			h, err := table.Lookup(name)
			if err != nil || h.Addr == "" {
				continue
			}
			e := hostEntry{name: name, host: h}
			if hc, ok := readHostSSH(h); ok {
				e.target = hc.Target
			}
			entries = append(entries, e)
		}
	}
	for _, e := range entries {
		if e.target.Complete() && e.target == c.Target {
			matches = append(matches, e.name)
		}
	}
	return c.Target, entries, matches, true
}

func reportIsLogin(report, dest, hostName string) bool {
	if report == dest || report == hostName {
		return true
	}
	first, _, dotted := strings.Cut(hostName, ".")
	return dotted && !strings.Contains(report, ".") && report == first
}

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
			params, _ := jsonx.Marshal(map[string]string{"id": j.id})
			_, _ = transferControl("cancel")(d, nil, params)
			return "", ctx.Err()
		}
	}
}

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
