package session

import (
	"encoding/json"
	"sync"

	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// list-programs: the programs on this machine's PATH, for a client's
// launcher (parity gap 18). The TUI's launcher (internal/app/run_anything.go)
// lists them from its own process; guios shows sessions of other machines
// too, so it asks the daemon of the machine the session runs on, and a
// session on a linked host is answered by that host's daemon
// (forwardHostedCall), whose PATH is the one its panes run with. Desktop
// entries are not listed: they start a desktop program, not a pane.

// programCache is the scan of the PATH, refreshed on each call: Refresh
// reads again only the directories whose time moved.
var programCache struct {
	once sync.Once
	c    *applist.Cache
}

// maxPrograms bounds the answer. A PATH holds a few thousand programs at
// most; the bound keeps a PATH full of generated files from filling a reply.
const maxPrograms = 10000

func programVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"list-programs": {
			scope:       scopeRead,
			link:        needList,
			description: "List the programs on the PATH of the machine the session runs on, by name, for a launcher. Open one in a pane with new-window and its path as command. Desktop entries are not listed.",
			params:      []verbParam{sessionParam},
			returns: []verbParam{
				{Name: "programs", Type: "[]object", Description: "One entry per program, by name: name, and path, the file to run."},
				{Name: "truncated", Type: "bool", Description: "True when the PATH holds more programs than the answer carries."},
			},
			examples: []string{`{"id":1,"verb":"list-programs","params":{"session":"work"}}`},
			handler:  (*Daemon).verbListPrograms,
		},
	}
}

func (d *Daemon) verbListPrograms(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if _, verr := d.resolveVerbSession(cs, p.Session); verr != nil {
		return nil, verr
	}
	programCache.once.Do(func() { programCache.c = applist.NewCache() })
	entries, _ := programCache.c.Refresh()
	type program struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	out := make([]program, 0, min(len(entries), maxPrograms))
	for _, e := range entries {
		if len(out) == maxPrograms {
			break
		}
		out = append(out, program{Name: e.Name, Path: e.Path})
	}
	return map[string]any{"programs": out, "truncated": len(entries) > maxPrograms}, nil
}
