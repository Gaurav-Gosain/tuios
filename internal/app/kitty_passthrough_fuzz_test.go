package app

import (
	"bytes"
	"encoding/base64"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"tuios.dev/vt"
)

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
	hostAPCBody   = regexp.MustCompile(`^[A-Za-z0-9=,\-]*(;[A-Za-z0-9+/=]*)?$`)
	hostCUP       = regexp.MustCompile(`^\x1b\[[0-9]+;[0-9]+H`)
	apcImageID    = regexp.MustCompile(`(?:^|,)i=([0-9]+)`)
	apcFreesImage = regexp.MustCompile(`^a=d,(?:.*,)?d=I(?:,|$)`)
)

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

type kittyStep struct {
	win     int
	body    string
	imageID uint32
	op      string
	shift   int
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
			key := ",i="
			if b1&0x20 != 0 {
				key = ",I="
			}
			ids = key + strconv.Itoa(int(id))
		}
		var body string
		switch b2 % 13 {
		case 0:
			body = "a=T,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ";" + payload
		case 1:
			body = "a=t,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ";" + payload
		case 2:
			body = "a=p" + ids + ",c=" + strconv.Itoa(int(b3%5)) + ",r=" + strconv.Itoa(int(b3>>4%5))
		case 3:
			body = "a=d,d=" + string("aAiIpPcCzZnN"[int(b3)%12]) + ids
		case 4:
			half := len(payload) / 8 * 4
			body = "a=T,f=" + f + ",s=" + strconv.Itoa(w) + ",v=" + strconv.Itoa(h) + ids + ",m=1;" + payload[:half]
		case 5:
			body = "m=0;" + payload[len(payload)/8*4:]
		case 6:
			body = "a=q" + ids + ",f=24,s=1,v=1;AAAA"
		case 7:
			body = "a=p,U=1" + ids + ",c=2,r=2"
		case 8:
			body = "a=f,r=1,x=0,y=0,s=1,v=1,f=24" + ids + ";" + base64.StdEncoding.EncodeToString(px[:3])
		case 9:
			body = "a=T,f=100" + ids + ",q=" + strconv.Itoa(int(b3%3)) + ";" + payload
		case 10:
			steps = append(steps, kittyStep{win: win, op: "close"})
			continue
		case 11:
			steps = append(steps, kittyStep{win: win, op: "clear"})
			continue
		default:
			steps = append(steps, kittyStep{win: win, op: "refresh", shift: int(b3 % 7)})
			continue
		}
		steps = append(steps, kittyStep{win: win, body: body, imageID: id})
	}
	return steps
}

func FuzzKittyPassthrough(f *testing.F) {
	f.Add([]byte{0x03, 0x00, 0x00, 0x10, 0x02, 0x00, 0x01, 0x20})
	f.Add([]byte{0x02, 0x05, 0x04, 0x00, 0x02, 0x05, 0x05, 0x00, 0x02, 0x00, 0x02, 0x11})
	f.Add([]byte{0x03, 0x00, 0x00, 0x00, 0x02, 0x00, 0x03, 0x00, 0x03, 0x00, 0x03, 0x02})
	f.Add([]byte{0x04, 0x10, 0x06, 0x00, 0x05, 0x00, 0x07, 0x00, 0x00, 0x00, 0x08, 0x01})
	f.Add([]byte{0x02, 0x05, 0x00, 0x10, 0x03, 0x05, 0x00, 0x20, 0x02, 0x00, 0x0c, 0x03,
		0x03, 0x00, 0x0b, 0x00, 0x02, 0x00, 0x0a, 0x00, 0x03, 0x00, 0x0c, 0x01})
	f.Add([]byte{0x02, 0x20, 0x00, 0x10, 0x02, 0x20, 0x00, 0x20, 0x02, 0x00, 0x03, 0x00,
		0x02, 0x20, 0x02, 0x00, 0x02, 0x20, 0x03, 0x0b})

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

		owner := map[uint64]string{}
		freeIDs := func() map[uint32]struct{} {
			kp.mu.Lock()
			defer kp.mu.Unlock()
			return maps.Clone(kp.freeLowIDs)
		}
		claim := func(win string, from uint32, freeBefore map[uint32]struct{}) {
			kp.mu.Lock()
			defer kp.mu.Unlock()
			for id := from; id != kp.nextHostID; id++ {
				owner[uint64(id)] = win
			}
			for id := range freeBefore {
				if _, still := kp.freeLowIDs[id]; !still {
					if _, taken := owner[uint64(id)]; !taken {
						owner[uint64(id)] = win
					}
				}
			}
			for _, h := range kp.imageIDMap[win] {
				if _, taken := owner[uint64(h)]; !taken {
					owner[uint64(h)] = win
				}
			}
		}
		geometry := func(shift int) map[string]*WindowPositionInfo {
			out := map[string]*WindowPositionInfo{}
			for i, w := range wins {
				out[w] = &WindowPositionInfo{
					WindowX: 10*i + 40*i + shift, WindowY: 2 + shift,
					ContentOffsetX: 1, ContentOffsetY: 1,
					Width: 30, Height: 10, ContentWidth: 28, ContentHeight: 8,
					Visible: true, ScreenWidth: 100, ScreenHeight: 30, WindowZ: i,
					LayoutX: 0, LayoutY: 0, LayoutW: 100, LayoutH: 30,
				}
			}
			return out
		}
		guestImages := map[string]*vt.KittyGuestImages{}
		for n, st := range steps {
			win := wins[st.win]
			kp.mu.Lock()
			firstNew := kp.nextHostID
			kp.mu.Unlock()
			freeBefore := freeIDs()
			if st.op != "" {
				switch st.op {
				case "close":
					kp.OnWindowClose(win)
					delete(guestImages, win)
				case "clear":
					kp.ClearWindow(win)
				case "refresh":
					geo := geometry(st.shift)
					kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo { return geo })
				}
				out := append(kp.FlushPending(), host.take()...)
				apcs, bad := hostTokens(out)
				if bad != "" {
					t.Fatalf("step %d (%s of %s): host got %s", n, st.op, win[:6], bad)
				}
				deleted := map[uint64]bool{}
				for _, body := range apcs {
					m := apcImageID.FindStringSubmatch(body)
					if m == nil {
						continue
					}
					hostID, _ := strconv.ParseUint(m[1], 10, 32)
					if apcFreesImage.MatchString(body) {
						deleted[hostID] = true
					}
					got := owner[hostID]
					if hostID != 0 && (got == "" || (st.op != "refresh" && got != win)) {
						t.Fatalf("step %d: the %s of %s sent image id %d (owner %q):\n%q",
							n, st.op, win[:6], hostID, got, body)
					}
				}
				for id := range freeIDs() {
					if deleted[uint64(id)] {
						delete(owner, uint64(id))
					}
				}
				continue
			}
			cmd, err := vt.ParseKittyCommand([]byte(st.body))
			if err != nil || cmd == nil {
				t.Fatalf("step %d: the harness built an unparseable command %q", n, st.body)
			}
			if guestImages[win] == nil {
				guestImages[win] = &vt.KittyGuestImages{}
			}
			if !guestImages[win].Handle(cmd, nil) {
				continue
			}
			raw := []byte("\x1b_G" + st.body + "\x1b\\")
			var replies [][]byte
			kp.ForwardCommand(cmd, raw, win, 10*st.win, 2, 30, 10, 1, 1, 0, 0, 0, false,
				func(b []byte) { replies = append(replies, append([]byte(nil), b...)) })

			out := append(kp.FlushPending(), host.take()...)
			claim(win, firstNew, freeBefore)
			apcs, bad := hostTokens(out)
			if bad != "" {
				t.Fatalf("step %d (%s %q): host got %s", n, win[:6], st.body, bad)
			}
			deleted := map[uint64]bool{}
			for _, body := range apcs {
				m := apcImageID.FindStringSubmatch(body)
				if m == nil {
					continue
				}
				hostID, _ := strconv.ParseUint(m[1], 10, 32)
				if hostID == 0 {
					continue
				}
				if got := owner[hostID]; got != win {
					t.Fatalf("step %d: %s sent %q and the host got image id %d, which tuios never allocated to that pane (owner %q):\n%q",
						n, win[:6], st.body, hostID, got, body)
				}
				if apcFreesImage.MatchString(body) {
					deleted[hostID] = true
				}
			}
			for id := range freeIDs() {
				if deleted[uint64(id)] {
					delete(owner, uint64(id))
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
