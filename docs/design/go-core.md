# Cadrei 0.2.0 in Go: package layout and core design

Status: draft, written before Go was installed. Section N is read and
folded in; parts marked **(O)** wait for section O.

## Goals

- One static binary, `cadrei`, with no Python, jq or shell helpers at run time.
- The behaviour the bash 0.2.0 work settled after nine review rounds, kept
  exactly where the spec does not change it. The bug classes those reviews
  kept finding (quoting into tmux and shells, `python3 -I`, escapes, bash 3.2,
  `set -e`, tab-separated parsing) should be impossible by construction.
- `tests/smoke.sh` stays the black-box acceptance suite; Go unit tests cover
  the logic beneath it.

## Layout

O.1 fixes the package names; as built in O-T2: `internal/paths`, `fsx`,
`jsonx` (with json_edit's operations and the hook matcher), `settings`
(validation, export, fingerprints and grants), `allow` (the checker, with the
glob reader inside it), and `shellwords` (word splitting for the checker and
the hook matcher). The sketch below is the earlier plan.

```
cmd/cadrei/main.go          flags, dispatch, exit codes; nothing else
internal/
  cli/         command table (visible, advanced, old names), help text
  paths/       HOME, config and cache folders, physical paths (realpath)
  fsx/         atomic write, mkdir lock with stale takeover, mode and owner checks
  jsonx/       ordered JSON tree with raw leaves; strict decode (no duplicate keys)
  jsonedit/    safe edits of ~/.claude.json and settings.json (trust, hook, unhook)
  psettings/   member settings: validate, export, fingerprints
  allow/       rule checks: tables, command checks, path checks, --auto checks
  shellwords/  POSIX word splitting and operator detection for Bash(...) rules
  glob/        gitignore-style segment matcher and reaches()
  tmux/        exact targets, options, sessions and windows, argv commands only
  sessions/    names, attribution (@cadrei_home), legacy, start, stop, ls data
  conf/        cadrei.conf parsed as KEY=VALUE, never sourced (N.1)
  home/        the ~/.cadrei layout: config/, framework/, <name>/
  cadreis/      the cadrei list, reserved names, resolution (N.3), and
               what cadrei recognizes of 0.1.x (read only)
  registry/    projects.yaml: projects linked by path, the projects folder
  orchestrator/ plain cadrei, its lock, --tmux                      (M-T2)
  health/      fast and full checks                                (M-T3)
  update/, uninstall/, install/   by install kind, Homebrew         (M-T4)
  hooks/       SessionStart and statusLine as cadrei subcommands    (O)
  assets/      go:embed of skill, protocol.md and template/         (O)
testdata/
  allow/       regression inputs from every review (see Testing)
  jsonedit/    config fixtures (surrogates, symlinks, odd shapes)
tests/smoke.sh               acceptance suite, unchanged harness
```

Only `cmd/cadrei` and `internal/cli` know about argv. Every other package
takes typed values and returns errors with the user-facing message, so the
tests call them directly.

Dependencies, kept small and vendored:
- `golang.org/x/text` for NFKC (`unicode/norm`) and case folding (`cases.Fold`).
- `mvdan.cc/sh/v3/syntax` is an option for `shellwords` (see below).
- No CLI framework: a hand-written dispatch table, so the advanced commands
  and old names stay one table that `help` and `help advanced` print from.

## The runtime boundary

Built in O-T4, trimmed in O-T5b:
- `internal/runtime` holds the interface (`Runtime`, `Capabilities`, `LaunchSpec`, `Command`, `PermissionOps`, `TrustOps`), the registry, the default runtime and `Usable` (fixed denies, messaging, permission mode). Claude Code is the only runtime and there is no user-facing choice (no `.runtime` files, no `RUNTIME` or `ORCHESTRATOR_RUNTIME`); a test build alone may name the fake with `CADREI_TEST_RUNTIME` (`cmd/cadrei/runtime_cadreitest.go`).
- `internal/runtime/claude` is the only adapter, and the only code that names Claude Code: its command and flags, `.claude` folders, `~/.claude.json` trust and hooks. It holds `settings` and `allow` (moved there) and the trust and hook edits that used to be in `jsonx`, which is now a generic editor.
- `internal/runtime/fake` (`-tags cadreitest` only) is the test adapter.
- `session.Up` runs what `Launch` returns, as argv with `-e` environment, never through a shell. The prompt goes into the runtime's `BuildDir`.
- A Go test parses every other source file and fails on Claude Code names in string literals.
- Operations of later steps (health, hooks and instructions, sessions, context) join the interface in those steps.

## First run and the health check

Built in O-T5c (`cmd/cadrei/setup.go`, `health.go`, `hook.go`):
- **First run**: plain `cadrei` with no cadrei asks "new or restore". New asks a name (the login name suggested), creates `~/.cadrei/<name>` with the starter team (dev: engineer, reviewer) as the default, and offers to link the git repository the user is in. Then it offers the hook, runs the full health check (which offers the skill link), prints the greeting and opens the orchestrator. Restore is O-T6b. Without a terminal it says what to run and changes nothing.
- **Health check** on every plain `cadrei`, silent unless something is wrong. Fast checks look only at files and `PATH`; the full check (first run, a version change recorded in `state.json`, a fast finding, `cadrei --check`) also runs the runtime (`Health(full)`: `--version`, and `auth status` from the version whose docs list it). Each finding has a fix; the skill link and the hook are fixed only after a yes, and a kept hook is remembered per pair of programs. Fatal: the runtime or git missing.
- **Framework folder**: `~/.cadrei/framework` holds the skill written out from the binary, with `VERSION`; it is rewritten when the version differs (always, for a build from source). The skill link (`InstructionOps`) points at it; the hook (`HookOps`) runs `framework.Binary()`, the Homebrew opt path rather than the Cellar one.
- **`cadrei hook orchestrator`** (hidden) prints the runtime's hook answer with `orchestrator.md`, and nothing under `CADREI_MEMBER`, `CADREI_OFF` or `CADREI_ORCHESTRATOR`. It never fails.

## Backup and restore

Built in O-T6b (`internal/backup`, `cmd/cadrei/setup.go`):
- **Restore** is the first run's other choice: the repository (owner/repo or a URL) is cloned into `~/.cadrei/<name>` (the repository's name without `cadrei-`, confirmed by the user), refused and removed when it has no `members/`, made the default, and given the pre-push guard; then `project sync` asks for the projects folder when needed, clones, records places and trusts.
- **The pre-push guard**: `.git/hooks/pre-push` in each cadrei (written on init and restore, and kept on every plain `cadrei`; a hook of the user's own is left alone and reported) runs `cadrei hook pre-push`. It reads git's ref updates, lists every object the push carries that no remote has (`rev-list --objects <sha> --not --remotes`), and through one `cat-file --batch` checks names (`.credentials.json`, `.env` and `.env.*` but templates, private keys, key stores), sizes over 50 MB, and contents up to 8 MB for tokens (`sk-ant-`, `ghp_`, `github_pat_`, `gh[ousr]_`, private key headers). A finding, or any error, stops the push.
- **The cadrei's `.gitignore`** tracks `teams/` and leaves out dependency folders, build output and logs.
- **Backing up** is the orchestrator's job (the skill): a private `<user>/cadrei-<name>` with `gh repo create`, only after the user's yes, then a push after each commit. No cadrei command is involved.

## Data model (section N)

```
~/.cadrei/config/{default, projects-dir, member-settings.sha256, state.json}
~/.cadrei/framework/            install.sh installs only
~/.cadrei/<name>/               one cadrei, its own git repository
```

- `home.Root()` is `~/.cadrei`. Every path below it is built by one function
  each (`home.Config("default")`, `home.Cadrei(name)`, `home.Build(name)`), so
  no package concatenates paths by hand.
- **The cadrei list** is the directory listing of `~/.cadrei` (entries with
  `members/`, minus the reserved `config`, `framework`, dot names and
  symlinks). Cadreis live only there. A cadrei's name is its folder's
  basename, compared case-insensitively for clashes (I-T1 review).
- **Resolution** (`cadreis.Resolve(cwd)`) returns the cadrei, how it was found
  and the default:
  1. `CADREI_HOME`;
  2. cwd inside `~/.cadrei/<name>/`;
  3. cwd inside a linked project (deepest match, by physical path); several
     linking cadreis means a prompt with a terminal and a refusal without one;
  4. the default from `config/default`.

  Configuration is only ever read from `~/.cadrei/` or `CADREI_HOME`, never
  from a folder the user is in.
- **Coming from 0.1.x** (O-T6e): cadrei never changes a 0.1.x cadre or
  `~/.config/cadre`. It reads `~/.config/cadre/home` for one line on the
  first run, and recognizes an old cadre's top folder (`personas/` and
  `projects.yaml`, not a known cadrei: `cadreis.OldCadre`) to point to the
  bring-in and to refuse linking or trusting it. Bringing an old cadre in is
  the orchestrator's job, guided by the skill.
- **`cadrei.conf`** (`conf.Parse`) reads `KEY=VALUE` lines, `#` comments and
  optional single or double quotes, with no expansion. Any other line is
  ignored with a warning naming it. Only `PERMISSION_MODE` is read (for the
  members and the orchestrator alike), so a stray key never changes
  behaviour.
- **The registry** (`projects.yaml`) keeps its flat format (written by cadrei,
  read by the orchestrator): each project's `repo`, `team` and `about`, and
  no local path, so it works on any machine. A `path` line from an older
  build or another machine is not read.
- **Places** (`cadreis.Place`, O-T6a): `~/.cadrei/config/places/<cadrei>.json`
  maps each project to its folder on this machine (`~/...` under home,
  absolute otherwise), written under the config lock. `project add` and
  `--path` record it, `project link` changes it, `project unlink` removes the
  entry and the place (never the folder), and `project sync` clones what has
  no folder here, to the recorded place or into the projects folder, using
  a clone of the same repo already there. A project's state is `present`,
  `not here`, `missing` or `drive` (on a `/Volumes`, `/media`, `/run/media`
  or `/mnt` drive that is not mounted); `up`, `attach` and `project path`
  refuse a project that is not present, saying how to get it back, and
  cadrei never unlinks one by itself.

## Core primitives

### Physical paths (`paths`)

- `Real(p)` resolves symlinks in the longest existing prefix and joins the
  rest, the way Python's `os.path.realpath` does. `filepath.EvalSymlinks`
  fails on missing paths, which `cadrei allow` and `cadreis check` both need.
- Every stored path is physical (I-T1 review): the known-cadreis list, the
  default pointer, `@cadrei_home` and `CADREI_HOME` for members.
- The macOS `/System/Volumes/Data` firmlink prefix is stripped before
  comparing.

### Atomic writes and locks (`fsx`) (built in O-T2; see Known pitfalls for the lock)

- `WriteAtomic(target, data, mode)`: a temp file in the target's own (real)
  folder, `fchmod`, write, `fsync`, `rename`. Shared by every writer.
- `Lock(dir)`: `mkdir` lock (macOS has no `flock(1)`, and mkdir works on
  every filesystem cadrei meets).
  - A lock older than 60 s is taken over by renaming it to a unique name
    (`.stale-<pid>-<ns>`) and retrying the acquire; it is never renamed back
    (PR #10 review).
  - A lock that vanishes between `mkdir` and `stat` is retried, not an error.
  - Release ignores a missing lock.
- Used for: the known-cadreis list, the fingerprint file (machine-wide, I.5),
  `cadrei allow` (per cadrei), the orchestrator lock (M.3, which adds pid,
  start time and command checks).

### JSON that is not ours (`jsonx`, `jsonedit`)

Go's `encoding/json` is not safe for `~/.claude.json` as it stands:
- maps lose key order, so every edit would reorder Claude Code's file;
- a lone surrogate (`"\ud83d"`) decodes to U+FFFD, so a round trip changes
  user data. The bash version hit this as a crash; Go would corrupt instead;
- `<`, `>` and `&` are HTML-escaped unless `SetEscapeHTML(false)`.

Design: `jsonx.Decode` walks `json.Decoder.Token()` into an ordered tree
(`Object` is a slice of key/value pairs), keeping every scalar as its raw
bytes (`json.RawMessage`). Only objects that an edit touches are rebuilt.
Untouched strings are written back byte for byte, so surrogates, escapes and
number formats survive. Duplicate keys are an error for member settings
(security) and kept as they are for Claude Code's files (not ours to judge).

`jsonedit` keeps the bash rules exactly:
- the file is never created (exit 4 when missing);
- unreadable, invalid, an unexpected shape, or owned by another user: left
  untouched (exit 5);
- nothing to change: exit 3, no write and no backup;
- a backup of the original before the first change, never overwritten
  (`.bak-cadrei`); unhook writes its own (`.bak-cadrei-uninstall`);
- the file's mode is kept, and its owner is checked;
- size, mtime and sha256 are re-checked just before the rename; on change,
  retry up to 3 times, then exit 6 with the file untouched;
- a write failure is exit 7 with no temp file left;
- the test hook `CADREI_TEST_JSON_EDIT_HOOK` runs between the write and the
  re-check, as now;
- a symlinked config: the target is edited and the link kept.

Hook detection for unhook parses the hook `command` with `shellwords`, never
`strings.Fields` (the quoted-path bug from PR #4). **(O)** hooks become
`cadrei hook session-start` and friends, so detection matches the command by
the cadrei binary path instead of `bash .../orchestrator-hook.sh`; old entries
are still recognised for upgrades.

### Member settings (`psettings`)

FIXED_DENY gains the N.7 entries, as `Edit` rules only (Claude Code ignores
`Write(path)` rules and warns about them, PR #5 review, so N.7's "the same for
`Write`" is left out):
`Edit(//**/.cadrei/config/**)`, `Edit(//**/.cadrei/framework/**)`,
`Edit(//**/.cadrei/*/.claude/**)`, `Edit(//**/.cadrei/*/cadrei.conf)`,
`Edit(//**/.cadrei/*/members/**)`, `Edit(//**/.cadrei/*/playbook.md)`,
`Edit(//**/.cadrei/*/protocol.md)`, `Edit(//**/.cadrei/*/projects.yaml)` and
`Edit(//**/.cadrei/*/.git/**)`. The soft_deny line from N.7 is added the same
way. As with today's newer entries, a file must hold only the `PROTECT`
entries; the rest are added to every copy.

Otherwise it is a straight port of today's rules: only `permissions.{allow,deny}` and
`autoMode.{allow,soft_deny}`, `"$defaults"` required, `PROTECT` entries
required, `FIXED_DENY` added to every copy, strict decode with duplicate keys
refused, the export written fresh at every start (mode 0400, named by hash),
fingerprints keyed by real path under a machine-wide lock, and the
"changed outside cadrei allow" check against `git show HEAD:` with cadrei's
commit subjects. Git runs as `exec.Command("git", ...)` with argv.

### tmux (`tmux`)

- Every call is `exec.Command("tmux", args...)` with `-L <socket>` when
  `CADREI_TMUX_SOCKET` is set. No shell is ever involved on cadrei's side.
- Targets are always exact: `=session`, `=session:=window`, and `=session:` for
  options. Helpers take a session and a window, never a target string.
- Members start with the command as separate arguments
  (`new-session -d -s S -n R -c DIR -e CADREI_HOME=... -e CADREI_MEMBER=... -- claude --name ...`).
  With more than one argument, tmux 3.0 and newer execs the command directly
  with no shell, and `-e` sets the environment, so paths with quotes, spaces
  or `$` need no quoting at all. That removes the whole `sq()` class.
  - Cost: the user's shell no longer starts first, so a `PATH` set only in
    shell startup files is not applied. cadrei resolves `claude` with
    `exec.LookPath` in its own environment and passes the absolute path.
  - Requires tmux 3.0 (2019). The health check (M-T3) reports an older tmux.
- A start is confirmed by the window existing and its pane not dead, after a
  short wait, as now.

### Bash rule parsing (`shellwords`)

What `cadrei allow` needs from a `Bash(...)` specifier:
- the program word after leading `NAME=value` words, and the words after it;
- whether there is an operator outside quotes (`&& || ; | |& &` and
  newlines), reading escapes in pairs (`\;` is an argument, `\\;` is a
  backslash and then a real `;`);
- whether the program word is computed (`$`, backticks, `(`, `<`, `>`).

Two options:
1. A small hand-written POSIX lexer (single quotes, double quotes with their
   own escapes, backslash escapes outside quotes). It is about 150 lines and
   is fuzz-tested against the regression inputs.
2. `mvdan.cc/sh/v3/syntax`, a full shell parser. It is exact about
   operators, substitutions and redirections, but a parse error must count as
   a refusal, and the dependency is large.

Leaning to (1), because the checks need only the three facts above. Whichever
is chosen, the regression tables decide.

### Glob reading (`glob`)

A port of `seg_regex`, `segments`, `spells` and `reaches` from PR #9:
- `[...]`, `[!...]` and `[^...]` classes, backslash escapes, and `{a,b}`
  read as alternatives;
- `\/` is read as `/` before splitting, and escapes are read in pairs;
- refused before matching: a trailing unpaired backslash, `[:class:]`, nested
  braces, or a `/` inside braces;
- `reaches(rule, target)` matches the target, a folder holding it, or (for a
  folder target ending in `/`) anything inside it, with `**` crossing
  segments, all case-insensitive;
- both forms are checked: the rule as written and with its folders before the
  first wildcard resolved, against each target and its real path.

Go's `regexp` (RE2) has no backreferences or lookbehind; neither is needed
here. `seg_regex` builds RE2 patterns, and the lookbehind in `spells`
becomes a scan for unescaped `*` and `?`.

### The allow checker (`allow`)

Tables move verbatim into `allow/tables.go`: KNOWN, BARE, RUNNERS,
SUBRUNNERS, FROM_FILES, PROTECTED, OUTSIDE, SECRETS, VERSIONED, BLANKET and
AUTO_REFUSED. The checks keep their order and their messages, since smoke and
the skill match on message text. Text is normalised as now (NFKC, format
characters dropped, dashes and spaces made plain, case folded). `--auto` stays
ASCII-only.

Targets from section N (N.7):
- refused, as OUTSIDE entries:
  - `~/.cadrei/config/` and `~/.cadrei/framework/`;
  - for every cadrei under `~/.cadrei`: `.claude/`,
    `cadrei.conf`, `members/`, `playbook.md`, `protocol.md`, `projects.yaml`
    and `.git/`;
- warned: another cadrei's `teams/`;
- allowed: the cadrei's own `teams/`.

`cadrei.conf` is no longer shell code (N.1), but the refusal stays as a
second layer, as N.7 says.

## Migrations

0.2.0 to 0.2.1 had one migration, `cadreis.MoveIn`, written on its own. Before the second one, migrations follow these rules:

- An ordered list of steps, each with an id that never changes, and each idempotent: running it again after it finished, or after it stopped halfway, does nothing more.
- A step that changes anything tracked in a crew is keyed by the crew's `FORMAT`: it runs when the crew's format is below its own, and writes the new `FORMAT` in the same commit as its change, so an older cadrei then only reads that crew. A step that changes only this machine (`~/.cadrei/config`, a crew's `.claude/local`) is recorded by its id in `~/.cadrei/config/state.json`.
- Each says once what it did, the way `MoveIn` does, and never asks.
- Each has its downgrade story written next to it: what an older version sees afterwards, and that it refuses rather than misreads (the crew format, `localVersion`, `ManifestVersion`).
- A step never deletes what it can't put back: it keeps the old copy (as `places.0.2.0.json` does) until a later release removes it on purpose.

## Testing

- **Unit tests, table-driven.** `testdata/allow/*.txt` holds every rule and
  `--auto` input from the reviews and from smoke.sh, one per line:
  `<expect>\t<input>[\t<message fragment>]`, where expect is `refuse`,
  `accept`, `warn` or `secret-warn`. The reviewer's rules*.txt, narrow.txt,
  extra.txt and autos*.txt are imported as they are, with the expectation
  each review recorded.
  - Symlink cases (stow-style `~/.config`, a symlink into the cadrei) are
    built in `t.TempDir()` with a fake HOME.
- **Fuzz tests** (`go test -fuzz`) for `glob`, `shellwords` and `jsonx`:
  - never panic;
  - `jsonx` round trip of an untouched document is byte-identical;
  - a rule that passes never `reaches` a protected target in a brute-force
    check over small generated paths.
- **jsonedit** keeps the race tests through the test hook: one change then
  success, a change on every attempt giving exit 6, plus surrogates, a
  symlinked config, mode 0644 kept, another owner, and an unreadable file.
- **smoke.sh** runs against the built binary from the sessions step on. Its
  harness (fake HOME, stub `claude`, private tmux socket, physical temp root,
  private bare clone) does not change. Sections are updated for the new
  command names and N's layout as each step lands.
- CI: `go vet`, `staticcheck`, `go test -race ./...`, and smoke.sh on macOS
  and Ubuntu.

## Porting order

The remaining tasks are in section 12 of the spec
(`docs/specs/0.2.0-update-allow-trust.md`).

## Known pitfalls carried from the bash reviews

Open in the last bash PR (#11) when it was frozen; the Go code must get them right:
- **Restart commands for another cadrei** (`update`, `allow`) must carry that
  cadrei: `CADREI_HOME=<path> cadrei stop ... && CADREI_HOME=<path> cadrei up ...`,
  or a form that names the cadrei, since the user runs them from anywhere.
- **Claude session names can collide through hyphens, in different tmux
  sessions.** In one cadrei, team `dev` with role `x-y` and team `dev-x` with
  role `y` are both `c-dev-x-y`. Across cadreis, cadrei `a` with team `x` and
  role `y-z` and cadrei `a-x` with team `y` and role `z` are both `a-x-y-z`
  (the reviewer's exp32). The tmux join check does not catch these, since the
  sessions differ. Attribution uses tmux options, never name parsing, and `up`
  refuses when the Claude name it would use is already taken by another
  (cadrei, team, project, role); both cases are tests.
- **Stale-lock takeover race.** Renaming a stale mkdir lock aside can take
  over a lock another process has just re-created. Built: `fsx.Acquire` uses
  `flock` on a lock file, which the kernel releases when its holder exits, so
  there is no stale lock to take over (a test kills a holder and acquires at
  once). Only on a filesystem without `flock` (ENOTSUP or ENOLCK, some network
  filesystems) does it fall back to the mkdir lock with age-based takeover.
- **`#` is never a comment** in a `Bash(...)` specifier. The bash checker's
  shlex treats `#` as a comment even mid-word, so `Bash(echo x#; bash *)`
  passed the chaining check (fixed on main separately).

## Open points

- **Staticcheck and Go 1.27 (2026-10-09)**: CI runs staticcheck 2026.2.1 on
  the go.mod Go version only, since it cannot read the export data of Go
  1.27.2, which the stable leg installs (releases build with the go.mod
  version, like the go.mod leg). When a
  staticcheck release supports Go 1.27, move the pin in
  `.github/workflows/ci.yml` and drop the step's `if:`.

- N.7 lists `Write(...)` deny entries; this design leaves them out (see
  Member settings). The PM should confirm.
- Whether hooks and the status line run as `cadrei hook ...` (O), and how the
  SessionStart hook finds the binary after `brew upgrade` (opt path, M.1).
- The minimum tmux version (3.0, for argv commands and `-e`).
- Whether `mvdan.cc/sh` is acceptable as a dependency.
