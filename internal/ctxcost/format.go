package ctxcost

import (
	"fmt"
	"math"
)

// Unmeasured is the sentinel a caller passes to Human when a source could not
// be measured. It renders as "-" so an unknown is never shown as a number.
const Unmeasured = -1

// Human formats an estimated token count for display. Always "≈"-prefixed: the
// underlying encoder is not Anthropic's, so no figure here is exact.
func Human(n int) string {
	if n < 0 {
		return "-"
	}
	switch {
	case n >= 1_000_000:
		// math.Round forces round-half-away-from-zero. fmt.Sprintf("%.1f") alone
		// uses banker's rounding (round-half-to-even), so 1250000/1e6=1.25 would
		// render as "1.2M" instead of the expected "1.3M". Scale by 10, round, then
		// scale back to get the right behavior.
		v := math.Round(float64(n)/1_000_000*10) / 10
		return fmt.Sprintf("≈%.1fM", v)
	case n >= 1000:
		// Same rounding fix as millions case.
		v := math.Round(float64(n)/1000*10) / 10
		return fmt.Sprintf("≈%.1fk", v)
	default:
		return fmt.Sprintf("≈%d", n)
	}
}

// HumanCost renders the loaded/deferred pair on one line.
func HumanCost(c Cost) string {
	return fmt.Sprintf("loaded %s    deferred %s", Human(c.Loaded), Human(c.Deferred))
}
