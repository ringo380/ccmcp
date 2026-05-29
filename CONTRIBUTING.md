# Contributing to ccmcp

Thanks for your interest in improving ccmcp! This document covers how to build,
test, and submit changes.

By participating you agree to interact respectfully and constructively. Be
excellent to each other.

## Project scope

ccmcp is a Go CLI + Bubble Tea TUI for managing Claude Code MCP servers,
plugins, and per-project overrides. Changes should fit that purpose. If you're
planning a substantial feature, please open an issue first so we can align on
the approach before you invest the time.

## Development setup

You need a recent Go toolchain (see `go.mod` for the minimum version).

```sh
# Build a local test binary (don't overwrite a Homebrew-installed one)
go build -ldflags='-s -w' -o /tmp/ccmcp-test .

# Run the full test suite
go test ./...

# Format and vet before committing
gofmt -w .
go vet ./...

# After adding a dependency you import directly
go mod tidy
```

## Coding guidelines

- **Format and vet must be clean.** Run `gofmt -w` on touched files and
  `go vet ./...` before opening a PR. CI runs `go vet ./...` and the full test
  suite.
- **Match the surrounding code.** Follow the existing naming, structure, and
  comment style of the package you're editing.
- **Add tests for behavior changes.** New logic should come with table tests or
  headless TUI tests (see existing `*_test.go` files for patterns). Tests
  sandbox state via `t.Setenv` — never touch real user config.
- **Keep the README test count in sync.** The "Testing" section of `README.md`
  cites a test total. If you add or remove top-level `Test*` functions, update
  it. The canonical count is:

  ```sh
  grep -rhc "^func Test" --include="*_test.go" . | awk '{s+=$1} END {print s}'
  ```

- **Keep changes focused.** Prefer small, reviewable PRs over sweeping ones, and
  avoid unrelated refactors in the same change.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
`fix:`, `chore:`, `docs:`, `refactor:`, `test:`, etc. Keep the subject in the
imperative mood and explain the "why" in the body when it isn't obvious.

## Developer Certificate of Origin (DCO)

Contributions to ccmcp require a [Developer Certificate of
Origin](https://developercertificate.org/) sign-off. This is a lightweight way
to certify that you wrote the patch or otherwise have the right to submit it
under the project's license — no separate agreement or account is needed.

Add a sign-off to each commit by committing with `-s`:

```sh
git commit -s -m "fix: correct the thing"
```

This appends a line like:

```
Signed-off-by: Your Name <you@example.com>
```

The full DCO text (version 1.1) is reproduced below; the `Signed-off-by` line
means you agree to it.

<details>
<summary>Developer Certificate of Origin 1.1</summary>

```
By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

</details>

## Licensing of contributions (inbound = outbound)

Unless you state otherwise in your contribution, any contribution you submit is
offered under the same [MIT License](LICENSE) that covers the project. You
retain copyright to your contribution.

Note that the project's [trademark and naming policy](TRADEMARK.md) reserves the
"ccmcp" name and brand — contributing code does not grant any rights to that
name.

## Pull request checklist

- [ ] `go test ./...` passes
- [ ] `gofmt -w` and `go vet ./...` are clean
- [ ] Tests added/updated for the change
- [ ] README test count updated if `Test*` functions changed
- [ ] `CHANGELOG.md` updated under `[Unreleased]` for user-facing changes
- [ ] Commits are signed off (`git commit -s`)
- [ ] Commit messages follow Conventional Commits
