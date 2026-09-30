package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// fakeClipboard is a clipboard the fake tools read. No test here touches the
// real one: every command goes through run, and run never starts a process.
type fakeClipboard struct {
	types []string
	image []byte
	// ran records every command line, so a test can say which tool was used.
	ran []string
	// hang makes every command wait for its context.
	hang bool
}

func (f *fakeClipboard) env(goos string, vars map[string]string, tools ...string) imageClipboardEnv {
	return imageClipboardEnv{
		getenv:   func(k string) string { return vars[k] },
		lookPath: func(name string) bool { return slices.Contains(tools, name) },
		goos:     goos,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			f.ran = append(f.ran, line)
			if f.hang {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			switch {
			case strings.Contains(line, "--list-types"), strings.Contains(line, "TARGETS"):
				if len(f.types) == 0 {
					return nil, errors.New("exit status 1")
				}
				return []byte(strings.Join(f.types, "\n") + "\n"), nil
			case name == "osascript" && strings.Contains(line, "clipboard info"):
				var parts []string
				for _, t := range f.types {
					switch t {
					case "image/png":
						parts = append(parts, "«class PNGf», 1234")
					case "text/plain":
						parts = append(parts, "«class utf8», 12, string, 12")
					}
				}
				return []byte(strings.Join(parts, ", ")), nil
			case name == "osascript":
				return []byte("«data PNGf" + strings.ToUpper(hexOf(f.image)) + "»\n"), nil
			case name == "powershell" && strings.Contains(line, "ContainsImage"):
				return []byte(strings.Join(f.types, "\r\n")), nil
			case name == "powershell":
				return []byte(base64.StdEncoding.EncodeToString(f.image) + "\r\n"), nil
			default:
				return f.image, nil
			}
		},
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

var fakePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRfake")

var wayland = map[string]string{"XDG_RUNTIME_DIR": "/run/user/1", "WAYLAND_DISPLAY": "wayland-0"}

func TestTheImageToolIsChosenPerPlatform(t *testing.T) {
	f := &fakeClipboard{}
	cases := []struct {
		name  string
		env   imageClipboardEnv
		want  imageClipboardBackend
		found bool
	}{
		{"wayland", f.env("linux", wayland, "wl-paste", "xclip"), imageClipWayland, true},
		{"x11", f.env("linux", map[string]string{"DISPLAY": ":0"}, "xclip"), imageClipX11, true},
		{"x11 with only xsel", f.env("linux", map[string]string{"DISPLAY": ":0"}, "xsel"), 0, false},
		{"wayland without the runtime dir", f.env("linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "wl-paste"), 0, false},
		{"headless linux", f.env("linux", nil, "wl-paste", "xclip"), 0, false},
		{"macos", f.env("darwin", nil, "osascript"), imageClipDarwin, true},
		{"macos over ssh", f.env("darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, "osascript"), 0, false},
		{"windows", f.env("windows", nil, "powershell"), imageClipWindows, true},
	}
	for _, c := range cases {
		got := detectImageClipboard(c.env)
		if (got != nil) != c.found || (got != nil && got.backend != c.want) {
			t.Errorf("%s: detected %+v, want backend %d found %v", c.name, got, c.want, c.found)
		}
	}
}

func TestEachPlatformReadsTheImageBytes(t *testing.T) {
	for _, c := range []struct {
		name string
		goos string
		vars map[string]string
		tool []string
	}{
		{"wayland", "linux", wayland, []string{"wl-paste"}},
		{"x11", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xclip"}},
		{"macos osascript", "darwin", nil, []string{"osascript"}},
		{"macos pngpaste", "darwin", nil, []string{"osascript", "pngpaste"}},
		{"windows", "windows", nil, []string{"powershell"}},
	} {
		f := &fakeClipboard{types: []string{"image/png"}, image: fakePNG}
		clip := detectImageClipboard(f.env(c.goos, c.vars, c.tool...))
		if clip == nil {
			t.Fatalf("%s: no tool", c.name)
		}
		types, err := clip.Types(context.Background())
		if err != nil || bestImageType(types) != "image/png" {
			t.Fatalf("%s: types %v, %v", c.name, types, err)
		}
		got, err := clip.ReadImage(context.Background(), types)
		if err != nil || string(got) != string(fakePNG) {
			t.Errorf("%s: read %q, %v, want the image", c.name, got, err)
		}
	}
}

func TestTheClipboardTypesSayImageOrText(t *testing.T) {
	cases := []struct {
		types []string
		image string
		text  bool
	}{
		{[]string{"image/png"}, "image/png", false},
		{[]string{"TARGETS", "image/png", "text/html"}, "image/png", false},
		{[]string{"image/jpeg", "image/png"}, "image/png", false},
		{[]string{"text/plain;charset=utf-8", "UTF8_STRING"}, "", true},
		{[]string{"image/png", "text/plain"}, "image/png", true},
		{[]string{"image/x-custom"}, "image/x-custom", false},
		{nil, "", false},
	}
	for _, c := range cases {
		if got := bestImageType(c.types); got != c.image {
			t.Errorf("bestImageType(%v) = %q, want %q", c.types, got, c.image)
		}
		if got := clipboardHasText(c.types); got != c.text {
			t.Errorf("clipboardHasText(%v) = %v, want %v", c.types, got, c.text)
		}
	}
	if got := darwinClipTypes("«class PNGf», 1234, «class 8BPS», 99, TIFF picture, 88"); !slices.Equal(got, []string{"image/png", "image/tiff"}) {
		t.Errorf("darwinClipTypes = %v", got)
	}
}

func TestAHungClipboardToolTimesOut(t *testing.T) {
	f := &fakeClipboard{hang: true}
	clip := detectImageClipboard(f.env("linux", wayland, "wl-paste"))
	start := time.Now()
	_, err := clip.Types(context.Background())
	if err == nil {
		t.Fatal("a tool that never answered was taken as an answer")
	}
	if waited := time.Since(start); waited > 3*imageClipTypesTimeout {
		t.Errorf("the type probe waited %v", waited)
	}
}

// imagePasteOS is a client with one pane whose typed input is recorded, a
// fake clipboard and a fake daemon.
func imagePasteOS(t *testing.T, f *fakeClipboard) (*OS, *strings.Builder, *[]map[string]any) {
	t.Helper()
	w, typed := layoutWindow(t, "w1")
	w.Workspace = 1
	m := layoutOS(w)
	m.Mode = TerminalMode
	m.FocusedWindow = 0
	m.SessionName = "work"
	var calls []map[string]any
	env := f.env("linux", wayland, "wl-paste")
	m.SetImagePasteSeams(&env, func(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
		params["verb"] = verb
		calls = append(calls, params)
		return json.RawMessage(`{"path":"/run/user/1/tuios/paste/tuios-paste-1.png","host":"build"}`), nil
	}, nil)
	m.Inbox.nonce = func() string { return "nonce" }
	return m, typed, &calls
}

// driveImagePaste runs a command and feeds every message it yields back through Update,
// the way the program loop does, until nothing is left.
func driveImagePaste(t *testing.T, m *OS, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var seen []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		seen = append(seen, msg)
		switch msg.(type) {
		case imageProbeMsg, ImagePastedMsg:
			_, next := m.Update(msg)
			// A text paste asked for is where an image paste ends. Its
			// command waits on a terminal that is not here.
			if !m.pastePending {
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func TestThePasteKeyPastesAnImageAsAPath(t *testing.T) {
	f := &fakeClipboard{types: []string{"image/png"}, image: fakePNG}
	m, typed, calls := imagePasteOS(t, f)
	driveImagePaste(t, m, m.RequestPaste())

	if len(*calls) != 1 {
		t.Fatalf("the daemon was called %d times, want once", len(*calls))
	}
	c := (*calls)[0]
	data, _ := base64.StdEncoding.DecodeString(c["content"].(string))
	if c["verb"] != "paste-image" || c["window"] != "w1" || c["human_nonce"] != "nonce" || string(data) != string(fakePNG) {
		t.Errorf("the daemon was asked %v", c)
	}
	if got := typed.String(); got != "/run/user/1/tuios/paste/tuios-paste-1.png" {
		t.Errorf("the pane got %q, want the far path", got)
	}
	if msg := lastMessage(m); !strings.Contains(msg, "build") {
		t.Errorf("the message does not name the machine: %q", msg)
	}
}

func TestThePasteKeyStillPastesTextWhenTheClipboardHoldsText(t *testing.T) {
	for _, types := range [][]string{{"text/plain"}, {"image/png", "text/plain"}, nil} {
		f := &fakeClipboard{types: types, image: fakePNG}
		m, typed, calls := imagePasteOS(t, f)
		seen := driveImagePaste(t, m, m.RequestPaste())
		if len(*calls) != 0 || typed.Len() != 0 {
			t.Errorf("types %v: the image was pasted (%d calls, %q typed)", types, len(*calls), typed.String())
		}
		if !m.pastePending {
			t.Errorf("types %v: the paste key did not ask for the text (%d messages)", types, len(seen))
		}
	}
}

func TestThePasteImageActionTakesTheImageBesideText(t *testing.T) {
	f := &fakeClipboard{types: []string{"image/png", "text/plain"}, image: fakePNG}
	m, typed, _ := imagePasteOS(t, f)
	driveImagePaste(t, m, m.RequestImagePaste())
	if typed.Len() == 0 {
		t.Fatal("paste_image did not paste the image")
	}

	f.types = []string{"text/plain"}
	m, typed, calls := imagePasteOS(t, f)
	driveImagePaste(t, m, m.RequestImagePaste())
	if len(*calls) != 0 || typed.Len() != 0 {
		t.Error("paste_image pasted with no image on the clipboard")
	}
	if msg := lastMessage(m); !strings.Contains(msg, "no image") {
		t.Errorf("paste_image with no image says %q", msg)
	}
}

func TestAnEmptyPasteLooksForAnImageAndSaysNothingWithout(t *testing.T) {
	f := &fakeClipboard{types: []string{"image/png"}, image: fakePNG}
	m, typed, _ := imagePasteOS(t, f)
	driveImagePaste(t, m, m.PasteImageOnEmptyPaste())
	if typed.Len() == 0 {
		t.Fatal("an empty paste with an image on the clipboard pasted nothing")
	}

	f.types = []string{"text/plain"}
	m, typed, _ = imagePasteOS(t, f)
	driveImagePaste(t, m, m.PasteImageOnEmptyPaste())
	if typed.Len() != 0 || len(m.Notifications) != 0 {
		t.Errorf("an empty paste with no image typed %q and said %q", typed.String(), lastMessage(m))
	}
}

// The person's clipboard is theirs. A client whose person is elsewhere, and a
// paste that send-keys asked for, never reads it.
func TestAnImagePasteIsRefusedWhereTheClipboardIsNotThePersons(t *testing.T) {
	for name, set := range map[string]func(*OS){
		"browser":   func(m *OS) { m.BrowserClient, m.RemoteClient = true, true },
		"remote":    func(m *OS) { m.RemoteClient = true },
		"send-keys": func(m *OS) { m.ProcessingRemoteKeys = true },
	} {
		f := &fakeClipboard{types: []string{"image/png"}, image: fakePNG}
		m, typed, _ := imagePasteOS(t, f)
		set(m)
		driveImagePaste(t, m, m.RequestImagePaste())
		driveImagePaste(t, m, m.PasteImageOnEmptyPaste())
		if len(f.ran) != 0 || typed.Len() != 0 {
			t.Errorf("%s: the clipboard was read (%v) or pasted (%q)", name, f.ran, typed.String())
		}
	}
}

func TestAnImageOverTheLimitIsNotSent(t *testing.T) {
	big := append(append([]byte{}, fakePNG...), make([]byte, session.PasteImageMaxBytes)...)
	f := &fakeClipboard{types: []string{"image/png"}, image: big}
	m, typed, calls := imagePasteOS(t, f)
	driveImagePaste(t, m, m.RequestImagePaste())
	if len(*calls) != 0 || typed.Len() != 0 {
		t.Error("an image over the limit was sent")
	}
	if msg := lastMessage(m); !strings.Contains(msg, "limit") {
		t.Errorf("the message does not name the limit: %q", msg)
	}
}

func TestAClientWithNoDaemonWritesTheImageItself(t *testing.T) {
	f := &fakeClipboard{types: []string{"image/png"}, image: fakePNG}
	w, typed := layoutWindow(t, "w1")
	w.Workspace = 1
	m := layoutOS(w)
	m.Mode = TerminalMode
	var saved []byte
	env := f.env("linux", wayland, "wl-paste")
	m.SetImagePasteSeams(&env, nil, func(b []byte) (string, error) {
		saved = b
		return "/tmp/tuios-1/paste/tuios-paste-2.png", nil
	})
	driveImagePaste(t, m, m.RequestImagePaste())
	if string(saved) != string(fakePNG) || typed.String() != "/tmp/tuios-1/paste/tuios-paste-2.png" {
		t.Errorf("saved %q, typed %q", saved, typed.String())
	}
}

func TestAPathIsQuotedOnlyWhenAShellWouldSplitIt(t *testing.T) {
	for in, want := range map[string]string{
		"/run/user/1000/tuios/paste/tuios-paste-20261001-1.png": "/run/user/1000/tuios/paste/tuios-paste-20261001-1.png",
		"/tmp/my dir/a.png": "'/tmp/my dir/a.png'",
		"/tmp/it's.png":     `'/tmp/it'\''s.png'`,
	} {
		if got := pastePathText(in); got != want {
			t.Errorf("pastePathText(%q) = %q, want %q", in, got, want)
		}
	}
}
