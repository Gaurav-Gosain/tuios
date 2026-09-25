package app

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Fuzzing the kitty graphics passthrough: what several panes' guests send, and
// what reaches the host terminal because of it.
//
// The passthrough is a boundary. Every pane's guest writes kitty commands, and
// they all land on one host terminal, which has one image id namespace and
// executes whatever escape sequences arrive. So what goes to the host has to
// be graphics and cursor bookkeeping only, and what one pane's guest causes
// has to touch only the images tuios allocated for that pane.
//
// The ways this could fail, written down before the target:
//
//  1. A guest's bytes reach the host outside a graphics command: a control
//     byte, a stray escape, a CSI that is not the cursor save, move and
//     restore the passthrough brackets a placement with.
//  2. A graphics command sent to the host carries control data or payload
//     characters a well-formed command cannot, so the host parses the rest
//     as something else.
//  3. A command one pane's guest sent names a host image id tuios never
//     allocated for that pane: the guest's own id forwarded untranslated,
//     or another pane's host id, so pane A overwrites or deletes pane B's
//     image.
//  4. A reply to a guest names an id other than the one that guest used, so
//     the guest cannot match it or learns a host id.
//  5. A panic on a chunk stream that never ends, a placement of an image
//     never sent, or a delete of everything.

// lockedBuffer is the host terminal: the async frame writer and the render
// path may both write.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) take() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]byte(nil), l.b.Bytes()...)
	l.b.Reset()
	return out
}

var (
	hostAPCBody = regexp.MustCompile(`^[A-Za-z0-9=,\-]*(;[A-Za-z0-9+/=]*)?$`)
	hostCUP     = regexp.MustCompile(`^\x1b\[[0-9]+;[0-9]+H`)
	apcImageID  = regexp.MustCompile(`(?:^|,)i=([0-9]+)`)
)

// hostTokens splits host output into the sequences the passthrough may send
// and reports the first byte that is none of them. It returns the bodies of
// the graphics commands.
func hostTokens(out []byte) (apcs []string, bad string) {
	s := string(out)
	for len(s) > 0 {
		switch {
		case strings.HasPrefix(s, "\x1b_G"):
			end := strings.Index(s, "\x1b\\")
			if end < 0 {
				return apcs, "an unterminated graphics command: " + strconv.Quote(trimQ(s))
			}
			body := s[3:end]
			if !hostAPCBody.MatchString(body) {
				return apcs, "a graphics command with bytes no command carries: " + strconv.Quote(trimQ(body))
			}
			apcs = append(apcs, body)
			s = s[end+2:]
		case strings.HasPrefix(s, "\x1b7"), strings.HasPrefix(s, "\x1b8"):
			s = s[2:]
		case strings.HasPrefix(s, "\x1b[?2026h"), strings.HasPrefix(s, "\x1b[?2026l"):
			s = s[8:]
		case hostCUP.MatchString(s):
			s = s[len(hostCUP.FindString(s)):]
		default:
			return apcs, "bytes outside any graphics or cursor sequence: " + strconv.Quote(trimQ(s))
		}
	}
	return apcs, ""
}

func trimQ(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// kittyStep reads one guest command from the fuzz input.
type kittyStep struct {
	win     int
	body    string
	imageID uint32
}

func decodeKittySteps(in []byte) []kittyStep {
	var steps []kittyStep
	for i := 0; i+4 <= len(in) && len(steps) < 64; i += 4 {
		b0, b1, b2, b3 := in[i], in[i+1], in[i+2], in[i+3]
		win := int(b0 & 1)
		id := uint32(b0>>1) % 4
		w, h := 1+int(b1&3), 1+int(b1>>2&3)
		f, bpp := "24", 3
		if b1&0x10 != 0 {
			f, bpp = "32", 4
		}
		px := make([]byte, w*h*bpp)
		for j := range px {
			px[j] = b3 + byte(j)
		}
		payload := base64.StdEncoding.EncodeToString(px)
		ids := ""
		if id != 0 {
			ids = ",i=" + strconv.Itoa(int(id))
		}
		var body string
		switch b2 % 10 {
		case 0:
			body = "a=T,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ";" + payload
		case 1:
			body = "a=t,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ";" + payload
		case 2:
			body = "a=p" + ids + ",c=" + strconv.Itoa(int(b3%5)) + ",r=" + strconv.Itoa(int(b3>>4%5))
		case 3:
			body = "a=d,d=" + string("aAiIpPcCzZ"[int(b3)%10]) + ids
		case 4:
			// The first chunk of a chunked transmission.
			half := len(payload) / 8 * 4
			body = "a=T,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ",m=1;" + payload[:half]
		case 5:
			body = "m=0;" + payload[len(payload)/8*4:]
		case 6:
			body = "a=q" + ids + ",f=24,s=1,v=1;AAAA"
		case 7:
			body = "a=p,U=1" + ids + ",c=2,r=2"
		case 8:
			// An animation frame edit of an image the pane may never have
			// sent.
			body = "a=f,r=1,x=0,y=0,s=1,v=1,f=24" + ids + ";" + base64.StdEncoding.EncodeToString(px[:3])
		default:
			body = "a=T,f=100" + ids + ",q=" + strconv.Itoa(int(b3%3)) + ";" + payload
		}
		steps = append(steps, kittyStep{win: win, body: body, imageID: id})
	}
	return steps
}

func FuzzKittyPassthrough(f *testing.F) {
	// Pane B transmits and places image 1, then pane A transmits its own
	// image 1 without placing it.
	f.Add([]byte{0x03, 0x00, 0x00, 0x10, 0x02, 0x00, 0x01, 0x20})
	f.Add([]byte{0x02, 0x05, 0x04, 0x00, 0x02, 0x05, 0x05, 0x00, 0x02, 0x00, 0x02, 0x11})
	f.Add([]byte{0x03, 0x00, 0x00, 0x00, 0x02, 0x00, 0x03, 0x00, 0x03, 0x00, 0x03, 0x02})
	f.Add([]byte{0x04, 0x10, 0x06, 0x00, 0x05, 0x00, 0x07, 0x00, 0x00, 0x00, 0x08, 0x01})

	f.Fuzz(func(t *testing.T, in []byte) {
		steps := decodeKittySteps(in)
		if len(steps) == 0 {
			return
		}
		host := &lockedBuffer{}
		kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
			Output: host,
			Caps:   &HostCapabilities{KittyGraphics: true, KittyAnimation: true, TrueColor: true, TerminalName: "kitty"},
		})
		wins := []string{"pane-a-0000000000000000", "pane-b-1111111111111111"}

		// owned is every host id the pane holds, before or after a step: a
		// delete removes the mapping of the image it names.
		ownedBy := func(win string, into map[uint64]bool) {
			kp.mu.Lock()
			defer kp.mu.Unlock()
			for _, h := range kp.imageIDMap[win] {
				into[uint64(h)] = true
			}
		}
		for n, st := range steps {
			win := wins[st.win]
			owned := map[uint64]bool{}
			ownedBy(win, owned)
			cmd, err := vt.ParseKittyCommand([]byte(st.body))
			if err != nil || cmd == nil {
				t.Fatalf("step %d: the harness built an unparseable command %q", n, st.body)
			}
			raw := []byte("\x1b_G" + st.body + "\x1b\\")
			var replies [][]byte
			kp.ForwardCommand(cmd, raw, win, 10*st.win, 2, 30, 10, 1, 1, 0, 0, 0, false,
				func(b []byte) { replies = append(replies, append([]byte(nil), b...)) })

			out := append(kp.FlushPending(), host.take()...)
			ownedBy(win, owned)
			apcs, bad := hostTokens(out)
			if bad != "" {
				t.Fatalf("step %d (%s %q): host got %s", n, win[:6], st.body, bad)
			}
			for _, body := range apcs {
				m := apcImageID.FindStringSubmatch(body)
				if m == nil {
					continue
				}
				hostID, _ := strconv.ParseUint(m[1], 10, 32)
				if hostID == 0 {
					continue
				}
				if !owned[hostID] {
					t.Fatalf("step %d: %s sent %q and the host got image id %d, which tuios never allocated to that pane:\n%q",
						n, win[:6], st.body, hostID, body)
				}
			}
			for _, r := range replies {
				m := regexp.MustCompile(`\x1b_Gi=([0-9]+)`).FindSubmatch(r)
				if m == nil {
					continue
				}
				if got, _ := strconv.ParseUint(string(m[1]), 10, 32); uint32(got) != cmd.ImageID {
					t.Fatalf("step %d: a reply to %s's %q names image %d, the guest used %d", n, win[:6], st.body, got, cmd.ImageID)
				}
			}
		}
	})
}
