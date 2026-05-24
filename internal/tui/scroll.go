package tui

// windowLines returns the slice of lines visible in a pageH-tall viewport,
// auto-scrolling so the line at cursorLine stays visible, plus the adjusted top
// offset. It is the canonical physical-line scroll gate for views whose entries
// span multiple lines (so a row-count clamp would overflow the terminal). Mirror
// of the inline pattern in summary.go/doctor.go.
//
// cursorLine is the index (into lines) of the line that must stay on-screen; pass
// a negative value when there is no cursor to track. top is the caller's current
// scroll offset; the returned newTop should be stored back on the view.
func windowLines(lines []string, cursorLine, top, pageH int) (visible []string, newTop int) {
	if pageH < 1 {
		pageH = 1
	}
	if len(lines) <= pageH {
		return lines, 0
	}
	// A stale cursor (list shrank since it was set) would otherwise scroll past
	// the end; clamp it to the last line so the window still lands on content.
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}
	// Keep the cursor line inside [top, top+pageH).
	if cursorLine >= 0 {
		if cursorLine < top {
			top = cursorLine
		} else if cursorLine >= top+pageH {
			top = cursorLine - pageH + 1
		}
	}
	// Clamp top to a valid range.
	if max := len(lines) - pageH; top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	end := top + pageH
	if end > len(lines) {
		end = len(lines)
	}
	return lines[top:end], top
}

// scrollArrows returns a "▲ "/"▼ " prefix indicating whether content is scrolled
// off above and/or below the visible window of `shown` lines starting at `top`.
func scrollArrows(top, shown, total int) string {
	var s string
	if top > 0 {
		s += "▲ "
	}
	if top+shown < total {
		s += "▼ "
	}
	return s
}
