package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ringo380/ccmcp/internal/config"
)

// settingKind distinguishes how a row is edited.
type settingKind int

const (
	kindToggle settingKind = iota // bool
	kindCycle                     // string choice from `choices`
	kindInfo                      // read-only display (e.g. API-key status)
)

type settingRow struct {
	label   string
	key     string      // config.Key* (empty for kindInfo)
	kind    settingKind
	choices []string    // for kindCycle
	// value + source resolved at render/edit time from state.appcfg.
	value func(c *config.AppConfig) (string, config.Source)
	// info renders a read-only line (kindInfo only).
	info func(st *state) string
}

type settingsView struct {
	st     *state
	w, h   int
	top    int
	cursor int
	rows   []settingRow
	flash  string
}

func newSettingsView(st *state) *settingsView {
	v := &settingsView{st: st}
	v.rows = []settingRow{
		{
			label: "Default LLM model", key: config.KeyClaudeModel, kind: kindCycle,
			choices: []string{"", "claude-haiku-4-5", "claude-sonnet-4-6", "claude-opus-4-8"},
			value: func(c *config.AppConfig) (string, config.Source) {
				m, src := c.ClaudeModel()
				if m == "" {
					return "(version default)", src
				}
				return m, src
			},
		},
		{
			label: "API key", kind: kindInfo,
			info: func(st *state) string { return apiKeyStatus() },
		},
		{
			label: "Update check", key: config.KeyUpdateCheck, kind: kindToggle,
			value: func(c *config.AppConfig) (string, config.Source) {
				on, src := c.UpdateCheckEnabled()
				return onOff(on), src
			},
		},
		{
			label: "Offline discovery", key: config.KeyOfflineDiscovery, kind: kindToggle,
			value: func(c *config.AppConfig) (string, config.Source) {
				on, src := c.OfflineDiscovery()
				return onOff(on), src
			},
		},
		{
			label: "Default scope (new MCPs)", key: config.KeyDefaultScope, kind: kindCycle,
			choices: []string{"user", "project"},
			value:   func(c *config.AppConfig) (string, config.Source) { return c.DefaultScope() },
		},
		{
			label: "Prune: include stash ghosts", key: config.KeyPruneGhosts, kind: kindToggle,
			value: func(c *config.AppConfig) (string, config.Source) {
				on, src := c.PruneIncludeStashGhosts()
				return onOff(on), src
			},
		},
		{
			label: "Confirm before apply", key: config.KeyConfirmApply, kind: kindToggle,
			value: func(c *config.AppConfig) (string, config.Source) {
				on, src := c.ConfirmBeforeApply()
				return onOff(on), src
			},
		},
		{
			label: "Auto-backup on mutation", key: config.KeyAutoBackup, kind: kindToggle,
			value: func(c *config.AppConfig) (string, config.Source) {
				on, src := c.AutoBackupOnMutation()
				return onOff(on), src
			},
		},
	}
	return v
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func hasEnv(name string) bool { return strings.TrimSpace(os.Getenv(name)) != "" }

func apiKeyStatus() string {
	var present []string
	if hasEnv("ANTHROPIC_API_KEY") {
		present = append(present, "Anthropic")
	}
	if hasEnv("OPENAI_API_KEY") {
		present = append(present, "OpenAI")
	}
	if len(present) == 0 {
		return "none detected"
	}
	return strings.Join(present, ", ") + " detected"
}

func (v *settingsView) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "j", "down":
		if v.cursor < len(v.rows)-1 {
			v.cursor++
		}
	case "k", "up":
		if v.cursor > 0 {
			v.cursor--
		}
	case " ", "enter":
		v.edit()
	}
	return nil
}

// edit flips/cycles the selected row, unless an env override owns it.
func (v *settingsView) edit() {
	row := v.rows[v.cursor]
	if row.kind == kindInfo {
		return
	}
	if _, src := row.value(v.st.appcfg); src == config.SrcEnv {
		v.flash = styleDim.Render("set via env var - unset it to edit here")
		return
	}
	switch row.kind {
	case kindToggle:
		cur, _ := row.value(v.st.appcfg)
		v.st.appcfg.SetBool(row.key, cur != "on")
	case kindCycle:
		v.st.appcfg.SetString(row.key, nextChoice(v.st.appcfg, row))
	}
	v.st.dirtyAppConfig = true
}

// nextChoice advances the row's value to the next entry in choices, wrapping.
func nextChoice(c *config.AppConfig, row settingRow) string {
	cur, _ := row.value(c)
	// For the model row the displayed "(version default)" maps back to "".
	if cur == "(version default)" {
		cur = ""
	}
	idx := 0
	for i, ch := range row.choices {
		if ch == cur {
			idx = i
			break
		}
	}
	return row.choices[(idx+1)%len(row.choices)]
}

func (v *settingsView) render() string {
	var b strings.Builder
	b.WriteString(styleDim.Render("ccmcp settings - " + v.st.appcfg.Path))
	b.WriteString("\n\n")
	for i, row := range v.rows {
		cursor := "  "
		if i == v.cursor {
			cursor = "> "
		}
		if row.kind == kindInfo {
			b.WriteString(fmt.Sprintf("%s%-28s %s\n", cursor, row.label, styleDim.Render(row.info(v.st))))
			continue
		}
		val, src := row.value(v.st.appcfg)
		tag := styleDim.Render("[" + string(src) + "]")
		b.WriteString(fmt.Sprintf("%s%-28s %-22s %s\n", cursor, row.label, val, tag))
	}
	return b.String()
}

func (v *settingsView) resize(w, h int)      { v.w, v.h = w, h }
func (v *settingsView) helpText() string     { return "j/k: move  space: edit" }
func (v *settingsView) capturingInput() bool { return false }
