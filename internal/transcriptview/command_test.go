package transcriptview

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The parser table for CommandLabel. A label is a small shell parser, so a
// wrapper it misreads shows the wrong program in every thread; each row is a
// command a harness writes.
func TestCommandLabel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"go test ./...", "go test ./..."},
		{"bash -lc 'cd /work/project && go test ./...'", "go test ./..."},
		{`bash -c "go build ./cmd/tuios"`, "go build ./cmd/tuios"},
		{"sh -c 'make lint'", "make lint"},
		{"zsh -lc 'npm test'", "npm test"},
		{"/bin/bash -lc 'ls -la'", "ls -la"},
		{"/usr/bin/env bash -c 'echo hi'", "echo hi"},
		{"bash -o pipefail -c 'go test | tee out'", "go test | tee out"},
		{"bash --login -c 'pytest -q'", "pytest -q"},
		{"bash -c 'echo $0' name", "echo $0"},
		{"bash script.sh", "bash script.sh"},
		{"bash -c 'a' | tee log", "bash -c 'a' | tee log"},
		{"env FOO=1 BAR=2 go test", "go test"},
		{"env -i PATH=/bin ls", "ls"},
		{"env -u HOME ls", "ls"},
		{"env", "env"},
		{"GOFLAGS=-count=1 go test ./...", "go test ./..."},
		{"FOO=1", "FOO=1"},
		{"sudo apt install jq", "apt install jq"},
		{"sudo -E -u root make install", "make install"},
		{"sudo -Eu root make install", "make install"},
		{"sudo -v", "sudo -v"},
		{"builtin echo hi", "echo hi"},
		{"exec -a name prog", "prog"},
		{"time go build", "go build"},
		{"time -p go build", "go build"},
		{"command -p ls", "ls"},
		{"command -v go", "command -v go"},
		{"nohup ./server &", "./server &"},
		{"nice -n 10 go test", "go test"},
		{"nice -10 go test", "go test"},
		{"cd src && go vet ./...", "go vet ./..."},
		{"cd src; make", "make"},
		{`cd "my dir" && ls`, "ls"},
		{"cd src || exit 1", "cd src || exit 1"},
		{"go build && go test", "go build && go test"},
		{"go test ./... 2>&1 | tail -20", "go test ./... 2>&1 | tail -20"},
		{"sudo env FOO=1 bash -lc 'cd x && nice -n 5 time make'", "make"},
		{"bash -lc 'bash -lc \"cd x && ls\"'", "ls"},
		{"bash -c 'it'\\''s'", "it's"},
		{`bash -c "say \"hi\""`, `say "hi"`},
		{"bash -lc 'unclosed && x", "unclosed && x"},
		{"powershell -NoProfile -Command \"Get-ChildItem\"", "Get-ChildItem"},
		{"pwsh -NoLogo -ExecutionPolicy Bypass -c 'Get-Process | Select -First 5'", "Get-Process | Select -First 5"},
		{"powershell.exe -NoProfile -Command Get-Date -Format o", "Get-Date -Format o"},
		{"pwsh -File build.ps1", "pwsh -File build.ps1"},
		{"cmd /c dir", "dir"},
		{"printf '\x1b[31mred'", "printf '␛[31mred'"},
		{"bash -lc 'printf \x1b]0;t\x07'", "printf ␛]0;t␇"},
		{"echo a\tb\x7f", "echo a␉b␡"},
		{"  ls  ", "ls"},
		{"bash -lc ''", "bash -lc ''"},
		{"   ", "   "},
		{"\x1b", "␛"},
	} {
		if got := CommandLabel(c.in); got != c.want {
			t.Errorf("CommandLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// FuzzCommandLabel holds the promises the thread and the Inbox draw on: no
// panic, a label for every non-empty command, no raw control byte, and
// never more runes than the command.
func FuzzCommandLabel(f *testing.F) {
	for _, s := range []string{"bash -lc 'cd x && go test ./...'", "sudo -Eu root env A=1 nice -n 1 ls", "pwsh -c \"a\"", "cd \"", "'", "\"\\", "env -u", "\x1b[0m", "cmd /c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := CommandLabel(in)
		if in != "" && got == "" {
			t.Fatalf("CommandLabel(%q) is empty", in)
		}
		for i := 0; i < len(got); i++ {
			if c := got[i]; c < 0x20 || c == 0x7f {
				t.Fatalf("CommandLabel(%q) = %q holds the raw byte %#x", in, got, c)
			}
		}
		if !utf8.ValidString(got) {
			t.Fatalf("CommandLabel(%q) = %q is not UTF-8", in, got)
		}
		if n, m := utf8.RuneCountInString(got), utf8.RuneCountInString(in); n > m {
			t.Fatalf("CommandLabel(%q) = %q has %d runes, more than the %d of the command", in, got, n, m)
		}
		if strings.TrimSpace(in) != "" && strings.TrimSpace(got) == "" {
			t.Fatalf("CommandLabel(%q) = %q is only space", in, got)
		}
	})
}
