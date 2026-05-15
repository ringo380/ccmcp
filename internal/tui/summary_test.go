package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// seedOrphanOverride installs a per-project disabledMcpServers entry that
// classify.Classify will bucket as OrphanStdio (plain name, no source on disk).
func seedOrphanOverride(t *testing.T, st *state, key string) {
	t.Helper()
	st.cj.AddProjectDisabledMcpServer(st.project, key)
}

// TestSummaryCursorLandsOnFixableRow verifies that after switching to the
// Summary tab with one orphan override seeded, the cursor (▶) renders next
// to the fixable row and the help footer mentions the f/l keys.
func TestSummaryCursorLandsOnFixableRow(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-orphan-row")

	m := newModel(st)
	out := drive(m, "9")

	clean := stripANSI(out)
	if !strings.Contains(clean, "1 fixable issue(s)") {
		t.Fatalf("expected fixable count, got:\n%s", clean)
	}
	if !strings.Contains(clean, "▶") {
		t.Fatalf("expected cursor marker, got:\n%s", clean)
	}
	if !strings.Contains(clean, "ghost-orphan-row") {
		t.Fatalf("expected the orphan key in output:\n%s", clean)
	}
	if !strings.Contains(clean, "f: fix") {
		t.Fatalf("expected help footer to mention f, got:\n%s", clean)
	}
}

// TestSummaryFixOrphanOverride drives `9 f y` and asserts the orphan override
// is removed from the in-memory ClaudeJSON and dirtyClaude is set.
func TestSummaryFixOrphanOverride(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-to-prune")

	m := newModel(st)
	// 9 = Summary tab, f = open confirm, y = apply
	drive(m, "9", "f", "y")

	overrides := st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range overrides {
		if k == "ghost-to-prune" {
			t.Fatalf("orphan key still present in overrides: %v", overrides)
		}
	}
	if !st.dirtyClaude {
		t.Fatalf("expected dirtyClaude to be set after in-memory fix")
	}
}

// TestSummaryFixCancelLeavesStateUnchanged: pressing `n` at the confirm gate
// must not modify state.
func TestSummaryFixCancelLeavesStateUnchanged(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-cancel")
	before := append([]string{}, st.cj.ProjectDisabledMcpServers(st.project)...)

	m := newModel(st)
	drive(m, "9", "f", "n")

	after := st.cj.ProjectDisabledMcpServers(st.project)
	if len(before) != len(after) {
		t.Fatalf("cancel mutated overrides: before=%v after=%v", before, after)
	}
	if st.dirtyClaude {
		t.Fatalf("dirtyClaude should not be set on cancel")
	}
}

// TestSummaryFixWithoutFixableRowsNoOps: when there are no actionable issues,
// `f` flashes a friendly message instead of crashing the cursor lookup.
func TestSummaryFixWithoutFixableRowsNoOps(t *testing.T) {
	st, _ := buildState(t)
	m := newModel(st)
	out := drive(m, "9", "f")
	if !strings.Contains(stripANSI(out), "no fixable issues") {
		t.Fatalf("expected friendly flash, got:\n%s", out)
	}
}

// TestSummaryLLMReviewRendersResponse stubs claudeReviewCmd and asserts the
// response text lands in the rendered output.
func TestSummaryLLMReviewRendersResponse(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-llm-target")

	orig := claudeReviewCmd
	t.Cleanup(func() { claudeReviewCmd = orig })
	claudeReviewCmd = func(workdir, prompt string) (string, error) {
		return "Verdict: yes, the orphan override is safe to remove.\nReason: classify bucket is OrphanStdio.", nil
	}

	m := newModel(st)
	m.summary.claudeOnPath = true
	drive(m, "9")

	var im tea.Model = m
	im, cmd := im.Update(key("l"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "LLM review — Orphan stdio override") {
		t.Fatalf("expected review header in output:\n%s", view)
	}
	if !strings.Contains(view, "Verdict: yes") {
		t.Fatalf("expected review content in output:\n%s", view)
	}
}

// TestDoctorFixRunsInTUIWithSpinner asserts that a CLI fix sets fixRunning,
// renders the spinner panel, and the result handler clears the flag.
func TestDoctorFixRunsInTUIWithSpinner(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })

	// Stub returns a tea.Cmd that we can manually invoke to simulate completion.
	var captured *fixProposal
	var capturedOrigin tabID
	execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
		captured = p
		capturedOrigin = origin
		// Simulate claude rewriting the file.
		_ = os.WriteFile(p.target, []byte("# rewritten\n"), 0o644)
		return func() tea.Msg {
			return fixDoneMsg{err: nil, proposal: p, origin: origin}
		}
	}

	m := newModel(st)
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub for spinner test",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.doctor.pendingFix = prop

	drive(m, "0") // Doctor tab

	// Press 'y'. executeFix sets fixRunning=true and returns a tea.Cmd.
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if !m.doctor.fixRunning {
		t.Fatalf("expected fixRunning=true after executeFix")
	}
	if capturedOrigin != tabDoctor {
		t.Fatalf("expected origin=tabDoctor, got %d", capturedOrigin)
	}
	if captured == nil {
		t.Fatalf("execFixCmd was not called")
	}
	// Render reflects in-flight state.
	view := stripANSI(im.View())
	if !strings.Contains(view, "Applying LLM fix to MEMORY.md") {
		t.Fatalf("expected in-flight progress panel, got:\n%s", view)
	}

	// Drive completion message.
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	if m.doctor.fixRunning {
		t.Fatalf("expected fixRunning=false after fixDoneMsg")
	}
	view = stripANSI(im.View())
	if !strings.Contains(view, "Keep? y") {
		t.Fatalf("expected post-review gate, got:\n%s", view)
	}
}

// TestDoctorFixErrorSurfacesOutputInline: when the stub returns an error +
// output, the error flash should include a tail of the captured output —
// no terminal handoff means stderr no longer prints "above" the TUI.
func TestDoctorFixErrorSurfacesOutputInline(t *testing.T) {
	st, _ := buildState(t)
	mem := seedBadMemory(t, st)

	orig := execFixCmd
	t.Cleanup(func() { execFixCmd = orig })
	execFixCmd = func(cmd *exec.Cmd, p *fixProposal, origin tabID) tea.Cmd {
		return func() tea.Msg {
			return fixDoneMsg{
				err:      &exec.ExitError{}, // any non-nil
				proposal: p,
				output:   []byte("Error: missing API key\nclaude: see --help for usage"),
				origin:   origin,
			}
		}
	}

	m := newModel(st)
	m.doctor.claudeOnPath = true
	prop := &fixProposal{
		summary:   "stub failure",
		kind:      fixClaudeCLI,
		target:    mem,
		cliPrompt: "x",
		cliArgs:   []string{"--print", "x"},
	}
	m.doctor.pendingFix = prop

	drive(m, "0")
	var im tea.Model = m
	im, cmd := im.Update(key("y"))
	if cmd != nil {
		msg := cmd()
		im, _ = im.Update(msg)
	}
	view := stripANSI(im.View())
	if !strings.Contains(view, "fix failed") {
		t.Fatalf("expected fail flash, got:\n%s", view)
	}
	if !strings.Contains(view, "missing API key") {
		t.Fatalf("expected captured stderr tail in flash, got:\n%s", view)
	}
}

// TestSummaryCursorClampedAfterFix asserts that after an in-memory fix
// shrinks the fixable set, the cursor + v.top reset so subsequent key
// handlers can't index fixable[v.cursor] out of bounds (regression guard
// for the gap between executeFix() and render()'s clamp).
func TestSummaryCursorClampedAfterFix(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-clamp-a")
	seedOrphanOverride(t, st, "ghost-clamp-b")

	m := newModel(st)
	// Switch to Summary and move the cursor to the LAST fixable row.
	drive(m, "9", "j")
	if got := m.summary.cursor; got != 1 {
		t.Fatalf("expected cursor=1 after one j, got %d", got)
	}

	// Apply the fix at cursor=1. After apply, the row disappears.
	drive(m, "f", "y")
	if m.summary.cursor != 0 {
		t.Fatalf("expected cursor reset to 0 after fix, got %d", m.summary.cursor)
	}
	if m.summary.top != 0 {
		t.Fatalf("expected v.top reset to 0 after fix, got %d", m.summary.top)
	}

	// Pressing f again on the remaining fixable row must succeed.
	out := drive(m, "f")
	if !strings.Contains(stripANSI(out), "Apply? y") {
		t.Fatalf("expected confirm panel for second fix, got:\n%s", out)
	}
}

// TestSummaryDownAtEndOfListIsNoop asserts that pressing j past the last
// fixable row does not increment v.top (state-drift regression guard).
func TestSummaryDownAtEndOfListIsNoop(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-only-row")

	m := newModel(st)
	drive(m, "9")
	topBefore := m.summary.top
	drive(m, "j", "j", "j")
	if m.summary.top != topBefore {
		t.Fatalf("v.top drifted on j-past-end: before=%d after=%d", topBefore, m.summary.top)
	}
}

// TestSummaryStashGhostFix asserts a stash ghost can be removed via cursor+f.
func TestSummaryStashGhostFix(t *testing.T) {
	st, _ := buildState(t)
	// Put a stash entry that doesn't exist anywhere else.
	st.stash.Put("ghost-stash-only", map[string]any{"command": "echo"})
	// And add it as an override so it becomes a StashGhost bucket member.
	st.cj.AddProjectDisabledMcpServer(st.project, "ghost-stash-only")

	m := newModel(st)
	out := drive(m, "9")
	if !strings.Contains(stripANSI(out), "stash ghost") {
		t.Fatalf("expected stash-ghost row, got:\n%s", out)
	}

	// Apply the fix.
	drive(m, "9", "f", "y")

	overrides := st.cj.ProjectDisabledMcpServers(st.project)
	for _, k := range overrides {
		if k == "ghost-stash-only" {
			t.Fatalf("stash ghost override not removed: %v", overrides)
		}
	}
}

// TestSummaryAssetLintRowsAppear seeds an oversize skill description and verifies
// the asset-lint row shows up in the Summary tab as a fixable item.
func TestSummaryAssetLintRowsAppear(t *testing.T) {
	st, p := buildState(t)
	// Write a skill with a description over the 1536-char display limit.
	skillDir := p.ClaudeConfigDir + "/skills/oversize-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("a", 1700)
	body := "---\nname: oversize-skill\ndescription: " + long + "\n---\n"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(st)
	out := drive(m, "9")
	clean := stripANSI(out)
	if !strings.Contains(clean, "SKILL003") {
		t.Fatalf("expected SKILL003 row in Summary; got:\n%s", clean)
	}
	if !strings.Contains(clean, "asset-lint findings") {
		t.Fatalf("expected asset-lint header; got:\n%s", clean)
	}
}

// TestBulkFixCategoryGating verifies that bulkFixCategory accepts the expected
// categories and rejects in-memory ones.
func TestBulkFixCategoryGating(t *testing.T) {
	bulkable := []summaryCat{
		catUserDupPlugin, catStaleMcpjson, catDuplicateLoad, catPluginInstalledNotEnabled,
		catSlashConflict, catSkillNameInvalid, catSkillNameTooLong, catSkillDescTooLong,
		catAgentDescTooLong, catCommandDescTooLong,
	}
	for _, c := range bulkable {
		if !bulkFixCategory(c) {
			t.Errorf("bulkFixCategory(%v) = false; expected true", c)
		}
	}
	notBulkable := []summaryCat{catOrphanPlugin, catOrphanStdio, catStashGhost, catPluginEnabledNotInstalled, catNone}
	for _, c := range notBulkable {
		if bulkFixCategory(c) {
			t.Errorf("bulkFixCategory(%v) = true; expected false", c)
		}
	}
}

// TestBuildBulkFixProposalPermissions verifies the permission profile selection:
// rename categories get Bash; description-rewrite categories don't.
func TestBuildBulkFixProposalPermissions(t *testing.T) {
	st, _ := buildState(t)
	cases := []struct {
		cat       summaryCat
		wantBash  bool
	}{
		{catSkillNameInvalid, true},
		{catSkillNameTooLong, true},
		{catSkillDescTooLong, false},
		{catAgentDescTooLong, false},
		{catCommandDescTooLong, false},
	}
	for _, c := range cases {
		row := summaryRow{cat: c.cat, key: "/tmp/x.md"}
		all := []summaryRow{row}
		p, _, ok := buildBulkFixProposal(row, all, st)
		if !ok {
			t.Errorf("bulk proposal for %v unexpectedly refused", c.cat)
			continue
		}
		joined := strings.Join(p.cliArgs, " ")
		hasBash := strings.Contains(joined, "Bash")
		if hasBash != c.wantBash {
			t.Errorf("cat %v: hasBash=%v, want %v (args=%q)", c.cat, hasBash, c.wantBash, joined)
		}
	}
}

// TestBulkFixRefusesInMemoryCategories: F on an orphan row must not invoke the
// bulk machinery; should flash a hint pointing at `p`.
func TestBulkFixRefusesInMemoryCategories(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-bulk-test")
	m := newModel(st)
	m.summary.claudeOnPath = true
	out := drive(m, "9", "F")
	clean := stripANSI(out)
	if !strings.Contains(clean, "bulk-fix not available") {
		t.Fatalf("expected refusal flash for in-memory category; got:\n%s", clean)
	}
}

// TestSummaryAssetCacheLazyLoadsOnce: ensureAssets is sentinel-gated; calling
// it twice does not re-read from disk. Verified by mutating an underlying file
// after the first load — the cached values must NOT reflect the on-disk change
// until invalidateAssets is called.
func TestSummaryAssetCacheLazyLoadsOnce(t *testing.T) {
	st, p := buildState(t)
	skillDir := p.ClaudeConfigDir + "/skills/cache-test"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillDir+"/SKILL.md",
		[]byte("---\nname: cache-test\ndescription: short\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := &summaryView{st: st}
	v.ensureAssets()
	firstCount := len(v.cachedSkills)
	if firstCount == 0 {
		t.Fatalf("expected at least one cached skill")
	}

	// Add a new skill on disk. Without invalidation, the cache should NOT see it.
	skillDir2 := p.ClaudeConfigDir + "/skills/added-after-cache"
	if err := os.MkdirAll(skillDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillDir2+"/SKILL.md",
		[]byte("---\nname: added-after-cache\ndescription: short\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v.ensureAssets()
	if len(v.cachedSkills) != firstCount {
		t.Errorf("ensureAssets re-read disk without invalidation: had %d skills, now %d", firstCount, len(v.cachedSkills))
	}

	v.invalidateAssets()
	v.ensureAssets()
	if len(v.cachedSkills) != firstCount+1 {
		t.Errorf("after invalidate+ensure, expected %d skills, got %d", firstCount+1, len(v.cachedSkills))
	}
}

// TestSummaryAssetCachePopulatedFromBuildRows: the first call to buildRows
// should populate the cache (since render() goes through it), so update()'s
// row-build path always sees lint findings on the very next keypress.
func TestSummaryAssetCachePopulatedFromBuildRows(t *testing.T) {
	st, _ := buildState(t)
	v := &summaryView{st: st}
	if v.assetsLoaded {
		t.Fatal("assetsLoaded should start false")
	}
	_ = v.buildRows()
	if !v.assetsLoaded {
		t.Fatal("buildRows should have populated assets cache via ensureAssets")
	}
}

// TestSummaryAssetCachePreservedAcrossOrphanFix: pruning an orphan override
// (an in-memory fix that doesn't touch skills/agents/commands) must NOT drop
// the asset cache — re-scanning all skill files on every unrelated fix would
// negate the lazy-load optimization.
func TestSummaryAssetCachePreservedAcrossOrphanFix(t *testing.T) {
	st, _ := buildState(t)
	seedOrphanOverride(t, st, "ghost-cache-test")
	m := newModel(st)

	// Force first render so the cache populates, then snapshot loaded state.
	_ = drive(m, "9")
	if !m.summary.assetsLoaded {
		t.Fatal("expected assetsLoaded=true after first render of summary tab")
	}

	// Apply the orphan-prune fix (catOrphanPlugin/Stdio — does NOT affect assets).
	drive(m, "f", "y")
	if !m.summary.assetsLoaded {
		t.Errorf("orphan-prune fix should preserve asset cache (categoryAffectsAssets=false), but cache was invalidated")
	}
}

// TestCategoryAffectsAssetsTable locks down the per-category invalidation policy.
func TestCategoryAffectsAssetsTable(t *testing.T) {
	affecting := []summaryCat{
		catPluginEnabledNotInstalled, catPluginInstalledNotEnabled, catSlashConflict,
		catSkillNameInvalid, catSkillNameTooLong, catSkillDescTooLong,
		catAgentDescTooLong, catCommandDescTooLong,
	}
	for _, c := range affecting {
		if !categoryAffectsAssets(c) {
			t.Errorf("expected categoryAffectsAssets(%v)=true", c)
		}
	}
	notAffecting := []summaryCat{
		catNone, catOrphanPlugin, catOrphanStdio, catStashGhost,
		catStashRedundantWithUser, catStashGhostedByPlugin,
		catUserDupPlugin, catDuplicateLoad, catStaleMcpjson,
	}
	for _, c := range notAffecting {
		if categoryAffectsAssets(c) {
			t.Errorf("expected categoryAffectsAssets(%v)=false", c)
		}
	}
}
