package agentupdate

import "strconv"

// CompareVersions orders two dotted-numeric agent versions, returning -1, 0, or
// +1. It tolerates an optional leading "v", uneven segment counts (missing
// trailing segments are zero), and non-numeric suffixes (only the leading
// digits of each segment are read, so "1.5.0-rc1" compares as 1.5.0). It never
// errors — the fleet's versions are simple dotted numerics, and a total order
// that degrades gracefully beats a parse error that strands the no-downgrade
// check.
func CompareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := segAt(as, i), segAt(bs, i)
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// IsDowngrade reports whether target is strictly older than current — the
// rule the agent.update handler enforces (ADR-035 §2): an equal or newer
// target is allowed, an older one is refused even with a valid signature.
func IsDowngrade(current, target string) bool {
	return CompareVersions(target, current) < 0
}

func splitVersion(v string) []string {
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		v = v[1:]
	}
	var segs []string
	start := 0
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			segs = append(segs, v[start:i])
			start = i + 1
		}
	}
	return append(segs, v[start:])
}

func segAt(segs []string, i int) int {
	if i >= len(segs) {
		return 0
	}
	return leadingInt(segs[i])
}

// leadingInt parses the leading run of digits in s (0 if none) — so a
// pre-release suffix like "0-rc1" contributes its numeric prefix.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
