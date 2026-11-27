//go:build linux

package panescope

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// serviceSlice is the slice the daemon's unit is in, and whether the daemon
// is a service of a user manager at all. It is read once. strings.Split and
// a plain sync.Once here, not the iterator and generic forms: they add bytes
// to the binary for code that runs once.
func serviceSlice() (string, bool) {
	sliceOnce.Do(func() {
		raw, err := os.ReadFile("/proc/self/cgroup")
		if err == nil {
			// The e2e suite turns scopes off (pane_scope=off): a test run
			// inside a service, such as a job of a build queue, would put
			// its panes in scopes that outlive the job when it is stopped.
			// pane_scope=1 turns them on for a daemon that is no service.
			switch knob := testknob.String("pane_scope"); knob {
			case "off":
			default:
				theSlice, isService = sliceOf(string(raw), knob != "")
			}
		}
	})
	return theSlice, isService
}

var (
	sliceOnce sync.Once
	theSlice  string
	isService bool
)

// sliceOf reads the unified line of /proc/self/cgroup. The daemon counts as
// a service when its cgroup is a .service under user@, or anywhere when
// force is set (the e2e suite's knob). The slice is the folder above the
// unit when that is a .slice, else empty, which the manager reads as its
// default.
func sliceOf(cgroup string, force bool) (string, bool) {
	for _, line := range strings.Split(cgroup, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		unit := filepath.Base(path)
		if !force && (!strings.Contains(path, "/user@") || !strings.HasSuffix(unit, ".service")) {
			return "", false
		}
		slice := filepath.Base(filepath.Dir(path))
		if !strings.HasSuffix(slice, ".slice") {
			slice = ""
		}
		return slice, true
	}
	return "", false
}

// mu keeps one call at a time, so a restore of many panes does not open many
// connections at once.
var mu sync.Mutex

// told is set once a scope was made, so the success is logged once.
var told atomic.Bool

func place(pid int, slice string, logf func(string, ...any)) {
	mu.Lock()
	defer mu.Unlock()
	if off.Load() {
		return
	}
	name := "tuios-pane-" + strconv.Itoa(pid) + ".scope"
	if err := startScope(name, pid, slice); err != nil {
		off.Store(true)
		if logf != nil {
			logf("Panes stay in the daemon's unit: could not start the scope %s: %v", name, err)
		}
		return
	}
	if logf != nil && told.CompareAndSwap(false, true) {
		logf("Each pane runs in a systemd scope of its own, such as %s", name)
	}
}

// callTimeout bounds the whole call: connect, auth and reply.
const callTimeout = 2 * time.Second

// startScope asks the user manager for the scope.
func startScope(name string, pid int, slice string) error {
	conn, bus, err := dialManager()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))
	r := bufio.NewReader(conn)
	if err := auth(conn, r); err != nil {
		return err
	}
	serial := uint32(1)
	if bus {
		// A bus wants Hello first. The private socket talks to the
		// manager alone and takes none.
		if _, err := conn.Write(methodCall(serial, "/org/freedesktop/DBus", "org.freedesktop.DBus", "Hello", "org.freedesktop.DBus", "", nil)); err != nil {
			return err
		}
		if err := awaitReply(r, serial); err != nil {
			return err
		}
		serial++
	}
	body := transientScopeBody(name, pid, slice)
	if _, err := conn.Write(methodCall(serial, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "StartTransientUnit", "org.freedesktop.systemd1", "ssa(sv)a(sa(sv))", body)); err != nil {
		return err
	}
	return awaitReply(r, serial)
}

// dialManager connects to the user manager's private socket, else to the
// user bus. bus says which.
func dialManager() (net.Conn, bool, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if c, err := net.DialTimeout("unix", filepath.Join(dir, "systemd", "private"), callTimeout); err == nil {
			return c, false, nil
		}
	}
	addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	path := ""
	for _, part := range strings.Split(addr, ";") {
		if rest, ok := strings.CutPrefix(part, "unix:"); ok {
			for _, kv := range strings.Split(rest, ",") {
				if p, ok := strings.CutPrefix(kv, "path="); ok {
					path = p
				}
			}
		}
		if path != "" {
			break
		}
	}
	if path == "" {
		if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
			path = filepath.Join(dir, "bus")
		}
	}
	if path == "" {
		return nil, false, errors.New("no user manager: XDG_RUNTIME_DIR and DBUS_SESSION_BUS_ADDRESS are not set")
	}
	c, err := net.DialTimeout("unix", path, callTimeout)
	if err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// auth runs the SASL EXTERNAL exchange with this process's uid.
func auth(conn net.Conn, r *bufio.Reader) error {
	uid := hex.EncodeToString([]byte(strconv.Itoa(os.Getuid())))
	if _, err := conn.Write([]byte("\x00AUTH EXTERNAL " + uid + "\r\n")); err != nil {
		return err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "OK ") {
		return errors.New("the user manager refused the connection: " + strings.TrimSpace(line))
	}
	_, err = conn.Write([]byte("BEGIN\r\n"))
	return err
}

// enc writes D-Bus values, little endian, aligned from the start of the
// buffer. The header ends on an 8-byte boundary, so the body is encoded on
// its own and keeps the same alignment.
type enc struct{ b []byte }

func (e *enc) align(n int) {
	for len(e.b)%n != 0 {
		e.b = append(e.b, 0)
	}
}

func (e *enc) byte(v byte) { e.b = append(e.b, v) }

func (e *enc) u32(v uint32) {
	e.align(4)
	e.b = binary.LittleEndian.AppendUint32(e.b, v)
}

// str writes a string or an object path.
func (e *enc) str(s string) {
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
	e.b = append(e.b, 0)
}

func (e *enc) sig(s string) {
	e.b = append(e.b, byte(len(s)))
	e.b = append(e.b, s...)
	e.b = append(e.b, 0)
}

// field writes one header field whose value is a string, an object path
// or a signature.
func (e *enc) field(code byte, typ, v string) {
	e.align(8)
	e.byte(code)
	e.sig(typ)
	if typ == "g" {
		e.sig(v)
	} else {
		e.str(v)
	}
}

// prop starts one property of StartTransientUnit: its name and the
// signature of its value. The caller writes the value.
func (e *enc) prop(key, typ string) {
	e.align(8)
	e.str(key)
	e.sig(typ)
}

// beginArray writes an array's length as zero and pads to the elements'
// alignment. It returns where the length is and where the elements start,
// for endArray.
func (e *enc) beginArray(elemAlign int) (int, int) {
	e.align(4)
	at := len(e.b)
	e.b = append(e.b, 0, 0, 0, 0)
	e.align(elemAlign)
	return at, len(e.b)
}

// endArray writes the length of the array beginArray started.
func (e *enc) endArray(at, start int) {
	binary.LittleEndian.PutUint32(e.b[at:], uint32(len(e.b)-start))
}

// methodCall is a whole METHOD_CALL message.
func methodCall(serial uint32, path, iface, member, dest, signature string, body []byte) []byte {
	h := &enc{}
	h.byte('l')
	h.byte(1) // METHOD_CALL
	h.byte(0)
	h.byte(1) // protocol version
	h.u32(uint32(len(body)))
	h.u32(serial)
	at, start := h.beginArray(8)
	h.field(1, "o", path)
	h.field(2, "s", iface)
	h.field(3, "s", member)
	h.field(6, "s", dest)
	if signature != "" {
		h.field(8, "g", signature)
	}
	h.endArray(at, start)
	h.align(8)
	return append(h.b, body...)
}

// transientScopeBody is the body of StartTransientUnit for the pane's scope.
func transientScopeBody(name string, pid int, slice string) []byte {
	e := &enc{}
	e.str(name)
	e.str("fail")
	at, start := e.beginArray(8)
	e.prop("Description", "s")
	e.str("tuios pane")
	e.prop("PIDs", "au")
	pat, pstart := e.beginArray(4)
	e.u32(uint32(pid))
	e.endArray(pat, pstart)
	if slice != "" {
		e.prop("Slice", "s")
		e.str(slice)
	}
	e.prop("CollectMode", "s")
	e.str("inactive-or-failed")
	e.prop("OOMPolicy", "s")
	e.str("continue")
	e.endArray(at, start)
	// No auxiliary units: an empty array, padded all the same.
	at, start = e.beginArray(8)
	e.endArray(at, start)
	return e.b
}

// awaitReply reads messages until the reply to serial, and returns the
// error the manager sent, if any. Signals on the way are skipped.
func awaitReply(r *bufio.Reader, serial uint32) error {
	for {
		typ, reply, errName, body, order, err := readMessage(r)
		if err != nil {
			return err
		}
		if reply != serial || (typ != 2 && typ != 3) {
			continue
		}
		if typ == 2 {
			return nil
		}
		msg := errName
		if len(body) >= 4 {
			if n := int(order.Uint32(body)); 4+n <= len(body) {
				msg += ": " + string(body[4:4+n])
			}
		}
		return errors.New(msg)
	}
}

// maxMessage bounds what is read for one message: a reply here is small.
const maxMessage = 1 << 20

// readMessage reads one message and returns its type, its reply serial, its
// error name and its body.
func readMessage(r *bufio.Reader) (byte, uint32, string, []byte, binary.ByteOrder, error) {
	fixed := make([]byte, 16)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return 0, 0, "", nil, nil, err
	}
	var order binary.ByteOrder = binary.LittleEndian
	if fixed[0] == 'B' {
		order = binary.BigEndian
	}
	bodyLen := int(order.Uint32(fixed[4:]))
	fieldsLen := int(order.Uint32(fixed[12:]))
	pad := (8 - (16+fieldsLen)%8) % 8
	if bodyLen > maxMessage || fieldsLen > maxMessage {
		return 0, 0, "", nil, nil, errors.New("the user manager sent a message too large")
	}
	rest := make([]byte, fieldsLen+pad+bodyLen)
	if _, err := io.ReadFull(r, rest); err != nil {
		return 0, 0, "", nil, nil, err
	}
	fields := rest[:fieldsLen]
	var reply uint32
	var errName string
	// Offsets in fields are 16 bytes into the message, which is 8-aligned.
	i := 0
	alignTo := func(n int) { i = (i + n - 1) / n * n }
	for i < len(fields) {
		alignTo(8)
		if i+2 > len(fields) {
			break
		}
		code := fields[i]
		sigLen := int(fields[i+1])
		i += 2
		if i+sigLen+1 > len(fields) {
			break
		}
		sig := string(fields[i : i+sigLen])
		i += sigLen + 1
		switch sig {
		case "u":
			alignTo(4)
			if i+4 > len(fields) {
				return 0, 0, "", nil, nil, errors.New("bad header")
			}
			if code == 5 {
				reply = order.Uint32(fields[i:])
			}
			i += 4
		case "s", "o":
			alignTo(4)
			if i+4 > len(fields) {
				return 0, 0, "", nil, nil, errors.New("bad header")
			}
			n := int(order.Uint32(fields[i:]))
			i += 4
			if i+n+1 > len(fields) {
				return 0, 0, "", nil, nil, errors.New("bad header")
			}
			if code == 4 {
				errName = string(fields[i : i+n])
			}
			i += n + 1
		case "g":
			if i >= len(fields) {
				return 0, 0, "", nil, nil, errors.New("bad header")
			}
			n := int(fields[i])
			i += 1 + n + 1
		default:
			return 0, 0, "", nil, nil, errors.New("bad header field type " + sig)
		}
	}
	return fixed[1], reply, errName, rest[fieldsLen+pad:], order, nil
}
