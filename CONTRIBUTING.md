# Contributing

Thanks for helping. Cadrei is small on purpose: one program that starts and stops sessions, guards permissions and trust, and keeps its own files consistent. Everything else is the orchestrator's job, guided by the skill. Changes that keep it that way are the easiest to accept.

## Before you start

- **Be kind:** everyone here follows the [code of conduct](CODE_OF_CONDUCT.md).
- **Bugs:** open an issue with what you ran, what you expected and what happened, plus your OS and `cadrei --version`.
- **Features:** open an issue first to talk it through. If the orchestrator can do something on request, it needs no code: many ideas fit better in the skill, or in your own crew (a member, a playbook rule), than in the program.

## Making a change

1. Fork and branch from `main`.
2. **Go version:** `go.mod` declares Go 1.26.0. CI tests with it and with the latest stable release, and releases build with the go.mod version (`.github/workflows/release.yml`).
3. **Dependencies:** the standard library, plus `golang.org/x/term`, `golang.org/x/sys` and `golang.org/x/text` (Unicode normalization for `cadrei allow`). Nothing else without an issue first.
4. Run the checks:
   ```bash
   gofmt -l .
   go vet ./...
   go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...
   go test -race ./...
   go test -race -tags cadreitest ./...
   tests/smoke.sh
   tests/bridge.sh
   ```
   The tests and the smoke test run in a throwaway HOME with a stub `claude` and a private tmux server, so they need no Claude account and never touch your own setup. The `cadreitest` build tag holds the parts only tests use (a fake runtime and test switches); release binaries leave them out. `tests/bridge.sh` installs cadre 0.1.1 from the `v0.1.1` tag and checks that a `git pull` to your last commit keeps that install quiet (see below); it needs `jq` and `python3`, as 0.1.1 did, and tests your last commit, so commit first. CI runs the tests with `-v` and pipes them to `tests/skips.sh`, which fails on a skip that isn't by design: when a test should skip somewhere, add it there with the reason.
5. **Trying your build by hand:** a build from source (`go run ./cmd/cadrei`, or `go build` without a version stamp, at a release tag too) runs only with HOME inside a temporary folder (`/tmp` or `/var/folders`), so trying a change never moves or rewrites your real setup. Its teams run on a tmux server of their own (`tmux -L cadrei-dev`), never the one your own teams use, unless you name one with `CADREI_TMUX_SOCKET`. Run it as `HOME=$(mktemp -d) go run ./cmd/cadrei`. It also refuses a `CADREI_HOME` or `CLAUDE_CONFIG_DIR` outside a temporary folder, so unset them when you try it from inside a cadrei session. Set `CADREI_DEV_REAL_HOME=1` only when you mean to run it on your real setup.
6. Update `CHANGELOG.md` under "Unreleased", and the docs if behaviour changes.
7. Open a pull request describing the problem and the change.

**Releases:** `.goreleaser.yaml` builds static binaries for macOS and Linux (amd64, arm64) with `checksums.txt`, and `packaging/homebrew` makes the tap's formula from them. Check a change to the goreleaser file with `go run github.com/goreleaser/goreleaser/v2@v2.18.2 check`.

## What belongs where

| Change | Place |
|---|---|
| Commands, their help and messages | `cmd/cadrei` |
| Crews, projects, sessions, grants, backups | `internal/...` |
| Anything specific to Claude Code (flags, settings format, hooks, trust, skill location) | `internal/runtime/claude`; a test keeps it out of the rest |
| How members receive and answer work | `protocol.md` |
| How the orchestrator behaves | `skills/cadrei/SKILL.md`, and `orchestrator.md` for its opening prompt |
| What a new crew starts with | `template/` |
| Installation | `install.sh`, `packaging/homebrew` |
| The bridge for 0.1.x installs | `bin/cadre`, `bin/orchestrator-hook.sh`, `skills/cadre/SKILL.md` (see below) |

Personal workflows, project-specific members and house rules belong in a user's own crew, not here.

## Compatibility

A crew travels through git between machines on different cadrei versions, and cadrei writes into files it doesn't own. These rules keep each release safe for the ones before and after it:

- **The crew format.** A change to anything tracked in a crew (a file moved or renamed, a new key a file must have, a new key in the grants file, a new grammar for `projects.yaml`) raises `cadreis.Format` and writes the new number as `FORMAT` in the crew's `cadrei.conf`, in the same commit as its migration. An older cadrei then only reads that crew. A new key that restricts members (a per-member permission mode, a deny) needs a format bump too, since an older version would silently ignore it. Every front matter key in a member file is cadrei's to define, and front matter is read only in a file's first 20 lines.
- **Frozen text.** The grants file's required entries (`settings.Protect` and `settings.FixedSoft`) never change: every version checks a crew from any other against them. New limits go into `FixedDeny` or `CadreiSoft`, which only the build copy holds.
- **Machine notes and archives** carry a version. Raise `localVersion` or `cadreis.ManifestVersion` when their meaning changes; an older cadrei then refuses to write them, or to restore the archive.
- **Hooks in files cadrei doesn't own.** Every hook form a release wrote stays recognized forever (listed above `OrchestratorHook`), or `cadrei uninstall` couldn't find it. The hidden `hook <sub>` names (`orchestrator`, `pre-push`, `session`, `state`, `snapshot`, `team-status`, `guard`, `notify`) are never renamed or dropped; new ones are only added. A hook command runs only a program named `cadrei`.
- **Names.** Names already shipped (folders, files, keys, options, environment variables, JSON fields) keep saying "cadrei". New words people read say "crew"; new names programs read say "crew" only where no existing "cadrei" name sits next to them. Anything new cadrei keeps in `~/.cadrei`, `~/.cadrei-archive`, a crew's top folder or `members/<team>/` starts with a dot, which no crew, team or role name can.
- **Environment switches** such as `CADREI_OFF` are on when set to any non-empty value. Variables cadrei sets in its sessions (`CADREI_MEMBER`, `CADREI_ORCHESTRATOR`, `CADREI_OPENS_IN`, `CADREI_OPENED_FROM`, `CADREI_OPENED_PROJECT`) are its own; the guide says not to rely on them.
- **Sessions** record `@cadrei_version`, so a later cadrei can tell which version started one; none means 0.2.0 or older.
- **`cadrei ls --json`** follows the promise in [For tool builders](docs/tools.md): values only grow within version 1, and a version 2 keeps `--json-version 1` working.
- **Migrations** follow docs/design/go-core.md's Migrations section: the first one, `MoveIn`, was written before it; every one after uses it.

## The bridge for 0.1.x installs

Cadre 0.1.x installed itself as a clone of this repository: its `cadre` command and orchestrator skill are links into the clone, and its SessionStart hook runs `bash <clone>/bin/orchestrator-hook.sh` in every Claude Code session. So `main` keeps three things at those paths through 0.x:

- `bin/orchestrator-hook.sh` always exits 0 and prints nothing, so a `git pull` never breaks anyone's Claude Code sessions;
- `bin/cadre` prints one line, "Cadre is now Cadrei" with the install command, and exits 1;
- `skills/cadre/SKILL.md` is a short skill that only points to that install command (the program embeds `skills/cadrei/`).

Keep them that way: no pass-through to the new program and no install offer. `tests/bridge.sh` checks them.

## License

By sending a contribution, you agree that it's under the [MIT license](LICENSE), like the rest of the code. The brand art isn't open for contributions: the logo, the characters, the lead's terminal art and everything in `docs/brand/` are all rights reserved (see [NOTICE](NOTICE)). If something in them looks wrong, open an issue instead of a pull request.

## Writing style

Plain words and short sentences in messages and docs. No em dashes anywhere, in code, comments or docs.
