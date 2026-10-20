package transcriptview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/transcript"
)

// FormatOpenCode is opencode 2.x, which keeps its sessions in a database and
// serves them from a background service. The transcript path is the
// service's service.json, and Options.Session names the session the
// opencode plugin reported. See opencodeRead.
const FormatOpenCode = "opencode-service"

// ErrService reports that neither opencode's service nor its export command
// gave the session. It says nothing more: the service's address and password
// stay out of every error.
var ErrService = errors.New("transcriptview: opencode did not give the session")

// Bounds on what the opencode reader reads.
const (
	// openCodeFileMax bounds service.json.
	openCodeFileMax = 64 << 10
	// openCodeBodyMax bounds one reply or one export.
	openCodeBodyMax = 32 << 20
	// openCodeReadBudget bounds one read, the service and the export
	// together. The client gives a read 5 s.
	openCodeReadBudget = 4 * time.Second
	// openCodeServiceBudget bounds the requests to the service.
	openCodeServiceBudget = 1500 * time.Millisecond
	// openCodeShare is how long the result of one fetch serves other reads
	// of the session.
	openCodeShare = 2 * time.Second
	// openCodePage is the most messages one read asks for, the service's
	// own limit.
	openCodePage = 200
)

// openCodeService is what service.json says. The password lives here, in
// this call's memory, and nowhere else.
type openCodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	PID      int    `json:"pid"`
}

// opencodeRead reads the newest messages of an opencode session.
//
// It asks the background service the session's opencode is attached to.
// service.json gives its address and password. The file must belong to this
// user and be writable by no one else, since whoever can write it chooses
// where the password goes, and the address must be 127.0.0.1. Before the
// password goes out, the server side of the connection must be shown to be
// this user's: a service that exited leaves its file, and another user may
// hold its port now. The check reads the kernel's socket table for the
// connection itself, after the connect (connOwnedByMe): on Linux the uid in
// /proc/net/tcp, on macOS the so_uid in the sysctl net.inet.tcp.pcblist_n,
// and on Windows the owning pid in GetExtendedTcpTable, which must be the pid
// in service.json and run as this user. A request goes to that address
// alone, with no proxy.
//
// When no service is there (no service.json, a server side not this user's,
// or nothing listening), it runs `opencode session export --standalone
// <id>`, which reads the same database through a private server and starts
// no service. A service that is there but slow or failing is an error, never
// a reason to start the export: the export is a whole opencode process. An
// export is used again while opencode's database files are unchanged
// (openCodeExportCached).
//
// The whole read stays under openCodeReadBudget, inside the client's own
// limit, so a read the client gave up on is not still running. Reads of one
// session share one fetch, kept for openCodeShare, so the events of a
// streaming session do not start a fetch each.
//
// There is no cursor into a session the service pages by its own cursors, so
// a read after a cursor returns the newest page again with Reset, and a read
// before one returns nothing: the page is all this reader shows.
func opencodeRead(path string, opts Options) (Page, error) {
	if !validOpenCodeSession(opts.Session) {
		return Page{}, ErrService
	}
	if opts.Before != "" {
		return Page{Cursor: "oc1"}, nil
	}
	msgs, err := openCodeShared(path+"\x00"+opts.Session, func() ([]any, error) {
		return openCodeFetch(path, opts.Session)
	})
	if err != nil {
		return Page{}, ErrService
	}
	limit := min(opts.Limit, openCodePage)
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	r := &reader{opts: opts, lcsLeft: LCSCellsPerCall}
	var entries []Entry
	for _, m := range msgs {
		entries = append(entries, openCodeMessage(asObj(m))...)
	}
	foldResults(entries)
	total, keep := 0, 0
	for i := len(entries) - 1; i >= 0; i-- {
		n := r.entrySize(&entries[i])
		if total+n > opts.MaxBytes-pageOverhead {
			keep = i + 1
			break
		}
		total += n
	}
	return Page{Entries: entries[keep:], Cursor: "oc1", Reset: opts.After != ""}, nil
}

// errNoService says no service of this user is there to ask, which is the
// one case the export runs for.
var errNoService = errors.New("transcriptview: no opencode service")

// openCodeFetch reads the newest messages of a session, oldest first: from
// the service, or by the export when there is none.
func openCodeFetch(path, session string) ([]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openCodeReadBudget)
	defer cancel()
	svcCtx, svcCancel := context.WithTimeout(ctx, openCodeServiceBudget)
	defer svcCancel()
	msgs, err := openCodeFromService(svcCtx, path, session)
	if errors.Is(err, errNoService) {
		return openCodeExportCached(ctx, session)
	}
	return msgs, err
}

// connOwnedByMe reports whether the server side of conn, a loopback
// connection, belongs to this user. It reads the kernel's socket table after
// the connect, so the socket it checks is the one the request goes to. The
// server side can reach the table a moment after the connect returns, so an
// absent row is read again a few times.
func connOwnedByMe(ctx context.Context, conn net.Conn, svcPID int) bool {
	local, ok1 := conn.LocalAddr().(*net.TCPAddr)
	remote, ok2 := conn.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 || !local.IP.Equal(net.IPv4(127, 0, 0, 1)) || !remote.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return false
	}
	c := serverSide{port: remote.Port, peer: local.Port}
	for range 20 {
		switch serverOwner(c, svcPID) {
		case ownerMine:
			return true
		case ownerOther:
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Millisecond):
		}
	}
	return false
}

// openCodeFlight is one fetch of a session, shared by the reads that come
// while it runs and for openCodeShare after.
type openCodeFlight struct {
	done chan struct{}
	msgs []any
	err  error
	at   time.Time
}

var openCodeFlights struct {
	sync.Mutex
	m map[string]*openCodeFlight
}

// openCodeShared runs fetch once for key at a time, and gives a result less
// than openCodeShare old to every read that asks for it.
func openCodeShared(key string, fetch func() ([]any, error)) ([]any, error) {
	fl := &openCodeFlights
	fl.Lock()
	now := time.Now()
	for k, f := range fl.m {
		if !f.at.IsZero() && now.Sub(f.at) > openCodeShare {
			delete(fl.m, k)
		}
	}
	if f := fl.m[key]; f != nil {
		fl.Unlock()
		<-f.done
		return f.msgs, f.err
	}
	f := &openCodeFlight{done: make(chan struct{})}
	if fl.m == nil {
		fl.m = make(map[string]*openCodeFlight)
	}
	fl.m[key] = f
	fl.Unlock()
	// The entry is finished in a defer, so a fetch that panics into a
	// recover above still lets the reads that wait on it go, and lets the
	// entry be evicted.
	finished := false
	defer func() {
		if !finished {
			f.msgs, f.err = nil, ErrService
		}
		fl.Lock()
		f.at = time.Now()
		fl.Unlock()
		close(f.done)
	}()
	f.msgs, f.err = fetch()
	finished = true
	return f.msgs, f.err
}

// validOpenCodeSession reports an opencode session id: "ses_" and letters
// and digits. It is checked before it goes into a URL or an argument.
func validOpenCodeSession(id string) bool {
	rest, ok := strings.CutPrefix(id, "ses_")
	if !ok || rest == "" || len(id) > 64 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// readServiceFile reads service.json, refusing a file another user owns or
// another user could have written. A missing file is errNoService.
func readServiceFile(path string) (openCodeService, *url.URL, error) {
	var svc openCodeService
	f, err := transcript.OpenRegular(path)
	if err != nil {
		if os.IsNotExist(err) {
			return svc, nil, errNoService
		}
		return svc, nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return svc, nil, err
	}
	if !serviceFileSafe(f, info) || info.Size() > openCodeFileMax {
		return svc, nil, ErrService
	}
	data, err := io.ReadAll(io.LimitReader(f, openCodeFileMax))
	defer clear(data)
	if err != nil {
		return svc, nil, err
	}
	if json.Unmarshal(data, &svc) != nil || svc.Password == "" {
		return svc, nil, ErrService
	}
	u, err := url.Parse(svc.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil {
		return svc, nil, ErrService
	}
	return svc, u, nil
}

// openCodeClient talks to 127.0.0.1 only: no proxy, no redirect, and a dial
// that refuses any other address whatever the URL says. The dial gives a
// connection only when its server side belongs to this user (connOwnedByMe),
// so a request, and the password in it, goes out on no other.
func openCodeClient(svcPID int) *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host != "127.0.0.1" {
					return nil, ErrService
				}
				conn, err := dialer.DialContext(ctx, "tcp4", addr)
				if err != nil {
					return nil, err
				}
				if !connOwnedByMe(ctx, conn, svcPID) {
					_ = conn.Close()
					return nil, errNoService
				}
				return conn, nil
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// openCodeFromService asks the service for the newest messages, oldest
// first. It returns errNoService when no service of this user is there: no
// file, a connection it cannot show to be this user's, or nothing
// listening.
func openCodeFromService(ctx context.Context, path, session string) ([]any, error) {
	svc, base, err := readServiceFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { svc.Password = "" }()
	client := openCodeClient(svc.PID)
	get := func(path string, query url.Values) (jobj, error) {
		u := *base
		u.Path, u.RawPath, u.RawQuery, u.Fragment = "", "", query.Encode(), ""
		u = *u.JoinPath(path)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, ErrService
		}
		req.SetBasicAuth("opencode", svc.Password)
		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, errNoService) {
				return nil, errNoService
			}
			return nil, ErrService
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, ErrService
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeBodyMax))
		defer clear(body)
		if err != nil {
			return nil, ErrService
		}
		o, ok := parseObj(body)
		if !ok || o == nil {
			return nil, ErrService
		}
		return o, nil
	}
	info, err := get("/api/info", nil)
	if err != nil {
		return nil, err
	}
	if pid, _ := info["pid"].(float64); svc.PID != 0 && int(pid) != svc.PID {
		// Another process of this user holds the port now.
		return nil, ErrService
	}
	page, err := get("/api/session/"+session+"/message", url.Values{
		"order": {"desc"}, "limit": {strconv.Itoa(openCodePage)},
	})
	if err != nil {
		return nil, err
	}
	msgs := page.list("data")
	slices.Reverse(msgs)
	return msgs, nil
}

// Bounds on reusing an export.
const (
	// openCodeExportKeep is the longest an export is used again. It bounds
	// how stale a page can be when a change leaves the database files
	// looking the same, such as a write inside one tick of the file clock.
	openCodeExportKeep = 30 * time.Second
	// openCodeExportsMax bounds the sessions whose export is kept.
	openCodeExportsMax = 16
)

type openCodeExported struct {
	sig  string
	msgs []any
	at   time.Time
}

var openCodeExports struct {
	sync.Mutex
	m map[string]openCodeExported
}

// openCodeExportCached runs the export only when opencode's database may
// have changed since the last export of the session. The database files'
// sizes and times are read first, and an export taken while they matched is
// given again. A session that streams changes them and is exported again; a
// thread that is open on a quiet session starts no process.
//
// The signature is read before the export runs, so a write during the
// export makes the next read export again. The export itself leaves the
// files as they were after its first run.
func openCodeExportCached(ctx context.Context, session string) ([]any, error) {
	sig := openCodeDBSig()
	now := time.Now()
	ex := &openCodeExports
	ex.Lock()
	for k, e := range ex.m {
		if now.Sub(e.at) > openCodeExportKeep {
			delete(ex.m, k)
		}
	}
	if e, ok := ex.m[session]; ok && sig != "" && e.sig == sig {
		ex.Unlock()
		return e.msgs, nil
	}
	ex.Unlock()
	msgs, err := openCodeFromExport(ctx, session)
	if err != nil || sig == "" {
		return msgs, err
	}
	ex.Lock()
	if ex.m == nil {
		ex.m = make(map[string]openCodeExported)
	}
	if _, ok := ex.m[session]; !ok && len(ex.m) >= openCodeExportsMax {
		oldest := ""
		for k, e := range ex.m {
			if oldest == "" || e.at.Before(ex.m[oldest].at) {
				oldest = k
			}
		}
		delete(ex.m, oldest)
	}
	ex.m[session] = openCodeExported{sig: sig, msgs: msgs, at: now}
	ex.Unlock()
	return msgs, nil
}

// openCodeDBSig is the size and time of each database file of opencode, or
// "" when none is found. opencode keeps its database in its data folder,
// $XDG_DATA_HOME/opencode or ~/.local/share/opencode on every system, as
// opencode.db, or opencode-<channel>.db for a build of another channel, or
// the name in OPENCODE_DB. SQLite writes to the -wal file next to it, and
// to the database when it checkpoints.
func openCodeDBSig() string {
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		data = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(data, "opencode")
	var names []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, "opencode") && strings.HasSuffix(n, ".db") {
				names = append(names, filepath.Join(dir, n))
			}
		}
	}
	if db := os.Getenv("OPENCODE_DB"); db == ":memory:" {
		return ""
	} else if db != "" {
		if !filepath.IsAbs(db) {
			db = filepath.Join(dir, db)
		}
		if !slices.Contains(names, db) {
			names = append(names, db)
		}
	}
	var b strings.Builder
	found := false
	for _, db := range names {
		for _, p := range []string{db, db + "-wal"} {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			found = found || p == db
			fmt.Fprintf(&b, "%s\x00%d\x00%d\n", p, info.Size(), info.ModTime().UnixNano())
		}
	}
	if !found {
		return ""
	}
	return b.String()
}

// openCodeFromExport runs the export command, oldest message first. The
// command and every process it starts are killed when ctx ends.
func openCodeFromExport(ctx context.Context, session string) ([]any, error) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		return nil, err
	}
	cmd := childproc.CommandContext(ctx, bin, "session", "export", "--standalone", session)
	var out capWriter
	cmd.Stdout = &out
	cmd.Stdin = nil
	defer clear(out.buf.Bytes())
	if err := runTree(cmd); err != nil || out.over {
		return nil, ErrService
	}
	o, ok := parseObj(out.buf.Bytes())
	if !ok || o == nil {
		return nil, ErrService
	}
	return o.list("messages"), nil
}

// capWriter keeps at most openCodeBodyMax bytes and notes when more came.
type capWriter struct {
	buf  bytes.Buffer
	over bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > openCodeBodyMax {
		w.over = true
		return 0, io.ErrShortWrite
	}
	return w.buf.Write(p)
}

// openCodeMessage turns one message into entries. A user message is its
// text; an assistant message its text, reasoning and tool calls, each call
// with its result once it finished; a shell message the command the person
// ran. Switches, compactions, idle markers and the context opencode adds are
// left out.
func openCodeMessage(m jobj) []Entry {
	id := m.str("id")
	created, _ := m.obj("time")["created"].(float64)
	var out []Entry
	add := func(part string, e Entry) {
		e.ID, e.At = keyID(id, part), int64(created)
		out = append(out, e)
	}
	switch m.str("type") {
	case "user":
		if e, ok := textEntry(RoleUser, KindText, m.str("text")); ok {
			add("u", e)
		}
	case "assistant":
		for i, c := range m.list("content") {
			c := asObj(c)
			part := itoa(i)
			switch c.str("type") {
			case "text":
				if e, ok := textEntry(RoleAssistant, KindText, c.str("text")); ok {
					add(part, e)
				}
			case "reasoning":
				if e, ok := textEntry(RoleAssistant, KindThinking, c.str("text")); ok {
					add(part, e)
				}
			case "tool":
				for j, e := range openCodeTool(c) {
					add(part+"."+itoa(j), e)
				}
			}
		}
		if e, ok := textEntry(RoleAssistant, KindError, m.obj("error").str("message")); ok {
			add("err", e)
		}
	case "shell":
		call := callEntry("shell", id, classOther, jobj{"command": m.str("command")})
		for j, e := range call {
			add("s"+itoa(j), e)
		}
		if st := m.str("status"); st != "" && st != "running" {
			exit, _ := m["exit"].(float64)
			add("r", resultEntry(id, "", st != "exited" || exit != 0))
		}
	}
	return out
}

// openCodeTool is a tool call and, once it finished, its result. An edit's
// result carries opencode's unified patch with the file's line numbers.
func openCodeTool(c jobj) []Entry {
	name, id, st := c.str("name"), c.str("id"), c.obj("state")
	out := callEntry(name, id, openCodeClass(name), args(st["input"]))
	status := st.str("status")
	if status != "completed" && status != "error" {
		// streaming or running: the call shows running.
		return out
	}
	var text string
	for _, x := range st.list("content") {
		x := asObj(x)
		if x.str("type") == "text" {
			text += x.str("text")
		}
	}
	if status == "error" {
		if msg := st.obj("error").str("message"); msg != "" {
			text = msg
		}
	}
	res := resultEntry(id, text, status == "error")
	for _, f := range st.obj("metadata").list("files") {
		f := asObj(f)
		if files := parseUnified(f.str("patch"), f.str("file")); len(files) > 0 {
			res.raw.diff = &diffSrc{file: files[0].file, patch: files[0].hunks}
			break
		}
	}
	return append(out, res)
}

// openCodeClass is what an opencode tool does.
func openCodeClass(name string) toolClass {
	switch name {
	case "edit":
		return classEdit
	case "write":
		return classWrite
	case "todowrite":
		return classTodos
	case "apply_patch", "patch":
		return classPatch
	}
	return classOther
}
