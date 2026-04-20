# ccmcp

A dynamic CLI and TUI for managing Claude Code's MCP servers, plugins, and per-project overrides across every scope Claude Code honors.

## Why

Claude Code auto-loads MCP servers from at least **six** distinct places — user config, local project state, committed `.mcp.json`, bundled plugin `.mcp.json`s, claude.ai OAuth integrations, and in-memory overrides. Starting a session with all of them enabled burns hundreds of tokens of context before you type a character. The built-in `/mcp` menu lets you toggle things one row at a time; `ccmcp` lets you toggle them in bulk, by scope, or by filter, and gives you a single view that reflects what will actually load in the current project.

## Install

**Homebrew** (macOS, Linux):

```sh
brew install ringo380/tap/ccmcp
```

**`go install`** (needs Go 1.25+):

```sh
go install github.com/ringo380/ccmcp@latest
```

**Prebuilt binaries**: download from the [releases page](https://github.com/ringo380/ccmcp/releases/latest).

**From source**:

```sh
git clone https://github.com/ringo380/ccmcp
cd ccmcp
go build -ldflags='-s -w' -o ~/bin/ccmcp .
```

## Quick start

```sh
# In any project directory:
ccmcp
```

The "effective" view is the default. It lists every MCP that will load in this project, with a badge showing its source and a checkbox showing whether it's currently enabled here. Three keystrokes to turn off every plugin-registered MCP for this project:

```
/plugin      <enter>      N      w
```

- `/plugin` — filter rows whose name contains "plugin" (or filter on a plugin name like `cloudflare`)
- `N` — bulk-disable everything visible (writes to `disabledMcpServers` for this project only)
- `w` — save

Undo the same way with `A`.

## What ccmcp sees

| Badge | Source | Location | Effect |
|---|---|---|---|
| `u` | user | `~/.claude.json#/mcpServers` | loads in every project |
| `l` | local | `~/.claude.json#/projects[<cwd>]/mcpServers` | this directory only |
| `p` | project | `./.mcp.json` | shared via git, team-wide |
| `P` | plugin | bundled `.mcp.json` per enabled plugin | loads whenever plugin enabled |
| `@` | claude.ai | OAuth remote integration | loads wherever you're signed in |
| `s` | stash | `~/.claude-mcp-stash.json` | ccmcp-owned, parked |

The effective view's `[x]` / `[~]` / `[ ]` marks mean:

- `[x]` — will load in this project
- `[~]` — disabled here via a per-project override (easy to undo)
- `[ ]` — not active in this project by any source

## TUI keys

**MCPs tab** (default)

| Key | Action |
|---|---|
| `space` | toggle current row in the active scope |
| `A` / `N` | bulk enable/disable every visible row |
| `m` | move an MCP's config between user / local / stash |
| `s` | cycle scope: effective → local → user → project → stash |
| `/` | filter by substring (enter to lock, esc to cancel) |
| `c` | clear filter |
| `j` / `k` / arrows | navigate |
| `g` / `G` | top / bottom |
| `pgup` / `pgdn` | page |

**Plugins tab**

| Key | Action |
|---|---|
| `space` | toggle plugin enabled/disabled |
| `A` / `N` | bulk enable/disable every visible plugin |
| `f` | cycle filter: all → enabled → disabled |
| `/` | search |

**Profiles tab**

| Key | Action |
|---|---|
| `enter` | apply profile (replaces current project's MCPs) |
| `n` | create profile from current state |
| `d` | delete |

**Summary tab**

Read-only overview of every scope's counts, per-project overrides, and redundancies (e.g., "MCPs in stash that are also provided by an enabled plugin — stash entry is redundant").

**Global**

| Key | Action |
|---|---|
| `tab` / `shift+tab` | cycle tabs |
| `1` / `2` / `3` / `4` | jump to MCPs / Plugins / Profiles / Summary |
| `w` | save all staged changes |
| `q` | quit (warns if unsaved) |
| `Q` | force quit, discard changes |

Changes are **staged** until you press `w`. An `UNSAVED` badge appears in the header whenever you have pending edits.

## CLI

Every TUI action is scriptable from the CLI:

```sh
ccmcp status                                   # show everything at once
ccmcp status --json                            # JSON for scripts

ccmcp mcp list [--scope SCOPE]                 # SCOPE: user|local|project|plugin|claudeai|overrides|stash|all
ccmcp mcp enable  <name> [--scope SCOPE]
ccmcp mcp disable <name> [--scope SCOPE] [--to-stash]
ccmcp mcp move    <name> --to {user|local|stash}
ccmcp mcp override <name> [--undo]             # per-project disable (writes disabledMcpServers)
ccmcp mcp stash   [<name>...]                  # user-scope → stash
ccmcp mcp restore [<name>...]                  # stash → user-scope

ccmcp profile save|list|show|use|delete <name> [<mcp>...]
ccmcp plugin list [--enabled|--disabled]
ccmcp plugin enable|disable|install|remove <id> [--marketplace M] [--purge]
ccmcp marketplace list|add|remove <name> [--source github|git|local] [--repo R]

ccmcp tui --dump [--tab mcps|plugins|profiles|summary]   # print initial render, no TTY
```

**Global flags:** `--path <dir>` (override cwd), `--dry-run`, `--json`, `--no-color`.

## Per-project overrides

`/mcp` in Claude Code writes per-project disables to `~/.claude.json#/projects[<path>].disabledMcpServers`. ccmcp reads and writes the same field with the same prefix encoding:

| Example key | Source |
|---|---|
| `"dropbox"` | stdio MCP (plain name) |
| `"claude.ai Gmail"` | claude.ai integration |
| `"plugin:context7:context7"` | plugin-registered MCP (`plugin:<plugin>:<server>`) |

Pressing `space` on an effective-view row flips the appropriate override key; `A`/`N` do it for every visible row at once.

## Plugin installer

`ccmcp plugin install <name> --marketplace <m>` fetches source code from the marketplace and records the install in `~/.claude/plugins/installed_plugins.json`. Four marketplace source formats are supported:

- bare string path (`./plugins/foo`) — subdir of an already-cloned marketplace repo
- `url` — full-repo clone, optional `sha` pin
- `git-subdir` — clone a repo and copy a subpath
- `github` — repo-shorthand (`owner/name`, optional `ref`)

`--register-only` skips the fetch if the cache dir already exists. `--purge` on `plugin remove` also deletes the cache.

## Safety

- Every write is atomic (`temp + fsync + rename`). No partial writes survive a crash.
- Before any mutation, ccmcp snapshots the target file to `~/.claude-mcp-backups/<name>-<YYYYMMDD-HHMMSS>.json`. Same-second collisions get a numeric suffix.
- `--dry-run` never touches disk (asserted by a test against before/after md5s).
- `~/.claude.json` has 80+ telemetry / onboarding / cache fields; ccmcp mutates only what it owns and preserves unknown fields round-trip. Verified by `TestClaudeJSONPreservesUnknownFields`.

## Testing

```sh
go test ./...
```

61 tests across config readers/writers, CLI sandbox runs, installer, and a headless TUI state-machine that drives the real `tea.Model` with synthesized key events.

## Project layout

```
cmd/              cobra subcommands (status, mcp, profile, plugin, marketplace, compat aliases)
internal/
  config/         readers + writers for every Claude Code config file; override-key helpers
  install/        plugin marketplace installer (4 source formats)
  paths/          config path resolution ($CLAUDE_CONFIG_DIR aware)
  stringslice/    shared slice helpers
  tui/            bubbletea app: 4 tabs, scope-aware toggle, move, bulk
main.go
```

## Scope terminology

ccmcp uses Claude Code's native names:

- **user** — `~/.claude.json#/mcpServers`
- **local** — `~/.claude.json#/projects[<cwd>]/mcpServers` (what Claude Code calls "local")
- **project** — `./.mcp.json` (what Claude Code calls "project" — shared via git)
- **stash** — `~/.claude-mcp-stash.json` (ccmcp-owned)
- **effective** — the union of what actually loads in the current project

Legacy aliases `--scope project` → local and `--scope mcpjson` → project are still accepted so older scripts keep working.
