package review

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestGitHeadReadsOnlyItsLimit holds GitHead to a memory budget. Quick look's
// details view shows the first 256 KiB of a file's diff, and reading git's
// whole diff to cut it there made a 500 MB diff read and allocate about
// 1.5 GB. The budgets are in bytes, which do not depend on the machine's
// speed.
func TestGitHeadReadsOnlyItsLimit(t *testing.T) {
	repo := testutil.GitRepo(t)
	write(t, repo, "big", "")
	write(t, repo, "small", "")
	testutil.Git(t, repo, "add", "big", "small")
	testutil.Git(t, repo, "commit", "-q", "-m", "empty files")
	write(t, repo, "big", strings.Repeat("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n", 8<<20/64))
	write(t, repo, "small", "one\n")
	ctx := t.Context()

	t.Run("a long diff stops at the limit", func(t *testing.T) {
		const limit = 256 << 10
		// The whole diff is read before the measured window.
		want, err := Git(ctx, repo, nil, "diff", "HEAD", "--", "big")
		if err != nil || len(want) < 8<<20 {
			t.Fatalf("git diff gave %d bytes (%v), want about 8 MB", len(want), err)
		}
		allocated := func() uint64 {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			return ms.TotalAlloc
		}
		// rchar counts every byte this process read, from git's pipe too.
		readBytes := func() (uint64, bool) {
			b, err := os.ReadFile("/proc/self/io")
			if err != nil {
				return 0, false
			}
			for line := range strings.Lines(string(b)) {
				if v, ok := strings.CutPrefix(line, "rchar:"); ok {
					n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
					return n, err == nil
				}
			}
			return 0, false
		}
		readBefore, haveIO := readBytes()
		before := allocated()
		got, err := GitHead(ctx, repo, nil, limit, "diff", "HEAD", "--", "big")
		used := allocated() - before
		readAfter, haveAfter := readBytes()
		if err != nil {
			t.Fatalf("GitHead failed on a diff past its limit: %v", err)
		}
		if len(got) != limit || got != want[:limit] {
			t.Fatalf("GitHead gave %d bytes, want the first %d bytes of the diff", len(got), limit)
		}
		const budget = 4 << 20
		if used > budget {
			t.Errorf("GitHead allocated %.1f MB to keep 256 KiB, the budget is %d MB", float64(used)/(1<<20), budget>>20)
		}
		if haveIO && haveAfter && readAfter >= readBefore && readAfter-readBefore > budget {
			t.Errorf("GitHead read %.1f MB to keep 256 KiB, the budget is %d MB", float64(readAfter-readBefore)/(1<<20), budget>>20)
		}
	})

	t.Run("a short diff is whole up to the limit", func(t *testing.T) {
		want, err := Git(ctx, repo, nil, "diff", "HEAD", "--", "small")
		if err != nil || !strings.Contains(want, "+one\n") {
			t.Fatalf("git diff gave %q (%v), want the added line", want, err)
		}
		for _, c := range []struct {
			limit int
			want  string
		}{
			{0, want},
			{256 << 10, want},
			{len(want) + 1, want},
			{len(want), want},
			{len(want) - 1, want[:len(want)-1]},
		} {
			got, err := GitHead(ctx, repo, nil, c.limit, "diff", "HEAD", "--", "small")
			if err != nil || got != c.want {
				t.Errorf("GitHead with limit %d of a %d byte diff gave %q (%v), want %q", c.limit, len(want), got, err, c.want)
			}
		}
	})
}
