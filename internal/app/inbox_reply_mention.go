package app

import (
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

const mentionsMax = 50

const mentionFolderTimeout = 3 * time.Second

type replyMentions struct {
	m           *OS
	dir         string
	window      string
	folders     map[string][]mentionFile
	asked       map[string]bool
	version     uint64
	names       []composer.Mention
	namesAt     uint64
	last        []composer.Mention
	lastQuery   string
	lastVersion uint64
	lastNames   uint64
}

type mentionFile struct {
	name string
	dir  bool
}

type mentionFolderMsg struct {
	c     *replyMentions
	rel   string
	files []mentionFile
}

func (m *OS) newReplyMentions(sessionID, windowID string) *replyMentions {
	c := &replyMentions{m: m, window: windowID, folders: map[string][]mentionFile{}, asked: map[string]bool{}}
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

func (c *replyMentions) Version() uint64 { return c.version }

func (c *replyMentions) Mentions(query string) []composer.Mention {
	sig := c.m.sidebarSignature()
	if c.names == nil || c.namesAt != sig {
		c.names, c.namesAt = c.m.mentionNames(c.window), sig
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

func (m *OS) mentionNames(skip string) []composer.Mention {
	tree := m.BuildSessionTree()
	var out []composer.Mention
	agents := map[string]bool{}
	for _, e := range m.agentEntries(tree.Sessions) {
		if e.Host != "" {
			continue
		}
		agents[e.WindowID] = true
		if e.WindowID == skip {
			continue
		}
		addr, insert := mentionAddress(e.SessionLabel, e.SessionID, agentName(e))
		out = append(out, composer.Mention{Kind: composer.MentionAgent, Label: addr, Insert: insert, Note: sidebarStateWords(e.State)})
	}
	for _, s := range tree.Sessions {
		if s.Host != "" {
			continue
		}
		for _, w := range s.Children {
			if agents[w.ID] || w.Title == "" || w.ID == skip {
				continue
			}
			addr, insert := mentionAddress(s.Title, s.ID, printableTitle(w.Title))
			out = append(out, composer.Mention{Kind: composer.MentionPane, Label: addr, Insert: insert})
		}
	}
	return out
}

func mentionAddress(label, id, name string) (shown, insert string) {
	sess := label
	if sess == "" {
		sess = id
	}
	return sess + "/" + name, shquote.Word(sess) + "/" + shquote.Word(name)
}

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
		if jsonx.Unmarshal(raw, &res) == nil {
			for _, e := range res.Entries {
				dir := e.Kind == "dir" || e.Kind == "symlink" && e.LinkKind == "dir"
				msg.files = append(msg.files, mentionFile{name: e.Name, dir: dir})
			}
		}
		return msg
	}
}

func (m *OS) applyMentionFolder(msg mentionFolderMsg) {
	msg.c.folders[msg.rel] = msg.files
	msg.c.version++
}
