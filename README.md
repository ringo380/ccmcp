# ccmcp

A dynamic CLI and TUI for managing Claude Code's MCP servers, plugins, and per-project overrides across every scope Claude Code honors.

## Why

Claude Code auto-loads MCP servers from at least **six** distinct places — user config, local project state, committed `.mcp.json`, bundled plugin `.mcp.json`s, claude.ai OAuth integrations, and in-memory overrides. Starting a session with all of them enabled burns hundreds of tokens of context before you type a character. The built-in `/mcp` menu lets you toggle things one row at a time; `ccmcp` lets you toggle them in bulk, by scope, or by filter, and gives you a single view that reflects what will actually load in the current project.

## Install

**Homebrew** (macOS, Linux):

```sh
brew install robworks-code/tap/ccmcp
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

### Shell completions

`ccmcp completion <shell>` prints a completion script for `bash`, `zsh`, `fish`, or `powershell`. The static `--scope` flag and dynamic `mcp stash <name>` / `mcp restore <name>` arguments tab-complete to live values.

**bash** (Linux):

```sh
ccmcp completion bash | sudo tee /etc/bash_completion.d/ccmcp
```

**bash** (Homebrew on macOS, after `brew install bash-completion`):

```sh
ccmcp completion bash > "$(brew --prefix)/etc/bash_completion.d/ccmcp"
```

**zsh** (one-shot, current shell):

```sh
source <(ccmcp completion zsh)
```

**zsh** (persistent, requires `compinit` enabled in `~/.zshrc`):

```sh
ccmcp completion zsh > "${fpath[1]}/_ccmcp"
```

**fish**:

```sh
ccmcp completion fish > ~/.config/fish/completions/ccmcp.fish
```

**powershell**:

```powershell
ccmcp completion powershell | Out-String | Invoke-Expression
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
- `[?]` — in `disabledMcpServers` but no live source found (stale); the row description
  explains why (plugin not installed, name deleted/renamed, etc.) and `ccmcp mcp prune`
  offers to clean it up.

Every row has a resolved source. MCPs whose parent plugin is installed but globally
disabled render as `[ ]` with description `(currently disabled)` so you can tell them
apart from genuinely absent sources.

## TUI keys

**MCPs tab** (default)

| Key | Action |
|---|---|
| `space` | toggle current row in the active scope |
| `A` / `N` | bulk enable/disable every visible row |
| `S` | stash the current row (or unstash if it's already in stash) |
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
| `B` | bulk update — re-fetch every installed plugin |
| `F` | show last bulk-update failures (stderr + hint; `R` retries one) |
| `f` | cycle filter: all → enabled → disabled |
| `/` | search |

**Profiles tab**

| Key | Action |
|---|---|
| `enter` | apply profile (replaces current project's MCPs) |
| `n` | create profile from current state |
| `d` | delete |

**Summary tab**

| Key | Action |
|---|---|
| `j` / `k` / arrows | navigate fixable issues (skips display rows; scrolls past the ends) |
| `f` | preview a fix for the selected issue (in-place orphan/stash prune, or `claude --print` for config edits) |
| `F` | bulk-fix all issues in the cursor's category in one `claude` run (skill/agent/command frontmatter rewrites, slash conflicts, etc.) |
| `l` | run an LLM review on the selected issue without applying it |
| `y` / `n` / `esc` | approve / cancel in the confirm panel; `u` reverts a landed CLI fix |
| `p` | bulk-prune orphan overrides (legacy; press twice to confirm) |

Bird's-eye overview of every scope's counts, per-project overrides, and redundancies. Each actionable row (orphan override, stash redundancy, duplicate-load, slash-command conflict, plugin registration drift) is cursor-selectable and fixable in place — orphan prunes and stash drops apply directly to the in-memory state (save with `w`), and config edits hand off to `claude --print` non-interactively with an in-TUI spinner.

**Doctor tab**

| Key | Action |
|---|---|
| `r` | re-run lint checks |
| `l` | run one bundled LLM review across CLAUDE.md + MEMORY.md (Haiku, single call) |
| `a` | apply the bundled review back to disk (single Claude call) |
| `j` / `k` / arrows | scroll |
| `g` / `G` | top / bottom |
| `f` | preview a fix for the selected issue (programmatic when possible) |
| `F` | bulk-fix every issue sharing the cursor's code in one keystroke (programmatic stack, or single bundled `claude` call when CLI-only) |
| `y` / `n` | approve / reject the previewed fix (in confirm panel) |
| `u` | revert a CLI fix from its on-disk snapshot (in post-review panel) |

Runs structural lint on `CLAUDE.md` and `MEMORY.md` for the current project. Pressing `f` opens a preview panel: in-TUI fixes show a unified diff of the exact change before you approve; Claude-CLI fixes show the full prompt first, then after the CLI runs, show the resulting diff and let you keep (`y`) or revert (`u`). Every fix snapshots the original file to `~/.claude-mcp-backups/doctor/` (kept: 20 newest per file, max age 30 days). `F` bulk-fixes every issue that shares the cursor's lint code in a single keystroke — programmatic codes (broken index entries, missing frontmatter fields, standalone broken links, empty MEMORY.md) are applied directly with per-file snapshots; CLI codes (line-too-long, file-too-long, content rewrites) are bundled into one `claude --print --model claude-haiku-4-5 --max-turns 4` invocation with a strict-imperative envelope that forces Edit-tool calls instead of prose responses.

**Global**

| Key | Action |
|---|---|
| `tab` / `shift+tab` | cycle tabs |
| `1`–`9`, `0` | jump to MCPs / Plugins / Marketplaces / Discover / Skills / Agents / Commands / Profiles / Summary / Doctor |
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
ccmcp mcp prune [--dry-run] [--yes]            # remove stale entries from disabledMcpServers
       [--include-stash-ghosts]                # (keeps disabled-but-installed plugin entries)
ccmcp mcp stash   [<name>...]                  # user-scope → stash
ccmcp mcp restore [<name>...]                  # stash → user-scope (alias: unstash)
ccmcp mcp unstash [<name>...]                  # same as restore

ccmcp profile save|list|show|use|delete <name> [<mcp>...]
ccmcp profile export <name> [--out FILE] [--with-config]
ccmcp profile import [FILE|-] [--overwrite]
ccmcp plugin list [--enabled|--disabled]
ccmcp plugin enable|disable|install|remove <id> [--marketplace M] [--purge]
ccmcp marketplace list|add|remove <name> [--source github|git|local] [--repo R]

ccmcp skill   list [--scope user|project|plugin] [--enabled|--disabled]
ccmcp skill   enable|disable <name> [<name>...]    # writes to skillOverrides
ccmcp skill   new <name> [--scope user|project] [--description D]
ccmcp skill   move <name> --to {user|project}
ccmcp skill   rm   <name> [--scope user|project]
ccmcp skill   show <name>
ccmcp agent   list|new|move|rm|show <name>         # same verb shape as skill
ccmcp command list [--scope user|project|plugin]
ccmcp command conflicts [--include-ignored] [--json]
ccmcp command resolve <effective-name> [--strategy disable-skill|ignore|list]

ccmcp report snapshot [--out FILE] [--format json|md|csv]   # point-in-time state dump
ccmcp report sweep    [--base PATH] [--format json|md|csv]  # summary table across all projects
ccmcp report drift    --from <snapshot.json> [--format json|md]  # what changed since baseline
ccmcp report audit    [--format json|md|csv]                # stale overrides, conflicts, redundancies

ccmcp doctor md [--user] [--memory-dir DIR]                # lint CLAUDE.md + MEMORY.md
ccmcp doctor md --llm-review [--provider anthropic|openai] # + LLM quality review

ccmcp tui --dump [--tab mcps|plugins|marketplaces|discover|skills|agents|commands|profiles|summary|doctor]   # print initial render, no TTY
```

**Global flags:** `--path <dir>` (override cwd), `--dry-run`, `--json`, `--no-color`, `--no-update-check` (also `$CCMCP_NO_UPDATE_CHECK`).

**Self-update check:** the first interactive launch each day queries
`api.github.com/repos/ringo380/ccmcp/releases/latest` (2-second timeout, silent on failure),
and if a newer release is out it prints a release-notes excerpt and prompts
`Update now? [Y/n]`. On Y it auto-detects Homebrew vs `go install` vs a manual
binary and runs the matching upgrade command. On n the prompt is suppressed for
24 hours or until a newer release ships. Cached at
`~/.claude/plugins/cache/ccmcp-update-check.json`.

## Per-project overrides

`/mcp` in Claude Code writes per-project disables to `~/.claude.json#/projects[<path>].disabledMcpServers`. ccmcp reads and writes the same field with the same prefix encoding:

| Example key | Source |
|---|---|
| `"dropbox"` | stdio MCP (plain name) |
| `"claude.ai Gmail"` | claude.ai integration |
| `"plugin:context7:context7"` | plugin-registered MCP (`plugin:<plugin>:<server>`) |

Pressing `space` on an effective-view row flips the appropriate override key; `A`/`N` do it for every visible row at once.

### Pruning stale overrides

Over time, `disabledMcpServers` collects entries that no longer match any live source — e.g. an MCP that was renamed, a plugin that was uninstalled, or a server that moved into the stash. `ccmcp mcp prune` classifies every entry and removes the stale ones:

```sh
ccmcp mcp prune --dry-run                      # list proposed removals, no changes
ccmcp mcp prune --yes                          # go ahead, skip confirmation
ccmcp mcp prune --include-stash-ghosts         # also sweep plain-name overrides that match a stash entry
```

Orphan entries (plugin not installed, plain name with no source) are pruned by default. **Disabled-but-installed plugin overrides are preserved** — re-enabling the plugin would re-activate the MCP, and the user likely wanted it off per-project. Remove those explicitly with `ccmcp mcp override <key> --undo` if that's actually what you want.

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

320 tests across config readers/writers, CLI sandbox runs, installer, skill/agent CRUD, command discovery + conflict classifier + ignore list, profile export/import, marketplace + plugin update probes, doctor LLM-review provider fallback, doctor autofix preview/snapshot/revert flow, asset lint (skill/agent/command/plugin description + slug rules), bulk plugin-update failure capture + retry, marketplace discovery (sources, cache, conflict scan), and a headless TUI state-machine that drives the real `tea.Model` with synthesized key events.

## Project layout

```
cmd/              cobra subcommands (status, mcp, profile, plugin, marketplace,
                  skill, agent, command, discover, report, doctor, compat
                  aliases)
internal/
  agents/         agent CRUD + file-backed store
  classify/       override-key classifier (7 buckets)
  commands/       command discovery, conflict detection, ignore list
  config/         readers + writers for every Claude Code config file
  discovery/      remote marketplace discovery (4 sources merged, preview-clone
                  + conflict detection)
  doctor/         CLAUDE.md + MEMORY.md structural linter
  install/        plugin marketplace installer (4 source formats)
  paths/          config path resolution ($CLAUDE_CONFIG_DIR aware)
  report/         snapshot / sweep / drift / audit report generators
  selfupdate/     launch-time check vs GitHub releases + Y/n prompt + brew/go
                  dispatch (oh-my-zsh style)
  skills/         skill CRUD + file-backed store
  stringslice/    shared slice helpers
  tui/            bubbletea app: 10 tabs (MCPs, Plugins, Marketplaces,
                  Discover, Skills, Agents, Commands, Profiles, Summary,
                  Doctor)
  updates/        upstream version probes for marketplaces, plugins, MCPs
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
