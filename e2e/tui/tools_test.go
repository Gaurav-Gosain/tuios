package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const requireToolsEnv = "TUIOS_E2E_REQUIRE_TOOLS"

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
