//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellFor(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "my-sh")
	if err := os.Symlink("/bin/sh", existing); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	absent := filepath.Join(dir, "absent-sh")

	tests := []struct {
		name        string
		preferred   string
		envShell    string
		want        string
		wantMissing bool
	}{
		{name: "preferred exists", preferred: existing, envShell: "/bin/zsh", want: existing},
		{name: "preferred missing", preferred: absent, envShell: "/bin/zsh", want: "/bin/zsh", wantMissing: true},
		{name: "no preference", envShell: "/bin/zsh", want: "/bin/zsh"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHELL", tc.envShell)
			got, missing := ShellFor(tc.preferred)
			if got != tc.want || missing != tc.wantMissing {
				t.Errorf("ShellFor(%q) = (%q, %v), want (%q, %v)", tc.preferred, got, missing, tc.want, tc.wantMissing)
			}
		})
	}
}
