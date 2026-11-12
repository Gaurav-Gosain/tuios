package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var shippedGoFlags = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("sh", "../../scripts/goflags.sh").Output()
	return strings.TrimSpace(string(out)), err
})

// goBuild is go build with the GOFLAGS of a shipped build: inlining off in
// the packages scripts/noinline-packages.txt lists. Every build of
// ./cmd/tuios here goes through it, so the suite tests the code the release
// ships. The internal/lint test TestEveryTuiosBuildReadsTheNoinlineList
// holds the e2e files to it.
func goBuild(args ...string) *exec.Cmd {
	flags, err := shippedGoFlags()
	if err != nil {
		// A build that fails and says why, rather than one without the list.
		return exec.Command("sh", "-c", `echo "scripts/goflags.sh: $1" >&2; exit 1`, "sh", err.Error())
	}
	cmd := exec.Command("go", append([]string{"build"}, args...)...)
	cmd.Env = append(os.Environ(), "GOFLAGS="+flags)
	return cmd
}
