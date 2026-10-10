package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// Following copies the daemon runs: tuios cp and tuios transfers wait.
//
// A copy is a job in the daemon, so the command only watches it. It
// subscribes to the transfer events before it starts anything, so no change
// is missed, and reads the job's row from each event. A caller that may not
// subscribe (a pane without the read grant) reads transfer-list instead,
// twice a second.
//
// What it prints depends on where the output goes. On a terminal a block on
// stderr is drawn again in place: the copy, its bars, the rate and the time
// left. Elsewhere one line per change of state and at most one progress line
// each ten seconds. With --json, one JSON object per line on stdout, the
// events of the copy.

// copyRow is the part of a transfer row the CLI reads.
type copyRow struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Src              session.Endpoint       `json:"src"`
	Dst              session.Endpoint       `json:"dst"`
	Final            string                 `json:"final"`
	Target           string                 `json:"target"`
	Kind             string                 `json:"kind"`
	State            string                 `json:"state"`
	Size             int64                  `json:"size"`
	Done             int64                  `json:"done"`
	Wire             int64                  `json:"wire"`
	Rate             float64                `json:"rate"`
	ETAms            int64                  `json:"eta_ms"`
	Files            int                    `json:"files"`
	FilesDone        int                    `json:"files_done"`
	Current          string                 `json:"current"`
	CurrentDone      int64                  `json:"current_done"`
	CurrentSize      int64                  `json:"current_size"`
	Error            string                 `json:"error"`
	Code             string                 `json:"code"`
	SHA256           string                 `json:"sha256"`
	Verified         bool                   `json:"verified"`
	RetryInMs        int64                  `json:"retry_in_ms"`
	Skipped          int                    `json:"skipped"`
	SkippedItems     []session.SkippedItem  `json:"skipped_items"`
	Same             int                    `json:"same"`
	ConflictsSkipped int                    `json:"conflicts_skipped"`
	ConflictsLeft    int                    `json:"conflicts_left"`
	Conflicts        []session.ConflictItem `json:"conflicts"`
	ConflictCount    int                    `json:"conflict_count"`
	FailedFiles      int                    `json:"failed_files"`
	Created          int64                  `json:"created"`
	Pane             string                 `json:"pane"`
}

func (r copyRow) ended() bool {
	return r.State == "done" || r.State == "failed" || r.State == "cancelled"
}

// where is the copy's destination as a person writes it.
func (r copyRow) where() string {
	p := r.Final
	if p == "" {
		p = r.Target
	}
	if p == "" {
		p = r.Dst.Path
	}
	if r.Dst.Host == "" {
		return p
	}
	return r.Dst.Host + ":" + p
}

func (r copyRow) host() string {
	if r.Src.Host != "" {
		return r.Src.Host
	}
	return r.Dst.Host
}

// The exit codes of tuios cp and tuios transfers wait. They are stable: the
// errors skill topic lists them.
const (
	exitCopyFailed    = 1
	exitCopyUsage     = 2
	exitCopyPartial   = 3
	exitCopyRefused   = 4
	exitCopyNoHost    = 5
	exitCopyCheck     = 6
	exitCopyCancelled = 130
)

// exitForCode is the exit code of a copy that failed with a daemon error code.
func exitForCode(code string) int {
	switch code {
	case session.ErrVerbForbidden:
		return exitCopyRefused
	case session.ErrVerbHostUnreachable, session.ErrVerbHostRefused:
		return exitCopyNoHost
	case session.ErrVerbHashMismatch, session.ErrVerbSourceChanged:
		return exitCopyCheck
	case session.ErrVerbUnknownHost, session.ErrVerbInvalidParams:
		return exitCopyUsage
	}
	return exitCopyFailed
}

// rowExit is the exit code one finished copy gives. explicit says the person
// chose the conflict policy, so files it skipped are what they asked for.
func rowExit(r copyRow, explicit bool) int {
	switch r.State {
	case "done":
		if r.Skipped > 0 || r.ConflictsLeft > 0 || (!explicit && r.ConflictsSkipped > 0) {
			return exitCopyPartial
		}
		return 0
	case "cancelled":
		return exitCopyCancelled
	}
	return exitForCode(r.Code)
}

// worseExit is the exit code that says more of two: a cancel, then any
// failure (the first one found), then a partial copy, then success.
func worseExit(a, b int) int {
	rank := func(c int) int {
		switch c {
		case 0:
			return 0
		case exitCopyPartial:
			return 1
		case exitCopyCancelled:
			return 3
		}
		return 2
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// copyOutput is how the follower prints.
type copyOutput int

const (
	outTerminal copyOutput = iota
	outPlain
	outJSON
	outQuiet
)

// follower watches some copies to their end.
type follower struct {
	ctl      *session.VerbClient
	ids      []string
	rows     map[string]copyRow
	out      copyOutput
	stdout   io.Writer
	stderr   io.Writer
	explicit bool
	// files are the files a copy skipped or could not copy, by copy id, for
	// the summary.
	files map[string][]fileNote
	// ask answers conflicts on the terminal.
	ask func(r copyRow) bool

	mu        sync.Mutex
	events    chan json.RawMessage
	polling   bool
	drawn     int
	lastDraw  time.Time
	lastPlain map[string]time.Time
	lastState map[string]string
	lastJSON  map[string]time.Time
	asked     map[string]bool
	ratesEMA  map[string]float64
	paused    atomic.Bool
	pal       overlay.Palette
	width     int
}

type fileNote struct {
	rel, state, code, msg string
}

// newFollower sets up the output for the given mode.
func newFollower(ctl *session.VerbClient, out copyOutput, explicit bool) *follower {
	f := &follower{
		ctl: ctl, rows: map[string]copyRow{}, out: out, explicit: explicit,
		stdout: os.Stdout, stderr: os.Stderr,
		files: map[string][]fileNote{}, lastPlain: map[string]time.Time{}, lastState: map[string]string{},
		lastJSON: map[string]time.Time{}, asked: map[string]bool{}, ratesEMA: map[string]float64{},
		events: make(chan json.RawMessage, 256), width: 80,
	}
	if out == outTerminal || out == outPlain {
		p := colorprofile.Detect(os.Stderr, os.Environ())
		theme.SetColorProfile(p)
		f.pal = theme.UI()
		f.stderr = &colorprofile.Writer{Forward: os.Stderr, Profile: p}
		if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 20 {
			f.width = w
		}
	}
	return f
}

// subscribe opens the event stream on a connection of its own, before any
// copy starts. A caller that may not subscribe polls instead.
func (f *follower) subscribe() {
	ev, err := dialVerb()
	if err == nil {
		_, err = ev.Call("subscribe", map[string]any{"types": []string{"transfer", "transfer-progress", "transfer-file"}})
	}
	if err != nil {
		if ev != nil {
			_ = ev.Close()
		}
		f.polling = true
		return
	}
	go func() {
		defer func() { _ = ev.Close() }()
		for {
			line, err := ev.ReadEventLine(0)
			if err != nil {
				close(f.events)
				return
			}
			f.events <- json.RawMessage(line)
		}
	}()
}

// add follows a copy from its first row.
func (f *follower) add(r copyRow) {
	f.mu.Lock()
	f.ids = append(f.ids, r.ID)
	f.rows[r.ID] = r
	f.mu.Unlock()
	if f.out == outJSON {
		f.emit(map[string]any{"event": "started", "id": r.ID, "src": r.Src, "dst": r.Dst, "files": r.Files, "bytes": r.Size})
	}
}

func (f *follower) emit(v map[string]any) {
	line, _ := json.Marshal(v)
	_, _ = fmt.Fprintln(f.stdout, string(line))
}

// allEnded reports whether every copy followed has ended.
func (f *follower) allEnded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.ids {
		if !f.rows[id].ended() {
			return false
		}
	}
	return true
}

// event takes one line of the event stream.
func (f *follower) event(line json.RawMessage) {
	var ev struct {
		Type     string                `json:"type"`
		Transfer *copyRow              `json:"transfer"`
		File     *session.TransferFile `json:"file"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	switch {
	case ev.Transfer != nil:
		f.update(*ev.Transfer)
	case ev.File != nil:
		f.mu.Lock()
		_, mine := f.rows[ev.File.ID]
		if mine && ev.File.State != "done" && ev.File.State != "same" {
			if len(f.files[ev.File.ID]) < 1000 {
				f.files[ev.File.ID] = append(f.files[ev.File.ID], fileNote{ev.File.Rel, ev.File.State, ev.File.Code, ev.File.Error})
			}
		}
		f.mu.Unlock()
		if mine && f.out == outJSON {
			v := map[string]any{"event": "file", "id": ev.File.ID, "rel": ev.File.Rel, "state": ev.File.State}
			if ev.File.SHA256 != "" {
				v["sha256"] = ev.File.SHA256
			}
			if ev.File.Code != "" {
				v["code"] = ev.File.Code
			}
			f.emit(v)
		}
	}
}

// update takes a new row of a copy.
func (f *follower) update(r copyRow) {
	f.mu.Lock()
	old, mine := f.rows[r.ID]
	if !mine || (old.ended() && !r.ended()) {
		f.mu.Unlock()
		return
	}
	f.rows[r.ID] = r
	was := f.lastState[r.ID]
	f.lastState[r.ID] = r.State
	if r.Rate > 0 {
		// The daemon's rate is over five seconds. A little more smoothing
		// keeps the number readable while bytes come in bursts.
		if e := f.ratesEMA[r.ID]; e > 0 {
			f.ratesEMA[r.ID] = e*0.7 + r.Rate*0.3
		} else {
			f.ratesEMA[r.ID] = r.Rate
		}
	}
	f.mu.Unlock()
	switch f.out {
	case outJSON:
		f.jsonRow(r, was)
	case outPlain:
		f.plainRow(r, was)
	}
}

func (f *follower) jsonRow(r copyRow, was string) {
	switch {
	case r.State != was && r.State == "waiting":
		f.emit(map[string]any{"event": "waiting", "id": r.ID, "host": r.host(), "retry_in_ms": r.RetryInMs, "message": r.Error})
	case r.State != was && r.State == "conflict":
		for _, c := range r.Conflicts {
			f.emit(map[string]any{"event": "conflict", "id": r.ID, "rel": c.Rel, "path": c.Path, "src_size": c.SrcSize, "src_mtime": c.SrcMTime, "dst_size": c.DstSize, "dst_mtime": c.DstMTime})
		}
	case r.State == "done":
		if was == "done" {
			return
		}
		f.emit(map[string]any{"event": "done", "id": r.ID, "dst": r.where(), "files": r.Files, "files_done": r.FilesDone, "bytes": r.Size, "wire_bytes": r.Wire, "same": r.Same, "conflicts_skipped": r.ConflictsSkipped, "conflicts_left": r.ConflictsLeft, "skipped": r.Skipped, "verified": r.Verified, "sha256": r.SHA256, "exit": rowExit(r, f.explicit)})
		return
	case r.State == "failed" || r.State == "cancelled":
		if was == r.State {
			return
		}
		f.emit(map[string]any{"event": r.State, "id": r.ID, "code": r.Code, "message": r.Error, "exit": rowExit(r, f.explicit)})
		return
	}
	if r.State == "running" || r.State == "verifying" {
		f.mu.Lock()
		due := time.Since(f.lastJSON[r.ID]) >= 250*time.Millisecond
		if due {
			f.lastJSON[r.ID] = time.Now()
		}
		f.mu.Unlock()
		if due {
			f.emit(map[string]any{"event": "progress", "id": r.ID, "state": r.State, "done": r.Done, "size": r.Size, "rate": r.Rate, "eta_ms": r.ETAms, "files": r.Files, "files_done": r.FilesDone, "current": r.Current, "wire_bytes": r.Wire})
		}
	}
}

func (f *follower) plainRow(r copyRow, was string) {
	if r.State != was && !r.ended() && r.State != "queued" && r.State != "verifying" && !(r.State == "running" && was == "verifying") {
		_, _ = fmt.Fprintf(f.stderr, "%s: %s\n", r.Name, stateWords(r))
	}
	if r.State == "running" {
		f.mu.Lock()
		due := time.Since(f.lastPlain[r.ID]) >= 10*time.Second
		if due {
			f.lastPlain[r.ID] = time.Now()
		}
		f.mu.Unlock()
		if due && r.Size > 0 {
			_, _ = fmt.Fprintf(f.stderr, "%s: %s of %s (%d%%)%s\n", r.Name, humanBytes(r.Done), humanBytes(r.Size), percent(r.Done, r.Size), f.rateText(r))
		}
	}
}

// stateWords says a copy's state as a person reads it.
func stateWords(r copyRow) string {
	switch r.State {
	case "queued":
		return "waits for its turn"
	case "running":
		return "copies"
	case "verifying":
		return "checks"
	case "waiting":
		if h := r.host(); h != "" {
			return h + " is not reachable. The copy continues when it comes back."
		}
		return "waits"
	case "paused":
		return "is paused. Resume it with: tuios transfers resume " + r.ID
	case "conflict":
		n := r.ConflictCount
		return fmt.Sprintf("waits for an answer about %d %s that %s there and %s", n, pluralWord(n, "file", "files"), pluralWord(n, "is", "are"), pluralWord(n, "differs", "differ"))
	}
	return r.State
}

func (f *follower) rateText(r copyRow) string {
	f.mu.Lock()
	rate := f.ratesEMA[r.ID]
	f.mu.Unlock()
	if rate <= 0 {
		return ""
	}
	s := ", " + humanBytes(int64(rate)) + "/s"
	if r.ETAms > 0 && rate > 0 {
		left := time.Duration(float64(r.Size-r.Done)/rate) * time.Second
		s += ", " + humanDuration(left) + " left"
	}
	return s
}

// run follows every copy to its end, drawing as it goes. It returns when they
// have all ended, or when stop says so.
func (f *follower) run(stop <-chan struct{}) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	f.refresh()
	for !f.allEnded() {
		f.answerConflicts()
		select {
		case line, ok := <-f.events:
			if !ok {
				f.polling = true
				f.events = nil
				continue
			}
			f.event(line)
		case <-poll.C:
			if f.polling {
				f.refresh()
			}
		case <-tick.C:
			f.draw(false)
		case <-stop:
			return
		}
	}
	// Events that came with the end, such as the last files.
	for {
		select {
		case line, ok := <-f.events:
			if !ok {
				f.draw(true)
				return
			}
			f.event(line)
			continue
		case <-time.After(50 * time.Millisecond):
		}
		break
	}
	f.draw(true)
}

// refresh reads every followed copy's row from transfer-list.
func (f *follower) refresh() {
	f.mu.Lock()
	ids := append([]string(nil), f.ids...)
	f.mu.Unlock()
	for _, id := range ids {
		raw, err := f.ctl.Call("transfer-list", map[string]any{"id": id})
		if err != nil {
			continue
		}
		var out struct {
			Transfers []copyRow `json:"transfers"`
		}
		if json.Unmarshal(raw, &out) == nil && len(out.Transfers) == 1 {
			f.update(out.Transfers[0])
		}
	}
}

// answerConflicts asks the person about a copy that waits on conflicts.
func (f *follower) answerConflicts() {
	if f.ask == nil {
		return
	}
	f.mu.Lock()
	var waiting []copyRow
	for _, id := range f.ids {
		if r := f.rows[id]; r.State == "conflict" && len(r.Conflicts) > 0 {
			key := r.ID + "\x00" + r.Conflicts[0].Rel
			if !f.asked[key] {
				f.asked[key] = true
				waiting = append(waiting, r)
			}
		}
	}
	f.mu.Unlock()
	for _, r := range waiting {
		f.clear()
		if f.ask(r) {
			// The answers changed the row; read it again.
			if raw, err := f.ctl.Call("transfer-list", map[string]any{"id": r.ID}); err == nil {
				var out struct {
					Transfers []copyRow `json:"transfers"`
				}
				if json.Unmarshal(raw, &out) == nil && len(out.Transfers) == 1 {
					f.update(out.Transfers[0])
				}
			}
		}
	}
}

// clear takes the drawn block away, so a question or a line can be printed.
func (f *follower) clear() {
	if f.out != outTerminal || f.drawn == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\r\x1b[%dA", f.drawn)
	for range f.drawn {
		b.WriteString("\x1b[2K\n")
	}
	fmt.Fprintf(&b, "\x1b[%dA", f.drawn)
	_, _ = io.WriteString(f.stderr, b.String())
	f.drawn = 0
}

// draw draws the block again on a terminal, at most ten times a second.
// final takes the block away for the summary.
func (f *follower) draw(final bool) {
	if f.out != outTerminal || f.paused.Load() {
		return
	}
	if final {
		f.clear()
		return
	}
	if time.Since(f.lastDraw) < 100*time.Millisecond {
		return
	}
	f.lastDraw = time.Now()
	f.mu.Lock()
	var lines []string
	shown, waiting := 0, 0
	for _, id := range f.ids {
		r := f.rows[id]
		if r.ended() {
			continue
		}
		if shown == 3 {
			waiting++
			continue
		}
		shown++
		lines = append(lines, f.block(r)...)
	}
	f.mu.Unlock()
	if waiting > 0 {
		lines = append(lines, f.dim(fmt.Sprintf("  %d more %s wait.", waiting, pluralWord(waiting, "copy", "copies"))))
	}
	var b strings.Builder
	if f.drawn > 0 {
		fmt.Fprintf(&b, "\r\x1b[%dA", f.drawn)
	}
	for _, l := range lines {
		b.WriteString("\x1b[2K" + l + "\n")
	}
	for i := len(lines); i < f.drawn; i++ {
		b.WriteString("\x1b[2K\n")
	}
	if extra := f.drawn - len(lines); extra > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", extra)
	}
	f.drawn = len(lines)
	_, _ = io.WriteString(f.stderr, b.String())
}

func (f *follower) style(c color.Color) lipgloss.Style {
	if c == nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(c)
}

func (f *follower) dim(s string) string { return f.style(f.pal.FgDim).Render(s) }

// block is the lines of one copy on a terminal.
func (f *follower) block(r copyRow) []string {
	pal := f.pal
	name := r.Name
	if r.Kind == "dir" {
		name += "/"
	}
	head := truncateCells(f.style(pal.Fg).Bold(true).Render(name)+f.dim("  to "+r.where()), f.width-1)
	lines := []string{head}
	barW := max(min(f.width-34, 44), 10)
	switch r.State {
	case "waiting":
		return append(lines, "  "+f.style(pal.Warning).Render(stateWords(r)))
	case "paused", "queued":
		return append(lines, "  "+f.dim(capitalize(stateWords(r))+"."))
	case "conflict":
		return append(lines, "  "+f.style(pal.Warning).Render(capitalize(stateWords(r))+"."))
	}
	if r.Kind == "dir" {
		now := ""
		if r.Current != "" {
			now = "  now: " + r.Current
		}
		lines = append(lines, truncateCells("  "+fmt.Sprintf("%s of %s files", commas(r.FilesDone), commas(r.Files))+f.dim(now), f.width-1))
		if r.CurrentSize > 0 {
			lines = append(lines, "  "+f.dim("file ")+" "+f.bar(r.CurrentDone, r.CurrentSize, barW)+fmt.Sprintf("  %3d%%  %s of %s", percent(r.CurrentDone, r.CurrentSize), humanBytes(r.CurrentDone), humanBytes(r.CurrentSize)))
		}
		lines = append(lines, "  "+f.dim("total")+" "+f.bar(r.Done, r.Size, barW)+fmt.Sprintf("  %3d%%  %s of %s", percent(r.Done, r.Size), humanBytes(r.Done), humanBytes(r.Size))+f.rateLine(r))
		return lines
	}
	lines = append(lines, fmt.Sprintf("  %s of %s  %d%%", humanBytes(r.Done), humanBytes(r.Size), percent(r.Done, r.Size))+f.rateLine(r))
	return append(lines, "  "+f.bar(r.Done, r.Size, barW))
}

func (f *follower) rateLine(r copyRow) string {
	if r.State == "verifying" {
		return "  " + f.dim("checks")
	}
	s := strings.ReplaceAll(strings.TrimPrefix(f.rateText(r), ", "), ", ", "  ")
	if s == "" {
		return ""
	}
	return "  " + s
}

// bar is a progress bar of width cells. The filled part is #, the rest -, so
// it reads with no colour; the colours are the accent and a muted track.
func (f *follower) bar(done, size int64, width int) string {
	fill := 0
	if size > 0 {
		fill = int(float64(width) * float64(min(done, size)) / float64(size))
	} else {
		fill = width
	}
	return "[" + f.style(f.pal.Accent).Render(strings.Repeat("#", fill)) + f.style(f.pal.FgMute).Render(strings.Repeat("-", width-fill)) + "]"
}

// summary prints how each copy ended and returns the exit code.
func (f *follower) summary() int {
	code := 0
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.ids {
		r := f.rows[id]
		code = worseExit(code, rowExit(r, f.explicit))
		if f.out == outJSON {
			continue
		}
		switch r.State {
		case "done":
			if f.out != outQuiet {
				_, _ = fmt.Fprintln(f.stdout, doneLine(r))
			}
		case "cancelled":
			_, _ = fmt.Fprintf(f.stderr, "The copy of %s to %s was cancelled. Nothing more is written.\n", r.Name, r.where())
		default:
			_, _ = fmt.Fprintf(f.stderr, "The copy of %s to %s failed: %s\n", r.Name, r.where(), copyFailText(r))
		}
		if f.out == outQuiet && r.State == "done" {
			continue
		}
		f.notes(r)
	}
	return code
}

// notes lists what a copy left out, under its summary line.
func (f *follower) notes(r copyRow) {
	files := f.files[r.ID]
	var skipped, left, failed []fileNote
	for _, n := range files {
		switch n.state {
		case "skipped":
			skipped = append(skipped, n)
		case "conflict":
			left = append(left, n)
		case "failed":
			failed = append(failed, n)
		}
	}
	list := func(w io.Writer, head string, ns []fileNote, count int) {
		if count == 0 {
			return
		}
		_, _ = fmt.Fprintln(w, head)
		for i, n := range ns {
			if i == 20 {
				_, _ = fmt.Fprintf(w, "  and %d more\n", count-20)
				break
			}
			line := "  " + n.rel
			if n.msg != "" {
				line += ": " + n.msg
			}
			_, _ = fmt.Fprintln(w, line)
		}
	}
	if r.ConflictsSkipped > 0 {
		list(f.stdout, fmt.Sprintf("%s %s there and %s, so %s skipped:", commas(r.ConflictsSkipped), pluralWord(r.ConflictsSkipped, "file was", "files were"), pluralWord(r.ConflictsSkipped, "differs", "differ"), pluralWord(r.ConflictsSkipped, "it was", "they were")), skipped, r.ConflictsSkipped)
	}
	if r.ConflictsLeft > 0 {
		list(f.stdout, fmt.Sprintf("%s %s made there during the copy, or %s in the way. %s not replaced:", commas(r.ConflictsLeft), pluralWord(r.ConflictsLeft, "file was", "files were"), pluralWord(r.ConflictsLeft, "a folder is", "folders are"), pluralWord(r.ConflictsLeft, "It was", "They were")), left, r.ConflictsLeft)
	}
	if r.Skipped > 0 {
		items := make([]fileNote, 0, len(r.SkippedItems))
		for _, s := range r.SkippedItems {
			items = append(items, fileNote{rel: s.Rel + " (" + s.Kind + ")"})
		}
		list(f.stdout, fmt.Sprintf("%s %s not files or folders, so %s not copied:", commas(r.Skipped), pluralWord(r.Skipped, "item is", "items are"), pluralWord(r.Skipped, "it was", "they were")), items, r.Skipped)
	}
	if len(failed) > 1 {
		list(f.stderr, "These files did not copy:", failed, max(r.FailedFiles, len(failed)))
	}
}

// doneLine is the one line a finished copy prints on stdout.
func doneLine(r copyRow) string {
	copied := r.FilesDone - r.Same
	check := ""
	if r.Verified || (r.Kind == "dir" && copied > 0) {
		check = " (checked)"
	}
	if r.Kind != "dir" {
		switch {
		case r.Same > 0:
			return fmt.Sprintf("%s is on %s already, with the same bytes. Nothing was copied.", r.Name, r.where())
		case r.ConflictsSkipped > 0 || r.ConflictsLeft > 0:
			return fmt.Sprintf("%s was not copied: %s is there and differs.", r.Name, r.where())
		}
		return fmt.Sprintf("Copied %s, %s, to %s%s.", r.Name, humanBytes(r.Size), r.where(), check)
	}
	if copied <= 0 && r.Same > 0 {
		return fmt.Sprintf("The %s %s of %s %s on %s already, with the same bytes. Nothing was copied.", commas(r.Same), pluralWord(r.Same, "file", "files"), r.Name, pluralWord(r.Same, "is", "are"), r.where())
	}
	line := fmt.Sprintf("Copied %s %s, %s, to %s%s.", commas(copied), pluralWord(copied, "file", "files"), humanBytes(r.Size), r.where(), check)
	if r.Same > 0 {
		line += fmt.Sprintf(" %s %s the same already.", commas(r.Same), pluralWord(r.Same, "file was", "files were"))
	}
	return line
}

// copyFailText is why a copy failed and what to do.
func copyFailText(r copyRow) string {
	msg := r.Error
	if msg == "" {
		msg = r.Code
	}
	switch r.Code {
	case session.ErrVerbForbidden:
		return msg + "\nThat is the decision of the person who set up " + r.host() + ". Ask them, or copy to a folder it allows."
	case session.ErrVerbDiskFull:
		return msg + "\nMake space on that machine, then run: tuios transfers resume " + r.ID
	case session.ErrVerbHashMismatch, session.ErrVerbSourceChanged:
		return msg
	}
	if r.FailedFiles > 0 {
		return msg + "\nTry the files that failed again with: tuios transfers resume " + r.ID
	}
	return msg
}

// ---- words and numbers ------------------------------------------------------

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func percent(done, size int64) int {
	if size <= 0 {
		return 100
	}
	return int(min(done, size) * 100 / size)
}

// commas writes n with a comma every three digits: 1,204.
func commas(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// humanBytes writes a size in the binary units: 2.00 GiB.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	units := "KMGTP"[exp : exp+1]
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %siB", v, units)
	case v >= 10:
		return fmt.Sprintf("%.1f %siB", v, units)
	}
	return fmt.Sprintf("%.2f %siB", v, units)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// truncateCells cuts a styled line to width cells.
func truncateCells(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return overlay.Truncate(s, width)
}

// errCode is the daemon's error code in err, or "".
func errCode(err error) string {
	if call, ok := errors.AsType[*session.VerbCallError](err); ok {
		return call.Code
	}
	return ""
}
