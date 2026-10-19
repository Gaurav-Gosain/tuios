package transcriptview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
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
	// openCodeTimeout bounds one request to the service.
	openCodeTimeout = 2 * time.Second
	// openCodeExportTimeout bounds the export command.
	openCodeExportTimeout = 10 * time.Second
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
// It first asks the background service the session's opencode is attached
// to. service.json gives its address and password. The file must belong to
// this user and be writable by no one else, since whoever can write it
// chooses where the password goes, and the address must be 127.0.0.1. A
// request goes to that address alone, with no proxy and a short timeout,
// and the service must name the same process as the file (GET /api/info)
// before the session is asked for.
//
// When the service does not answer, it runs `opencode session export
// --standalone <id>`, which reads the same database through a private server
// and starts no service. That runs only here, when a person reads the
// thread.
//
// There is no cursor into a session the service pages by its own cursors, so
// a read after a cursor returns the newest page again with Reset. The thread
// grows on the plugin's reports, which come once per opencode event, so the
// page is read again only when the session changed.
func opencodeRead(path string, opts Options) (Page, error) {
	if !validOpenCodeSession(opts.Session) {
		return Page{}, ErrService
	}
	limit := min(opts.Limit, openCodePage)
	msgs, err := openCodeFromService(path, opts.Session, limit)
	if err != nil {
		msgs, err = openCodeFromExport(opts.Session)
		if err != nil {
			return Page{}, ErrService
		}
	}
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
// another user could have written.
func readServiceFile(path string) (openCodeService, error) {
	var svc openCodeService
	f, err := transcript.OpenRegular(path)
	if err != nil {
		return svc, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return svc, err
	}
	if info.Mode().Perm()&0o022 != 0 || !ownedByMe(info) || info.Size() > openCodeFileMax {
		return svc, ErrService
	}
	data, err := io.ReadAll(io.LimitReader(f, openCodeFileMax))
	defer clear(data)
	if err != nil {
		return svc, err
	}
	if json.Unmarshal(data, &svc) != nil || svc.Password == "" {
		return svc, ErrService
	}
	u, err := url.Parse(svc.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil {
		return svc, ErrService
	}
	return svc, nil
}

// openCodeClient talks to 127.0.0.1 only: no proxy, no redirect, and a dial
// that refuses any other address whatever the URL says.
func openCodeClient() *http.Client {
	dialer := &net.Dialer{Timeout: openCodeTimeout}
	return &http.Client{
		Timeout: openCodeTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host != "127.0.0.1" {
					return nil, ErrService
				}
				return dialer.DialContext(ctx, "tcp4", addr)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// openCodeFromService asks the service for the newest limit messages, oldest
// first.
func openCodeFromService(path, session string, limit int) ([]any, error) {
	svc, err := readServiceFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { svc.Password = "" }()
	client := openCodeClient()
	get := func(p string) (jobj, error) {
		req, err := http.NewRequest(http.MethodGet, svc.URL+p, nil)
		if err != nil {
			return nil, ErrService
		}
		req.SetBasicAuth("opencode", svc.Password)
		resp, err := client.Do(req)
		if err != nil {
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
	info, err := get("/api/info")
	if err != nil {
		return nil, err
	}
	if pid, _ := info["pid"].(float64); svc.PID != 0 && int(pid) != svc.PID {
		// Another process holds the port now.
		return nil, ErrService
	}
	page, err := get("/api/session/" + session + "/message?order=desc&limit=" + strconv.Itoa(max(limit, 1)))
	if err != nil {
		return nil, err
	}
	msgs := page.list("data")
	slices.Reverse(msgs)
	return msgs, nil
}

// openCodeFromExport runs the export command, oldest message first.
func openCodeFromExport(session string) ([]any, error) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), openCodeExportTimeout)
	defer cancel()
	cmd := childproc.CommandContext(ctx, bin, "session", "export", "--standalone", session)
	var out capWriter
	cmd.Stdout = &out
	cmd.Stdin = nil
	defer clear(out.buf.Bytes())
	if err := cmd.Run(); err != nil || out.over {
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

// ownedByMe reports whether info is a file of the user this process runs as.
func ownedByMe(info os.FileInfo) bool { return fileOwner(info) == os.Getuid() }
