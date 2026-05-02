# Changelog

All notable changes to this project are documented here. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ringo380/ccmcp/compare/v0.5.0...HEAD
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
