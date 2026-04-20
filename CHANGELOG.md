# Changelog

All notable changes to this project are documented here. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ringo380/ccmcp/compare/v0.2.4...HEAD
[0.2.4]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.4
[0.2.3]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.3
[0.2.2]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.2
[0.2.1]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.1
[0.2.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.2.0
[0.1.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.1.0
