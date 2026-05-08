package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/doctor"
)

type fixKind int

const (
	fixInTUI fixKind = iota
	fixClaudeCLI
)

type fixProposal struct {
	summary string
	kind    fixKind
	inTUI   func() error // non-nil only for fixInTUI
	cliArgs []string     // args for exec.Command("claude", cliArgs...)
}

type doctorFixDoneMsg struct{ err error }

// doctorView runs structural lint checks on CLAUDE.md and MEMORY.md and
// displays the results. Press 'r' to re-run lint; 'l' to run an LLM review;
// 'j/k' to navigate issues; 'f' to fix the selected issue.
type doctorView struct {
	st     *state
	groups []docGroup
	w, h   int
	top    int
	loaded bool // false until first lint run; set false again on 'r'

	allIssues  []doctor.Issue // flattened after runLint, for cursor indexing
	cursor     int
	pendingFix *fixProposal
	lastFix    *fixProposal // last attempted fix, for retry hint
	lastFixErr error        // result of last fix run

	// LLM review state
	llmRunning bool
	llmResults []llmReviewResult
	showLLM    bool

	// claudeOnPath is cached at view init: when false, LLM review and fix-via-CLI
	// are unavailable and the keys 'l' / 'f' surface a friendly hint instead.
	claudeOnPath bool

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
	v := &doctorView{st: st}
	if _, err := exec.LookPath("claude"); err == nil {
		v.claudeOnPath = true
	}
	return v
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

	// Rebuild flat issue list for cursor navigation.
	v.allIssues = v.allIssues[:0]
	for _, g := range v.groups {
		v.allIssues = append(v.allIssues, g.issues...)
	}
	v.cursor = 0
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
	if done, ok := msg.(doctorFixDoneMsg); ok {
		v.lastFixErr = done.err
		if done.err != nil {
			v.flash = styleErr.Render("fix failed: " + enrichExitStatus(done.err.Error()))
		} else {
			v.flash = styleOK.Render("fix applied")
			v.lastFix = nil
		}
		v.loaded = false // trigger re-lint on next render
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	// Confirm dialog intercepts all keys.
	if v.pendingFix != nil {
		switch key.String() {
		case "y":
			return v.executeFix()
		case "n", "esc":
			v.pendingFix = nil
		}
		return nil
	}

	pageH := v.pageHeight()
	numIssues := len(v.allIssues)

	switch key.String() {
	case "r":
		v.loaded = false // render() will re-run lint on the next frame
		v.showLLM = false
		v.llmResults = nil
	case "l":
		if v.llmRunning {
			return nil
		}
		if !v.canRunLLM() {
			v.flash = styleWarn.Render("LLM review unavailable — install the claude CLI or set ANTHROPIC_API_KEY/OPENAI_API_KEY")
			return nil
		}
		v.llmRunning = true
		v.showLLM = false
		v.flash = styleDim.Render("running LLM review…")
		claudePath := filepath.Join(v.st.project, "CLAUDE.md")
		memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
		memPath := filepath.Join(memDir, "MEMORY.md")
		return func() tea.Msg {
			// Zero-value Provider: doctor.Review auto-selects the best available
			// backend (claude-cli when no API keys are set, anthropic/openai otherwise).
			opts := doctor.ReviewOptions{}
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
	case "f":
		if !v.showLLM && !v.llmRunning && numIssues > 0 {
			proposal, ok := buildFixProposal(v.allIssues[v.cursor], v.st.project)
			if !ok {
				v.flash = styleDim.Render("no automatic fix for " + v.allIssues[v.cursor].Code)
			} else if proposal.kind == fixClaudeCLI && !v.claudeOnPath {
				v.flash = styleWarn.Render("auto-fix unavailable — claude CLI not found in PATH")
			} else {
				v.pendingFix = proposal
			}
		}
	case "j", "down":
		if v.showLLM {
			totalLines := v.totalLines()
			if v.top < totalLines-pageH {
				v.top++
			}
		} else if numIssues > 0 && v.cursor < numIssues-1 {
			v.cursor++
		}
	case "k", "up":
		if v.showLLM {
			if v.top > 0 {
				v.top--
			}
		} else if v.cursor > 0 {
			v.cursor--
		}
	case "g", "home":
		v.top = 0
		v.cursor = 0
	case "G", "end":
		if v.showLLM {
			totalLines := v.totalLines()
			if totalLines > pageH {
				v.top = totalLines - pageH
			}
		} else if numIssues > 0 {
			v.cursor = numIssues - 1
		}
	case "pgdn":
		if v.showLLM {
			totalLines := v.totalLines()
			v.top += pageH
			if v.top > totalLines-pageH {
				v.top = totalLines - pageH
			}
			if v.top < 0 {
				v.top = 0
			}
		} else {
			v.cursor += pageH
			if v.cursor >= numIssues {
				v.cursor = numIssues - 1
			}
			if v.cursor < 0 {
				v.cursor = 0
			}
		}
	case "pgup":
		if v.showLLM {
			v.top -= pageH
			if v.top < 0 {
				v.top = 0
			}
		} else {
			v.cursor -= pageH
			if v.cursor < 0 {
				v.cursor = 0
			}
		}
	}
	return nil
}

func (v *doctorView) executeFix() tea.Cmd {
	p := v.pendingFix
	v.pendingFix = nil
	v.lastFix = p
	v.lastFixErr = nil

	if p.kind == fixInTUI {
		if err := p.inTUI(); err != nil {
			v.lastFixErr = err
			v.flash = styleErr.Render("fix failed: " + err.Error())
		} else {
			v.flash = styleOK.Render("fixed: " + p.summary)
			v.loaded = false
			v.lastFix = nil
		}
		return nil
	}

	// Claude CLI fix.
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		v.lastFixErr = doctor.ErrClaudeCLINotFound
		v.flash = styleErr.Render("claude CLI not found in PATH — install it or run the fix manually")
		return nil
	}
	cmd := exec.Command(cliPath, p.cliArgs...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return doctorFixDoneMsg{err: err}
	})
}

// canRunLLM reports whether at least one provider backend is currently available:
// the claude CLI on PATH, or an Anthropic/OpenAI API key in the environment.
func (v *doctorView) canRunLLM() bool {
	if v.claudeOnPath {
		return true
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	return false
}

// enrichExitStatus rewrites bare "exit status N" messages from tea.ExecProcess
// (which hands the TTY to the subprocess and so loses stderr capture) into a
// hint that points the user to the output that scrolled by above.
func enrichExitStatus(msg string) string {
	re := regexp.MustCompile(`^exit status (\d+)$`)
	if m := re.FindStringSubmatch(strings.TrimSpace(msg)); m != nil {
		return fmt.Sprintf("claude CLI exit %s — see output above", m[1])
	}
	return msg
}

// wrapStyled hard-wraps s on whitespace to fit width-2 columns, returning one
// styled line per wrapped row. Embedded newlines are preserved as separators.
func wrapStyled(s string, width int, style lipgloss.Style) []string {
	if width < 8 {
		width = 80
	}
	max := width - 2
	var out []string
	for _, segment := range strings.Split(s, "\n") {
		if segment == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(segment)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		var line strings.Builder
		for _, w := range words {
			if line.Len() == 0 {
				line.WriteString(w)
				continue
			}
			if line.Len()+1+len(w) > max {
				out = append(out, style.Render(line.String()))
				line.Reset()
				line.WriteString(w)
				continue
			}
			line.WriteByte(' ')
			line.WriteString(w)
		}
		if line.Len() > 0 {
			out = append(out, style.Render(line.String()))
		}
	}
	return out
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
	if !v.claudeOnPath {
		b.WriteString(styleWarn.Render("claude CLI not found in PATH — LLM review and auto-fix unavailable"))
		b.WriteString("\n")
	}
	if total == 0 {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleOK.Render("all clear"))
	} else {
		fmt.Fprintf(&b, "Doctor — ")
		b.WriteString(styleWarn.Render(fmt.Sprintf("%d issue(s)", total)))
	}
	b.WriteString("\n")

	lines, issueLineIndices := v.buildLintLines()

	// Scroll to keep cursor visible.
	pageH := v.pageHeight()
	if v.pendingFix != nil {
		pageH -= 4 // reserve space for confirm banner
		if pageH < 1 {
			pageH = 1
		}
	}
	if len(issueLineIndices) > 0 && v.cursor < len(issueLineIndices) {
		cursorLine := issueLineIndices[v.cursor]
		if cursorLine < v.top {
			v.top = cursorLine
		} else if cursorLine >= v.top+pageH {
			v.top = cursorLine - pageH + 1
		}
	}
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

	if v.pendingFix != nil {
		b.WriteString(styleDim.Render(strings.Repeat("─", 44)))
		b.WriteString("\n")
		b.WriteString("Fix: " + v.pendingFix.summary + "\n")
		if v.pendingFix.kind == fixClaudeCLI {
			preview := "claude " + strings.Join(v.pendingFix.cliArgs, " ")
			if len(preview) > 100 {
				preview = preview[:100] + "…"
			}
			b.WriteString(styleDim.Render(preview) + "\n")
		}
		b.WriteString(styleWarn.Render("Apply? y / n"))
	}

	return b.String()
}

// buildLintLines returns the render lines plus the line-index of each issue (for cursor scroll).
func (v *doctorView) buildLintLines() (lines []string, issueLineIndices []int) {
	issueIdx := 0
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
				cursor := "  "
				if issueIdx == v.cursor {
					cursor = styleOK.Render("▶ ")
				}
				issueLineIndices = append(issueLineIndices, len(lines))
				lines = append(lines, fmt.Sprintf("%s%s [%s] %s — %s",
					cursor,
					icon,
					styleDim.Render(iss.Code),
					styleDim.Render(loc),
					iss.Message,
				))
				issueIdx++
			}
		}
		lines = append(lines, "")
	}
	return lines, issueLineIndices
}

func (v *doctorView) renderLLM() string {
	var b strings.Builder
	b.WriteString("Doctor — LLM review\n")

	var lines []string
	for _, r := range v.llmResults {
		lines = append(lines, styleDim.Render("── "+r.path+" ──"))
		if r.err != nil {
			for _, wrapped := range wrapStyled("error: "+r.err.Error(), v.w-2, styleErr) {
				lines = append(lines, "  "+wrapped)
			}
			if errors.Is(r.err, doctor.ErrClaudeCLINotFound) {
				lines = append(lines, "  "+styleDim.Render("hint: install the claude CLI or set ANTHROPIC_API_KEY/OPENAI_API_KEY"))
			}
			var apiErr *doctor.APIError
			if errors.As(r.err, &apiErr) && apiErr.Status == 401 {
				lines = append(lines, "  "+styleDim.Render("hint: run `claude /login` or rerun with --provider claude-cli"))
			}
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
	suffix := ""
	if !v.claudeOnPath {
		suffix = "  " + styleDim.Render("(claude CLI missing)")
	}
	if v.showLLM {
		return "r: re-run lint  l: LLM review  j/k: scroll  g/G: top/bottom" + suffix
	}
	return "r: re-run lint  l: LLM review  j/k: navigate  f: fix issue  g/G: top/bottom" + suffix
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

// buildFixProposal returns a fix proposal for the given issue, or nil/false if no fix is available.
func buildFixProposal(issue doctor.Issue, projectPath string) (*fixProposal, bool) {
	switch issue.Code {
	case "MEM002":
		// Remove the broken index entry line from MEMORY.md.
		f := issue.File
		ln := issue.Line
		return &fixProposal{
			summary: "Remove broken index entry from MEMORY.md",
			kind:    fixInTUI,
			inTUI: func() error {
				return removeFileLine(f, ln)
			},
		}, true

	case "MEM005":
		// Add missing frontmatter field with a placeholder value.
		field := extractQuotedWord(issue.Message)
		if field == "" {
			return nil, false
		}
		f := issue.File
		return &fixProposal{
			summary: fmt.Sprintf("Add missing frontmatter field %q to %s", field, filepath.Base(f)),
			kind:    fixInTUI,
			inTUI: func() error {
				return addFrontmatterField(f, field)
			},
		}, true

	case "MD003":
		content := readLineContent(issue.File, issue.Line)
		prompt := fmt.Sprintf(
			"In %s at line %d, the line is too long (%s). Shorten it without losing meaning:\n\n%s",
			issue.File, issue.Line, issue.Message, content,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Shorten line %d in %s", issue.Line, filepath.Base(issue.File)),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MD004":
		// Broken local link — extract target from message.
		prompt := fmt.Sprintf(
			"In %s at line %d, there is a broken markdown link (%s). Remove or fix the link.",
			issue.File, issue.Line, issue.Message,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Fix broken link at line %d in %s", issue.Line, filepath.Base(issue.File)),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MD005":
		prompt := fmt.Sprintf(
			"%s is too long (%s). Trim it to under 500 lines while preserving all critical content.",
			issue.File, issue.Message,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Trim %s to under 500 lines", filepath.Base(issue.File)),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MD002":
		claudePath := filepath.Join(projectPath, "CLAUDE.md")
		prompt := fmt.Sprintf(
			"%s is empty. Add a minimal project overview and key development guidelines.",
			claudePath,
		)
		return &fixProposal{
			summary: "Populate empty CLAUDE.md",
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MEM001":
		prompt := fmt.Sprintf(
			"Create a MEMORY.md index file at %s for this project. It should be an empty memory index with just a level-1 heading.",
			issue.File,
		)
		return &fixProposal{
			summary: "Initialise MEMORY.md",
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MEM003":
		content := readLineContent(issue.File, issue.Line)
		prompt := fmt.Sprintf(
			"In MEMORY.md at line %d, shorten this index entry to ≤150 characters without losing the key information:\n\n%s",
			issue.Line, content,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Shorten MEMORY.md entry at line %d", issue.Line),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MEM004":
		prompt := fmt.Sprintf(
			"Fix the frontmatter in memory file %s: %s",
			issue.File, issue.Message,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Fix frontmatter in %s", filepath.Base(issue.File)),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true

	case "MEM006":
		prompt := fmt.Sprintf(
			"Fix the 'type' field in %s — must be one of: user, feedback, project, reference. %s",
			issue.File, issue.Message,
		)
		return &fixProposal{
			summary: fmt.Sprintf("Fix invalid type in %s", filepath.Base(issue.File)),
			kind:    fixClaudeCLI,
			cliArgs: claudeFixArgs(prompt),
		}, true
	}

	return nil, false
}

func claudeFixArgs(prompt string) []string {
	return []string{"--allowedTools", "Edit,Write,Read", "--permission-mode", "acceptEdits", "--print", prompt}
}

func removeFileLine(path string, lineNum int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}
	lines = append(lines[:lineNum-1], lines[lineNum:]...)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

func addFrontmatterField(path, field string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fmt.Errorf("no frontmatter found in %s", path)
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return fmt.Errorf("frontmatter block not closed in %s", path)
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:closeIdx]...)
	newLines = append(newLines, field+": ")
	newLines = append(newLines, lines[closeIdx:]...)
	return os.WriteFile(path, []byte(strings.Join(newLines, "\n")), 0644)
}

func readLineContent(path string, lineNum int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return ""
	}
	return lines[lineNum-1]
}

// extractQuotedWord pulls the first double-quoted word from s (used to parse field names from lint messages).
func extractQuotedWord(s string) string {
	re := regexp.MustCompile(`"([^"]+)"`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}
