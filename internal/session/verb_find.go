package session

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

const (
	searchPatternMax = 512
	searchPaneBudget = 50 * time.Millisecond
	searchTextMax    = 400

	searchMaxDefault     = 200
	searchMaxCap         = 1000
	searchPerPaneDefault = 20
	searchPerPaneCap     = 200

	historyLimitDefault = 200
	historyLimitCap     = 2000
)

var searchCases = []string{"smart", "sensitive", "insensitive"}

func smartCaseInsensitive(mode, query string) bool {
	switch mode {
	case "sensitive":
		return false
	case "insensitive":
		return true
	}
	return !strings.ContainsFunc(query, unicode.IsUpper)
}

type searchPane struct {
	sess *Session
	win  WindowState
	pty  *PTY
}

func (d *Daemon) localPanes(cs *connState, names []string) ([]searchPane, *verbError) {
	var sessions []*Session
	if len(names) == 0 {
		sessions = d.manager.AllSessions()
	} else {
		seen := map[string]bool{}
		for _, name := range names {
			sess, verr := d.resolveVerbSession(cs, name)
			if verr != nil {
				return nil, verr
			}
			if !seen[sess.ID] {
				seen[sess.ID] = true
				sessions = append(sessions, sess)
			}
		}
	}
	var panes []searchPane
	for _, sess := range sessions {
		for _, w := range sess.GetState().Windows {
			if pty := sess.GetPTY(w.PTYID); pty != nil {
				panes = append(panes, searchPane{sess: sess, win: w, pty: pty})
			}
		}
	}
	last := make(map[*PTY]int64, len(panes))
	for _, p := range panes {
		last[p.pty] = p.pty.LastOutput()
	}
	sortx.SortStableFunc(panes, func(a, b searchPane) int {
		return cmp.Compare(last[b.pty], last[a.pty])
	})
	return panes, nil
}

const searchChunkRows = 256

func searchRowText(line uv.Line) string {
	return strings.TrimRight(vt.StripSixelMarkers(line.String()), " ")
}

func (p *PTY) scanLines(deadline time.Time, fn func(row int, line string) bool) (bottom int, complete bool) {
	p.terminalMu.RLock()
	term := p.terminal
	if term == nil {
		p.terminalMu.RUnlock()
		return -1, true
	}
	sbLen := term.ScrollbackLen()
	width, height := term.Width(), term.Height()
	screen := make([]string, 0, height)
	var b strings.Builder
	for y := range height {
		b.Reset()
		for x := 0; x < width; x++ {
			cell := term.MainCellAt(x, y)
			if cell == nil || cell.Content == "" {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(cell.Content)
			if cell.Width > 1 {
				x += cell.Width - 1
			}
		}
		screen = append(screen, strings.TrimRight(b.String(), " "))
	}
	trimmed0 := 0
	if p.marks != nil {
		trimmed0 = p.marks.Trimmed()
	}
	p.terminalMu.RUnlock()

	bottom = sbLen + height - 1
	for y := height - 1; y >= 0; y-- {
		if !fn(sbLen+y, screen[y]) {
			return bottom, true
		}
	}
	next := sbLen
	chunk := make([]string, 0, searchChunkRows)
	for next > 0 {
		if time.Now().After(deadline) {
			return bottom, false
		}
		p.terminalMu.RLock()
		term = p.terminal
		if term == nil {
			p.terminalMu.RUnlock()
			return bottom, true
		}
		dropped := 0
		if p.marks != nil {
			dropped = p.marks.Trimmed() - trimmed0
		}
		hi := min(next-dropped, term.ScrollbackLen())
		if hi <= 0 {
			p.terminalMu.RUnlock()
			return bottom, true
		}
		lo := max(hi-searchChunkRows, 0)
		chunk = chunk[:0]
		term.ScrollbackRows(lo, hi, func(_ int, line uv.Line) bool {
			chunk = append(chunk, searchRowText(line))
			return true
		})
		p.terminalMu.RUnlock()
		for i, c := range slices.Backward(chunk) {
			if !fn(lo+dropped+i, c) {
				return bottom, true
			}
		}
		next = lo + dropped
	}
	return bottom, true
}

func matchWindow(line string, from, to int) (text string, start, end int) {
	runeFrom := utf8.RuneCountInString(line[:from])
	runeTo := runeFrom + utf8.RuneCountInString(line[from:to])
	total := runeFrom + utf8.RuneCountInString(line[from:])
	if total <= searchTextMax {
		return line, runeFrom, runeTo
	}
	lead := max((searchTextMax-(runeTo-runeFrom))/2, 0)
	first := max(runeFrom-lead, 0)
	first = min(first, total-searchTextMax)
	runes := []rune(line)
	text = string(runes[first : first+searchTextMax])
	return text, runeFrom - first, min(runeTo-first, searchTextMax)
}

func (d *Daemon) verbSearchPanes(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Query    string   `json:"query"`
		Regex    bool     `json:"regex"`
		Case     string   `json:"case"`
		Sessions []string `json:"sessions"`
		Max      int      `json:"max"`
		PerPane  int      `json:"per_pane"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Query == "" {
		return nil, invalidParam("query", "query is required: the text to find")
	}
	if len(p.Query) > searchPatternMax {
		return nil, invalidParam("query", "query is longer than 512 bytes. Use a shorter query")
	}
	if p.Case != "" && !slices.Contains(searchCases, p.Case) {
		return nil, invalidParam("case", "case is smart, sensitive or insensitive", searchCases...)
	}
	pattern := p.Query
	if !p.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if smartCaseInsensitive(p.Case, p.Query) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, invalidParam("query", "query is not a valid regular expression: "+err.Error())
	}
	if re.MatchString("") {
		return nil, invalidParam("query", "the pattern matches an empty line, so it matches every row. Use a pattern that needs at least one character")
	}
	limit := clampCount(p.Max, searchMaxDefault, searchMaxCap)
	perPane := clampCount(p.PerPane, searchPerPaneDefault, searchPerPaneCap)

	panes, verr := d.localPanes(cs, p.Sessions)
	if verr != nil {
		return nil, verr
	}
	matches := make([]map[string]any, 0)
	truncated := false
	searched := 0
	for _, pane := range panes {
		if len(matches) >= limit {
			truncated = true
			break
		}
		searched++
		found := 0
		bottom, complete := pane.pty.scanLines(time.Now().Add(searchPaneBudget), func(row int, line string) bool {
			if line == "" {
				return true
			}
			loc := re.FindStringIndex(line)
			if loc == nil {
				return true
			}
			if found >= perPane || len(matches) >= limit {
				truncated = true
				return false
			}
			text, start, end := matchWindow(line, loc[0], loc[1])
			matches = append(matches, map[string]any{
				"session":   pane.sess.Name(),
				"window":    pane.win.ID,
				"name":      windowLabelOf(pane.win),
				"workspace": pane.win.Workspace,
				"row":       row,
				"text":      text,
				"start":     start,
				"end":       end,
				"age_rows":  -row,
			})
			found++
			return true
		})
		if !complete {
			truncated = true
		}
		for _, m := range matches[len(matches)-found:] {
			m["age_rows"] = bottom - m["row"].(int)
		}
	}
	return map[string]any{
		"matches":        matches,
		"panes_searched": searched,
		"truncated":      truncated,
	}, nil
}

func clampCount(v, def, limit int) int {
	if v <= 0 {
		return def
	}
	return min(v, limit)
}

func (d *Daemon) verbCommandHistory(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Limit   int    `json:"limit"`
		Query   string `json:"query"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	limit := clampCount(p.Limit, historyLimitDefault, historyLimitCap)
	var panes []searchPane
	switch {
	case p.Window != "":
		sess, verr := d.resolveVerbSession(cs, p.Session)
		if verr != nil {
			return nil, verr
		}
		state := sess.GetState()
		idx, err := findWindowStateIndex(state.Windows, p.Window)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		w := state.Windows[idx]
		if pty := sess.GetPTY(w.PTYID); pty != nil {
			panes = []searchPane{{sess: sess, win: w, pty: pty}}
		}
	case p.Session != "":
		var verr *verbError
		if panes, verr = d.localPanes(cs, []string{p.Session}); verr != nil {
			return nil, verr
		}
	default:
		panes, _ = d.localPanes(cs, nil)
	}
	insensitive := smartCaseInsensitive("smart", p.Query)
	query := p.Query
	if insensitive {
		query = strings.ToLower(query)
	}

	type row struct {
		finished time.Time
		seq      uint64
		data     map[string]any
	}
	var rows []row
	for _, pane := range panes {
		records := pane.pty.shell.commandHistory()
		origin := 0
		if pane.pty.marks != nil {
			origin = pane.pty.marks.Trimmed()
		}
		seen := map[string]bool{}
		for _, r := range slices.Backward(records) {
			if seen[r.Cmdline] {
				continue
			}
			seen[r.Cmdline] = true
			if query != "" {
				hay := r.Cmdline
				if insensitive {
					hay = strings.ToLower(hay)
				}
				if !strings.Contains(hay, query) {
					continue
				}
			}
			var exit any
			if r.Exit != nil {
				exit = *r.Exit
			}
			promptRow := r.promptOrigin - origin
			if promptRow < 0 {
				promptRow = -1
			}
			rows = append(rows, row{
				finished: r.Started.Add(r.Duration),
				seq:      r.Seq,
				data: map[string]any{
					"session":     pane.sess.Name(),
					"window":      pane.win.ID,
					"name":        windowLabelOf(pane.win),
					"seq":         r.Seq,
					"cmdline":     r.Cmdline,
					"exit":        exit,
					"at":          r.Started.UnixMilli(),
					"duration_ms": r.Duration.Milliseconds(),
					"cwd":         r.Cwd,
					"row":         promptRow,
				},
			})
		}
	}
	sortx.SortStableFunc(rows, func(a, b row) int {
		if c := b.finished.Compare(a.finished); c != 0 {
			return c
		}
		return cmp.Compare(b.seq, a.seq)
	})
	commands := make([]map[string]any, 0, min(len(rows), limit))
	for _, r := range rows[:min(len(rows), limit)] {
		commands = append(commands, r.data)
	}
	return map[string]any{"commands": commands}, nil
}

func findVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"search-panes": {
			scope:       scopeGlobal,
			link:        needList,
			description: "Find text in the history and the screen of panes on this machine. The newest match in a pane comes first, and the pane with the newest output comes first. Each pane gets 50 ms. A pane that takes longer is cut off, and truncated is then true.",
			params: []verbParam{
				{Name: "query", Type: "string", Required: true, Description: "The text to find, at most 512 bytes. It is literal text unless regex is true."},
				{Name: "regex", Type: "bool", Description: "Read query as a Go regular expression. A pattern that matches an empty line is refused.", Default: "false"},
				{Name: "case", Type: "string", Description: "How letter case counts. smart ignores case unless the query has a capital letter.", Accepted: searchCases, Default: "smart"},
				{Name: "sessions", Type: "[]string", Description: "The sessions to search. Omit to search every session."},
				{Name: "max", Type: "int", Description: "The most matches to return, at most 1000.", Default: "200"},
				{Name: "per_pane", Type: "int", Description: "The most matches to return from one pane, at most 200.", Default: "20"},
			},
			returns: []verbParam{
				{Name: "matches", Type: "[]object", Description: "One entry per matched row: session, window (the window id), name, workspace, row, text, start, end and age_rows. row counts history rows from the top, then screen rows. text is the row, cut to 400 characters around the match. start and end are the first match's character offsets in text. age_rows is how many rows the row is above the bottom of the screen."},
				{Name: "panes_searched", Type: "int", Description: "How many panes were read."},
				{Name: "truncated", Type: "bool", Description: "True when more matches exist than came back: max or per_pane was reached, or a pane took longer than 50 ms."},
			},
			examples: []string{
				`{"id":1,"verb":"search-panes","params":{"query":"error"}}`,
				`{"id":1,"verb":"search-panes","params":{"query":"panic: .*nil","regex":true,"sessions":["work"],"max":50}}`,
			},
			handler: (*Daemon).verbSearchPanes,
		},
		"command-history": {
			scope:       scopeRead,
			link:        needList,
			description: "List the commands that shells in panes on this machine ran, newest first. Only a shell that sends OSC 133 marks reports its commands. A pane keeps its last 200 commands. A command that ran again in the same pane is listed once, at its newest run.",
			params: []verbParam{
				{Name: "session", Type: "string", Description: "List only this session's panes. Omit to list every session."},
				{Name: "window", Type: "string", Description: "List only this pane. Without session, the window is looked for in the most recently active session."},
				{Name: "limit", Type: "int", Description: "The most commands to return, at most 2000.", Default: "200"},
				{Name: "query", Type: "string", Description: "List only the commands whose text holds this. Case does not count unless the query has a capital letter."},
			},
			returns: []verbParam{
				{Name: "commands", Type: "[]object", Description: "One entry per command: session, window (the window id), name, seq (the pane's command_seq when it finished), cmdline (at most 512 bytes, with likely secrets masked), exit (null when the shell sent no status), at (when it started, Unix ms), duration_ms, cwd (the folder the shell last reported over OSC 7 when it started, empty when it never reported one) and row (the history row of its prompt, -1 when the prompt has left the history)."},
			},
			examples: []string{
				`{"id":1,"verb":"command-history"}`,
				`{"id":1,"verb":"command-history","params":{"session":"work","window":"build","limit":20,"query":"go test"}}`,
			},
			handler: (*Daemon).verbCommandHistory,
		},
		"list-ports": {
			scope:       scopeRead,
			link:        needList,
			description: "List the TCP ports that processes in panes on this machine listen on. A pane's processes are its program and every process under it. A process that left the pane's process tree is not listed. Linux and macOS only. The answer can be up to 1 second old.",
			params: []verbParam{
				{Name: "session", Type: "string", Description: "List only this session's panes. Omit to list every session."},
			},
			returns: []verbParam{
				{Name: "supported", Type: "bool", Description: "False on a system where tuios cannot read listening ports. ports is then empty."},
				{Name: "ports", Type: "[]object", Description: "One entry per listening socket: session, window (the window id), port, addr (the address it listens on, such as 127.0.0.1, 0.0.0.0 or ::), pid, comm (the process name) and worktree (the folder of the session's worktree, empty for a session that is not a worktree session)."},
			},
			examples: []string{
				`{"id":1,"verb":"list-ports"}`,
				`{"id":1,"verb":"list-ports","params":{"session":"work"}}`,
			},
			handler: (*Daemon).verbListPorts,
		},
	}
}
