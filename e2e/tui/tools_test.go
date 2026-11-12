package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// requireToolsEnv is set by the CI workflow. With it set, a tool a test needs
// that is not installed fails the test, because a skip on CI is a test that
// never ran and nobody noticed. Without it, a missing tool skips the test, so
// a developer machine without fish still runs the rest of the suite.
const requireToolsEnv = "TUIOS_E2E_REQUIRE_TOOLS"

// installedTool reports whether tool is on PATH. lua stands for any Lua 5.1
// interpreter: luajit, lua, or the lua5.1 the package installs.
func installedTool(tool string) (path string, ok bool) {
	names := []string{tool}
	if tool == "lua" {
		names = []string{"luajit", "lua", "lua5.1"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

// needTool returns the path of tool, or skips the test when it is not
// installed, or fails it when CI says the tool must be there. A test that
// calls it must be named in tools.txt, which the workflow reads to install the
// tool and which TestToolsListMatchesTheTests keeps whole.
func needTool(t *testing.T, tool string) string {
	t.Helper()
	path, ok := installedTool(tool)
	if ok {
		return path
	}
	if os.Getenv(requireToolsEnv) != "" {
		t.Fatalf("%s is not installed, and %s is set: add it to the packages the workflow installs", tool, requireToolsEnv)
	}
	t.Skipf("%s is not installed", tool)
	return ""
}

// requireOnCI fails the test on CI when any of tools is missing, and does
// nothing elsewhere. A test that skips one round of a table for a missing tool
// calls it first, so that on CI the round cannot be skipped. The names are
// string literals: the source check reads them.
func requireOnCI(t *testing.T, tools ...string) {
	t.Helper()
	if os.Getenv(requireToolsEnv) == "" {
		return
	}
	var missing []string
	for _, tool := range tools {
		if _, ok := installedTool(tool); !ok {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s not installed, and %s is set: add them to the packages the workflow installs", strings.Join(missing, ", "), requireToolsEnv)
	}
}
