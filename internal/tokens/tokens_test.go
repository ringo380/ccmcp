package tokens

import "testing"

func TestCountIsNonZeroForProseAndZeroForEmpty(t *testing.T) {
	n, err := Count("The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n < 5 || n > 20 {
		t.Fatalf("expected a plausible BPE count for a 9-word sentence, got %d", n)
	}

	z, err := Count("")
	if err != nil {
		t.Fatalf("Count(empty): %v", err)
	}
	if z != 0 {
		t.Fatalf("empty string must be 0 tokens, got %d", z)
	}
}

func TestCountLinesEqualsSumOfParts(t *testing.T) {
	a, _ := Count("- alpha: does the alpha thing\n")
	b, _ := Count("- beta: does the beta thing\n")
	got, err := CountLines([]string{
		"- alpha: does the alpha thing\n",
		"- beta: does the beta thing\n",
	})
	if err != nil {
		t.Fatalf("CountLines: %v", err)
	}
	if got != a+b {
		t.Fatalf("CountLines = %d, want %d (a=%d b=%d)", got, a+b, a, b)
	}
}
