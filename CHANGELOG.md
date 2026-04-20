# Changelog

All notable changes to this project are documented here. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ringo380/ccmcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ringo380/ccmcp/releases/tag/v0.1.0
