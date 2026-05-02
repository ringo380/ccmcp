package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

// doctorView runs structural lint checks on CLAUDE.md and MEMORY.md and
// displays the results. Press 'r' to re-run lint; 'l' to run an LLM review.
type doctorView struct {
	st     *state
	groups []docGroup
	w, h   int
	top    int
	loaded bool // false until first lint run; set false again on 'r'

	// LLM review state
	llmRunning bool
	llmResults []llmReviewResult
	showLLM    bool

	flash string
}

type docGroup struct {
	label  string
	issues []doctor.Issue
}

type llmReviewResult struct {
	path    string
	content string
	err     error
}

type doctorLLMResultMsg struct {
	results []llmReviewResult
}

func newDoctorView(st *state) *doctorView {
	return &doctorView{st: st}
}

func (v *doctorView) runLint() {
	v.groups = v.groups[:0]

	// Project CLAUDE.md
	claudePath := filepath.Join(v.st.project, "CLAUDE.md")
	v.groups = append(v.groups, docGroup{
		label:  "CLAUDE.md (project)",
		issues: doctor.LintClaudeMD(claudePath),
	})

	// MEMORY.md for this project
	memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
	v.groups = append(v.groups, docGroup{
		label:  "MEMORY.md",
		issues: doctor.LintMemoryIndex(memDir),
	})

	v.loaded = true
	v.top = 0
}

// tuiMemoryPath derives the memory directory path using the same slug logic as cmd/doctor.go.
func tuiMemoryPath(claudeConfigDir, projectPath string) string {
	slug := strings.ReplaceAll(projectPath, "/", "-")
	return filepath.Join(claudeConfigDir, "projects", slug, "memory")
}

func (v *doctorView) update(msg tea.Msg) tea.Cmd {
	if result, ok := msg.(doctorLLMResultMsg); ok {
		v.llmRunning = false
		v.llmResults = result.results
		v.showLLM = true
		v.top = 0
		v.flash = ""
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	totalLines := v.totalLines()
	pageH := v.pageHeight()
	switch key.String() {
	case "r":
		v.loaded = false // render() will re-run lint on the next frame
		v.showLLM = false
		v.llmResults = nil
	case "l":
		if v.llmRunning {
			return nil
		}
		v.llmRunning = true
		v.showLLM = false
		v.flash = styleDim.Render("running LLM review…")
		claudePath := filepath.Join(v.st.project, "CLAUDE.md")
		memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
		memPath := filepath.Join(memDir, "MEMORY.md")
		return func() tea.Msg {
			opts := doctor.ReviewOptions{Provider: doctor.ProviderAnthropic}
			var results []llmReviewResult
			if content, err := doctor.Review(claudePath, opts); err != nil {
				results = append(results, llmReviewResult{path: claudePath, err: err})
			} else {
				results = append(results, llmReviewResult{path: claudePath, content: content})
			}
			if content, err := doctor.Review(memPath, opts); err != nil {
				results = append(results, llmReviewResult{path: memPath, err: err})
			} else {
				results = append(results, llmReviewResult{path: memPath, content: content})
			}
			return doctorLLMResultMsg{results: results}
		}
	case "j", "down":
		if v.top < totalLines-pageH {
			v.top++
		}
	case "k", "up":
		if v.top > 0 {
			v.top--
		}
	case "g", "home":
		v.top = 0
	case "G", "end":
		if totalLines > pageH {
			v.top = totalLines - pageH
		}
	case "pgdn":
		v.top += pageH
		if v.top > totalLines-pageH {
			v.top = totalLines - pageH
		}
		if v.top < 0 {
			v.top = 0
		}
	case "pgup":
		v.top -= pageH
		if v.top < 0 {
			v.top = 0
		}
	}
	return nil
}

func (v *doctorView) render() string {
	if !v.loaded && !v.llmRunning {
		v.runLint()
	}

	if v.llmRunning {
		return "Doctor — " + styleDim.Render("LLM review in progress…") + "\n" + v.flash
	}

	if v.showLLM {
		return v.renderLLM()
	}

	total := 0
	for _, g := range v.groups {
		total += len(g.issues)
	}

	var b strings.Builder
	if total == 0 {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleOK.Render("all clear"))
	} else {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleWarn.Render(fmt.Sprintf("%d issue(s)", total)))
	}
	b.WriteString("\n")

	// Build all content lines, then apply scroll window.
	var lines []string
	for _, g := range v.groups {
		lines = append(lines, styleDim.Render("── "+g.label+" ──"))
		if len(g.issues) == 0 {
			lines = append(lines, "  "+styleOK.Render("✓")+" no issues")
		} else {
			for _, iss := range g.issues {
				icon := styleDim.Render("·")
				switch iss.Severity {
				case doctor.SeverityError:
					icon = styleErr.Render("✗")
				case doctor.SeverityWarning:
					icon = styleWarn.Render("⚠")
				}
				loc := iss.File
				if iss.Line > 0 {
					loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
				}
				lines = append(lines, fmt.Sprintf("  %s [%s] %s — %s",
					icon,
					styleDim.Render(iss.Code),
					styleDim.Render(loc),
					iss.Message,
				))
			}
		}
		lines = append(lines, "")
	}

	pageH := v.pageHeight()
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + pageH
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[v.top:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

func (v *doctorView) renderLLM() string {
	var b strings.Builder
	b.WriteString("Doctor — LLM review\n")

	var lines []string
	for _, r := range v.llmResults {
		lines = append(lines, styleDim.Render("── "+r.path+" ──"))
		if r.err != nil {
			lines = append(lines, styleErr.Render("  error: "+r.err.Error()))
		} else {
			for _, l := range strings.Split(r.content, "\n") {
				lines = append(lines, "  "+l)
			}
		}
		lines = append(lines, "")
	}

	pageH := v.pageHeight()
	if v.top < 0 {
		v.top = 0
	}
	end := v.top + pageH
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[v.top:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

func (v *doctorView) resize(w, h int) { v.w, v.h = w, h }

func (v *doctorView) helpText() string {
	if v.llmRunning {
		return "LLM review in progress…"
	}
	return "r: re-run lint  l: LLM review  j/k: scroll  g/G: top/bottom"
}

func (v *doctorView) capturingInput() bool { return false }

func (v *doctorView) pageHeight() int {
	h := v.h - 4
	if h < 4 {
		h = 4
	}
	return h
}

func (v *doctorView) totalLines() int {
	if v.showLLM {
		n := 0
		for _, r := range v.llmResults {
			n++ // group label
			if r.err != nil {
				n++ // error line
			} else {
				n += len(strings.Split(r.content, "\n"))
			}
			n++ // blank separator
		}
		return n
	}
	n := 0
	for _, g := range v.groups {
		n++ // group label
		if len(g.issues) == 0 {
			n++ // "no issues" line
		} else {
			n += len(g.issues)
		}
		n++ // blank separator
	}
	return n
}
