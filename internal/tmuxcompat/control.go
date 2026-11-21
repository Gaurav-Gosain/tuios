package tmuxcompat

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// EventStream is a daemon event stream: one JSON event per Next.
type EventStream interface {
	Next() ([]byte, error)
	Close() error
}

var (
	controlPoll        = 2 * time.Second
	controlSettle      = 30 * time.Millisecond
	controlOutputEvery = 50 * time.Millisecond
)

var lifecycleEvents = []string{
	"window-created", "window-closed", "window-moved", "window-retitled",
	"window-focused", "workspace-switched", "session-created", "session-closed",
}

var readOnlyCommands = []string{
	"list-sessions", "list-windows", "list-panes", "list-clients", "has-session",
	"display-message", "capture-pane", "show-options", "show-window-options",
	"refresh-client", "detach-client", "show-buffer", "list-buffers", "show-environment",
}

type control struct {
	s                 *Shim
	out               io.Writer
	num               int
	readOnly          bool
	noOutput          bool
	quit              bool
	sessID, sessName  string
	prev              *view
	pending           map[string]bool
	outcome           string
	detail            []string
	attaches          int
	streams           []EventStream
	lifecycle, output <-chan []byte
	stop              chan struct{}
}

func (s *Shim) runControl(full []string, g Global, words []string, detail []string) int {
	s.control = true
	c := &control{s: s, out: s.Stdout, pending: map[string]bool{}, outcome: OutcomeOK, detail: detail}
	s.attached = c.session
	if g.Control == 2 {
		fmt.Fprint(c.out, "\x1bP1000p")
	}
	cmds := SplitCommands(words)
	if len(cmds) == 0 {
		cmds = [][]string{{"attach-session"}}
	}
	ok := true
	for _, cmd := range cmds {
		ok = c.command(cmd, 0) && ok
	}
	if !ok && c.sessName == "" {
		c.exit(g)
		s.Log.Record(full, c.outcome, c.detail)
		return 1
	}
	c.loop()
	c.exit(g)
	s.Log.Record(full, c.outcome, c.detail)
	return 0
}

func (c *control) exit(g Global) {
	_, _ = io.WriteString(c.out, "%exit\n")
	if g.Control == 2 {
		fmt.Fprint(c.out, "\x1b\\")
	}
}

func (c *control) line(format string, args ...any) {
	fmt.Fprintf(c.out, format+"\n", args...)
}

func (c *control) command(argv []string, flags int) bool {
	s := c.s
	name := argv[0]
	if full, err := lookupCommand(name); err == nil {
		name = full
	}
	c.num++
	num, started := c.num, time.Now().Unix()
	var out, errOut bytes.Buffer
	stdout, stderr := s.Stdout, s.Stderr
	s.Stdout, s.Stderr = &out, &errOut
	var err error
	var outcome string
	var detail []string
	switch {
	case name == "attach-session":
		outcome, err = c.attach(argv[1:])
	case name == "detach-client" && len(argv) == 1:
		c.quit = true
		outcome = OutcomeOK
	case name == "refresh-client":
		outcome = OutcomeIgnored
	case c.readOnly && (name == "detach-client" || !slices.Contains(readOnlyCommands, name)):
		outcome, err = OutcomeError, errors.New("client is read-only")
	default:
		s.created = ""
		outcome, detail, err = s.runOne(name, argv[1:])
		if err == nil && name == "new-session" && s.created != "" {
			var o string
			o, err = c.attach([]string{"-t", "=" + s.created})
			outcome = worse(outcome, o)
		}
	}
	s.Stdout, s.Stderr = stdout, stderr
	c.outcome = worse(c.outcome, outcome)
	c.detail = mergeDetail(c.detail, detail)
	c.line("%%begin %d %d %d", started, num, flags)
	_, _ = c.out.Write(out.Bytes())
	if err != nil {
		c.detail = mergeDetail(c.detail, []string{logText(err)})
		c.line("%s", err.Error())
		c.line("%%error %d %d %d", started, num, flags)
		return false
	}
	c.line("%%end %d %d %d", started, num, flags)
	if name == "attach-session" || name == "new-session" {
		if sv := c.session(c.prev); sv != nil {
			c.line("%%session-changed %s %s", sv.sessionID(), sv.name)
		}
	}
	return true
}

func (c *control) attach(args []string) (string, error) {
	p, err := parseFlags("attach-session", spec{bools: "dErx", values: "cft"}, args)
	if err != nil {
		return OutcomeUnsupported, err
	}
	if p.Has('r') {
		c.readOnly = true
	}
	if f, ok := p.Value('f'); ok {
		if c.readOnly && slices.Contains(strings.Split(f, ","), "!read-only") {
			return OutcomeError, errors.New("client is read-only")
		}
		for flag := range strings.SplitSeq(f, ",") {
			switch strings.TrimPrefix(flag, "!") {
			case "read-only":
				c.readOnly = !strings.HasPrefix(flag, "!")
			case "no-output":
				c.noOutput = !strings.HasPrefix(flag, "!")
			}
		}
	}
	v, err := c.s.loadView()
	if err != nil {
		return OutcomeError, err
	}
	tv, _ := p.Value('t')
	var sv *sessionView
	if tv == "" {
		sv = v.def
		if cp := c.s.callerPane(v); cp != nil {
			sv = cp.sess
		}
		if sv == nil {
			return OutcomeError, errors.New("no sessions")
		}
	} else if named, ok := v.sessionOf(strings.TrimSuffix(tv, ":")); ok {
		sv = named
	} else if strings.ContainsAny(tv, ":.@%") {
		sv, _, err = v.resolveWindow(tv, nil)
		if err != nil {
			return OutcomeError, err
		}
	} else {
		return OutcomeError, errfmt.Errorf("can't find session: %s", tv)
	}
	c.sessID, c.sessName, c.prev = sv.id, sv.name, v
	c.attaches++
	return OutcomeOK, nil
}

func (c *control) session(v *view) *sessionView {
	if v == nil || c.sessName == "" {
		return nil
	}
	for _, sv := range v.sessions {
		if c.sessID != "" && sv.id == c.sessID || c.sessID == "" && sv.name == c.sessName {
			return sv
		}
	}
	return nil
}

func streamLines(r io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			ch <- sc.Text()
		}
		_ = sc.Err()
	}()
	return ch
}

func streamEvents(st EventStream, stop <-chan struct{}) <-chan []byte {
	ch := make(chan []byte)
	go func() {
		defer close(ch)
		for {
			ev, err := st.Next()
			if err != nil {
				return
			}
			select {
			case ch <- ev:
			case <-stop:
				return
			}
		}
	}()
	return ch
}

func (c *control) closeStreams() {
	if c.stop != nil {
		close(c.stop)
		c.stop = nil
	}
	for _, st := range c.streams {
		_ = st.Close()
	}
	c.streams, c.lifecycle, c.output = nil, nil, nil
}

func (c *control) subscribe() {
	c.closeStreams()
	clear(c.pending)
	s := c.s
	if s.Subscribe == nil {
		return
	}
	c.stop = make(chan struct{})
	params := map[string]any{"types": lifecycleEvents}
	if !s.AllSessions {
		params["session"] = s.Session
	}
	if st, err := s.Subscribe(params); err == nil {
		c.streams = append(c.streams, st)
		c.lifecycle = streamEvents(st, c.stop)
	} else {
		c.detail = mergeDetail(c.detail, []string{"control mode: no event stream, reading the session every " + controlPoll.String() + ": " + logText(err)})
	}
	if !c.noOutput && c.sessName != "" {
		if st, err := s.Subscribe(map[string]any{"types": []string{"output"}, "session": c.sessName}); err == nil {
			c.streams = append(c.streams, st)
			c.output = streamEvents(st, c.stop)
		}
	}
}

func (c *control) loop() {
	s := c.s
	var lines <-chan string
	if s.Stdin != nil {
		lines = streamLines(s.Stdin)
	}
	c.subscribe()
	defer c.closeStreams()
	poll := time.NewTicker(controlPoll)
	defer poll.Stop()
	var settle, flush <-chan time.Time
	for !c.quit {
		select {
		case l, ok := <-lines:
			if !ok {
				return
			}
			cmds, err := parseCommandLine(l)
			if err != nil {
				c.num++
				now := time.Now().Unix()
				c.line("%%begin %d %d 1", now, c.num)
				c.line("%s", err.Error())
				c.line("%%error %d %d 1", now, c.num)
				continue
			}
			attaches := c.attaches
			for _, cmd := range cmds {
				c.command(cmd, 1)
				if c.quit {
					return
				}
			}
			if c.attaches != attaches {
				c.subscribe()
			}
			if settle == nil {
				settle = time.After(controlSettle)
			}
		case ev, ok := <-c.lifecycle:
			if !ok {
				c.lifecycle = nil
				if !c.refresh() {
					return
				}
				continue
			}
			_ = ev
			if settle == nil {
				settle = time.After(controlSettle)
			}
		case ev, ok := <-c.output:
			if !ok {
				c.output = nil
				continue
			}
			var e struct {
				Type    string `json:"type"`
				Session string `json:"session"`
				Window  string `json:"window"`
			}
			if jsonx.Unmarshal(ev, &e) != nil || e.Type != "output" || e.Window == "" {
				continue
			}
			c.pending[e.Window] = true
			if flush == nil {
				flush = time.After(controlOutputEvery)
			}
		case <-settle:
			settle = nil
			if !c.refresh() {
				return
			}
		case <-poll.C:
			if !c.refresh() {
				return
			}
		case <-flush:
			flush = nil
			c.flushOutput()
		}
	}
}

func (c *control) flushOutput() {
	ids := make([]string, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, PaneID(id))
	}
	clear(c.pending)
	slices.Sort(ids)
	for _, id := range ids {
		c.line("%%output %s ", id)
	}
}

func (c *control) refresh() bool {
	if c.sessName == "" {
		return true
	}
	v, err := c.s.loadView()
	if err != nil {
		c.detail = mergeDetail(c.detail, []string{"control mode: " + logText(err)})
		return false
	}
	prev := c.prev
	c.prev = v
	return c.diff(prev, v)
}

func (c *control) diff(a, b *view) bool {
	old, cur := c.session(a), c.session(b)
	if b.server && sessionSet(a) != sessionSet(b) {
		c.line("%%sessions-changed")
	}
	if cur == nil {
		return false
	}
	if old == nil {
		return true
	}
	if old.name != cur.name {
		c.line("%%session-renamed %s %s", cur.sessionID(), cur.name)
		c.sessName = cur.name
	}
	c.diffWindows(old, cur, false)
	if old.current != cur.current && len(cur.panesOn(cur.current)) > 0 {
		c.line("%%session-window-changed %s %s", cur.sessionID(), cur.windowID(cur.current))
	}
	if b.server && a != nil {
		for _, o := range b.sessions {
			if o == cur {
				continue
			}
			for _, before := range a.sessions {
				if before.id == o.id && before.name == o.name {
					c.diffWindows(before, o, true)
				}
			}
		}
	}
	return true
}

func sessionSet(v *view) string {
	if v == nil {
		return ""
	}
	var ids []string
	for _, sv := range v.sessions {
		ids = append(ids, sv.id+"/"+sv.name)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func (c *control) diffWindows(old, cur *sessionView, unlinked bool) {
	prefix := "%"
	if unlinked {
		prefix = "%unlinked-"
	}
	before, after := old.windowsInUse(), cur.windowsInUse()
	for _, ws := range after {
		if !slices.Contains(before, ws) {
			c.line("%swindow-add %s", prefix, cur.windowID(ws))
		}
	}
	for _, ws := range before {
		if !slices.Contains(after, ws) {
			c.line("%swindow-close %s", prefix, old.windowID(ws))
		}
	}
	for _, ws := range after {
		if !slices.Contains(before, ws) {
			continue
		}
		if n := windowName(cur, ws); n != windowName(old, ws) {
			c.line("%swindow-renamed %s %s", prefix, cur.windowID(ws), n)
		}
		if unlinked {
			continue
		}
		if l := layoutOf(cur.panesOn(ws)); l != layoutOf(old.panesOn(ws)) {
			flags := ""
			if ws == cur.current {
				flags = "*"
			}
			c.line("%%layout-change %s %s %s %s", cur.windowID(ws), l, l, flags)
		}
		oa, na := old.active(ws), cur.active(ws)
		if na != nil && (oa == nil || oa.ID != na.ID) {
			c.line("%%window-pane-changed %s %s", cur.windowID(ws), PaneID(na.ID))
		}
	}
}

func windowName(sv *sessionView, ws int) string {
	vars := map[string]string{}
	(&Shim{}).windowVars(sv, ws, vars)
	return vars["window_name"]
}

func layoutOf(panes []*pane) string {
	if len(panes) == 0 {
		return ""
	}
	body := layoutCell(panes)
	return fmt.Sprintf("%04x,%s", layoutChecksum(body), body)
}

func layoutCell(panes []*pane) string {
	cell := func(w, h, x, y int) string {
		return strconv.Itoa(w) + "x" + strconv.Itoa(h) + "," + strconv.Itoa(x) + "," + strconv.Itoa(y)
	}
	if len(panes) == 1 {
		p := panes[0]
		return cell(p.Width, p.Height, p.X, p.Y) + "," + strconv.FormatUint(uint64(p.Num), 10)
	}
	minX, minY, maxX, maxY := bounds(panes)
	head := cell(maxX-minX, maxY-minY, minX, minY)
	join := func(groups [][]*pane, open, close string) string {
		kids := make([]string, len(groups))
		for i, g := range groups {
			kids[i] = layoutCell(g)
		}
		return head + open + strings.Join(kids, ",") + close
	}
	if cols := cutPanes(panes, func(p *pane) (int, int) { return p.X, p.X + p.Width }); len(cols) > 1 {
		return join(cols, "{", "}")
	}
	if rows := cutPanes(panes, func(p *pane) (int, int) { return p.Y, p.Y + p.Height }); len(rows) > 1 {
		return join(rows, "[", "]")
	}
	leaves := make([][]*pane, len(panes))
	for i, p := range panes {
		leaves[i] = []*pane{p}
	}
	return join(leaves, "{", "}")
}

func cutPanes(panes []*pane, span func(*pane) (int, int)) [][]*pane {
	sorted := slices.Clone(panes)
	slices.SortStableFunc(sorted, func(a, b *pane) int {
		sa, _ := span(a)
		sb, _ := span(b)
		return sa - sb
	})
	var groups [][]*pane
	end := 0
	for _, p := range sorted {
		s, e := span(p)
		if len(groups) == 0 || s < end {
			if len(groups) == 0 {
				groups = append(groups, nil)
			}
			groups[len(groups)-1] = append(groups[len(groups)-1], p)
		} else {
			groups = append(groups, []*pane{p})
		}
		end = max(end, e)
	}
	return groups
}

func layoutChecksum(layout string) uint16 {
	var csum uint16
	for i := 0; i < len(layout); i++ {
		csum = (csum >> 1) + ((csum & 1) << 15)
		csum += uint16(layout[i])
	}
	return csum
}

func parseCommandLine(line string) ([][]string, error) {
	var cmds [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCmd := func() {
		endWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == ' ' || ch == '\t':
			endWord()
		case ch == ';' && !inWord:
			endCmd()
		case ch == ';' && inWord && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t'):
			endCmd()
		case ch == '#' && !inWord:
			i = len(line)
		case ch == '\\':
			if i+1 < len(line) {
				i++
				word.WriteByte(line[i])
			}
			inWord = true
		case ch == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("syntax error: missing '")
			}
			word.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case ch == '"':
			i++
			for ; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				word.WriteByte(line[i])
			}
			if i >= len(line) {
				return nil, errors.New("syntax error: missing \"")
			}
			inWord = true
		default:
			word.WriteByte(ch)
			inWord = true
		}
	}
	endCmd()
	return cmds, nil
}
