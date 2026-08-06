package ctxcost

import "testing"

func TestHuman(t *testing.T) {
	cases := []struct{ in int; want string }{
		{0, "≈0"},
		{7, "≈7"},
		{999, "≈999"},
		{1000, "≈1.0k"},
		{23400, "≈23.4k"},
		{81800, "≈81.8k"},
		{1250000, "≈1.3M"},
	}
	for _, c := range cases {
		if got := Human(c.in); got != c.want {
			t.Errorf("Human(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDash(t *testing.T) {
	if got := Human(-1); got != "-" {
		t.Errorf("Human(-1) = %q, want %q (unmeasured renders a dash, never a number)", got, "-")
	}
}
