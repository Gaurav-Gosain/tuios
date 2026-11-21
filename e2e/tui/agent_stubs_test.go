package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

var (
	agentStubDir    string
	realAgentDirs   = map[string]bool{}
	agentShadows    = map[string]string{}
	agentShadowRoot string
	agentNames      []string
	agentShadowMu   sync.Mutex
)

const agentStubExit = 97

func agentProgramNames() ([]string, error) {
	reg, errs := harness.Load()
	if len(errs) > 0 {
		return nil, fmt.Errorf("load the bundled manifests: %v", errs[0].Err)
	}
	var names []string
	add := func(n string) {
		if n == "" || strings.ContainsAny(n, "/ ") || strings.HasPrefix(n, ".") || slices.Contains(names, n) {
			return
		}
		names = append(names, n)
	}
	for _, id := range reg.IDs() {
		m, cmd, ok := reg.Resolve(id)
		if !ok || m == nil {
			continue
		}
		add(cmd)
		for _, n := range m.Detect.Argv0 {
			add(n)
		}
		for _, n := range m.Detect.Comm {
			add(n)
		}
	}
	slices.Sort(names)
	return names, nil
}

func agentStubScript(name string) string {
	return "#!/bin/sh\n" +
		"log=\"${HOME:-/tmp}/.tuios-e2e-agent-stub.log\"\n" +
		"{ printf '%s' '" + name + "'; for a in \"$@\"; do printf ' %s' \"$a\"; done; echo; } >> \"$log\" 2>/dev/null\n" +
		"case \"$1\" in\n" +
		"--version|-v|version) echo '" + name + " 0.0.0-tuios-e2e-stub'; exit 0 ;;\n" +
		"auth|login|whoami|status) echo '{\"loggedIn\":false}'; exit 1 ;;\n" +
		"esac\n" +
		"echo 'tuios e2e: a test ran the agent CLI " + name + ", and the suite runs no real agent. Put a stand-in first on PATH.' >&2\n" +
		fmt.Sprintf("exit %d\n", agentStubExit)
}

func installAgentStubs() (func(), error) {
	names, err := agentProgramNames()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "tuios-e2e-agents")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	agentStubDir = filepath.Join(root, "stubs")
	if err := os.MkdirAll(agentStubDir, 0o755); err != nil {
		cleanup()
		return nil, err
	}
	agentNames, agentShadowRoot = names, root
	isAgent := func(n string) bool { return slices.Contains(names, n) }
	var stubbed []string

	dirs := []string{agentStubDir}
	for i, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			dirs = append(dirs, dir)
			continue
		}
		if !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return isAgent(e.Name()) }) {
			dirs = append(dirs, dir)
			continue
		}
		realAgentDirs[filepath.Clean(dir)] = true
		for _, e := range entries {
			if n := e.Name(); isAgent(n) && !slices.Contains(stubbed, n) {
				if err := os.WriteFile(filepath.Join(agentStubDir, n), []byte(agentStubScript(n)), 0o755); err != nil { //nolint:gosec // a stub this suite runs
					cleanup()
					return nil, err
				}
				stubbed = append(stubbed, n)
			}
		}
		shadow := filepath.Join(root, fmt.Sprintf("path-%02d", i))
		if err := os.MkdirAll(shadow, 0o755); err != nil {
			cleanup()
			return nil, err
		}
		for _, e := range entries {
			if isAgent(e.Name()) {
				continue
			}
			_ = os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(shadow, e.Name()))
		}
		agentShadows[filepath.Clean(dir)] = shadow
		dirs = append(dirs, shadow)
	}
	path := strings.Join(dirs, string(os.PathListSeparator))
	if err := os.Setenv("PATH", path); err != nil {
		cleanup()
		return nil, err
	}
	for _, n := range names {
		got, err := exec.LookPath(n)
		if err != nil && !slices.Contains(stubbed, n) {
			continue
		}
		if want := filepath.Join(agentStubDir, n); err != nil || got != want {
			cleanup()
			return nil, fmt.Errorf("%s resolves to %q, not its stub %s: a test could reach a real agent CLI", n, got, want)
		}
	}
	return cleanup, nil
}

func agentFreePath(dirs ...string) string {
	agentShadowMu.Lock()
	defer agentShadowMu.Unlock()
	var out []string
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if dir == "." || dir == filepath.Clean(agentStubDir) {
			continue
		}
		if shadow, ok := agentShadows[dir]; ok {
			out = append(out, shadow)
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil || !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return slices.Contains(agentNames, e.Name()) }) {
			out = append(out, dir)
			continue
		}
		shadow := filepath.Join(agentShadowRoot, fmt.Sprintf("free-%02d", len(agentShadows)))
		if err := os.MkdirAll(shadow, 0o755); err != nil {
			panic("e2e: make an agent-free shadow of " + dir + ": " + err.Error())
		}
		for _, e := range entries {
			if !slices.Contains(agentNames, e.Name()) {
				_ = os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(shadow, e.Name()))
			}
		}
		agentShadows[dir], realAgentDirs[dir] = shadow, true
		out = append(out, shadow)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func agentPathGuard(t *testing.T, kv string) {
	t.Helper()
	value, ok := strings.CutPrefix(kv, "PATH=")
	if !ok {
		return
	}
	for _, dir := range filepath.SplitList(value) {
		if realAgentDirs[filepath.Clean(dir)] {
			t.Fatalf("e2e guard: a test set PATH with %s, which holds a real agent CLI. Build the PATH from os.Getenv(\"PATH\"), which has the stubs, or leave the folder out", dir)
		}
	}
}

func agentStubCalls(base string) []string {
	data, err := os.ReadFile(filepath.Join(xdgDir(base, "HOME"), ".tuios-e2e-agent-stub.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
