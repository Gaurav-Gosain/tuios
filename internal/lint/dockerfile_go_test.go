package lint

import (
	"os"
	"regexp"
	"testing"
)

// TestDockerfileGoMatchesToolchain is the Go version lint. The image builds
// tuios with the Go in its first FROM line, and go.mod's toolchain line names
// the Go every other build uses. When they differ, the image ships a binary
// no developer or CI job built. Bump both lines together.
func TestDockerfileGoMatchesToolchain(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	docker, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	d := regexp.MustCompile(`(?m)^FROM golang:(\S+) AS `).FindSubmatch(docker)
	if d == nil {
		t.Fatal("Dockerfile has no FROM golang:VERSION AS line")
	}
	if string(d[1]) != string(m[1]) {
		t.Errorf("Dockerfile builds with Go %s, go.mod toolchain is go%s: change one to match the other", d[1], m[1])
	}
}
