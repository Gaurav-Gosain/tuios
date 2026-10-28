// Ported from pele tests/render/cjk-wrap.test.ts at 8aff183be57021b533f319a29341a529be934b08.
// The last case, which renders a flowchart, is covered by testdata/extra in the root package.

package text

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func cjkLines(raw string, max float64) []string {
	var out []string
	for _, line := range LayoutLabel(raw, false, MetricsMeasurer, 16, max, 0).Lines {
		var b strings.Builder
		for _, span := range line {
			b.WriteString(span.Text)
		}
		out = append(out, b.String())
	}
	return out
}

const cjkJA = "在庫引当の結果を基幹システムへ連携し、出荷指示を倉庫管理システムに送信して完了を確認する"

func TestCJKWrap(t *testing.T) {
	t.Run("wraps Japanese text that has no spaces and keeps every character", func(t *testing.T) {
		out := cjkLines(cjkJA, 200)
		if len(out) < 2 || strings.Join(out, "") != cjkJA {
			t.Fatalf("lines %q", out)
		}
		for _, w := range LayoutLabel(cjkJA, false, MetricsMeasurer, 16, 200, 0).Widths {
			if w > 200 {
				t.Fatalf("line width %v > 200", w)
			}
		}
	})
	t.Run("does not start a line with closing punctuation or end one with an opening bracket", func(t *testing.T) {
		src := "「" + cjkJA + "」（確認済み）。"
		badStart := regexp.MustCompile(`^[、。，．）」』】]`)
		badEnd := regexp.MustCompile(`[「『（【]$`)
		for max := 40.0; max <= 240; max += 8 {
			out := cjkLines(src, max)
			if strings.Join(out, "") != src {
				t.Fatalf("max %v: lines %q", max, out)
			}
			for _, line := range out {
				if badStart.MatchString(line) || badEnd.MatchString(line) {
					t.Fatalf("max %v: line %q", max, line)
				}
			}
		}
	})
	t.Run("wraps mixed Latin and Japanese text without splitting Latin words", func(t *testing.T) {
		out := cjkLines("AWS Lambda から呼び出されるバッチ処理の完了通知を送信する", 160)
		joined := strings.Join(strings.Fields(strings.Join(out, "")), "")
		if len(out) < 2 || joined != "AWSLambdaから呼び出されるバッチ処理の完了通知を送信する" || !strings.Contains(strings.Join(out, "|"), "Lambda") {
			t.Fatalf("lines %q", out)
		}
	})
	t.Run("keeps English and Korean wrapping on word boundaries", func(t *testing.T) {
		if out := cjkLines("one two three four five six", 80); !slices.Equal(out, []string{"one two", "three four", "five six"}) {
			t.Fatalf("lines %q", out)
		}
		ko := cjkLines("안녕하세요 반갑습니다 여러분 모두", 90)
		for _, line := range ko {
			if strings.TrimRight(line, " \t\n") != line {
				t.Fatalf("line %q ends in space", line)
			}
		}
		for _, word := range []string{"안녕하세요", "반갑습니다", "여러분", "모두"} {
			if !slices.ContainsFunc(ko, func(l string) bool { return strings.Contains(l, word) }) {
				t.Fatalf("%q split: %q", word, ko)
			}
		}
	})
	t.Run("keeps a short Japanese label on one line and a long unbroken Latin word whole", func(t *testing.T) {
		if out := cjkLines("在庫確認", 200); !slices.Equal(out, []string{"在庫確認"}) {
			t.Fatalf("lines %q", out)
		}
		if out := cjkLines("Supercalifragilisticexpialidocious", 60); !slices.Equal(out, []string{"Supercalifragilisticexpialidocious"}) {
			t.Fatalf("lines %q", out)
		}
	})
}

// FuzzHasCJK holds the rune loop of hasCJK to a Go regexp built from the same
// cjk class. It does not compare cjk with pele: the extra goldens do that.
func FuzzHasCJK(f *testing.F) {
	re := regexp.MustCompile(`[` + cjk + `]`)
	for _, s := range []string{"", "abc", "在", "⺀", "⺀"[:2], "ｰ", "｠", "｡", "豈", "￿", "𠮷"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := hasCJK(s), re.MatchString(s); got != want {
			t.Fatalf("hasCJK(%q) = %v, regexp %v", s, got, want)
		}
	})
}
