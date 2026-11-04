package preview

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// A Finder or Dock launch has a thin PATH, without Homebrew's folders, so a
// tool is looked for in PATH, then in the folders package managers use, then
// in the login shell's PATH, read once.

var (
	toolMu    sync.Mutex
	toolPaths = map[string]string{}
	loginOnce sync.Once
	loginDirs []string
)

// ErrNoTool is the error of a preview that needs a tool this machine lacks.
// Its text names the tool, so the viewer can say which to install.
type ErrNoTool string

func (e ErrNoTool) Error() string { return "This machine has no " + string(e) + "." }

// Tool is the path of a tool, "" when this machine has none. The answer is
// kept, a missing tool too.
func Tool(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	toolMu.Lock()
	p, ok := toolPaths[name]
	toolMu.Unlock()
	if ok {
		return p
	}
	p = findTool(name)
	toolMu.Lock()
	toolPaths[name] = p
	toolMu.Unlock()
	return p
}

func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin", filepath.Join(home, ".nix-profile", "bin"), "/run/current-system/sw/bin"}
	for _, d := range dirs {
		if p := filepath.Join(d, name); isExec(p) {
			return p
		}
	}
	loginOnce.Do(func() { loginDirs = loginPath() })
	for _, d := range loginDirs {
		if p := filepath.Join(d, name); isExec(p) {
			return p
		}
	}
	return ""
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// loginPath is the PATH of the login shell, read with a 3 s cap.
func loginPath() []string {
	sh := os.Getenv("SHELL")
	if sh == "" || runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, sh, "-lc", `printf %s "$PATH"`).Output()
	if err != nil {
		return nil
	}
	return filepath.SplitList(strings.TrimSpace(string(out)))
}

// toolTimeout bounds one run of a tool.
const toolTimeout = 20 * time.Second

// run runs a tool and returns what it wrote, at most limit bytes. A
// cancelled ctx kills it, which is how a newer request stops an older one.
func run(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
	bin := Tool(name)
	if bin == "" {
		return nil, ErrNoTool(name)
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out := &capped{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = &capped{limit: 4096}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return out.buf.Bytes(), err
	}
	if out.over {
		return out.buf.Bytes(), errors.New("the tool's answer is too large")
	}
	return out.buf.Bytes(), nil
}

// capped is a buffer that keeps at most limit bytes and drops the rest.
type capped struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room < len(p) {
		c.over = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}
