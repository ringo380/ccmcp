package tui

import (
	"strings"
	"testing"
)

func TestUnifiedDiffEmpty(t *testing.T) {
	if got := unifiedDiff("a\nb\n", "a\nb\n", 3); got != "" {
		t.Fatalf("expected empty diff for identical inputs, got %q", got)
	}
}

func TestUnifiedDiffRemoveLine(t *testing.T) {
	old := "alpha\nbeta\ngamma\n"
	new1 := "alpha\ngamma\n"
	out := unifiedDiff(old, new1, 1)
	if !strings.Contains(out, "-beta") {
		t.Fatalf("expected -beta in diff, got:\n%s", out)
	}
	if !strings.Contains(out, " alpha") || !strings.Contains(out, " gamma") {
		t.Fatalf("expected context lines, got:\n%s", out)
	}
}

func TestUnifiedDiffAddLine(t *testing.T) {
	old := "alpha\ngamma\n"
	new1 := "alpha\nbeta\ngamma\n"
	out := unifiedDiff(old, new1, 1)
	if !strings.Contains(out, "+beta") {
		t.Fatalf("expected +beta, got:\n%s", out)
	}
}

func TestUnifiedDiffMultipleHunks(t *testing.T) {
	old := strings.Repeat("same\n", 20) + "X\n" + strings.Repeat("same\n", 20) + "Y\n" + strings.Repeat("same\n", 20)
	new1 := strings.Repeat("same\n", 20) + "Xnew\n" + strings.Repeat("same\n", 20) + "Ynew\n" + strings.Repeat("same\n", 20)
	out := unifiedDiff(old, new1, 2)
	if strings.Count(out, "@@") < 2 {
		t.Fatalf("expected ≥2 hunks, got:\n%s", out)
	}
	if !strings.Contains(out, "-X\n") || !strings.Contains(out, "+Xnew\n") {
		t.Fatalf("missing first change:\n%s", out)
	}
	if !strings.Contains(out, "-Y\n") || !strings.Contains(out, "+Ynew\n") {
		t.Fatalf("missing second change:\n%s", out)
	}
}

func TestUnifiedDiffHunkHeaderCounts(t *testing.T) {
	old := "a\nb\nc\n"
	new1 := "a\nB\nc\n"
	out := unifiedDiff(old, new1, 1)
	// Expect "@@ -1,3 +1,3 @@" given 1 context, 1 del, 1 add, 1 context = 3 each.
	if !strings.Contains(out, "@@ -1,3 +1,3 @@") {
		t.Fatalf("unexpected hunk header:\n%s", out)
	}
}
