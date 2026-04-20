# Security policy

## Supported versions

Only the latest tagged minor release is actively maintained. Please upgrade
before reporting an issue to confirm it still reproduces.

## Reporting a vulnerability

If you find a security issue in `ccmcp`, please report it privately via
[GitHub Security Advisories](https://github.com/ringo380/ccmcp/security/advisories/new)
rather than opening a public issue. I'll aim to acknowledge within 72 hours.

Please include:

- A description of the issue and the impact you believe it has.
- Steps to reproduce (a minimal command or config snippet is ideal).
- Affected version (`ccmcp --version`).

## Threat model

`ccmcp` is a local-only tool that reads and writes files under `$HOME`. It does
not accept network input and does not run as a daemon. The sharpest edges to
be aware of:

- **Plugin installer fetches code from marketplaces.** Any marketplace you
  register (`ccmcp marketplace add`) is trusted to ship safe plugin source.
  Path containment on bare-string plugin sources is enforced by `withinDir`
  in `internal/install/installer.go`; sibling-directory escapes are rejected.
- **Mutations to `~/.claude.json`** are atomic (temp + rename) and preceded
  by a timestamped backup in `~/.claude-mcp-backups/`. If ccmcp corrupts
  your config, restore from the most recent backup.
- **`--dry-run`** is contractually non-mutating (asserted in tests). If a
  mutating command writes under `--dry-run`, that's a bug — please report.
