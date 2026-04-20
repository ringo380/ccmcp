// Package stringslice holds small, allocation-cheap helpers that were previously
// duplicated across cmd/ and internal/tui/. Keeping them in one place avoids drift
// (e.g. two slightly different definitions of "remove from slice").
package stringslice

// Set builds a map[string]bool from a slice for O(1) membership testing.
func Set(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

// Contains reports whether v appears in s.
func Contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// UniqueAppend returns s with v appended if it's not already present.
func UniqueAppend(s []string, v string) []string {
	if Contains(s, v) {
		return s
	}
	return append(s, v)
}

// Remove returns s without any elements equal to v, preserving order.
// The returned slice may alias s; callers that need to keep s intact should copy.
func Remove(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
