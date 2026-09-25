package release

import "testing"

// TestNewer, with the expected answers written out rather than computed by the
// same comparison the code uses.
//
// Negative control: compare tags as strings and the 0.9.0 against 0.10.0 row
// fails, which is the version of this bug that ships silently and then stops
// offering updates for a year.
func TestNewer(t *testing.T) {
	cases := []struct {
		current, candidate string
		newer, comparable  bool
	}{
		{"v0.7.0", "v0.7.1", true, true},
		{"v0.7.0", "v0.7.0", false, true},
		{"v0.7.1", "v0.7.0", false, true},
		{"v0.9.0", "v0.10.0", true, true},
		{"v0.10.0", "v0.9.0", false, true},
		{"v1.0.0", "v0.99.99", false, true},
		{"0.7.0", "v0.7.1", true, true},
		// A prerelease is below the release it leads to, so a user on the final
		// v0.8.0 is not offered v0.8.0-rc1 as an upgrade.
		{"v0.8.0", "v0.8.0-rc1", false, true},
		{"v0.8.0-rc1", "v0.8.0", true, true},
		{"v0.8.0-rc1", "v0.8.0-rc2", true, true},
		{"v0.7.0", "v0.8.0-rc1", true, true},
		// Build metadata is not a version difference.
		{"v0.7.0", "v0.7.0+build9", false, true},
		// A source build has no version to compare, and saying so is what stops
		// it being told it is out of date.
		{"dev", "v0.7.0", false, false},
		{"dev+abc123def456", "v0.7.0", false, false},
		{"v0.7.0", "nightly", false, false},
		{"", "v0.7.0", false, false},
	}
	for _, tc := range cases {
		newer, comparable := Newer(tc.current, tc.candidate)
		if newer != tc.newer || comparable != tc.comparable {
			t.Errorf("Newer(%q, %q) = (%v, %v), want (%v, %v)",
				tc.current, tc.candidate, newer, comparable, tc.newer, tc.comparable)
		}
	}
}

// TestParseVersion fills the components a human leaves off ("v1" and "v1.0"
// are both tags people write) and refuses four components or anything
// non-numeric, rather than reading a prefix and ignoring the rest.
//
// Negative control: require exactly three components and the short rows fail;
// drop the length check or ignore Atoi's error and the refused rows parse, and
// a "dev+sha" build starts being compared against real tags.
func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in                  string
		ok                  bool
		major, minor, patch int
	}{
		{"v1", true, 1, 0, 0},
		{"v1.2", true, 1, 2, 0},
		{"v1.2.3", true, 1, 2, 3},
		{"1.2.3.4", false, 0, 0, 0},
		{"v1.x.3", false, 0, 0, 0},
		{"dev", false, 0, 0, 0},
		{"dev+abc", false, 0, 0, 0},
		{"v-1.0.0", false, 0, 0, 0},
		{"..", false, 0, 0, 0},
		{"v", false, 0, 0, 0},
	} {
		got, ok := ParseVersion(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseVersion(%q) ok = %v, want %v (%+v)", tc.in, ok, tc.ok, got)
			continue
		}
		if ok && (got.Major != tc.major || got.Minor != tc.minor || got.Patch != tc.patch) {
			t.Errorf("ParseVersion(%q) = %+v, want %d.%d.%d", tc.in, got, tc.major, tc.minor, tc.patch)
		}
	}
}
