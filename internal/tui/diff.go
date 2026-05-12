package tui

import (
	"fmt"
	"strings"
)

// unifiedDiff returns a unified-diff string comparing oldText and newText line by line,
// with `context` lines of context around each change hunk. Returns "" when inputs are identical.
//
// The output is a plain unified-diff body (no file headers); callers render it with
// per-line styling. Lines are terminated by '\n'. Trailing newlines on the inputs are
// preserved as empty trailing tokens; this is intentional so a final-newline change shows up.
func unifiedDiff(oldText, newText string, context int) string {
	if oldText == newText {
		return ""
	}
	if context < 0 {
		context = 0
	}
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")

	ops := diffOps(a, b)
	if len(ops) == 0 {
		return ""
	}

	// Group ops into hunks separated by runs of "equal" longer than 2*context.
	type hunk struct {
		aStart, bStart int // 1-based line numbers
		lines          []string
	}
	var hunks []hunk
	var cur *hunk
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}

	var aLine, bLine = 1, 1
	pendingCtx := []string{}
	pendingA, pendingB := 0, 0

	for _, op := range ops {
		switch op.kind {
		case opEqual:
			for _, line := range op.lines {
				if cur != nil {
					if len(cur.lines) > 0 && trailingEqualCount(cur.lines) >= 2*context {
						// Enough context already; close hunk.
						// Trim trailing equals beyond `context`.
						cur.lines = trimTrailingEqual(cur.lines, context)
						flush()
						pendingCtx = []string{}
						pendingA, pendingB = aLine, bLine
					} else {
						cur.lines = append(cur.lines, " "+line)
					}
				} else {
					pendingCtx = append(pendingCtx, " "+line)
					if len(pendingCtx) > context {
						pendingCtx = pendingCtx[1:]
						pendingA++
						pendingB++
					}
				}
				aLine++
				bLine++
			}
		case opDel:
			if cur == nil {
				start := aLine - len(pendingCtx)
				if start < 1 {
					start = 1
				}
				bStart := bLine - len(pendingCtx)
				if bStart < 1 {
					bStart = 1
				}
				cur = &hunk{aStart: start, bStart: bStart, lines: append([]string{}, pendingCtx...)}
				pendingCtx = nil
			}
			for _, line := range op.lines {
				cur.lines = append(cur.lines, "-"+line)
				aLine++
			}
		case opAdd:
			if cur == nil {
				start := aLine - len(pendingCtx)
				if start < 1 {
					start = 1
				}
				bStart := bLine - len(pendingCtx)
				if bStart < 1 {
					bStart = 1
				}
				cur = &hunk{aStart: start, bStart: bStart, lines: append([]string{}, pendingCtx...)}
				pendingCtx = nil
			}
			for _, line := range op.lines {
				cur.lines = append(cur.lines, "+"+line)
				bLine++
			}
		}
	}
	if cur != nil {
		cur.lines = trimTrailingEqual(cur.lines, context)
		flush()
	}

	var b1 strings.Builder
	for _, h := range hunks {
		aCount := 0
		bCount := 0
		for _, l := range h.lines {
			switch l[0] {
			case ' ':
				aCount++
				bCount++
			case '-':
				aCount++
			case '+':
				bCount++
			}
		}
		fmt.Fprintf(&b1, "@@ -%d,%d +%d,%d @@\n", h.aStart, aCount, h.bStart, bCount)
		for _, l := range h.lines {
			b1.WriteString(l)
			b1.WriteString("\n")
		}
	}
	return b1.String()
}

func trailingEqualCount(lines []string) int {
	n := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) > 0 && lines[i][0] == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

func trimTrailingEqual(lines []string, keep int) []string {
	n := trailingEqualCount(lines)
	if n <= keep {
		return lines
	}
	return lines[:len(lines)-(n-keep)]
}

type opKind int

const (
	opEqual opKind = iota
	opDel
	opAdd
)

type diffOp struct {
	kind  opKind
	lines []string
}

// diffOps returns a sequence of edit operations from a → b using a basic LCS.
// Sufficient for short markdown files (≤ a few hundred lines). Not Myers-optimized.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// LCS table.
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var ops []diffOp
	push := func(kind opKind, line string) {
		if len(ops) > 0 && ops[len(ops)-1].kind == kind {
			ops[len(ops)-1].lines = append(ops[len(ops)-1].lines, line)
			return
		}
		ops = append(ops, diffOp{kind: kind, lines: []string{line}})
	}

	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			push(opEqual, a[i])
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			push(opDel, a[i])
			i++
		} else {
			push(opAdd, b[j])
			j++
		}
	}
	for ; i < n; i++ {
		push(opDel, a[i])
	}
	for ; j < m; j++ {
		push(opAdd, b[j])
	}
	return ops
}
