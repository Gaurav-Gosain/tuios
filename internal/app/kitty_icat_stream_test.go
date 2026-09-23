package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// icatHarness is a pane emulator wired to a passthrough whose host reads
// files, with a function that returns everything the host has been sent.
func icatHarness(t *testing.T) (*vt.Emulator, func() string) {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true, KittyFileTransfer: true,
		TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	const winID = "win"
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	em.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := em.CursorPosition()
		kp.ForwardCommand(cmd, rawData, winID, 0, 0, 80, 24, 0, 0,
			cur.X, cur.Y, em.ScrollbackLen(), em.IsAltScreen(), func([]byte) {})
	})
	info := &WindowPositionInfo{
		Width: 80, Height: 24, ContentWidth: 80, ContentHeight: 24,
		Visible: true, ScreenWidth: 80, ScreenHeight: 24,
	}
	host := func() string {
		kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			return map[string]*WindowPositionInfo{winID: info}
		})
		return string(kp.FlushPending())
	}
	return em, host
}

var hostAPC = regexp.MustCompile(`\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

// transmittedBytes joins and decodes the payload of every transmit the host
// was sent, which is the image the host will draw.
func transmittedBytes(t *testing.T, host string) []byte {
	t.Helper()
	var encoded []byte
	for _, m := range hostAPC.FindAllStringSubmatch(host, -1) {
		if bytes.Contains([]byte(m[1]), []byte("a=p")) || bytes.Contains([]byte(m[1]), []byte("a=d")) {
			continue
		}
		encoded = append(encoded, m[2]...)
	}
	out, err := vt.DecodeKittyPayload(encoded)
	if err != nil {
		t.Fatalf("the host was sent a payload it cannot decode: %v", err)
	}
	return out
}

// icatStream writes an image the way kitten icat streams it: a=T and the
// geometry on a first m=1 chunk, bare continuation chunks, and the whole of it
// unpadded base64, so the final chunk ends short of a group of four.
func icatStream(em *vt.Emulator, img []byte, chunk int) {
	enc := base64.RawStdEncoding.EncodeToString(img)
	first := true
	for len(enc) > 0 {
		n := min(chunk, len(enc))
		part := enc[:n]
		enc = enc[n:]
		more := ""
		if len(enc) > 0 {
			more = ",m=1"
		}
		ctl := "a=T,q=2" + more
		if first {
			ctl = "a=T,q=2,f=100,s=301,v=211,X=4" + more
			first = false
		}
		_, _ = em.Write(fmt.Appendf(nil, "\x1b_G%s;%s\x1b\\", ctl, part))
	}
}

// TestIcatStreamReachesHostIntact is the reported bug: kitten icat's stream
// mode, whose unpadded final chunk the parser refused and then passed on as
// raw base64 text, so the host decoded a PNG with text glued to its end.
func TestIcatStreamReachesHostIntact(t *testing.T) {
	img := make([]byte, 92563) // not a multiple of three
	for i := range img {
		img[i] = byte(i*31 + i/7)
	}
	for _, chunk := range []int{131072, 4096} {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			em, host := icatHarness(t)
			icatStream(em, img, chunk)
			got := transmittedBytes(t, host())
			if !bytes.Equal(got, img) {
				t.Fatalf("the host was sent %d image bytes, want the %d icat sent", len(got), len(img))
			}
		})
	}
}

// TestUndecodableChunkDropsTheTransmission checks a chunk that is not base64
// takes its whole transmission with it, and does not leak into the next one.
func TestUndecodableChunkDropsTheTransmission(t *testing.T) {
	em, host := icatHarness(t)
	_, _ = em.Write([]byte("\x1b_Ga=T,q=2,f=100,s=4,v=4,m=1;QUJD\x1b\\"))
	_, _ = em.Write([]byte("\x1b_Gm=1,q=2;@@@@\x1b\\"))
	_, _ = em.Write([]byte("\x1b_Gm=0,q=2;REVG\x1b\\"))
	if out := host(); hostAPC.MatchString(out) {
		t.Fatalf("a transmission with an undecodable chunk reached the host: %q", out)
	}

	good := []byte("the next image")
	icatStream(em, good, 4096)
	if got := transmittedBytes(t, host()); !bytes.Equal(got, good) {
		t.Fatalf("the next transmission reached the host as %q, want %q", got, good)
	}
}

// TestIcatFileModeForwardsThePath is icat's default mode for a PNG: t=f with
// an unpadded path. A path whose length is not a multiple of three used to
// decode to nothing, and the command was dropped without a trace.
func TestIcatFileModeForwardsThePath(t *testing.T) {
	for _, name := range []string{"a.png", "ab.png", "abc.png"} {
		t.Run(name, func(t *testing.T) {
			em, host := icatHarness(t)
			path := t.TempDir() + "/" + name
			if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _ = em.Write(fmt.Appendf(nil, "\x1b_Ga=T,q=2,f=100,t=f,s=200,v=120;%s\x1b\\",
				base64.RawStdEncoding.EncodeToString([]byte(path))))
			out := host()
			want := "t=f"
			var forwarded string
			for _, m := range hostAPC.FindAllStringSubmatch(out, -1) {
				if bytes.Contains([]byte(m[1]), []byte(want)) {
					dec, err := vt.DecodeKittyPayload([]byte(m[2]))
					if err != nil {
						t.Fatalf("forwarded path does not decode: %v", err)
					}
					forwarded = string(dec)
				}
			}
			if forwarded != path {
				t.Fatalf("host was handed path %q, want %q\n%q", forwarded, path, out)
			}
		})
	}
}
