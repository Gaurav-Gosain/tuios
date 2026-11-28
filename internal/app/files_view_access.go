package app

import (
	"cmp"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/filetrash"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Where a side on another machine may write.
//
// A far daemon says in its file-list reply what this machine may do in the
// folder (see session/verb_files_access.go). The machine row shows it as a
// badge, the footer of a read only side says where this machine may write,
// and a key that writes there says why it does nothing, before a call is
// tried. A far daemon too old to say leaves the badge at "unknown", and its
// refusals come back in its own words, as before.
//
// d on a far side moves the rows to that machine's trash with file-trash,
// and u puts them back with file-untrash.

// filesAccess is what this machine may do in a side's folder, as the far
// daemon said it. known is false for a side on this machine, and for a far
// daemon that does not say.
type filesAccess struct {
	known, files, write bool
	roots               []string
	// denied counts the names the far read rules leave out.
	denied int
}

// filesAccessReply is the access part of a file-list reply.
type filesAccessReply struct {
	Access *struct {
		Files bool     `json:"files"`
		Write bool     `json:"write"`
		Roots []string `json:"roots"`
	} `json:"access"`
	Denied int `json:"denied"`
}

// accessOf reads the access part of a file-list reply.
func (r filesAccessReply) accessOf() filesAccess {
	if r.Access == nil {
		return filesAccess{denied: r.Denied}
	}
	return filesAccess{known: true, files: r.Access.Files, write: r.Access.Write, roots: r.Access.Roots, denied: r.Denied}
}

// filesWriteRefusal says why this machine may not write in side s's
// folder, or "" when it may, or when the far daemon did not say.
func (m *OS) filesWriteRefusal(s *filesSide) string {
	if s == nil || !s.access.known || s.access.write {
		return ""
	}
	name, _ := m.filesMachineOf(s)
	name = printableTitle(name)
	if !s.access.files || len(s.access.roots) == 0 {
		return name + " does not let this machine change files. On " + name + ", add \"files\" to allow for this machine in [hosts]."
	}
	return name + " lets this machine write only in " + strings.Join(s.access.roots, ", ") + "."
}

// filesRefuseWrite shows why side s takes no write, and reports whether it
// did.
func (m *OS) filesRefuseWrite(s *filesSide) bool {
	why := m.filesWriteRefusal(s)
	if why == "" {
		return false
	}
	m.ShowNotification(why, "warning", m.Settings.NotificationDuration*2)
	return true
}

// filesAccessBadge is the badge at the right of side s's machine row, and
// its colour. A side on this machine has none.
func (m *OS) filesAccessBadge(s *filesSide, pal overlay.Palette) (string, color.Color) {
	if _, here := m.filesMachineOf(s); here || s.Dir == "" {
		return "", nil
	}
	a := s.access
	switch {
	case !a.known:
		return "write access unknown", pal.FgMute
	case !a.files:
		return "no file access", pal.Warn
	case a.write:
		return "you can write here", pal.Success
	}
	return "read only here", pal.Warning
}

// filesFarTrash moves names in dir on host to that machine's trash.
func (m *OS) filesFarTrash(host, dir string, names []string) tea.Cmd {
	call := m.verbOnHostCaller()
	// The sides name the machine this client is attached to by its name,
	// and the verb by "".
	side := cmp.Or(host, m.AttachedHost)
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filesJoinOn(dir, name)
	}
	return func() tea.Msg {
		raw, err := call(host, "file-trash", map[string]any{"paths": paths})
		if err != nil {
			return filesOpMsg{warn: "tuios could not trash it: " + verbErrText(err)}
		}
		var reply struct {
			Trashed []filetrash.Item `json:"trashed"`
			Error   string           `json:"error"`
		}
		_ = jsonx.Unmarshal(raw, &reply)
		msg := filesOpMsg{host: side, dirs: []string{dir}}
		if len(reply.Trashed) > 0 {
			u := &filesUndo{kind: "trash", far: true, host: host}
			for _, g := range reply.Trashed {
				u.pairs = append(u.pairs, [2]string{g.From, g.File})
			}
			msg.undo = u
			msg.note = "Moved " + countOf(len(reply.Trashed), "item") + " to the trash on " + printableTitle(side) + ". u brings them back."
		}
		if reply.Error != "" {
			msg.warn = "tuios could not trash everything: " + reply.Error
		}
		return msg
	}
}

// undoFarTrash puts back what a trash on another machine took.
func (m *OS) undoFarTrash(u filesUndo) tea.Cmd {
	call := m.verbOnHostCaller()
	side := cmp.Or(u.host, m.AttachedHost)
	return func() tea.Msg {
		msg := filesOpMsg{undone: true, host: side}
		for _, p := range u.pairs {
			msg.dirs = append(msg.dirs, filesParentOn(p[0]))
			if _, err := call(u.host, "file-untrash", map[string]any{"file": p[1], "to": p[0]}); err != nil {
				msg.warn = "tuios could not bring it back: " + verbErrText(err)
			}
		}
		if msg.warn == "" {
			msg.note = "Brought it back from the trash on " + printableTitle(side) + "."
		}
		return msg
	}
}

// filesParentOn is the folder of a path in the form of its machine.
func filesParentOn(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i > 0 {
		return p[:i]
	}
	return p
}
