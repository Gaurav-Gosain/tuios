package terminal

import "testing"

func TestSanitizePaste(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"tab\tline\nret\r", "tab\tline\nret\r"},
		{"a\x1b[201~b", "a[201~b"},
		{"a\x1b[200~b", "a[200~b"},
		{"bell\x07nul\x00del\x7f", "bellnuldel"},
		{"c1\u009b31m\u009cend", "c131mend"},
		{"bad\x9bbyte", "badbyte"},
		{"中文 ok", "中文 ok"},
	} {
		if got := SanitizePaste(tc.in); got != tc.want {
			t.Errorf("SanitizePaste(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
