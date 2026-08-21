package fuzzy

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// benchCorpus builds n plausible executable names: real ones from a typical
// /usr/bin, padded out with generated names in the same shapes (dashed,
// suffixed, versioned) so the matcher meets the character classes it will
// actually see.
func benchCorpus(n int) []string {
	seed := []string{
		"gcc", "g++", "git", "grep", "gzip", "gpg", "gawk", "gdb", "go", "gofmt",
		"ls", "lsblk", "lsof", "lspci", "lsusb", "ln", "less", "ldd", "ldconfig",
		"make", "cmake", "qmake", "makepkg", "man", "mount", "mv", "mkdir",
		"python3", "python3-config", "pip", "perl", "pkg-config", "ps", "pgrep",
		"systemctl", "systemd-analyze", "systemd-run", "ssh", "ssh-keygen", "scp",
		"gnome-calculator", "gnome-characters", "gnome-terminal", "gedit",
		"git-shell", "git-credential-cache", "git-upload-pack", "gnutls-serv",
		"nvim", "vim", "vi", "nano", "code", "helix", "emacs", "micro",
		"docker", "docker-compose", "podman", "kubectl", "helm", "terraform",
		"curl", "wget", "rsync", "tar", "unzip", "zstd", "xz", "bzip2",
		"awk", "sed", "sort", "uniq", "cut", "tr", "tee", "head", "tail",
	}
	prefixes := []string{"lib", "x11", "gnome", "kde", "qt5", "dbus", "net", "sys", "dev", "app"}
	stems := []string{"tool", "helper", "daemon", "monitor", "config", "viewer", "editor", "shell", "probe", "agent"}

	out := make([]string, 0, n)
	out = append(out, seed...)
	rng := rand.New(rand.NewPCG(7, 11))
	for len(out) < n {
		var b strings.Builder
		b.WriteString(prefixes[rng.IntN(len(prefixes))])
		b.WriteByte('-')
		b.WriteString(stems[rng.IntN(len(stems))])
		switch rng.IntN(4) {
		case 0:
			b.WriteString(strconv.Itoa(rng.IntN(90) + 10))
		case 1:
			b.WriteString("-" + stems[rng.IntN(len(stems))])
		case 2:
			b.WriteString("ctl")
		}
		out = append(out, b.String())
	}
	return out[:n]
}

// BenchmarkFilter is the per-keystroke cost: one full sweep over the corpus for
// one query. Every case is a query a user would plausibly be part-way through
// typing, since a one-character pattern and a five-character one take very
// different paths through the matrix.
func BenchmarkFilter(b *testing.B) {
	corpus := benchCorpus(3000)
	for _, pattern := range []string{"g", "gc", "sys", "gnome", "systemctl"} {
		b.Run("q="+pattern, func(b *testing.B) {
			var m Matcher
			at := func(i int) string { return corpus[i] }
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				m.FilterIndex(pattern, len(corpus), at)
			}
		})
	}
}

// BenchmarkFind isolates the single-candidate cost from the sort and the hit
// slice, which is what a caller running its own loop pays.
func BenchmarkFind(b *testing.B) {
	var m Matcher
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Find("gc", "gnome-calculator")
	}
}
