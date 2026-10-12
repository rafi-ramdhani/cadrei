# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org).

## Unreleased

### New

- **Answer members from the orchestrator.** When a member waits on a permission prompt or a question, your orchestrator tells you what it asks, from Claude Code's own record of it. For a prompt, it runs `cadrei answer <code> yes` and Claude Code asks you first, in its own permission prompt: your Yes there is the answer, one keypress. For a question, it shows the options, and you type `! cadrei answer <code> <n>`. `cadrei answer` with no code shows who's waiting, with the line that answers each. `yes` only ever picks the plain one-time "Yes", and an answer whose prompt changed answers nothing. Prompts cadrei can't check on the member's screen (anything but a command or a file to write or edit, like a page fetch, an MCP tool or a plan) are left to the member's window.
- **Grants offered first.** When auto mode or a rule blocks a member, your orchestrator asks you right there whether to let it run that once, and gives the one-time grant on your yes, instead of sending you to the member's window.
- **Members ask for your yes in their window.** A member that needs your own yes (its instructions say so, or it touches personal data, money or something it can't undo) asks with a question in its window and tells your orchestrator, which shows you the question to answer. A message from your orchestrator is never your yes to a member.
- **`cadrei compact <team>/<role> [project]`** compacts an idle member's conversation.
- A code of conduct, the Contributor Covenant 2.1, linked from the contributing guide.

### Changed

- **Cadrei sessions can't type into tmux windows.** The guard in members, and now in your orchestrator too, refuses any `tmux` command outside a list of ones that only read (`capture-pane`, `list-*`, `has-session`, `show-options`, `show-environment`, `display-message -p`, none with a format's `#(`), on any tmux server, and `osascript` keystrokes: text typed into a window reads as yours. A command that only mentions tmux or `cadrei answer` still runs. Your orchestrator compacts a member with `cadrei compact` instead. In your orchestrator, the guard lets uninstall and restore through, and lets every command run when cadrei is gone, so an uninstalled cadrei doesn't lock your chat. [SECURITY.md](SECURITY.md) says what it can and can't stop.
- `cadrei ls` shows a waiting member's `cadrei answer` line.
- The README's demo GIF is a third of its old size, so it loads faster on a phone.

### For tool builders

- `cadrei ls --json` (version 1): a waiting member has an optional `request` object, with `code`, `kind` (`approval` or `question`), `tool`, `summary`, `options` and `multi`. It's the member's request as Claude Code recorded it, shown as is: never consent. See [For tool builders](docs/tools.md).
- `cadrei hook guard` also runs in the orchestrator cadrei opens. Its answer is nothing (exit 0), a refusal (exit 2, the reason on stderr), or one "ask" object on stdout (exit 0). It never allows.

### Fixed

- **A first run that isn't logged in to Claude Code**: if Claude Code was set up before but is logged out now, it skipped its login screen, so your orchestrator's first message only got "Not logged in" back and never greeted you. Now the setup logs you in first, with `claude auth login` right in your terminal (it opens your browser), and opens your orchestrator once that's done. If the login doesn't go through, your crew is still made, and cadrei says to log in and run `cadrei` again. `cadrei --check` says how to log in too.
- **Every later `cadrei` checks your login too**, in the background while it gets ready (the login question waits 3 seconds at most). If Claude Code says you're logged out, it logs you in the same way before your orchestrator opens. If it can't tell (offline, or an older Claude Code), it opens as before. If your orchestrator is open already, cadrei switches to it without waiting for the check. Your orchestrator now always uses the Claude Code config folder (`CLAUDE_CONFIG_DIR`) of the terminal you run `cadrei` from, where the login was checked, even in a tmux server started with another one. Members use the one of whatever starts them, so the members your orchestrator starts share its folder, with its login and trusted folders.
- `cadrei up` says when a member it started is stuck on Claude Code's login or setup screen, and how to open its window to answer it, instead of only "Started".
- The first run's checks no longer show a stray blank line under git.
- **The members' guard no longer blocks commands that only mention uninstall or restore.** It used to refuse any command with those words next to a shell's name or a `$`, so a member couldn't `grep` for them, write code with a `restore()` in it, or open a pull request that talks about them. It now reads a command the way the shell does: running cadrei's `uninstall` or `restore` is still refused in every form it was before (`env`, `sh -c`, `eval`, a full path, quotes, variables, a pipe or heredoc into a shell), while text sent to a program that never runs it goes through.

## 0.2.1 - 2026-10-11

Your folder of teams, members and settings is now called your **crew** everywhere (the folder layout still says cadrei: `~/.cadrei/<name>`, `cadrei.conf`).

### New

- **A friendlier first run**: one screen checks your tools (one line when all is well), shows the whole plan, and asks for one key. Enter sets it all up and opens your orchestrator, `p` keeps it in the plain terminal, `r` restores a crew from GitHub, `q` quits and changes nothing. In a repo with its own Claude Code settings, skills or agents, the repo isn't trusted unless you press `a`. A new crew's folder is trusted in Claude Code, so your orchestrator opens without a prompt.
- **A mascot and the brand's colors**: a tiny mascot tops the first run and `cadrei --version` in a terminal with 256 colors or more, your crew's name shows in clay, and cadrei's tmux sessions have a green status bar. Plain mode, `NO_COLOR` and pipes stay as they were.
- **Model and effort per member**: `model:` and `effort:` at the top of a member's file, `MODEL=` and `EFFORT=` in `cadrei.conf` for the whole crew, or `cadrei up ... --model <model> --effort <level>` for one start. `cadrei ls` shows them, and your orchestrator keeps its own model unless you set `ORCHESTRATOR_MODEL`.
- **Plug back in**: after `cadrei uninstall`, or after deleting `~/.cadrei/config` and `~/.cadrei/framework`, the next `cadrei` finds your crews and plugs them back in. Each crew now keeps everything about itself: where its projects are on this machine and its orchestrator's chat (in `.claude/local/`, which git ignores), and the tmux choice (`ORCHESTRATOR` in `cadrei.conf`).
- **Delete everything**: `cadrei uninstall --all` removes cadrei for good: every crew, cadrei's settings and state, and every archive, after a dry run (`--dry-run`) that warns about crews whose backup is behind, and only once you type `delete everything` (`--yes` isn't enough; scripts pass `--confirm "delete everything"`). Your projects' folders are never deleted. Plain `cadrei uninstall` still keeps your crews.
- **Newer crews are safe from older versions**: a crew whose tracked `cadrei.conf` has a `FORMAT` above 1 (written by a later cadrei that changes the layout) is only read by this one: opening its orchestrator, starting members, grants, projects and the tmux setting are refused with a note to upgrade, while `ls`, `attach`, `stop`, `--check` and `uninstall` still work. Notes in `.claude/local/` and archives written by a newer cadrei are refused the same way.
- **Team members are kept away from uninstall and restore in every form**: a check that runs before each of a member's shell commands (`cadrei hook guard`) refuses them through `env`, `sh -c`, any path or quoting, beside the deny rules and the commands' own checks. Uninstalls also look for your teams on every tmux server they were started on, whatever `CADREI_TMUX_SOCKET` or `TMUX_TMPDIR` say. [SECURITY.md](SECURITY.md) says what each layer can and can't stop.
- A member can't be called `orchestrator`, a name kept for your orchestrator.
- **Your orchestrator hears when a member goes quiet**: if it sends a member a task without asking to hear when the member goes idle, a note from cadrei right after the send has it ask for that notice. The note never blocks or changes a message.
- A member whose reply is denied sends one short status instead, naming only the rule, and your orchestrator tells you which window to open. It never reads the blocked content from the member's window, chat record or files. A member that goes idle without replying gets at most one request for a short status, and then your orchestrator sends you to its window instead of reading it.
- `cadrei help advanced` starts with the everyday commands and every option they take, and lists every option of `project add` and `project link`.
- **Uninstall removes a Homebrew install too**: with Homebrew, every `cadrei uninstall` ends by running `brew uninstall cadrei`, as its plan's last line, after all of cadrei's own cleanup and under the same yes. `--keep-program` keeps the program. Other installs still end with the `rm` to run.
- **Archive and restore**: `cadrei uninstall --archive` checks each crew's backup, offers to push it, and packs your crews and settings away in `~/.cadrei-archive/<date-time>/`, so a reinstall is a true first run. `cadrei restore`, or `o` on that first run, brings it all back to the same places, chats included. A run that stopped halfway finishes when you run it again.
- **Your orchestrator opens in tmux** by default, so it keeps going when you close the window. `cadrei tmux on|off [--here]` changes that, and `--no-tmux`, `--detach` and `--fresh` work for one run.
- **The orchestrator greets you**: by name and crew, with the first thing worth knowing (a stuck member, leftover one-time permissions, a missing project). It gets a live snapshot of your setup when it opens, and a few read-only commands are allowed for it without a prompt.
- **A team status line** ends your orchestrator's replies while someone is running: `Team: engineer working 4m · reviewer idle`. Say "hide the team line" to turn it off for your crew.
- **`cadrei ls` is a status screen** showing what each member is doing: working, waiting on you, idle, starting, or stopped by Claude's safeguards. `cadrei ls --json` has the same states.
- **Safeguard stops**: when Claude's safeguards stop a member's reply, `cadrei up`, `cadrei ls`, the status line and your orchestrator tell you, with how to fix it in its window or start it fresh.
- **`cadrei new <name>`** makes a crew. `cadrei init` still works.
- **`cadrei hook on|off|skip`** manages the every-chat hook, and your orchestrator offers it once, after your first finished task.
- **Did you mean**: a mistyped command gets a suggestion, and `cadrei project` alone lists the project commands.

### Changed

- Every command speaks in one voice: errors say what happened and then how to fix it, checks use ✓, ! and ✗ (plain `+`, `!` and `x` when the output isn't a terminal), and paths show with `~`. Colors follow your terminal's theme, and `NO_COLOR` turns them off.
- `cadrei help` is shorter, with examples of what to ask your orchestrator, and `cadrei help advanced` has Crews and Setup groups.
- Hook commands cadrei writes do nothing when the program is gone, so an uninstall without `cadrei uninstall` leaves no errors behind.
- `cadrei project sync` and a GitHub restore trust only clones without their own Claude Code settings.
- The member protocol asks members never to write a real-looking secret, not even a fake one.
- Sessions cadrei starts from inside a Claude Code chat no longer take that chat's own session markers, so a member keeps its transcript and its own effort.
- `cadrei stop`, `uninstall` and `uninstall --archive` act only on sessions your crews recorded as theirs, never on another setup's sessions on the same tmux server. Every uninstall stops before changing anything when tmux can't say which teams run.
- Your orchestrator's greeting names a waiting or stopped member by its own team and project, with the command to open it.
- The README is short now and points to [cadrei.dev](https://cadrei.dev) for the docs. The new [command reference](docs/commands.md) lists every command and option.
- The brand art (the logo, the characters and the lead's terminal art, in `mascot/` and `docs/brand/`) is all rights reserved, not covered by the MIT license. [NOTICE](NOTICE) lists its files, and release archives carry it. The code stays MIT. The Homebrew formula's license says the same: MIT, plus terms Homebrew can't name.

### Security

- Team members can't edit a crew's `CLAUDE.md`, `CLAUDE.local.md` or `.mcp.json`, can't read or write `~/.cadrei-archive`, and can't run `cadrei new`, `cadrei uninstall` (any form) or `cadrei restore`, even with their own variables emptied: cadrei also knows them by their tmux window.
- The health check warns about `CLAUDE.md`, `CLAUDE.local.md`, `.mcp.json` and Claude Code settings at the top of a crew, since your orchestrator loads them.
- The pre-push check refuses this machine's own files (`.claude/local/`) if they were ever committed, and a password or token in a URL. A project's repo URL is stored and shown without its password or token.
- `cadrei project add` and `project link` don't trust a folder whose own Claude Code settings, or skills, agents or commands in any of its folders (for a worktree or a submodule, in its main checkout or superproject too), would run for your team, links among them included; trust it yourself with `cadrei project trust` once you've looked.
- An archive or restore refuses crew names that would climb out of their folder, and archives whose folders are links. A restore goes back to a linked place only for an archive made on this machine, needs `~/.cadrei` empty, keeps the config it would replace, names any model settings it brings back, and never deletes a crew's only whole copy, or anything it didn't make, while moving it. A restore of an archive from elsewhere names, before you agree, anything in a crew's git settings or layout that could run a program, and cadrei's own git commands in a crew run none of its hooks and use only the crew's own repo, never one in a folder above it. A restore won't bring back a `.git` into `~/.cadrei`, and names a crew that has no repo of its own.

### For tool builders

`cadrei ls --json` is now a documented public interface, version 1, with a compatibility promise: within a version, fields, line types and values in a field's list are only added, and a value your tool doesn't know has a safe meaning. See [For tool builders](docs/tools.md).

- New: `--json-version 1` pins the version a tool reads; any other number is refused.
- `session` and a member's `name` are keys, not to be split on `-`; `cadrei`, `cadreis` and `other_cadreis` keep their names for all of version 1.
- New: `cadrei ls --json --watch` streams the same state as JSON lines, a whole snapshot first and then each change. A snapshot of a crew that's gone carries `missing`, and a `team_started` line carries `legacy` for a team from cadre 0.1.x.
- Added at the top level: `orchestrator_opens_in` and `hook`. Added to each running member: `state`, `since` and `waiting_for`.
- Added to each running member: `no_activity_since` (working or waiting, with no activity for 10 minutes or more) and `turn_error` (idle after a turn that ended in an error), and `model`, `effort`, `model_from` and `effort_from`. Added to the crew: `model` and `effort`.
- `cadrei ls --json` never asks which crew; with a project in several crews it fails as it does without a terminal.
- A team from cadre 0.1.x counts as your default crew's: it shows in that crew's `running` with `legacy: true`, and with `--all` under `legacy` too.
- `cadrei ls --json --all` with no crews at all exits 0, with `"cadreis": []` and `orchestrator_opens_in` set to `terminal`.

### Upgrading from 0.2.0

Nothing to do. On its first run, 0.2.1 moves each crew's project places and orchestrator chat from `~/.cadrei/config` into the crew, adds `/.claude/local/` to the crew's `.gitignore`, and says so once. A crew you already have keeps opening its orchestrator in the terminal; run `cadrei tmux on` to move it to tmux.

Once 0.2.1 has run, 0.2.0 can't find your projects or pick up your orchestrator's chat: they're in each crew's `.claude/local/` now, where 0.2.0 doesn't look. Going back to 0.2.1 brings them back. `cadrei help`, `cadrei --version` and `cadrei --check` don't move anything, so you can try those first. A project you link again while on 0.2.0 loses to the crew's own copy, so link it after upgrading, not before.

## 0.2.0 - 2026-10-09

**Cadre is now Cadrei** (pronounced CAD-ray), since other AI agent tools already use the name Cadre. Cadrei is one program, written in Go, that you install with Homebrew or a small download script. Run `cadrei`, then talk to the orchestrator. **This is a breaking upgrade**: read "Upgrading from 0.1.x" below before you install.

### Breaking changes

| Change | What to know |
|---|---|
| Cadrei is a program, not a git clone | `git pull` in `projects/cadre` no longer upgrades. Upgrade with `brew upgrade cadrei`, or run `install.sh` again. |
| Cadreis live in `~/.cadrei/<name>` | 0.2.0 does not open a 0.1.x cadre. The orchestrator brings it in when you ask ("bring in my old cadre from <path>"). Projects stay where they are, and the old folder and `~/.config/cadre` are left as they are. |
| Personas are now members | `personas/` becomes `members/` and `persona-settings.json` becomes `member-settings.json` (bringing an old cadre in writes the new names). `CADRE_PERSONA` becomes `CADREI_MEMBER`. `cadrei ls --json` lists `members`. |
| Team and member names | They may use letters, digits, `-` and `_`. A team or member with a dot in its name (allowed in 0.1.x, for example `ml.ops`) cannot be started; rename its folder (the orchestrator does it on request, and does it when it brings in an old cadre). |
| Old command names | Each prints one line pointing to the new way and exits 1: `cadrei down` says to use `cadrei stop`, `cadrei add project` to ask the orchestrator or use `cadrei project add`, and so on. |
| `cadrei ls` output | A new status screen. Scripts use `cadrei ls --json`. |
| `cadrei.conf` | Read as `KEY=VALUE` lines. Shell code in it is ignored with a warning. |
| Projects | `projects.yaml` holds each project's repo, team and about, and no local paths. Where each project is on a machine is kept in `~/.cadrei/config/places/`. New clones go to a projects folder you choose (`~/Developer` is suggested), not into the cadrei. |
| Session names | They gain the cadrei's name. Sessions started by 0.1.x show as legacy until restarted. |
| The orchestrator hook | It runs `<cadrei program> hook orchestrator`. The first run offers to replace the 0.1.x hook. |
| Python and jq | No longer needed. |

### New

- **Plain `cadrei`** opens the orchestrator in your terminal, in your cadrei's folder, with the cadrei's permission mode. `cadrei --tmux [--detach]` runs it in tmux instead, to come back to later or over SSH. One orchestrator runs per cadrei: a second `cadrei` says where it is open, or attaches to it in tmux.
- **First run**: a new cadrei with a starter `dev` team (an engineer and a reviewer), or a restore from GitHub; offers to add the folder you are in as a project, link the skill and add the hook, each on your yes; then a short greeting and the orchestrator.
- **Health check** on every `cadrei`, silent unless something is wrong; `cadrei --check` runs the full one. It recognizes 0.1.x leftovers and offers to point them at the new program.
- **Projects across machines**: `cadrei project add | link | unlink | sync | trust | path | dir`. Missing projects are marked in `cadrei ls`, and the orchestrator offers to clone them again, link their new folder or unlink them. Cadrei never unlinks a project by itself, and unlinking never touches the folder.
- **Backup and restore**: ask the orchestrator to back up your cadrei to a private GitHub repository; the first run on a new machine restores it, shows what it brings in, and clones its projects after a yes. `teams/` is tracked, so members' work there travels too. A pre-push hook refuses to push files that look like credentials, and files over 50 MB.
- **Conversations resume**: members and the orchestrator continue their last conversation when started again. `--fresh` on `cadrei`, `cadrei up` and `cadrei stop` starts a new one.
- **`cadrei stop`** stops a team or a member; with no team, every member of this cadrei after a confirmation (`--all`: every cadrei). It never stops the orchestrator.
- **`cadrei allow`** grants narrow permissions to every member, the channel for consent you give in the orchestrator: `add <rule>`, `add --auto "<sentence>"`, `--once`, `list`, `remove`. It refuses blanket rules and anything that reaches cadrei's own files, warns about other wildcards, and commits every change. Members start with a validated, read-only copy of the grants, plus fixed rules that keep them off cadrei's own files.
- **Trust**: registered project folders are marked trusted in Claude Code, so members start there without the trust prompt (`--no-trust` skips it).
- **Several cadreis**: `cadrei init <name>` and `cadrei use <name>`. A command acts on the cadrei whose folder or project you are in, else the default. Configuration is read only from `~/.cadrei` or `CADREI_HOME`.
- **`cadrei uninstall`** shows its plan and asks. It removes the skill link, the hook, each cadrei's pre-push hook, `~/.cadrei/config` and `~/.cadrei/framework`, keeps every cadrei and project, and ends with the command that removes the program.
- **Install**: `brew install rafi-ramdhani/cadrei/cadrei`, or `install.sh`, which downloads the release binary, checks it against `checksums.txt` and places it in `~/.local/bin`.

### Security

- Members cannot grant permissions, stop the whole cadrei, uninstall, create or switch cadreis, or add, link, unlink or trust projects. Sessions started by 0.1.x carry `CADRE_PERSONA`, which counts the same way; this alias goes away in a later release.
- `cadrei.conf` is parsed, never run, and no configuration is read from the folder you happen to be in.
- Edits to `~/.claude.json` and `~/.claude/settings.json` keep every other key and their order, keep a backup, are written atomically, and are skipped on any doubt.
- 0.1.x ran its Python helpers with the current folder on the module path, so a folder holding a file such as `json.py` could run code as you whenever you ran `cadre` there. 0.2.0 has no Python helpers.

### Upgrading from 0.1.x

Nothing is migrated by code, and your old cadre is only read.

1. **Stop your teams.** In 0.1.x: `cadre down --all`. Or after the upgrade: `cadrei stop`, which also stops sessions 0.1.x started.
2. **Install 0.2.0**: `brew install rafi-ramdhani/cadrei/cadrei`, or `curl -fsSL --proto '=https' https://raw.githubusercontent.com/rafi-ramdhani/cadrei/main/install.sh | sh`.
3. **Run `cadrei`.** It creates a new cadrei with the starter team and offers to link the cadrei skill, point the orchestrator hook at the new program, and remove the old `cadre` command and skill links. When it finds your 0.1.x cadre, it prints its path.
4. **Tell the orchestrator: "bring in my old cadre from <path>".** It reads the old members, playbook, house rules, projects, permission mode and lasting grants, shows you one plan, and copies them on your yes. Projects are linked where they are. Grants go through `cadrei allow`, so a rule it refuses is reported, not forced. One-time grants are not carried over.
5. **Start teams again** as you need them. Members start new conversations under the new names.

The 0.1.x clone stays where it was. Running `git pull` in it is harmless: its `cadre` command and its skill then point you to Cadrei, and its hook does nothing.

If you changed files in your 0.1.x clone (for example `bin/cadre`, `bin/orchestrator-hook.sh` or `skills/cadre/`), `git pull --ff-only` stops and changes nothing, which is safe. To finish: keep your edits (`git stash` for uncommitted changes, or `git branch my-0.1-edits` for commits), then run `git reset --hard origin/main` in the clone. Or skip the pull: install Cadrei with Homebrew, and its first run offers to point the command, skill and hook away from the clone. The same holds for edits in any file the update changes, not only those three.

## 0.1.1 - 2026-10-08

- `cadre init`, `cadre add` and the installer work on a machine without a git identity: the change is left uncommitted with a note instead of failing.
- Clearer control flow in `bin/cadre` (no `A && B || C`), lint-clean at shellcheck's strictest level.
- The smoke test uses its own git config.

## 0.1.0 - 2026-10-08

First public release.

- `install.sh`: one command from nothing to a working cadre; `--from` restores an existing cadre and its projects on a new machine.
- `cadre` command: `init`, `use`, `add project|team|persona`, `up`, `down`, `attach`, `ls`, `projects`, `path`, `sync`, `version`.
- Per-project team sessions (`cadre up dev my-app` gives `dev-my-app-<role>`).
- Project registry (`projects.yaml`) with projects cloned into `projects/`.
- Shared persona protocol, orchestrator skill, optional SessionStart orchestrator hook.
- Starter template with a dev team and a research team.
- End-to-end smoke test and CI on macOS and Linux.
