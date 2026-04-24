package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ringo380/ccmcp/internal/doctor"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnostic checks for Claude Code configuration files",
}

var (
	doctorLLMReview  bool
	doctorProvider   string
	doctorModel      string
	doctorAPIKey     string
	doctorMemoryDir  string
	doctorUserLevel  bool
)

var doctorMDCmd = &cobra.Command{
	Use:   "md",
	Short: "Lint CLAUDE.md and MEMORY.md files",
	Long: `Runs structural lint checks on CLAUDE.md (project and/or user) and the
project's MEMORY.md index.

Checks performed:
  MD001  file not found
  MD002  file empty
  MD003  line over 200 characters
  MD004  broken relative file link
  MD005  file very large (>500 lines)
  MEM001 MEMORY.md not found
  MEM002 memory index points to missing file
  MEM003 index entry over 150 characters
  MEM004 memory file has invalid/missing frontmatter
  MEM005 memory file missing required frontmatter field
  MEM006 memory file has invalid type value

Use --llm-review to additionally send the file to an LLM for quality feedback
(requires ANTHROPIC_API_KEY or OPENAI_API_KEY in the environment, or --api-key).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolvePaths()
		if err != nil {
			return err
		}
		proj, err := projectPath()
		if err != nil {
			return err
		}

		var targets []lintTarget

		// Project-level CLAUDE.md
		targets = append(targets, lintTarget{
			kind: "CLAUDE.md (project)",
			path: filepath.Join(proj, "CLAUDE.md"),
		})

		// User-level CLAUDE.md
		if doctorUserLevel {
			targets = append(targets, lintTarget{
				kind: "CLAUDE.md (user)",
				path: filepath.Join(p.Home, ".claude", "CLAUDE.md"),
			})
		}

		// MEMORY.md
		memDir := doctorMemoryDir
		if memDir == "" {
			memDir = projectMemoryPath(p.ClaudeConfigDir, proj)
		}
		targets = append(targets, lintTarget{kind: "MEMORY.md", path: memDir, isMemory: true})

		anyError := false
		for _, t := range targets {
			var issues []doctor.Issue
			if t.isMemory {
				issues = doctor.LintMemoryIndex(t.path)
			} else {
				issues = doctor.LintClaudeMD(t.path)
			}

			if len(issues) == 0 {
				fmt.Printf("✓  %s — no issues\n", t.kind)
				continue
			}

			fmt.Printf("\n%s:\n", t.kind)
			for _, iss := range issues {
				icon := "·"
				switch iss.Severity {
				case doctor.SeverityError:
					icon = "✗"
					anyError = true
				case doctor.SeverityWarning:
					icon = "⚠"
				}
				loc := iss.File
				if iss.Line > 0 {
					loc = fmt.Sprintf("%s:%d", iss.File, iss.Line)
				}
				fmt.Printf("  %s [%s] %s — %s\n", icon, iss.Code, loc, iss.Message)
			}

			// LLM review only for files that exist and are non-empty
			if doctorLLMReview && !t.isMemory {
				if _, err := os.Stat(t.path); err == nil {
					fmt.Printf("\n  [LLM review: %s]\n", t.path)
					opts := doctor.ReviewOptions{
						Provider: doctor.Provider(doctorProvider),
						Model:    doctorModel,
						APIKey:   doctorAPIKey,
					}
					feedback, err := doctor.Review(t.path, opts)
					if err != nil {
						fmt.Printf("  LLM review failed: %v\n", err)
					} else {
						fmt.Println()
						for _, line := range strings.Split(feedback, "\n") {
							fmt.Printf("  %s\n", line)
						}
					}
				}
			}
		}

		// LLM review: also offer for MEMORY.md index if it exists
		if doctorLLMReview {
			memIndex := filepath.Join(memDir, "MEMORY.md")
			if _, err := os.Stat(memIndex); err == nil {
				fmt.Printf("\n  [LLM review: %s]\n", memIndex)
				opts := doctor.ReviewOptions{
					Provider: doctor.Provider(doctorProvider),
					Model:    doctorModel,
					APIKey:   doctorAPIKey,
				}
				feedback, err := doctor.Review(memIndex, opts)
				if err != nil {
					fmt.Printf("  LLM review failed: %v\n", err)
				} else {
					fmt.Println()
					for _, line := range strings.Split(feedback, "\n") {
						fmt.Printf("  %s\n", line)
					}
				}
			}
		}

		if anyError {
			return fmt.Errorf("lint errors found — see above")
		}
		return nil
	},
}

// lintTarget is one file (or memory dir) to check.
type lintTarget struct {
	kind     string
	path     string
	isMemory bool
}

// projectMemoryPath derives the memory directory from the Claude config dir and project path.
// The slug replaces every '/' with '-' (leading slash becomes leading '-').
func projectMemoryPath(claudeConfigDir, projectPath string) string {
	slug := strings.ReplaceAll(projectPath, "/", "-")
	return filepath.Join(claudeConfigDir, "projects", slug, "memory")
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.AddCommand(doctorMDCmd)

	doctorMDCmd.Flags().BoolVar(&doctorLLMReview, "llm-review", false, "send each file to an LLM for quality feedback")
	doctorMDCmd.Flags().StringVar(&doctorProvider, "provider", "anthropic", "LLM provider: anthropic|openai")
	doctorMDCmd.Flags().StringVar(&doctorModel, "model", "", "override model (default: claude-sonnet-4-6 / gpt-4o)")
	doctorMDCmd.Flags().StringVar(&doctorAPIKey, "api-key", "", "API key (defaults to ANTHROPIC_API_KEY or OPENAI_API_KEY env var)")
	doctorMDCmd.Flags().StringVar(&doctorMemoryDir, "memory-dir", "", "explicit path to memory directory (auto-detected by default)")
	doctorMDCmd.Flags().BoolVar(&doctorUserLevel, "user", false, "also lint the user-level ~/.claude/CLAUDE.md")
}
