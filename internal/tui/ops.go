package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/classify"
	"github.com/ringo380/ccmcp/internal/config"
	"github.com/ringo380/ccmcp/internal/doctor"
	"github.com/ringo380/ccmcp/internal/install"
	"github.com/ringo380/ccmcp/internal/stringslice"
)

type opsAction struct {
	label       string
	description string
	destructive bool // gates the Apply? y/n confirm
	run         func(v *opsView) (string, error)
}

type opsView struct {
	st      *state
	w, h    int
	cursor  int
	actions []opsAction

	pendingConfirm bool   // an Apply? prompt is showing for actions[cursor]
	result         string // last run's output panel
	flash          string
}

func newOpsView(st *state) *opsView {
	v := &opsView{st: st}
	v.actions = []opsAction{
		{
			label:       "Snapshot / backup",
			description: "Timestamped backup of ~/.claude.json",
			destructive: false,
			run: func(v *opsView) (string, error) {
				if err := config.Backup(v.st.paths.ClaudeJSON, v.st.paths.BackupsDir); err != nil {
					return "", err
				}
				return "backed up " + filepath.Base(v.st.paths.ClaudeJSON) + " -> " + v.st.paths.BackupsDir, nil
			},
		},
		{
			label:       "Prune orphans (this project)",
			description: "Remove orphaned disabledMcpServers keys",
			destructive: true,
			run:         func(v *opsView) (string, error) { return v.runPrune() },
		},
		{
			label:       "Plugin cache GC",
			description: "Delete stale plugin cache directories",
			destructive: true,
			run:         func(v *opsView) (string, error) { return v.runCacheGC() },
		},
		{
			label:       "Run health check",
			description: "CLAUDE.md + MEMORY.md lint; show pass/fail summary",
			destructive: false,
			run:         func(v *opsView) (string, error) { return v.runHealthCheck() },
		},
	}
	return v
}

func (v *opsView) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if v.pendingConfirm {
		switch k.String() {
		case "y":
			v.pendingConfirm = false
			v.execute(v.actions[v.cursor])
		case "n", "esc":
			v.pendingConfirm = false
			v.flash = styleDim.Render("cancelled")
		}
		return nil
	}
	switch k.String() {
	case "j", "down":
		if v.cursor < len(v.actions)-1 {
			v.cursor++
		}
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
		}
	case "enter":
		a := v.actions[v.cursor]
		if a.destructive && opsConfirmWanted(v.st) {
			v.pendingConfirm = true
			return nil
		}
		v.execute(a)
	}
	return nil
}

func opsConfirmWanted(st *state) bool {
	on, _ := st.appcfg.ConfirmBeforeApply()
	return on
}

func (v *opsView) execute(a opsAction) {
	out, err := a.run(v)
	if err != nil {
		v.result = styleErr.Render("error: " + err.Error())
		v.flash = styleErr.Render(a.label + " failed")
		return
	}
	v.result = out
	v.flash = styleOK.Render(a.label + " done")
}

func (v *opsView) render() string {
	var b strings.Builder
	for i, a := range v.actions {
		cursor := "  "
		if i == v.cursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("%s%-36s %s\n", cursor, a.label, styleDim.Render(a.description)))
	}
	if v.pendingConfirm {
		b.WriteString("\n")
		b.WriteString(styleWarn.Render(fmt.Sprintf("Apply %q? y/n", v.actions[v.cursor].label)))
	} else if v.result != "" {
		b.WriteString("\n")
		b.WriteString(v.result)
	}
	return b.String()
}

func (v *opsView) resize(w, h int)      { v.w, v.h = w, h }
func (v *opsView) helpText() string     { return "j/k: move  enter: run" }
func (v *opsView) capturingInput() bool { return false }

// runPrune mirrors cmd/prune.go selection logic exactly.
func (v *opsView) runPrune() (string, error) {
	overrides := v.st.cj.ProjectDisabledMcpServers(v.st.project)
	if len(overrides) == 0 {
		return "no per-project overrides to prune", nil
	}

	var stashNames []string
	if v.st.stash != nil {
		stashNames = v.st.stash.Names()
	}
	cls := classify.Classify(
		overrides,
		v.st.cj.UserMCPNames(),
		v.st.cj.ProjectMCPNames(v.st.project),
		v.st.cj.ClaudeAiEverConnected(),
		stashNames,
		v.st.pluginMCPs,
	)

	var toRemove []string
	toRemove = append(toRemove, cls.OrphanPlugin...)
	toRemove = append(toRemove, cls.OrphanStdio...)
	pruneGhosts, _ := v.st.appcfg.PruneIncludeStashGhosts()
	if pruneGhosts {
		toRemove = append(toRemove, cls.StashGhost...)
	}

	if len(toRemove) == 0 {
		return "no orphaned overrides found", nil
	}

	remaining := overrides
	for _, k := range toRemove {
		remaining = stringslice.Remove(remaining, k)
	}
	v.st.cj.SetProjectDisabledMcpServers(v.st.project, remaining)
	v.st.dirtyClaude = true

	return fmt.Sprintf("pruned %d orphan entr%s from %s", len(toRemove), classify.PluralY(len(toRemove)), v.st.project), nil
}

// runCacheGC flushes any pending stale cache dirs accumulated by plugin update operations.
func (v *opsView) runCacheGC() (string, error) {
	dirs := v.st.pendingCacheGC
	if len(dirs) == 0 {
		return "no stale plugin cache dirs to remove", nil
	}
	var removed int
	var errs []string
	for _, d := range dirs {
		if err := install.GCStaleCache(d); err != nil {
			errs = append(errs, err.Error())
		} else {
			removed++
		}
	}
	v.st.pendingCacheGC = nil
	if len(errs) > 0 {
		return fmt.Sprintf("removed %d dir(s); %d error(s): %s", removed, len(errs), strings.Join(errs, "; ")), nil
	}
	return fmt.Sprintf("removed %d stale cache dir(s)", removed), nil
}

// runHealthCheck runs the same lint the doctor sub-view uses and returns a summary line.
func (v *opsView) runHealthCheck() (string, error) {
	var issues []doctor.Issue

	claudePath := filepath.Join(v.st.project, "CLAUDE.md")
	issues = append(issues, doctor.LintClaudeMD(claudePath)...)

	memDir := tuiMemoryPath(v.st.paths.ClaudeConfigDir, v.st.project)
	issues = append(issues, doctor.LintMemoryIndex(memDir)...)

	if len(issues) == 0 {
		return styleOK.Render("health check: ok - 0 issues"), nil
	}

	var errs, warns int
	for _, iss := range issues {
		switch iss.Severity {
		case doctor.SeverityError:
			errs++
		case doctor.SeverityWarning:
			warns++
		}
	}
	total := len(issues)
	msg := fmt.Sprintf("health check: %d issue(s) - %d error(s), %d warning(s)", total, errs, warns)
	if errs > 0 {
		return styleErr.Render(msg), nil
	}
	return styleWarn.Render(msg), nil
}
