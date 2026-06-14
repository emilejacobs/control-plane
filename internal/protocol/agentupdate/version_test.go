package agentupdate

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.4.0", "v1.5.0", -1},
		{"v1.5.0", "v1.4.0", 1},
		{"v1.5.0", "v1.5.0", 0},
		{"1.5.0", "v1.5.0", 0},   // optional leading v on either side
		{"v1.5.0", "1.5.0", 0},   //
		{"0.1.0", "v1.4.0", -1},  // enrollment default vs a real release
		{"v1.10.0", "v1.9.0", 1}, // numeric, not lexical
		{"v2.0", "v1.9.9", 1},    // uneven segment counts
		{"v1.5", "v1.5.0", 0},    // missing trailing segment == 0
		{"v1.5.0-rc1", "v1.5.0", 0}, // leading-digit parse per segment; suffix ignored
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// IsDowngrade is the security predicate the handler uses: target strictly
// older than current.
func TestIsDowngrade(t *testing.T) {
	cases := []struct {
		current, target string
		want            bool
	}{
		{"v1.5.0", "v1.4.0", true},  // older — refuse
		{"v1.5.0", "v1.5.0", false}, // same — allowed (re-install)
		{"v1.5.0", "v1.6.0", false}, // newer — allowed
		{"0.1.0", "v1.4.0", false},  // upgrade from enrollment default
	}
	for _, c := range cases {
		if got := IsDowngrade(c.current, c.target); got != c.want {
			t.Errorf("IsDowngrade(current=%q,target=%q) = %v, want %v", c.current, c.target, got, c.want)
		}
	}
}
