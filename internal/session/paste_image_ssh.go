package session

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Pasting an image into a pane of this daemon whose shell is on another
// machine: the person ran ssh (or mosh) in the pane.
//
// The pane's process is here, but the program that reads the pasted path, a
// shell or an agent, is over there, so a file here means nothing to it.
//
// Where the pane is comes from the process, never from what the pane prints.
// The foreground of the pane must be an interactive ssh or mosh login
// (findRemoteLogin, the line the ssh split reads). A tunnel (ssh -N -L) or a
// remote command is not one. An OSC 7 report alone moves nothing: any program
// in the pane can print one, and a report outlives the ssh that made it until
// the next prompt. A report is used one way only: when it names a machine
// that is not the login's destination, the pane is on a further hop (ssh
// inside ssh), and the paste is refused rather than sent to the jump host.
//
// The login's destination and each [hosts] entry are resolved the same way,
// with ssh -G and the same user's ssh config, to a host name, a user and a
// port. Exactly one entry must resolve to the same three. A nickname, a first
// label or a host name alone is not enough: two machines share those easily,
// and a file on the wrong machine is worse than none. No match, or more than
// one, is refused, and nothing is written or typed.
//
// With one match, the image is copied there the way drop-files copies a
// dropped file: a private drop folder on the host, then a transfer into it.
// The path there is the answer.

// sshTarget is where an ssh line connects, as ssh -G resolves it.
type sshTarget struct{ host, user, port string }

// resolveSSHTarget runs ssh -G with args and reads the target, or reports
// false when ssh cannot say. A variable so the tests can stand in for ssh.
var resolveSSHTarget = func(args []string) (sshTarget, bool) {
	bin, err := lookPath("ssh")
	if err != nil {
		return sshTarget{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-G"}, args...)...)
	cmd.Stdin = nil
	// See resolveSSHHostName.
	killGroupOnCancel(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return sshTarget{}, false
	}
	var t sshTarget
	for line := range strings.Lines(string(out)) {
		key, val, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "hostname":
			t.host = strings.ToLower(val)
		case "user":
			t.user = val
		case "port":
			t.port = val
		}
	}
	return t, t.host != "" && t.user != "" && t.port != ""
}

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
	login, found := findRemoteLogin(pty.ShellPID())
	if !found || login.notShell {
		return "", false, nil
	}
	machine := destHost(login.dest)
	if report := pty.place.Elsewhere(); report != "" && !login.reportMatches(report) {
		return "", true, pasteRefusal{"The pane runs on " + report + " through " + machine + ", and tuios has no link to " + report + ". Add the machine with tuios hosts add.", true}
	}
	var args []string
	if !login.mosh {
		args = append(args, login.opts...)
	}
	if strings.HasPrefix(login.dest, "-") {
		args = append(args, "--")
	}
	want, resolved := resolveSSHTarget(append(args, login.dest))
	if !resolved {
		return "", true, pasteRefusal{"The pane runs on " + machine + ", and ssh cannot tell where that is. Nothing was pasted.", false}
	}
	var matches []string
	if d.federation != nil {
		table := d.federation.Table()
		for _, name := range table.Names() {
			h, err := table.Lookup(name)
			if err != nil || h.Addr == "" {
				continue
			}
			got, ok := resolveSSHTarget(append(slices.Clone(h.SSHOptions), h.Addr))
			if ok && got == want {
				matches = append(matches, name)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], true, nil
	case 0:
		return "", true, pasteRefusal{"The pane runs on " + machine + ", and tuios has no link to it. Add the machine with tuios hosts add.", true}
	default:
		return "", true, pasteRefusal{"The pane runs on " + machine + ", and the hosts " + strings.Join(matches, " and ") + " are all that machine. Keep one of them in [hosts].", false}
	}
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
