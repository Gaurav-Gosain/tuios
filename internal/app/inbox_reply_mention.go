package app

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The reply editor's @ list (composer/mention.go): the panes and the agents
// of every session, and the files in the folder of the pane the reply is
// for.
//
// Files are read with file-list on this machine, the person's own verb, from
// the pane's folder as its shell last reported it. Each folder is read once
// per list and kept while the list is open. A pane with no known folder, one
// whose shell is on another machine, and a served client (tuios-web, the SSH
// server) offer no files. Panes and agents
// are read from the session tree the rail draws, built once per change of
// the rail's signature.

// mentionsMax bounds the entries the list holds.
const mentionsMax = 50

// mentionFolderTimeout bounds one folder read.
const mentionFolderTimeout = 3 * time.Second

// replyMentions is the reply editor's completer.
type replyMentions struct {
	m *OS
	// dir is the target pane's folder, empty for no files.
	dir string
	// folders are the read folders, by path relative to dir, and asked those
	// out to the daemon.
	folders map[string][]mentionFile
	asked   map[string]bool
	version uint64
	// names are the panes and agents, and namesAt the rail signature they
	// were built from.
	names   []composer.Mention
	namesAt uint64
	// last is the answer for lastQuery at lastVersion, so a frame drawn
	// again costs a comparison.
	last        []composer.Mention
	lastQuery   string
	lastVersion uint64
	lastNames   uint64
}

// mentionFile is one entry of a folder.
type mentionFile struct {
	name string
	dir  bool
}

// mentionFolderMsg is a folder read for the @ list.
type mentionFolderMsg struct {
	c     *replyMentions
	rel   string
	files []mentionFile
}

// newReplyMentions is the completer for a reply to a pane.
func (m *OS) newReplyMentions(sessionID, windowID string) *replyMentions {
	c := &replyMentions{m: m, folders: map[string][]mentionFile{}, asked: map[string]bool{}}
	// A client served to someone elsewhere (tuios-web, the SSH server) runs
	// on the daemon's machine, and its file-list call reads that machine's
	// folders. The person is not at that machine, so the list offers no
	// files there, as the Copies dialog does not.
	if m.BrowserClient || (m.RemoteClient && !m.SSHIsLoopback) {
		return c
	}
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID && w.CwdHost == "" && m.paneIsLocal(w) {
				c.dir = m.paneDir(w)
			}
		}
	}
	return c
}

// Version changes when a folder's listing arrives.
func (c *replyMentions) Version() uint64 { return c.version }

// Mentions are the entries matching query, best first.
func (c *replyMentions) Mentions(query string) []composer.Mention {
	sig := c.m.sidebarSignature()
	if c.names == nil || c.namesAt != sig {
		c.names, c.namesAt = c.m.mentionNames(), sig
	}
	if c.last != nil && c.lastQuery == query && c.lastVersion == c.version && c.lastNames == sig {
		return c.last
	}
	type scored struct {
		it    composer.Mention
		score int
	}
	var hits []scored
	add := func(it composer.Mention, against string) {
		if query == "" {
			hits = append(hits, scored{it, 0})
			return
		}
		if s, ok := fuzzy.Score(query, against); ok {
			hits = append(hits, scored{it, s})
		}
	}
	// A query with a slash after a folder name walks into the folder.
	rel, base := "", query
	if i := strings.LastIndexByte(query, '/'); i >= 0 {
		rel, base = query[:i+1], query[i+1:]
	}
	if files, ok := c.folders[rel]; ok {
		for _, f := range files {
			kind := composer.MentionFile
			insert := rel + f.name
			if f.dir {
				kind, insert = composer.MentionFolder, insert+"/"
			}
			it := composer.Mention{Kind: kind, Label: insert, Insert: insert}
			if base == "" {
				hits = append(hits, scored{it, 0})
			} else if s, ok := fuzzy.Score(base, f.name); ok {
				hits = append(hits, scored{it, s})
			}
		}
	}
	if rel == "" || strings.Count(query, "/") == 1 {
		for _, it := range c.names {
			add(it, it.Label)
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
	out := make([]composer.Mention, 0, min(len(hits), mentionsMax))
	for _, h := range hits {
		if len(out) == mentionsMax {
			break
		}
		out = append(out, h.it)
	}
	c.last, c.lastQuery, c.lastVersion, c.lastNames = out, query, c.version, sig
	return out
}

// mentionNames are the panes and agents, as the list offers them: agents
// with their state, then the panes that run no agent.
func (m *OS) mentionNames() []composer.Mention {
	tree := m.BuildSessionTree()
	var out []composer.Mention
	agents := map[string]bool{}
	for _, e := range m.agentEntries(tree.Sessions) {
		if e.Host != "" {
			continue
		}
		addr := mentionAddress(e.SessionLabel, e.SessionID, agentName(e))
		agents[e.WindowID] = true
		out = append(out, composer.Mention{Kind: composer.MentionAgent, Label: addr, Insert: addr, Note: sidebarStateWords(e.State)})
	}
	for _, s := range tree.Sessions {
		if s.Host != "" {
			continue
		}
		for _, w := range s.Children {
			if agents[w.ID] || w.Title == "" {
				continue
			}
			addr := mentionAddress(s.Title, s.ID, printableTitle(w.Title))
			out = append(out, composer.Mention{Kind: composer.MentionPane, Label: addr, Insert: addr})
		}
	}
	return out
}

// mentionAddress is a pane's address as tuios takes it: session/window.
func mentionAddress(label, id, name string) string {
	sess := label
	if sess == "" {
		sess = id
	}
	return sess + "/" + name
}

// replyMentionCmd reads the folder the open @ list needs, when it has not
// been read. It is nil, at the cost of a map lookup, when there is nothing to
// read.
func (m *OS) replyMentionCmd() tea.Cmd {
	d, ok := m.openDraft()
	if !ok || d.wires.mentions == nil || d.wires.mentions.dir == "" {
		return nil
	}
	query, open := d.comp.MentionQuery()
	if !open {
		return nil
	}
	c := d.wires.mentions
	rel := ""
	if i := strings.LastIndexByte(query, '/'); i >= 0 {
		rel = query[:i+1]
	}
	if c.asked[rel] || strings.Contains(rel, "..") {
		return nil
	}
	c.asked[rel] = true
	call, dir := m.inboxCaller(), path.Join(c.dir, rel)
	return func() tea.Msg {
		raw, err := call("file-list", map[string]any{"dir": dir, "limit": 500, "hidden": false}, mentionFolderTimeout)
		msg := mentionFolderMsg{c: c, rel: rel}
		if err != nil {
			return msg
		}
		var res struct {
			Entries []struct {
				Name     string `json:"name"`
				Kind     string `json:"kind"`
				LinkKind string `json:"link_kind"`
			} `json:"entries"`
		}
		if json.Unmarshal(raw, &res) == nil {
			for _, e := range res.Entries {
				dir := e.Kind == "dir" || e.Kind == "symlink" && e.LinkKind == "dir"
				msg.files = append(msg.files, mentionFile{name: e.Name, dir: dir})
			}
		}
		return msg
	}
}

// applyMentionFolder keeps a folder read for the list it was read for.
func (m *OS) applyMentionFolder(msg mentionFolderMsg) {
	msg.c.folders[msg.rel] = msg.files
	msg.c.version++
}
