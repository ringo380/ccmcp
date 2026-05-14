# Changelog

All notable changes to this project are documented here. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.11.0] — 2026-05-14

### Review-pass fixes

- **Summary tab: skill/agent/command Discover + asset-lint now lazy-loaded
  once per session** instead of running on every keystroke. Cache lives on
  `summaryView`; `invalidateAssets()` fires only after fixes whose category
  could have affected the result (skill/agent/command frontmatter rewrites,
  slug renames, `enabledPlugins`/`skillOverrides` flips) — orphan-override
  prunes, stash drops, and `~/.claude.json` mcpServer edits keep the cache
  warm. Per CLAUDE.md "TUI lazy-load point: trigger expensive lazy work in
  render(), not update()".
- **Plugins tab: successful retry of a failed plugin removes it from the
  failures panel.** Previously the comment in `updateFailures` claimed this
  but the implementation was a no-op; the entry persisted until `X` cleared
  it. Panel auto-closes when its last entry is resolved.
- **`ccmcp doctor assets --json` emits `[]` instead of `null`** when zero
  issues are found. Downstream JSON consumers expecting an array no longer
  break on the empty case.
- **Plugin update probe for an uninstalled plugin invalidates the cache
  entry** rather than silently dropping the result. Eliminates a corner-case
  count drift after a plugin is removed mid-probe.
- **`claudeAssetFixArgs` permission profile tightened** to match its doc
  comment: `permDescription` now grants only `Edit,Write,Read` (was
  `Edit,Write,Read,Glob,Grep`); `permRename` keeps `Glob,Grep,Bash` since
  slug uniqueness check + `mv` genuinely need them.
- **`claudeOnPath` field comment updated** to mention the new `F` bulk-fix
  key (also gated on the claude CLI being available).

### Added

- **Plugins tab: bulk-update failures now show the actual error and a hint.**
  Per-plugin git stderr is captured by the installer and surfaced in a new
  failures panel (`F` from the plugins tab). The panel lists every failed
  plugin id, a one-line error summary, and a heuristic hint (network /
  permission / stale-pin / disk-full / etc.). `enter` expands the full
  stderr, `R` retries the selected plugin, `X` clears the record, `esc`
  closes. Failures are persisted to `~/.claude-mcp-backups/last-bulk-failures.json`
  (7-day TTL) so the panel survives tab switches and TUI restarts. The
  bulk-update flash gained a `(press F to view)` hint when any items failed.

- **Doctor: `ccmcp doctor assets` lints skills/agents/commands against
  Claude Code 2.1.141 frontmatter constraints.** New `internal/doctor/asset_lint.go`
  validates skill `name` (`^[a-z0-9-]+$`, ≤64 chars — hard requirement),
  skill `description`+`when_to_use` combined length (warn at 1200, error at
  1536 — content past the cap is silently dropped from skill listings),
  agent `description` length, command `description` palette-readability cap,
  and plugin manifest description. New issue codes: SKILL001/002/003,
  AGENT001, CMD001, PLUGIN001. CI-friendly exit code: 1 on any error-severity
  finding, 0 otherwise. `--json` for machine-readable output.

- **Summary tab: asset-lint findings appear as fixable rows + new `F` bulk
  fix.** Skill name/description, agent description, and command description
  violations from the new lint package surface in the Summary tab as
  selectable issues. `F` (capital) gathers every fixable row in the cursor's
  category and hands them all to Claude in one prompt, with per-category
  permission scoping (description rewrites get Edit/Write/Read/Glob/Grep;
  skill slug renames also get Bash for `mv`). Bulk-fix refuses in-memory
  categories (orphans) and redirects to the existing `p` prune flow. All
  affected files are snapshotted before dispatch under
  `~/.claude-mcp-backups/doctor/` so each can be reverted independently.

### Fixed

- **Plugins tab: "(N update available)" no longer stays stuck after a
  successful bulk update.** Update probes scheduled when the tab was first
  entered could arrive AFTER bulk-update completed and re-poison the cache
  with the pre-update local SHA, so the counter never dropped. The
  `pluginUpdateCheckMsg` handler now discards stale probe results whose
  Local SHA disagrees with the on-disk SHA and schedules a fresh probe in
  their place. Git operations in the installer also now capture stderr
  into wrapped errors instead of dumping it to the terminal.

## [0.10.0] — 2026-05-14

### Added

- **Summary tab: select-and-fix issues with LLM (parity with Doctor).** The
  Summary tab is no longer scroll-only — each actionable finding (orphan
  override, stash redundancy, duplicate-load, slash-command conflict, plugin
  registration drift) is cursor-selectable and fixable in place. `f` opens a
  confirm panel; `l` runs a non-applying LLM review of the selected issue;
  `y` / `n` / `esc` approve or cancel. Orphan and stash drops apply directly
  to in-memory state (save with `w`); config edits hand off to `claude --print`
  with snapshot + post-review keep/revert gate, mirroring the Doctor flow.
  See `#48`.

### Changed

- **Doctor / Summary: LLM fix runs inside the TUI with a live spinner.**
  Replaced `tea.ExecProcess` (which suspended the bubbletea loop and dumped
  users to raw terminal output until `claude --print` finished) with a
  goroutine-based `tea.Cmd`. The view now renders a spinner + elapsed-time
  panel during the fix (e.g. `Applying LLM fix to MEMORY.md… (12s)`), and
  surfaces captured stderr inline if the CLI exits non-zero — no more "see
  output above" hint that lost context after the screen redrew.

## [0.9.1] — 2026-05-13

### Fixed

- **Doctor tab: `a` (apply review) no longer marks the review applied until
  the user confirms with `y`.** Previously pressing `a` set
  `applied=true` immediately, so canceling with `n` left the review
  marked done and unreachable by a subsequent `a`. Approval is now
  deferred to the actual confirm gate, and canceling restores the LLM
  review screen so the user lands back on the suggestion they were
  considering.
- **Doctor tab: orphan fix-snapshots are cleaned up.** When the claude
  CLI exits cleanly but writes no changes, when an in-TUI fix's
  `os.WriteFile` fails after the snapshot was already taken, and after
  a successful revert from the post-apply gate, the now-redundant
  snapshot under `~/.claude-mcp-backups/doctor/` is removed
  immediately rather than waiting for the 30-day GC sweep. The "Keep"
  path still preserves the snapshot as a manual recovery escape hatch.

### Changed

- **Doctor tab polish.** `f` (fix issue) is now available from the LLM
  review view as well as the lint view. Pressing `r` (re-run lint)
  now also clears stale flash messages and last-fix state instead of
  letting them bleed into the fresh run. The post-apply ReadFile
  error message now names the affected file and points at the
  still-on-disk snapshot. Confirm and post-review panel footers, plus
  the help-text strip, advertise the full key set (`n`/`esc` cancel
  the apply gate; `u`/`n`/`esc` all revert from the post-review gate).

## [0.9.0] — 2026-05-12

### Changed

- **Doctor tab autofix: explicit preview and approval before any file write.**
  Pressing `f` on a doctor issue now opens a content-level preview panel
  before the fix is applied. In-TUI fixes (`MEM002` line-removal,
  `MEM005` frontmatter-field insertion) compute the post-state in memory
  and render a colorized unified diff (3 lines of context) which the
  user approves with `y` or rejects with `n`. Claude-CLI-driven fixes
  get a two-gate flow: the first panel shows the full prompt that
  will be sent (no more 100-char truncation), then after Claude
  finishes, a second panel renders the actual on-disk diff and the
  user keeps the change with `y` or reverts with `u`/`n`. `j`/`k`
  scroll inside long previews.
- **Doctor fix snapshots on disk.** Before any autofix writes (in-TUI or
  CLI), the target file is copied to
  `~/.claude-mcp-backups/doctor/<basename>-<unix-ts>-<N>.<ext>` so
  changes survive a TUI crash and can be restored manually. The
  snapshot path is shown in the post-fix flash message. Revert from
  the post-apply review reads from the on-disk snapshot, falling back
  to an in-memory copy only if the snapshot is unreadable.
- **Doctor snapshot GC.** On every lint run, snapshots are pruned in the
  background under two rules: per source file, keep the 20 newest;
  delete anything older than 30 days regardless of count. Errors are
  silent — they only affect cleanup, never the fix itself.

### Fixed

- **Discovery: `docs.claude.com` HTML SPA no longer surfaces JSON parse errors.**
  The Anthropic registry source now checks `Content-Type` and treats
  non-JSON 200 responses (the docs SPA serves HTML at every path,
  including unknown `.well-known/*` URLs) as "no registry yet" instead
  of bubbling `invalid character '<'` up into the Discover tab.
- **Discovery: clearer error when a curated awesome-list link is not a
  Claude Code marketplace.** `FetchManifest` now distinguishes "all
  candidate URLs returned HTTP 404" from "other transport errors" and,
  for GitHub-sourced repos that 404 across HEAD/main/master, reports
  `repo may not be a Claude Code marketplace` rather than echoing the
  last URL it tried.

## [0.8.0] — 2026-05-11

### Added

- **Launch-time self-update check (oh-my-zsh style).** The first
  interactive `ccmcp` launch each day queries
  `api.github.com/repos/ringo380/ccmcp/releases/latest` with a 2-second
  timeout. If a newer release is published, a release-notes excerpt is
  printed and the user is prompted `Update now? [Y/n]`. On Y, the
  install method is detected (Homebrew prefix vs `~/go/bin` vs raw
  binary) and the corresponding upgrade command runs
  (`brew upgrade ccmcp` / `go install github.com/ringo380/ccmcp@latest`).
  On n, the prompt is suppressed for 24h or until a newer release ships
  (whichever is sooner). Disable per-invocation with `--no-update-check`
  or persistently with `CCMCP_NO_UPDATE_CHECK=1`. Cached at
  `~/.claude/plugins/cache/ccmcp-update-check.json`. Subcommands skip
  the check entirely so scripted flows stay quiet; the prompt is also
  suppressed when stdin/stdout aren't TTYs.

## [0.7.0] — 2026-05-11

### Changed

- **TUI: high-contrast in-progress indicators with live spinner.** Every
  long-running operation (plugin install/update, bulk update, marketplace
  git pull, discovery fetch, doctor LLM review) now renders with an
  animated braille-dot spinner in bold cyan instead of dim grey, so it's
  obvious when work is happening vs. idle. The spinner is driven by a
  bubbles `spinner.Model` at the model level and the current frame is
  published via `state.spinnerFrame` for views to consume. Headless
  `Dump()` is unaffected (no TickMsg is processed).
- **TUI: per-item bulk-update progress.** `B`-key bulk updates on both
  the Plugins and Marketplaces tabs now process targets one at a time
  and stream `(N/M)` progress to the in-progress line plus a
  `updating <id>… (N/M)` flash for each item. The `↑ update available`
  annotation for each plugin/marketplace clears the moment its own item
  lands rather than waiting for the entire batch to finish — so a 20-item
  sweep doesn't look frozen. Final bulk-summary flash
  (`N updated, M already up to date, K failed`) is unchanged.

### Fixed

- **Bulk-update `(N/M)` counter alignment.** The in-line "bulk update in
  progress…" label and the per-item flash now both use the "currently on
  item N of M" semantic instead of differing by one (label was previously
  "N completed of M"). Capped at total during the brief window between
  the last item landing and the result message arriving.
- **Defensive nil-result guard in `pluginBulkItemDoneMsg` switch.** A
  `{result: nil, err: nil}` payload (should never happen given
  `install.Install`'s contract, but defensive) is now classified as a
  failure instead of panicking on the SHA comparison.
- **`dirtyPlugins` set per-item in the streaming bulk path** so an
  in-flight `Q` quit-confirmation still prompts to save even if the
  result handler never runs (e.g. session torn down mid-batch).
- **Skip redundant `UpdateInstall`/`InvalidatePlugin` loop in the result
  handler when items were already applied via streaming.** Adds a
  `streamed bool` field to `pluginBulkUpdateResultMsg`; direct senders
  (existing tests, future CLI integrations) leave it false so the result
  handler still lands state.

### Tests

- New TUI tests: `TestTUIPluginUpdateClearsIndicator`,
  `TestTUIPluginUpdateErrorPreservesIndicator`,
  `TestTUIPluginBulkUpdateClearsIndicators`,
  `TestTUIPluginUpdateInProgressVisible`, `TestTUISpinnerLoopsContinuously`,
  `TestTUIPluginBulkPerItemProgress`,
  `TestTUIPluginBulkResultHandlerStillAppliesForDirectSender`,
  `TestTUIPluginBulkNilResultIsTreatedAsFailure` — assert the
  `↑ update available` annotation clears on success, preserves on error,
  that the spinner tick loop self-perpetuates, per-item bulk progress
  increments the (N/M) counter and clears each plugin's indicator live,
  that the `streamed` flag correctly gates the redundant apply loop, and
  that nil-result payloads classify as failure. Total: 234.

## [0.6.0] — 2026-05-08

### Added

- **Doctor: `claude-cli` LLM-review provider** — `doctor md --llm-review`
  and the TUI `l` key now auto-fall-back to running the local `claude` CLI
  (via `--print` over stdin) when no `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`
  is set, so offline review works out of the box. Explicit selection via
  `--provider claude-cli`. The default `--provider` value is now empty
  (auto); existing `anthropic` / `openai` selections behave identically.
- **Doctor: typed `APIError` with parsed messages** — non-2xx responses
  from Anthropic/OpenAI now return a `*doctor.APIError` carrying the
  parsed `error.message` plus the raw body. CLI prints a single-line
  message instead of the noisy raw JSON; the TUI surfaces a 401-specific
  hint pointing at `/login` or `--provider claude-cli`.
- **Doctor TUI: `claude` CLI presence banner** — when `claude` is not on
  PATH, a warning banner is rendered at the top of the Doctor tab and the
  `l` / `f` keys surface a friendly hint instead of a cryptic failure.
- **Doctor TUI: enriched fix-failure messages** — bare `"exit status N"`
  errors from `tea.ExecProcess` (which loses subprocess stderr) are
  rewritten to `"claude CLI exit N — see output above"`. Long error
  strings from LLM review wrap cleanly to fit the viewport.
- **Discover tab + `ccmcp discover` CLI** — browse Claude Code marketplaces
  surfaced from a merged set of authoritative sources without ever touching
  the user's installed state. Sources: an embedded ccmcp-curated registry,
  an Anthropic-published registry probe (no-op until the canonical URL
  exists), `awesome-claude-code`-style README scrapers, and any user-
  supplied registry URLs configured under
  `settings.json#discoverySources`. Two-stage drill-down — list view shows
  every discovered marketplace, Enter fetches the marketplace's manifest
  (no clone) to list its plugins, second Enter shallow-clones the plugin
  to a sha-keyed preview cache (`~/.claude/plugins/cache/_discovery/`) and
  runs every existing scanner (skills, agents, commands, MCP servers,
  hooks) against it to produce a conflict report against the user's
  currently-installed state. Results are cached for 6h with a 72h offline
  grace window so reopening the tab is instant. New CLI surface:
  `ccmcp discover list [--json] [--refresh]`,
  `ccmcp discover show <marketplace>`, and
  `ccmcp discover plugin <marketplace> <plugin>`.
- **`CCMCP_DISCOVERY_OFFLINE`** — when set, restricts default discovery
  sources to the embedded curated registry only. Useful for hermetic test
  runs and air-gapped environments.

### Changed

- TUI gained a 10th tab; Doctor moved from the `9` numeric shortcut to `0`,
  Profiles 7→8, Summary 8→9, Commands 6→7, Agents 5→6, Skills 4→5. Tab
  order in the header bar (and `tab` / `shift+tab` cycling) follows the
  same shift, with the new Discover tab inserted directly after
  Marketplaces. The `?` help overlay and README key tables were updated to
  match.

### Fixed

- Discovery `PreviewClone` now keys the cache directory by the upstream-
  resolved sha (via `git ls-remote`) rather than a literal `HEAD`, so a
  branch tip moving upstream produces a fresh clone instead of a silently
  stale conflict report. Sha-pinned plugin sources check out the exact
  commit after clone instead of whatever HEAD pointed at.
- Discovery `shallowClone` no longer passes a 40-char commit SHA to
  `git clone --branch <ref>` (which `git` rejects with "Remote branch
  <sha> not found"); SHA refs are resolved via post-clone
  `fetch + checkout`.
- Discovery cache directory segments (`<owner>`, `<repo>`) derived from
  untrusted registry input are now sanitized — `..` runs / `/` /
  separator chars collapse to `_`, blocking a malicious
  `repo: "../evil"` entry from writing outside the preview cache.
- Discovery `Discover()` no longer overwrites a previously-good cache
  with an empty result when every source transiently fails and no
  in-grace cache exists; the previous-good cache survives the outage.
- Manifest fetches in `cmd/discover.go` and the TUI Discover tab now go
  through the same UA-injecting HTTP client as the orchestrator
  (`discovery.NewHTTPClient`), avoiding 403s from mirrors that reject
  empty User-Agent headers.
- TUI `discoveryView.fetchCmd` now snapshots the user-supplied registry
  URL list on the bubbletea goroutine before dispatching the background
  fetch, eliminating a data race against concurrent settings mutations
  on other tabs.

## [0.5.1] — 2026-05-03

### Added

- **Doctor tab: actionable fix for every lint issue** — press `f` on any
  selected issue to apply a fix. Trivial issues (MEM002 broken index link,
  MEM005 missing frontmatter field) are resolved in-TUI with a y/n confirm.
  Judgment-required issues (MD003 line too long, MD004 broken link, MD005
  file too long, MEM001/MEM003/MEM004/MEM006, etc.) build a contextual
  prompt and hand off to `claude` CLI via `tea.ExecProcess`; the TUI
  resumes and re-runs lint automatically when the CLI session exits.
- **Doctor tab: cursor navigation** — `j/k` moves a `▶` cursor through
  issues; `g/G` jump to first/last; `pgup/pgdn` page through. Scroll
  auto-follows the cursor in lint mode; `j/k` scroll the LLM review text
  directly (unchanged behaviour).
- **Marketplace update parity with Plugins tab** — `u` now shows
  `"already up to date"` vs `"updated abc123 → def456"` SHA feedback.
  Bulk update (`B`) reports updated / already-up-to-date / failed counts
  separately. `R` (force refresh) now invalidates all marketplace cache
  entries before re-probing, matching the behaviour of `R` on the Plugins
  tab.

## [0.5.0] — 2026-05-02

### Added

- **Marketplaces TUI tab** (key `3`) — full CRUD parity with Claude
  Code's `/plugins` interface: add (`a`) with multi-step prompt, update
  (`u`), bulk update (`B`), remove (`x`, two-step confirm with clone-dir
  purge), filter (`/`), and refresh update probes (`R`). Lists installed
  marketplaces with plugin counts and installed-vs-available badges.
- **"Newer version available" indicators** — `↑` markers next to
  outdated rows on the Plugins, Marketplaces, and MCPs tabs; aggregate
  count surfaced at the top of the Summary tab.
- **`internal/updates` package** — git `ls-remote` probes for
  marketplaces and plugin sources; best-effort `npm view` / PyPI JSON
  probes for npx- and uvx-launched stdio MCPs. Injectable `Runner` so
  tests never hit the network. In-process session cache, invalidated
  after successful updates.
- **`ccmcp plugin outdated`** and **`ccmcp marketplace outdated`** —
  CLI parity with the TUI indicators; reports rows whose upstream
  has advanced.
- **`marketplace add` clones automatically** — was settings-only;
  `--no-clone` opt-out preserved. `marketplace remove --purge` deletes
  the on-disk clone directory.

### Fixed

- Plugin bulk update (`B`) now applies state mutations on the main
  bubbletea goroutine rather than the worker, eliminating a race on
  `installed_plugins.json`. Sets `dirtyPlugins` and rescans plugin MCPs
  to match the single-update path.
- `marketplace remove --dry-run` now validates marketplace existence
  and plugin references before the dry-run guard, so dry-run can no
  longer promise success the real path would refuse.
- TUI marketplace add/remove persists settings synchronously after disk
  side-effects (clone / RemoveAll), preventing settings.json and the
  on-disk clone from diverging on `Q` force-quit.
- `install.RemoveMarketplace` surfaces `os.RemoveAll` errors instead
  of silently swallowing them.

### Changed

- Numeric tab shortcuts shifted to **1–9** to accommodate the new
  Marketplaces tab (was 1–8).
- `.gitignore` now excludes `.DS_Store`, `.claude-dev-helper/`, and
  `.plugin-config/`.

## [0.4.0] — 2026-04-30

### Added

- **Plugin install/update/uninstall** — `ccmcp plugin install <id>` and
  `ccmcp plugin update [id|--all]` fetch or refresh source from the
  marketplace; `ccmcp plugin update --all` bulk-updates every installed
  plugin. SHA comparison skips no-op updates with "already up to date".
- **Marketplace refresh** — `ccmcp marketplace update [name]` runs
  `git pull --ff-only` on each locally-cloned marketplace catalog;
  no args updates all.
- **`InstalledPlugin` metadata** — `gitCommitSha` and `installedAt`
  fields are now stored and parsed, enabling update-skip detection and
  preserving original install timestamps across updates.
- **Old-version GC** — `plugin update` automatically removes the
  previous versioned cache directory after a successful update.
- **claude.ai integration rows in the Plugins tab** — remote integrations
  (e.g. Stripe, Supabase) appear in the Plugins tab with `[~]` / `[-]`
  markers; `space` toggles their per-project disable state.
- **TUI `U` key** — async in-place plugin update from the Plugins tab;
  flashes old→new SHA on success.
- **TUI `x` key** — two-step confirmation to remove an installed plugin
  from the Plugins tab (press `x` again to confirm, any other key cancels).
- **TUI `I` key** — browse-and-install sub-view that loads available
  plugins from all locally-cloned marketplace catalogs, filtered to
  uninstalled entries; press `I` on a row to install.

## [0.3.1] — 2026-04-29

### Changed

- **MCPs tab default view is now load-accurate.** The effective scope
  (the default when opening `ccmcp tui`) hides rows that can never load
  in the current project — stash entries, MCPs from
  installed-but-globally-disabled plugins, and orphan
  `disabledMcpServers` keys whose source is gone. The title bar breaks
  out the count as `(N active · M disabled here · K hidden)` so noise
  is visible without cluttering the list. Press `H` to reveal the
  hidden rows.
- **Conflict indicator** — when the same MCP name is registered by
  multiple effective sources (e.g. user scope + an enabled plugin),
  rows are flagged with `⚠ 2x (also loads from another source)` so
  duplicate-load situations don't masquerade as redundant duplicates.

## [0.3.0] — 2026-04-24

### Added

- **Skills, Agents & Commands TUI tabs** — list, enable/disable, create,
  move, and remove skills, agents, and slash commands across user, project,
  and plugin scopes directly from the TUI.
- **Doctor tab** — lint `CLAUDE.md` and `MEMORY.md` for structural issues;
  add `--llm-review` for an LLM quality pass.
- **Reports** — `ccmcp report snapshot|sweep|drift|audit` for point-in-time
  dumps, cross-project sweep tables, drift diffs, and stale-override audits
  (JSON, Markdown, or CSV output).
- **Profile export/import** — `ccmcp profile export <name> [--with-config]`
  and `ccmcp profile import [FILE|-] [--overwrite]` for sharing profiles
  across machines or teams.
- **Command conflict detection** — `ccmcp command conflicts` and
  `ccmcp command resolve` surface and resolve shadowed slash commands.

### Fixed

- `command resolve --strategy ignore --dry-run` now correctly reports
  "already ignored" instead of "would add" when the entry is already
  present (was skipping the read-only `ig.Has()` check).
- `skill enable --dry-run` no longer reports or mutates skills that already
  have an explicit `"on"` override (first-pass change detection now checks
  `cur == "off"` to mirror the disable path).
- `rebuild()` backing-array aliasing in Skills, Agents, and Commands TUI
  tabs (was using `v.rows[:0]` + append which aliased the unfiltered slice).
- Flash message drain wired for Agents and Commands tabs in `updateActive()`.

## [0.2.5] — 2026-04-21

### Changed

- **Homebrew tap moved** from `ringo380/tap` to `robworks-code/tap`.
  New install command:

  ```sh
  brew install robworks-code/tap/ccmcp
  ```

  The old `ringo380/tap` formula is soft-deprecated for one release and
  will stop receiving updates after the next tag. Migrate with:

  ```sh
  brew uninstall ccmcp
  brew untap ringo380/tap
  brew install robworks-code/tap/ccmcp
  ```

## [0.2.4] — 2026-04-20

### Added

- **`S` key in the MCPs tab: stash/unstash the current row.** Smart
  toggle based on source — stashes a user/local-scope row, unstashes a
  stash row. Plugin / claude.ai / `.mcp.json` / orphan rows show a
  specific hint explaining why those can't be stashed. Saves the prior
  two-keystroke `m` + picker flow and makes the operation discoverable
  via the footer hint and `?` legend.

## [0.2.3] — 2026-04-20

### Added

- **`ccmcp mcp unstash`** as a symmetrical alias for `ccmcp mcp restore`.
  Pairs visually with `mcp stash`; the two commands do exactly the same
  thing (move entries from `~/.claude-mcp-stash.json` back into
  `~/.claude.json#/mcpServers`). Prior versions only exposed `restore`.

## [0.2.2] — 2026-04-20

### Added

- **Source attribution for every `disabledMcpServers` entry.** The TUI's
  MCPs tab no longer has a generic "unknown" bucket. Every entry is now
  classified into one of: plugin active, plugin disabled-but-installed,
  claude.ai, stdio live, stash ghost, orphan plugin (plugin not installed),
  or orphan stdio (no source anywhere). Rows show a specific reason text
  like *"plugin 'Notion' is not installed — stale override (safe to prune)"*
  instead of a vague question mark.
- **Disabled-but-installed plugins** (e.g. `plugin:postman:postman` when
  the `postman` plugin is globally disabled) now render as regular plugin
  rows with `PluginEnabled=false`, a description suffix `(currently
  disabled)`, and non-effective status. Previously these appeared as
  unknown because ccmcp only scanned enabled plugins.
- **Stash-ghost resolution.** Plain-name overrides from before an MCP was
  parked in the stash (e.g. `"dropbox"` in `disabledMcpServers` while
  `dropbox` now lives in the stash) now attach to the stash row as an
  informational marker instead of falling through to unknown.
- **`ccmcp mcp prune`** — new subcommand that removes orphaned entries
  from the current project's `disabledMcpServers`. Preserves
  disabled-but-installed plugin overrides (re-enabling the plugin would
  re-activate them — user intent respected). `--dry-run` lists what
  would be removed; `--yes` skips the confirmation prompt;
  `--include-stash-ghosts` also sweeps stash ghosts.
- **Summary tab** gains a classified breakdown of per-project overrides
  and a "Cleanup suggestions" block that points at `mcp prune` when there
  are recoverable entries.

### Changed

- `config.ScanEnabledPluginMCPs` is now a thin filter over the new
  `config.ScanAllInstalledPluginMCPs`. `PluginMCPSource` gains an
  `Enabled` flag so callers can distinguish "will load" from "known but
  inactive".
- TUI rows gain `MatchKey`, `PluginEnabled`, and `UnknownReason` fields.
  `isEffective()` now respects `PluginEnabled` so disabled-plugin rows
  render as `[ ]` in the effective view.

### Internal

- New package `internal/config`: `InstalledPlugins.ByName(name)` — match
  installed plugins by bare plugin name (without `@marketplace`), needed
  to attribute `plugin:X:Y` override keys back to a concrete plugin.

## [0.2.1] — 2026-04-20

### Added

- **Homebrew distribution** via `ringo380/homebrew-tap`. Every
  non-prerelease tag auto-publishes a `Formula/ccmcp.rb` via goreleaser,
  so users can install with:

  ```sh
  brew install ringo380/tap/ccmcp
  ```

  Install path updated on the release page and in the README to lead
  with Homebrew, then `go install`, then prebuilt binaries.

## [0.2.0] — 2026-04-20

### Added

- **`?` help overlay** in the TUI: a full-screen legend describing every
  source badge (`[u]` / `[l]` / `[p]` / `[P]` / `[@]` / `[s]` / `[?]`),
  every row mark (`[x]` / `[~]` / `[ ]` / `[!]`), and every key binding
  grouped by tab — including the `m`-move sub-prompt (`u`/`l`/`s`/esc)
  that was previously only visible when triggered. Close with `?` or
  `esc`. Discoverable via the new footer hint (`?: help`).
- `ccmcp tui --dump --tab help` dumps the legend as plain text for
  non-interactive inspection.
- **Bulk `A` / `N` toggle on the MCPs tab** (previously only on the
  Plugins tab): batched equivalent of per-row `space`, scope-aware,
  respects the active filter. Turns `/plugin` + `<enter>` + `N` + `w`
  into a four-keystroke workflow for "disable every plugin-registered
  MCP for this project".

### Changed

- **CI**: bumped `actions/checkout` v4→v6, `actions/setup-go` v5→v6,
  `goreleaser/goreleaser-action` v6→v7 ahead of the 2026-06-02 Node.js
  20 deprecation on GitHub runners. Goreleaser v2 config unchanged.
- Footer hint updated to include `?: help` so the overlay is discoverable.

## [0.1.0] — 2026-04-20

Initial public release.

### Added

- Interactive `bubbletea` TUI with four tabs: MCPs, Plugins, Profiles, Summary.
- Effective-view default that shows every MCP which will actually load in the
  current project, with source badges (`u` user, `l` local, `p` project `.mcp.json`,
  `P` plugin-bundled, `@` claude.ai, `s` stash) and `[x]` / `[~]` / `[ ]` marks.
- Per-row `space` toggle and bulk `A` / `N` that respect the active filter
  and scope, so `/plugin` + `<enter>` + `N` disables every plugin-registered
  MCP for the current project in one keystroke.
- `m` move action to relocate an MCP's config between user / local / stash
  scopes; plugin-sourced rows copy their bundled config with a duplicate-load
  warning.
- Scope cycling (`s`) across effective → local → user → project → stash.
- CLI subcommands:
  - `ccmcp status [--json]`
  - `ccmcp mcp list|enable|disable|stash|restore|move|override`
  - `ccmcp profile save|list|show|use|delete`
  - `ccmcp plugin list|enable|disable|install|remove`
  - `ccmcp marketplace list|add|remove`
  - `ccmcp tui --dump [--tab ...]` for headless diagnostics
- Back-compat aliases for the prior bash prototype: `apply`, `stash-user`,
  `restore-user`, `use-profile`, `save-profile`, `list-profiles`, `show-profile`,
  `remove-local`, `clear-local`.
- Per-project override management (`disabledMcpServers`) with the same
  prefix encoding Claude Code's `/mcp` dialog uses: plain (stdio),
  `claude.ai <Name>`, `plugin:<plugin>:<server>`.
- Plugin installer supporting all four marketplace source formats: bare-string
  subdir, `url`, `git-subdir`, `github`.
- Atomic writes (temp file + `fsync` + rename) with timestamped backups in
  `~/.claude-mcp-backups/`, same-second collision counter, leading-dot stripping.
- `--dry-run` on every mutating command (verified by
  `TestCLIDryRunDoesNotWrite`).
- 61-test suite across config readers / CLI sandbox / installer / headless TUI
  state machine.

[Unreleased]: https://github.com/ringo380/ccmcp/compare/v0.11.0...HEAD
[0.11.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.11.0
[0.10.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.10.0
[0.9.1]: https://github.com/ringo380/ccmcp/releases/tag/v0.9.1
[0.9.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.9.0
[0.8.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.8.0
[0.7.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.7.0
[0.6.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.6.0
[0.5.1]: https://github.com/ringo380/ccmcp/releases/tag/v0.5.1
[0.5.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.5.0
[0.4.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.4.0
[0.3.1]: https://github.com/ringo380/ccmcp/releases/tag/v0.3.1
[0.3.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.3.0
[0.2.4]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.4
[0.2.3]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.3
[0.2.2]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.2
[0.2.1]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.1
[0.2.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.0
[0.1.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.1.0
