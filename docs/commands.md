# Command reference

Most people only ever type one command: `cadrei`. It opens your orchestrator, and from then on you just tell it what you need, in plain words. It runs the commands below for you.

So you don't need anything on this page to use cadrei. It's here for when you want to do something by hand, write a script, or see what your orchestrator ran.

A command acts on the crew of the folder or project you're in, or else on your default crew. `cadrei help` shows the everyday commands, and `cadrei help advanced` shows the rest.

## Open your orchestrator

### `cadrei`

Opens your orchestrator. The first time, it checks your tools, shows its plan and sets everything up when you press Enter. After that, it takes you back to the same chat.

| Option | What it does |
|---|---|
| `--tmux` | open in tmux, this one time |
| `--no-tmux` | open right in this terminal, this one time |
| `--detach` | start your orchestrator in tmux without opening it |
| `--fresh` | start a fresh chat instead of picking up the last one |

```bash
cadrei --no-tmux
```

On the first run's plan, **Enter** sets it all up, **p** does the same but opens in this terminal, **r** restores a crew from GitHub, and **q** quits. **a** adds a repo that has its own Claude Code settings anyway, and **o** shows up when there's an archived setup to bring back.

### `cadrei tmux [on|off] [--here]`

Says where your orchestrator opens, or changes it: `on` for tmux, `off` for this terminal. The change is saved in your crew, so it travels with a backup. Add `--here` to change it for this machine only. It applies the next time you run `cadrei`.

```bash
cadrei tmux off
```

### `cadrei hook [on|off|skip]`

The every-chat hook makes every new Claude Code chat open as your orchestrator. On its own, `cadrei hook` says whether it's on. `on` adds it, `off` removes it, and `skip` records your no when your orchestrator offers it.

```bash
cadrei hook off
```

## Crews

Your crew is your own folder of teams, members and settings, in `~/.cadrei/<name>`. Most people need just one.

### `cadrei new <name>`

Makes another crew in `~/.cadrei/<name>`.

```bash
cadrei new work
```

### `cadrei use <name>`

Makes a crew your default, the one commands use when you're outside every crew and project.

```bash
cadrei use work
```

## Teams and members

### `cadrei up <team|team/role> [project|dir]`

Starts a team, or one member of it, on a project or a folder. Your orchestrator usually does this for you. A member picks up its last chat.

| Option | What it does |
|---|---|
| `--fresh` | start with a fresh chat |
| `--model <model>` | use this model, for this start only |
| `--effort <level>` | use this effort (`low`, `medium`, `high`, `xhigh` or `max`), for this start only |

```bash
cadrei up dev/reviewer my-app --model sonnet
```

### `cadrei stop [team[/role]] [project]`

Stops a team, or one member. With no team, it stops every member of this crew after asking you first. It never stops your orchestrator.

| Option | What it does |
|---|---|
| `--all` | every member of every crew |
| `--yes` | don't ask first |
| `--fresh` | their next start is a fresh chat |

```bash
cadrei stop dev my-app
```

### `cadrei attach [team] [project]`

Opens a running team's windows, so you can watch a member or answer a question it's asking you. With no team, it opens your orchestrator in tmux. Leave again with Ctrl-b then d.

```bash
cadrei attach dev my-app
```

### `cadrei answer [<code> yes|no|<n>]`

Answers a member that's waiting on you: a permission prompt (`yes` or `no`), or a question (the number of your pick). With no code, it shows who's waiting, with the line that answers each. Each request has a short code, so an answer never lands on a prompt that changed in the meantime.

Your orchestrator runs it for you, and Claude Code asks you first, in its own permission prompt: your Yes there is the answer. You can also type it yourself, in your own terminal or as `! cadrei answer 7f3 yes` in your orchestrator. It refuses in a member, and in a chat it recognizes as Claude Code other than the orchestrator cadrei opened.

```text
$ cadrei answer
Waiting on you:

  7f3  The engineer on my-app wants to run a command:
         gh pr merge 42
       Allow it once:  cadrei answer 7f3 yes
       Say no:         cadrei answer 7f3 no

  k2m  The writer in docs asks: Upload resume.pdf to the signing site?
         1. Yes
         2. No
       Answer:         cadrei answer k2m 1

$ cadrei answer 7f3 yes
✓ Allowed once: the engineer on my-app can run gh pr merge 42.
```

`yes` only ever picks the plain "Yes", the one-time allow, never a choice that allows more or changes the member's mode. `no` stops the member's turn: it waits for what to do instead. Some are answered in the member's window: a question that takes more than one pick, a prompt with no plain "Yes", and a prompt cadrei can't check on the member's screen (anything but a command, or a file to write or edit, and a command too long for cadrei's record to hold whole). `cadrei answer` says so.

### `cadrei compact <team>/<role> [project]`

Compacts an idle member's conversation: it types `/compact` into the member's window and presses Enter. A member that's working, or waiting on you, is left alone.

```bash
cadrei compact dev/engineer my-app
```

### `cadrei ls`

Shows your crew, your orchestrator, the running teams and what each member is doing (working, idle, waiting on you), and your projects.

| Option | What it does |
|---|---|
| `--all` | every crew, not just this one |

```bash
cadrei ls --all
```

## Projects

Your projects stay wherever they are. Cadrei just remembers where each one is on this machine. `cadrei project` on its own lists these commands.

### `cadrei project add <name> <repo> [team] [about]`

Clones a repo into your projects folder, adds it to your crew and trusts the folder in Claude Code. `<repo>` is `owner/repo` or a git URL. `team` is the team that usually handles it (`dev` if you leave it out), and `about` is one line for your orchestrator. Add `--no-trust` to skip the trust step. A repo with its own Claude Code settings isn't trusted until you run `cadrei project trust <name>`.

```bash
cadrei project add blog you/blog
```

### `cadrei project add <name> --path <dir>`

Adds a git folder you already have. Its repo comes from `origin`, or from `--repo <repo>`.

| Option | What it does |
|---|---|
| `--repo <repo>` | the project's repo, when `origin` isn't it |
| `--no-trust` | add it, but don't trust the folder in Claude Code |

```bash
cadrei project add app --path ~/code/app
```

### `cadrei project link <name> <dir> [--no-trust]`

Sets or changes where a project's folder is on this machine, and trusts the folder in Claude Code. Add `--no-trust` to skip that. A repo with its own Claude Code settings isn't trusted until you run `cadrei project trust <name>`.

```bash
cadrei project link blog ~/src/blog
```

### `cadrei project unlink <name> [--untrust]`

Removes a project from your crew. Its folder is never touched. `--untrust` also removes its trusted mark in Claude Code.

```bash
cadrei project unlink blog
```

### `cadrei project sync [--no-trust]`

Clones every project in your crew that isn't on this machine yet, and trusts each new folder in Claude Code. Add `--no-trust` to skip that. A repo with its own Claude Code settings isn't trusted until you run `cadrei project trust <name>`.

```bash
cadrei project sync
```

### `cadrei project trust <name> | --all`

Marks a project's folder (or every project's, with `--all`) as trusted in Claude Code, so members start there without the "do you trust this folder?" question. Only trust repos you trust: their own Claude Code settings and hooks run too.

```bash
cadrei project trust my-app
```

### `cadrei project path <name>`

Prints a project's folder.

```bash
cd "$(cadrei project path my-app)"
```

### `cadrei project dir [<folder>]`

Shows or sets the folder new clones go into.

```bash
cadrei project dir ~/Developer
```

## Permissions

Each member asks for its own permissions. These commands give every member the same grant, so they stop asking for it. The easy way is to tell your orchestrator, like "allow the members to run npm test".

### `cadrei allow [list]`

Lists the grants every member gets, numbered.

```bash
cadrei allow
```

### `cadrei allow add <rule>`

Grants a Claude Code permission rule to every member. Catch-all rules, like a bare `Bash`, are refused.

| Option | What it does |
|---|---|
| `--once` | for one task only, and removed afterwards |
| `--auto "<text>"` | instead of a rule, a plain-English sentence for Claude Code's auto mode, describing work that's expected |

```bash
cadrei allow add 'Bash(npm test)'
```

### `cadrei allow remove <rule|number|--once>`

Removes a grant, by its exact text or its number in the list. `--once` removes every one-time grant.

```bash
cadrei allow remove 2
```

A running member keeps the grants it started with. `cadrei allow` tells you which members to restart, and a restart picks up the same chat.

## Backups and restore

There's no backup command: say "back up my crew to GitHub", and your orchestrator makes a private repo and pushes to it after every change.

To restore that backup on a new machine, run `cadrei` and press **r** on the first run's plan.

### `cadrei restore [<archive>]`

Brings back a setup you packed away with `cadrei uninstall --archive`. With several archives, it lets you pick one. You can also give it an archive folder.

```bash
cadrei restore
```

## Uninstall and archive

### `cadrei uninstall`

Shows its plan, asks, then stops your team and removes cadrei's links and hooks. Your crews and projects stay, and a reinstall plugs them back in. If you installed with Homebrew, it removes the program too, as the plan's last step.

| Option | What it does |
|---|---|
| `--dry-run` | show the plan, change nothing |
| `--yes` | don't ask first |
| `--keep-program` | keep the cadrei program |

```bash
cadrei uninstall --dry-run
```

### `cadrei uninstall --archive`

Does the same, and also packs every crew and this machine's settings into `~/.cadrei-archive`. Nothing is deleted. Add `--keep-program` to try cadrei fresh right away: the next `cadrei` is a brand-new user's first run. Also takes `--dry-run` and `--yes`.

```bash
cadrei uninstall --archive --keep-program
```

### `cadrei uninstall --all`

Deletes every crew, every archive and all of cadrei's settings, leaving nothing of cadrei. Your projects' folders always stay. It can't be undone, so you type `delete everything` to confirm. Takes `--dry-run` and `--keep-program`, and `--confirm "delete everything"` when there's no terminal to type in.

```bash
cadrei uninstall --all --dry-run
```

## For tool builders

### `cadrei ls --json`

Prints the same data as `cadrei ls`, as JSON, for your own scripts and apps. [For tool builders](tools.md) describes every field.

| Option | What it does |
|---|---|
| `--watch` | keep running, and print a new line each time something changes |
| `--all` | every crew |
| `--json-version 1` | pin the JSON version you read |

```bash
cadrei ls --json --watch
```

## Health and help

### `cadrei --check`

Runs the full health check, and offers to fix what it can. It ends with `✓ Everything looks good.` when it is.

```bash
cadrei --check
```

### `cadrei --version`

Shows the version, and how cadrei was installed. `-v` works too.

```bash
cadrei --version
```

### `cadrei help [advanced]`

Shows the everyday commands. `cadrei help advanced` shows every command. `-h` and `--help` work too.

```bash
cadrei help advanced
```
