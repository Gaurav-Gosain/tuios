package tmuxcompat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Control mode (tmux -C and -CC).
//
// A control client reads tmux commands from its standard input, one per line,
// and writes their output framed by %begin and %end (or %error), with
// notifications between the blocks. The shim answers it for one tuios
// session, the one attach-session names:
//
//   - Commands: every command the shim answers, on the command line and on
//     standard input. Each is framed as tmux frames it: "%begin TIME NUMBER
//     FLAGS", the output, then "%end" or "%error" with the same three fields.
//     FLAGS is 1 for a command read from standard input and 0 for one on the
//     command line. A read-only client (attach-session -r, or -f read-only)
//     may run only the commands that change nothing.
//   - Notifications, read from the daemon's event stream (the subscribe verb)
//     and from reading the sessions again:
//     %session-changed on attach, %output when a pane of the session prints,
//     %window-add, %window-close, %window-renamed, %layout-change,
//     %window-pane-changed and %session-window-changed when the session's
//     workspaces change, %unlinked-window-add, -close and -renamed for the
//     other sessions the shim serves, %sessions-changed when a session starts
//     or ends, %session-renamed, and %exit when the client detaches, its
//     standard input closes, its session ends or the daemon stops.
//
// Not implemented: %output carries no bytes, since the daemon's event stream
// says that a pane printed and not what (read the pane with capture-pane).
// The flow control of tmux 3.2 (%pause, %continue, %extended-output,
// refresh-client -A and -f pause-after), format subscriptions (refresh-client
// -B, %subscription-changed), %pane-mode-changed, %client-session-changed,
// %client-detached, %paste-buffer-changed and -deleted, %message and
// %config-error are never sent. refresh-client -C (the client size) is
// accepted and changes nothing, since a tuios client sets the size.

// EventStream is a daemon event stream: one JSON event per Next.
type EventStream interface {
	Next() ([]byte, error)
	Close() error
}

// Timings of control mode. Variables so a test can shorten them.
var (
	// controlPoll is how often the session is read again when no event
	// says it changed: a workspace rename raises no event.
	controlPoll = 2 * time.Second
	// controlSettle gathers the events of one change (a window closes, focus
	// moves) into one read.
	controlSettle = 30 * time.Millisecond
	// controlOutputEvery gathers a pane's output events into one %output.
	controlOutputEvery = 50 * time.Millisecond
)

// lifecycleEvents are the event types that change what a control client is
// told about.
var lifecycleEvents = []string{
	"window-created", "window-closed", "window-moved", "window-retitled",
	"window-focused", "workspace-switched", "session-created", "session-closed",
}

// readOnlyCommands are the commands a read-only control client may run: the
// ones that change nothing.
var readOnlyCommands = []string{
	"list-sessions", "list-windows", "list-panes", "list-clients", "has-session",
	"display-message", "capture-pane", "show-options", "show-window-options",
	"refresh-client", "detach-client",
}

// control is one control-mode client.
type control struct {
	s        *Shim
	out      io.Writer
	num      int
	readOnly bool
	noOutput bool
	quit     bool
	// sessID and sessName are the session the client is attached to, empty
	// before an attach.
	sessID, sessName string
	prev             *view
	pending          map[string]bool
	outcome          string
	detail           []string
	// attaches counts the attaches, so the loop sees one and opens its event
	// streams again for the session attached to.
	attaches int
	// streams are the open event streams, lifecycle and output their
	// events, and stop ends the goroutines that read them.
	streams           []EventStream
	lifecycle, output <-chan []byte
	stop              chan struct{}
}

// runControl answers a control-mode client until it detaches.
func (s *Shim) runControl(full []string, g Global, words []string, detail []string) int {
	s.control = true
	c := &control{s: s, out: s.Stdout, pending: map[string]bool{}, outcome: OutcomeOK, detail: detail}
	if g.Control == 2 {
		// -CC: the DCS that tells iTerm2 a control client starts.
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

// exit ends the client the way tmux does.
func (c *control) exit(g Global) {
	_, _ = io.WriteString(c.out, "%exit\n")
	if g.Control == 2 {
		fmt.Fprint(c.out, "\x1b\\")
	}
}

// line writes one line of output.
func (c *control) line(format string, args ...any) {
	fmt.Fprintf(c.out, format+"\n", args...)
}

// command runs one command in a %begin block. It reports whether the command
// succeeded.
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
	case name == "detach-client":
		c.quit = true
		outcome = OutcomeOK
	case name == "refresh-client":
		outcome = OutcomeIgnored
	case c.readOnly && !slices.Contains(readOnlyCommands, name):
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

// attach attaches the client to the target session.
func (c *control) attach(args []string) (string, error) {
	p, err := parseFlags("attach-session", spec{bools: "dErx", values: "cft"}, args)
	if err != nil {
		return OutcomeUnsupported, err
	}
	if p.Has('r') {
		c.readOnly = true
	}
	if f, ok := p.Value('f'); ok {
		// A read-only client cannot make itself writable, as in tmux.
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
		// A window or pane target attaches to its session, as in tmux.
		sv, _, err = v.resolveWindow(tv, nil)
		if err != nil {
			return OutcomeError, err
		}
	} else {
		return OutcomeError, fmt.Errorf("can't find session: %s", tv)
	}
	c.sessID, c.sessName, c.prev = sv.id, sv.name, v
	c.attaches++
	return OutcomeOK, nil
}

// session finds the attached session in v.
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

// streamLines feeds a reader's lines to a channel, closed at the end.
func streamLines(r io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()
	return ch
}

// streamEvents feeds an event stream to a channel, closed when it ends.
// It stops, without closing the channel, once stop is closed.
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

// closeStreams closes the event streams and ends their readers.
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

// subscribe opens the event streams for the session attached to, closing
// the ones open before. It runs at the start and after every attach, so a
// client that attaches to another session, or attaches late, follows it.
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

// loop reads commands and events until the client detaches.
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
				// The stream ended: the daemon stopped, or dropped the
				// subscription. A read says which.
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
			if json.Unmarshal(ev, &e) != nil || e.Type != "output" || e.Window == "" {
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

// flushOutput writes one %output for each pane that printed since the last.
func (c *control) flushOutput() {
	// The output stream is filtered to the attached session, so every
	// window here is one of its panes.
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

// refresh reads the sessions again and writes what changed. It returns
// false when the client must exit: its session ended, or the daemon is gone.
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

// diff writes the notifications that take a client from view a to view b.
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

// sessionSet names the sessions of a view, for a cheap comparison.
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

// diffWindows writes the window notifications between two reads of one
// session. unlinked marks a session the client is not attached to, whose
// windows tmux reports as unlinked.
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

// windowName is the name of workspace ws as window_name reports it.
func windowName(sv *sessionView, ws int) string {
	vars := map[string]string{}
	(&Shim{}).windowVars(sv, ws, vars)
	return vars["window_name"]
}

// layoutOf is a tmux layout string for panes (window_layout): the checksum,
// then a tree of cells, each its size and offset, a leaf ending in its pane's
// number. Tiled panes are cut into the split tree tmux would hold: columns
// ({...}) where a vertical line crosses no pane, else rows ([...]). Panes no
// straight line separates, such as overlapping floating windows, are listed
// in one container at their own positions, which is the closest a tmux
// layout can say.
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

// cutPanes splits panes into the groups that lines across one axis separate:
// span gives a pane's start and end on that axis. One group means no line
// separates them.
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

// layoutChecksum is tmux's layout checksum (layout_checksum in layout.c).
func layoutChecksum(layout string) uint16 {
	var csum uint16
	for i := 0; i < len(layout); i++ {
		csum = (csum >> 1) + ((csum & 1) << 15)
		csum += uint16(layout[i])
	}
	return csum
}

// parseCommandLine splits a line of control-mode input into commands, the
// way tmux's parser reads one: words split on blanks, 'single' and "double"
// quotes, a backslash escaping the next character, and ";" between
// commands.
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
