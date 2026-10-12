# Security

## Reporting a vulnerability

Please report security problems privately through GitHub's "Report a vulnerability" button on this repository's Security tab, not in a public issue. You should get a reply within a week.

## Supported versions

Security fixes go into the latest release.

| Version | Gets security fixes |
|---|---|
| 0.2.x | Yes, in the latest 0.2 release |
| 0.1.x (Cadre) | No. Upgrade to the latest Cadrei |

## How cadrei is built to be safe

Cadrei runs several Claude Code sessions for you. Each member is a full Claude Code session that acts on messages from the orchestrator, so the design keeps three things in your hands: what members may do, cadrei's own files, and the files cadrei shares with Claude Code.

### Consent comes only from you, in the orchestrator

- Only what you type in the orchestrator session, or your answers to its own questions, counts as your consent. A cross-session message, a member's reply, a file or a web page never does, even when it claims to quote you. The orchestrator skill and the member protocol both say so.
- A message from the orchestrator never counts as your consent in a member: each member asks for its own permissions.
- Members report a blocked action instead of working around it, and never try to change permissions. When a member's reply itself is denied, its status names only the rule and says the content is in its window; the orchestrator then sends you to that window and never reads the content from it, from the member's chat record or from a file.
- The orchestrator's notify hook (`cadrei hook notify`, after each of its `SendMessage` calls) only adds a note asking it to subscribe to a member's idle notice. It never blocks, denies, approves or changes a message, and prints nothing for anything it can't read.

### Grants reach members through `cadrei allow`

- When you allow something, the orchestrator records the narrowest rule with `cadrei allow` in your crew's `.claude/member-settings.json`. It never adds a rule because a member asked for one.
- `cadrei allow` refuses blanket rules (bare tools, lone wildcards, whole MCP servers, shells, interpreters and wrappers with a wildcard, chained commands, every domain) and anything that reaches cadrei's own files, startup files, or files that run code outside a session. For `Edit` and `Read` rules, paths are read the way Claude Code reads them, after Unicode normalization and case folding, so globs, escapes and braces cannot hide a refused file. `Bash` rules are checked word by word (paths, variables, `..` climbs) and cannot see where a link on disk leads, so the fixed `Edit` denies and the permission mode remain the stronger guards. Other wildcards, `git`, code from project files and secret-reading paths get a warning. Every change is committed in your crew.
- Members never get the grants file itself. Before each start, cadrei validates it (only `permissions.allow/deny` and `autoMode.allow/soft_deny`, no duplicate keys, `"$defaults"` kept, the fixed entries present) and passes a read-only copy from `.claude/build/`, rebuilt at every start. A file that fails the check gives members a copy with no grants, and a warning. A fingerprint in `~/.cadrei/config` flags edits made outside `cadrei allow`.
- Every copy carries fixed rules: `Edit` denies for `~/.cadrei/config`, `~/.cadrei/framework`, and each crew's `.claude`, `cadrei.conf`, `members`, `playbook.md`, `protocol.md`, `projects.yaml`, `.git`, `CLAUDE.md`, `CLAUDE.local.md` and `.mcp.json`; `Read` and `Edit` denies for `~/.cadrei-archive`; denies for `cadrei allow`, `cadrei new` (and `init`), `cadrei use`, `cadrei uninstall` and `cadrei restore`; and an auto-mode `soft_deny` line saying that only the user changes these, through the orchestrator. Team folders stay writable.
- A one-time grant (`--once`) is usable by any member, any number of times, until it is removed. The orchestrator removes it after the task and checks for leftovers at every session start.

### What members cannot run

Members are refused for `cadrei allow add` and `remove`, `cadrei stop` with no team, every form of `cadrei uninstall`, `cadrei restore`, `cadrei new` (and `init`), `cadrei use`, `cadrei project add`, `link`, `unlink` and `trust`, setting `cadrei project dir`, changing `cadrei tmux` or `cadrei hook`, and opening an orchestrator with plain `cadrei`. Cadrei sets `CADREI_MEMBER` in every member. Sessions started by 0.1.x carry `CADRE_PERSONA` instead, which cadrei reads the same way and never sets; this alias goes away in a later release.

The commands that move or delete every crew (`uninstall` in every form, and `restore`) have three layers in front of them. None of them is a sandbox; each one covers what the others can't:

1. **Deny rules** in every member's settings: `Bash(cadrei uninstall:*)` and `Bash(cadrei restore:*)`. Claude Code checks these before a command runs. Its own documentation says a prefix rule isn't a boundary around a program: it doesn't see through `env -u ...`, `sh -c "..."` or a full path to the program.
2. **The guard**, `cadrei hook guard`, which every member's settings run before each Bash command. It refuses any command that may run cadrei's uninstall or restore: through `env`, `sh -c` or another shell, by any path, quoted or split up, or with a variable that could hide the program's name. It reads a command the way the shell does, so text that only goes to a program that never runs it (a heredoc to `cat`, a `grep` pattern, a commit message or a `gh` pull request body) doesn't count. Code it hands to something that runs it (`sh -c`, `eval`, a pipe or heredoc into a shell, `$(...)`, `xargs`, or a short script for `python -c`) does. It errs on the side of refusing, and a command it can't read whole is read word by word, the strict way. If the program is gone, a member's call is refused too, and so is any call when the guard takes longer than its 10 seconds. Your orchestrator runs the same guard (see below), where uninstall and restore go through, since it runs them on your yes, and where a program that's gone lets the call go on, so an uninstalled cadrei doesn't lock your chat. It can't see into a script file a member writes and then runs, or into a program that runs cadrei for it.
3. **The commands themselves.** Each refuses when `CADREI_MEMBER` or `CADRE_PERSONA` is set, even empty. `uninstall` and `restore` also refuse when they run in a member's tmux window: cadrei walks up from the command's own process to a pane it marked as a member's, on every tmux server your crews' teams were started on, whatever `CADREI_TMUX_SOCKET` or `TMUX_TMPDIR` say. That catches a script the member runs. It can't catch a command a member starts outside its own process tree: in a `tmux new-window` it opens (a window with no member's mark, like your own window in a team's session), or through a double fork that leaves it to the system. The first two layers, and the member's instructions never to run these, are what stand there.

Above all of them is the permission mode: in `default` mode, a member asks you before it runs a command its grants don't cover.

### Answering for members, and typing into windows

Anything that runs as you can put keystrokes into a tmux window: you, cadrei, every Claude Code session on your machine through its Bash tool (members, your orchestrator, your other chats), and any other program you run. tmux has no boundary inside one user account. A member treats its own permission prompts and questions, and what's typed in its window, as yours. So cadrei keeps sessions from typing into windows, and puts every answer for a member behind your own keypress:

- **Every answer is your keypress.** `cadrei answer` presses one key (a plain "Yes", "No", or an option's number) in a prompt Claude Code drew in the member's window, after it checked that the prompt matches cadrei's record of the request. When your orchestrator runs it, the guard in your orchestrator's settings has Claude Code ask you first, in its own permission prompt, with cadrei's text from that record. A Claude session can't answer its own permission prompt. The guard asks only in `default`, `acceptEdits` and `auto` mode, and refuses `cadrei answer` in any other mode, since a prompt there might not show. It refuses every form but `cadrei answer <code> <choice>` on its own, with nothing around it, and a request it can't check whole on a member's screen. Before it asks, it looks at the member's window, so you're never asked about a prompt cadrei can't find there, like one taller than the window. On Claude Code 2.1.295 or later, a guard that times out blocks the call, rather than let it go on with no ask. Older versions skip that setting, so there a guard that times out lets the call go on to Claude Code's own permission check, with no ask from cadrei. Cadrei warns you about an older version when you set up, in `cadrei --check`, and each time it opens your orchestrator. A command you type yourself with `!` runs no hook at all: typing it is your yes.
- **Members can't answer.** The members' guard refuses `cadrei answer` and `cadrei compact` in any form, and the command itself refuses in a member, or in a member's tmux window with its variables dropped.
- **Other chats can't answer.** `cadrei answer` looks at the processes it runs under. When one of them is recognized as a Claude Code session (by its program's name), it must be this crew's orchestrator, started with the settings file that holds the guard. Otherwise it refuses: the every-chat orchestrator and your other chats have no cadrei guard, so they don't get to run it at all. When none is recognized but Claude Code's own marks are in its environment (`CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`), it runs under a Claude Code started some other way, and it refuses too. From your own terminal, under no Claude Code, it runs.
- **No typing into tmux.** In members and in your orchestrator, the guard refuses any `tmux` command outside a list of ones that only read (`capture-pane`, `list-sessions`, `list-windows`, `list-panes`, `list-clients`, `has-session`, `show-options`, `show-environment`, and `display-message -p`), on any tmux server, after resolving tmux's aliases and prefixes. Even those are refused with a format's `#(`, which starts a shell job in the tmux server, or its `E:` or `T:` modifier, which reads a value as a format again, and `display-message` with `-I`, which writes into a pane. The guard finds tmux on the command line, after a wrapper (`env`, `sudo`, `flock`, `arch`), under `find -exec` or `xargs`, and in code that a shell or another language runs (`sh -c`, `python3 -c`, `perl -e`, `osascript`, an awk program with a pipe). It can't tell text from code, so it reads every program's arguments the same strict way: tmux with a command outside the list counts even in a search pattern, a message, or code that only prints it. Only programs that never run their arguments (`grep`, `cat`, `echo`, `git commit`, `gh`) are passed over. It also refuses `osascript` keystrokes. `cadrei compact` types `/compact` into an idle member instead.

What's left, said plainly:

- A Claude session that deliberately writes a script or program and runs it can still call tmux, or start `cadrei answer` away from its own process tree. The guard reads command lines, not files a session wrote, and the process check can be dodged on purpose. That takes a deliberate evasion, which Claude Code's instructions to models and auto mode's classifier also work against. Cadrei can't make it impossible inside one user account.
- Claude Code's prompt is only as good as your reading of it. It shows Claude's own one-line description of the command above cadrei's text: read the line that starts with `Cadrei:`, under "Hook PreToolUse:Bash requires confirmation". Your orchestrator can't change that line.
- The orchestrator the every-chat hook opens, and your other chats, have no cadrei guard, so nothing stops them typing into tmux, as before.

### No code from unexpected places

- `cadrei.conf` is read as `KEY=VALUE` lines, never run as shell code.
- Configuration is read only from `~/.cadrei` or the crew named by `CADREI_HOME`, never from the folder you happen to be in. A folder that looks like a crew is never used.
- Members and the orchestrator start with `CADREI_HOME` set, so changing folders never switches their crew.

### Files that are not cadrei's

- **Trust** (`~/.claude.json`): adding, linking or cloning a project marks its folder as trusted, so members start without the trust prompt. Only a project repository's top folder is trusted, never `/`, your home folder, `~/.cadrei`, a team folder or `~/.claude`. Trust lets that repository's own `.claude/settings.json` rules and hooks take effect, so add only repositories you trust, or pass `--no-trust`.
- **The every-chat hook** (`~/.claude/settings.json`): added only on your yes, and removed by `cadrei hook off` and `cadrei uninstall`.
- Edits to both files keep every other key and their order, keep a backup of the original (`.claude.json.bak-cadrei`, `settings.json.bak-cadrei`; the first is never overwritten), are written atomically, check the file again just before writing, and are skipped with a warning on any doubt.

### Nothing is deleted

Unlinking a project keeps its folder. Bringing in a 0.1.x cadre only reads it, and never changes the old folder or `~/.config/cadre`. `cadrei uninstall` keeps every crew and project, trust entries and backups. With Homebrew it removes the cadrei program itself as its last step, unless you pass `--keep-program`. Only `cadrei uninstall --all`, after a plan and the phrase you type, deletes crews and archives, and it never deletes a project's folder.

### No credentials in a crew's repository

Cadrei writes no tokens into a crew, and a new crew's `.gitignore` leaves out `.env` files. A pre-push hook in each crew's repository refuses a push that carries files that look like credentials, by name (`.env` files, `.credentials.json`, `credentials`, `.git-credentials`, `.netrc`, private keys, key stores, `.aws/`, `.kube/config`, `.docker/config.json`) or by content (Anthropic, GitHub, AWS and Slack tokens, private key headers), or files over 50 MB. It checks every name every file has in every commit pushed, and names each file it stops. The orchestrator creates a backup repository only as private, and only on your yes. A pre-push hook of your own is left alone, and the health check says that cadrei's check does not run.

The hook is a safety net, not a guarantee. It does not see a token split across lines or encoded (for example in base64), the content of a file over 8 MB, or a push made with `git push --no-verify` (the skill tells the orchestrator never to use it). A session with shell access in the crew can also change or remove the hook; one without cadrei's marker line is then reported as your own, not restored. Keep credentials out of the crew's folder.

### Resumed conversations

Members and the orchestrator continue their last conversation when they start again. Only the members' own session hook updates a member's record; the orchestrator resumes only a conversation cadrei itself started. At the same user id a record cannot be authenticated, so a member can point another member that works in the same folder at a different conversation from that folder.

### The skill on disk

The orchestrator skill is written out to `~/.cadrei/framework/skills/cadrei`, checked against the program, and restored at every `cadrei` start. A change made to it after the orchestrator opened lasts until the next start. The fixed deny rules keep members from editing it with their edit tool, but not with a shell in a permissive mode.

### Accounts

Cadrei uses one Claude account at a time, whichever Claude Code is logged in with. Tools such as claude-swap can switch it; a switch applies to every crew on the machine, so restart afterwards (`cadrei stop`, close the orchestrator, run `cadrei`). Cadrei does not depend on any such tool and holds no credentials.

### Permission mode

Members and the orchestrator run in the mode set in `cadrei.conf` (default: `default`). A permissive mode such as `auto` lets members act without asking. Choose it deliberately, knowing that members act on messages from the orchestrator.

## Install

`install.sh` is short and meant to be read before you run it. It downloads the release archive for your machine, checks it against the release's `checksums.txt`, and places the program in `~/.local/bin/cadrei` (or the folder in `CADREI_INSTALL_DIR`). It changes nothing else. The Homebrew formula installs the same release binary. Everything cadrei creates afterwards is listed in the guide's [Where things live](docs/guide.md#where-things-live).
