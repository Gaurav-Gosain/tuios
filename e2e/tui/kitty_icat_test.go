package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kitten icat draws nothing inside tuios, reported while recording.
//
// Two bugs, one cause. kitten icat is written in Go and encodes its payloads
// with base64.RawStdEncoding, so nothing it sends is padded, and tuios decoded
// with StdEncoding, which requires padding:
//
//   - In its default mode for a PNG, icat sends the file's path (t=f). A path
//     whose length is not a multiple of three decoded to nothing, and the
//     command was dropped. icat exits 0 and nothing is drawn.
//   - In stream mode (t=d, m=1 chunks) the unpadded final chunk failed to
//     decode and was passed on as raw base64 text inside the image bytes, so
//     the host was handed a corrupt PNG.
//
// Both tests run in the standalone TUI and against a daemon, whose kitty query
// answers are its own and not the standalone passthrough's.

// icatImage is the PNG these tests draw, with dimensions nothing else in tuios
// transmits so its commands can be told apart on the wire.
const (
	icatImageW = 37
	icatImageH = 23
)

// writeIcatPNG writes the test PNG under dir at a path whose length is not a
// multiple of three, so its unpadded base64 needs the padding it lacks.
func writeIcatPNG(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, icatImageW, icatImageH))
	for y := range icatImageH {
		for x := range icatImageW {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 11), 99, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	name := "icat.png"
	for len(filepath.Join(dir, name))%3 == 0 {
		name = "x" + name
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, buf.Bytes()
}

// startGraphicsPane boots tuios against a host that answers like kitty, opens
// one pane and leaves it in terminal mode at a shell prompt.
func startGraphicsPane(t *testing.T, daemon bool) (*tuitest.Terminal, *kittyHost) {
	t.Helper()
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:           host,
		daemonDefault: daemon,
	})
	if daemon {
		killDaemon(t, base)
	}
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo READY", "READY", shellTimeout)
	return term, host
}

// runToExit types a command and waits for the exit status it prints. The
// marker is split in the typed text so the echo of the command cannot match.
func runToExit(t *testing.T, term *tuitest.Terminal, cmd string) {
	t.Helper()
	typeLine(t, term, cmd+`; echo "DONE""-RC=$?"`)
	if err := term.WaitForText("DONE-RC=0", shellTimeout); err != nil {
		t.Fatalf("%s did not finish cleanly: %v\n%s", cmd, err, term.Snapshot())
	}
}

// hostTransmits returns what the host was told about the test image after the
// "icat" mark: the path of each file transmission, and the decoded bytes of
// each direct one, chunks joined.
func hostTransmits(t *testing.T, stream []byte) (paths []string, direct [][]byte) {
	t.Helper()
	if i := bytes.Index(stream, []byte(phaseMark+"icat")); i >= 0 {
		stream = stream[i:]
	}
	var run []byte
	inRun := false
	for _, m := range graphicsRE.FindAllSubmatch(stream, -1) {
		params, payload := string(m[1]), m[2]
		first := strings.Contains(params, fmt.Sprintf("s=%d,v=%d", icatImageW, icatImageH))
		switch {
		case first && strings.Contains(params, "t=f"):
			dec, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(payload), "="))
			if err != nil {
				t.Fatalf("host was handed a path it cannot decode: %q", payload)
			}
			paths = append(paths, string(dec))
			continue
		case first:
			inRun, run = true, nil
		case !inRun || strings.Contains(params, "a="):
			continue
		}
		run = append(run, payload...)
		if !strings.Contains(params, "m=1") {
			dec, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(run), "="))
			if err != nil {
				t.Fatalf("host was handed image data it cannot decode: %v", err)
			}
			direct = append(direct, dec)
			inRun = false
		}
	}
	return paths, direct
}

// TestIcatStyleStreamReachesHost is the fixture half, which needs nothing but
// a shell: printf writes an image the way kitten icat streams one, a first
// m=1 chunk and an unpadded final chunk, and the host must receive the bytes.
func TestIcatStyleStreamReachesHost(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			term, host := startGraphicsPane(t, daemon)
			_, img := writeIcatPNG(t, t.TempDir())
			enc := base64.RawStdEncoding.EncodeToString(img)
			if len(enc)%4 == 0 {
				t.Fatal("the image needs padding, or this tests nothing")
			}
			cut := len(enc) / 2 / 4 * 4
			first := fmt.Sprintf(`\033_Ga=T,q=2,f=100,s=%d,v=%d,m=1;%s\033\\`, icatImageW, icatImageH, enc[:cut])
			last := fmt.Sprintf(`\033_Ga=T,q=2;%s\033\\`, enc[cut:])
			host.mark("icat")
			runToExit(t, term, "printf '"+first+last+"'")
			time.Sleep(time.Second)

			_, direct := hostTransmits(t, host.bytes())
			if len(direct) != 1 {
				t.Fatalf("host received %d transmissions of the image, want 1", len(direct))
			}
			if !bytes.Equal(direct[0], img) {
				t.Fatalf("host received %d bytes, want the %d-byte PNG the guest sent", len(direct[0]), len(img))
			}
		})
	}
}

// TestKittenIcatDrawsInAPane runs the real kitten icat, when it is installed,
// in its default file mode and in stream mode.
func TestKittenIcatDrawsInAPane(t *testing.T) {
	kitten, err := exec.LookPath("kitten")
	if err != nil {
		kitten = "/Applications/kitty.app/Contents/MacOS/kitten"
		if _, statErr := os.Stat(kitten); statErr != nil {
			t.Skip("kitten is not installed")
		}
	}
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			term, host := startGraphicsPane(t, daemon)
			path, img := writeIcatPNG(t, t.TempDir())
			host.mark("icat")

			// Default mode. The host answered that it reads files, so icat
			// sends the path and tuios hands it to the host.
			runToExit(t, term, kitten+" icat --stdin=no "+path)
			// Stream mode. The bytes go through tuios.
			runToExit(t, term, kitten+" icat --stdin=no --transfer-mode=stream "+path)
			time.Sleep(time.Second)

			paths, direct := hostTransmits(t, host.bytes())
			if len(paths) != 1 || paths[0] != path {
				t.Errorf("file mode: host was handed paths %q, want [%q]", paths, path)
			}
			if len(direct) != 1 || !bytes.Equal(direct[0], img) {
				lens := make([]int, len(direct))
				for i, d := range direct {
					lens[i] = len(d)
				}
				t.Errorf("stream mode: host received transmissions of %v bytes, want one of the %d-byte PNG", lens, len(img))
			}
		})
	}
}
