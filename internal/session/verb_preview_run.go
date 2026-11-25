package session

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/previewspec"
)

// Previewers: commands quick look runs for the files they match, from
// [[quick_look.previewer]] in config.toml and from the enabled plugins'
// [[previewer]] entries, in that order. A previewer always runs on the
// machine that has the file, from that machine's own config and plugins: a
// client sends only the path and the size it wants, never a command.
//
// A run has no stdin and no shell. The path is one argument. It runs in the
// file's folder, in a process group of its own, under a timeout, and its
// output is capped: past the cap the group is killed and the output is cut.

const (
	previewRunTextMax = 1 << 20
	// previewRunImageMax is the largest picture quick look can draw. Its
	// base64 still fits in one frame over a link.
	previewRunImageMax  = composer.MaxImageBytes
	previewRunStderrMax = 4 << 10
	// previewRunWaitDelay bounds the wait for the output once the run ended:
	// a child that left the process group may hold it open.
	previewRunWaitDelay = 2 * time.Second
	// previewersFor is how long a list read from the config and the plugins
	// is kept while config.toml does not change, so a newly approved plugin
	// shows within that time.
	previewersFor = 30 * time.Second
)

// Previewer is one previewer of this machine.
type Previewer struct {
	Name string
	previewspec.Spec
	// Root is the plugin folder of a plugin's previewer, where a program
	// with a / in its name is found.
	Root string
}

// PreviewRun is what one run of a previewer gave.
type PreviewRun struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	// Data is the output: text for ansi and text, the picture for image.
	Data []byte `json:"data,omitempty"`
	// Cut says the output passed its cap and the rest was not read.
	Cut  bool `json:"cut,omitempty"`
	Exit int  `json:"exit"`
	// Stderr is the end of what the run wrote to its error output.
	Stderr   string `json:"stderr,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	// Missing says the program is not on this machine.
	Missing bool `json:"missing,omitempty"`
}

var previewerCache struct {
	sync.Mutex
	mod  time.Time
	at   time.Time
	list []Previewer
}

// MachinePreviewers is this machine's previewers. It reads config.toml and
// the plugins again when the file changed, or after previewersFor.
func MachinePreviewers() []Previewer {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil
	}
	var mod time.Time
	if fi, err := os.Stat(path); err == nil {
		mod = fi.ModTime()
	}
	c := &previewerCache
	c.Lock()
	defer c.Unlock()
	if !c.at.IsZero() && mod.Equal(c.mod) && time.Since(c.at) < previewersFor {
		return c.list
	}
	c.mod, c.at, c.list = mod, time.Now(), nil
	if ql, err := config.QuickLookInFile(path); err == nil {
		for _, p := range ql.Previewers {
			if p.Problem() == "" {
				c.list = append(c.list, Previewer{Name: p.Name, Spec: p.Spec})
			}
		}
	}
	if pc, err := config.PluginsInFile(path); err == nil {
		for _, e := range herdrplugin.Discover(herdrplugin.DefaultDirs(path, pc.Dirs), pc.Enabled) {
			if !e.Runnable() {
				continue
			}
			for _, x := range e.Plugin.Previewers {
				c.list = append(c.list, Previewer{Name: e.ID + "/" + x.ID, Spec: x.Spec, Root: e.Plugin.PluginRoot})
			}
		}
	}
	return c.list
}

// PreviewersFor is the previewers of this machine that match the file at
// path, in order. f, when it is open, is sniffed for a MIME type only when a
// pattern asks for one.
func PreviewersFor(path string, dir bool, f *os.File) []Previewer {
	all := MachinePreviewers()
	if len(all) == 0 {
		return nil
	}
	mime := ""
	sniff := func() string {
		if mime == "" && f != nil {
			var head [512]byte
			n, _ := f.ReadAt(head[:], 0)
			mime = http.DetectContentType(head[:n])
		}
		return mime
	}
	var out []Previewer
	for _, p := range all {
		if p.Matches(path, dir, sniff) {
			out = append(out, p)
		}
	}
	return out
}

// previewerNames is the names of list.
func previewerNames(list []Previewer) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Name
	}
	return out
}

// RunPreviewer runs p for the file at path and reads what it prints.
func RunPreviewer(ctx context.Context, p Previewer, path string, dir bool, size previewspec.Size) PreviewRun {
	output := cmp.Or(p.Output, previewspec.OutputANSI)
	run := PreviewRun{Name: p.Name, Output: output}
	argv := previewspec.Expand(p.Command, path, dir, size)
	prog := argv[0]
	if p.Root != "" && strings.ContainsRune(prog, '/') && !filepath.IsAbs(prog) {
		prog = filepath.Join(p.Root, prog)
	} else if found, err := exec.LookPath(prog); err == nil {
		prog = found
	} else {
		run.Missing = true
		return run
	}
	wait, _ := previewspec.Timeout(p.Timeout)
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := childproc.CommandContext(ctx, prog, argv[1:]...)
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = previewRunWaitDelay
	cmd.Dir = filepath.Dir(path)
	if dir {
		cmd.Dir = path
	}
	w, h := strconv.Itoa(size.Width), strconv.Itoa(size.Height)
	// The run gets no TUIOS_ variable of the process that runs it: a
	// previewer cannot call tuios as a pane or as a plugin.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "TUIOS_") })
	cmd.Env = append(env, "COLUMNS="+w, "LINES="+h, "TUIOS_PREVIEW_PATH="+path)
	if output == previewspec.OutputANSI {
		cmd.Env = append(cmd.Env, "CLICOLOR_FORCE=1", "FORCE_COLOR=1")
	}
	limit := previewRunTextMax
	if output == previewspec.OutputImage {
		limit = previewRunImageMax
	}
	out := &capWriter{max: limit, stop: cancel}
	errs := &capWriter{max: previewRunStderrMax, tail: true}
	cmd.Stdout, cmd.Stderr = out, errs
	err := cmd.Run()
	run.Data, run.Cut = out.buf.Bytes(), out.cut
	run.Stderr = strings.TrimSpace(errs.buf.String())
	var exit *exec.ExitError
	switch {
	case out.cut:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		run.TimedOut = true
	case errors.Is(err, exec.ErrWaitDelay):
		// The program ended well, and a child it left held the output.
	case errors.As(err, &exit):
		run.Exit = exit.ExitCode()
	case err != nil:
		run.Exit, run.Stderr = -1, err.Error()
	}
	return run
}

// capWriter keeps the first max bytes, or with tail the last, and calls
// stop once more come.
type capWriter struct {
	buf  bytes.Buffer
	max  int
	cut  bool
	tail bool
	stop func()
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.tail {
		w.buf.Write(p)
		if over := w.buf.Len() - w.max; over > 0 {
			w.buf.Next(over)
		}
		return len(p), nil
	}
	if room := w.max - w.buf.Len(); len(p) > room {
		w.buf.Write(p[:max(room, 0)])
		if !w.cut && w.stop != nil {
			w.cut = true
			w.stop()
		}
		return len(p), nil
	}
	return w.buf.Write(p)
}

// PreviewRunReply is the answer of file-preview-run.
type PreviewRunReply struct {
	Path       string      `json:"path"`
	Previewers []string    `json:"previewers"`
	Run        *PreviewRun `json:"run,omitempty"`
	Size       int64       `json:"size"`
	MTime      int64       `json:"mtime"`
}

// verbFilePreviewRun runs a previewer of this machine for one file here.
func (d *Daemon) verbFilePreviewRun(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path   string `json:"path"`
		Name   string `json:"name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Line   int    `json:"line"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	if verr := d.checkLinkRead(cs, path); verr != nil {
		return nil, verr
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fileError("preview", path, err)
	}
	var f *os.File
	if !fi.IsDir() {
		var verr *verbError
		if f, _, verr, err = d.openLinkRead(cs, path); verr != nil {
			return nil, verr
		} else if err != nil {
			return nil, fileError("preview", path, err)
		}
		defer func() { _ = f.Close() }()
	}
	// The run ends when the caller leaves, as exec does.
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	gone, stopGone := askCancel(cs)
	defer stopGone()
	go func() {
		select {
		case <-gone:
			cancel()
		case <-ctx.Done():
		}
	}()
	list := PreviewersFor(path, fi.IsDir(), f)
	out := PreviewRunReply{Path: path, Previewers: previewerNames(list), Size: fi.Size(), MTime: fi.ModTime().UnixMilli()}
	for _, x := range list {
		if p.Name == "" || x.Name == p.Name {
			run := RunPreviewer(ctx, x, path, fi.IsDir(), previewspec.Size{Width: p.Width, Height: p.Height, Line: p.Line})
			out.Run = &run
			break
		}
	}
	return out, nil
}

func previewRunVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"file-preview-run": {
			scope: scopeDeny,
			// A run starts a program on this machine, as opening a pane does.
			link: needFiles | needOpenBit,
			params: []verbParam{
				{Name: "path", Type: "string", Required: true},
				{Name: "name", Type: "string"},
				{Name: "width", Type: "int", Default: "80"},
				{Name: "height", Type: "int", Default: "24"},
				{Name: "line", Type: "int", Default: "1"},
			},
			returns: []verbParam{
				{Name: "path", Type: "string"},
				{Name: "previewers", Type: "[]string"},
				{Name: "run", Type: "object"},
				{Name: "size", Type: "int"},
				{Name: "mtime", Type: "int"},
			},
			handler: (*Daemon).verbFilePreviewRun,
		},
	}
}
