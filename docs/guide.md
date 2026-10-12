# Cadrei guide

You can use cadrei without reading this: run `cadrei`, then talk to your orchestrator. This guide is for when you want to know what happens underneath, shape your crew by hand, or fix something.

Your crew is your own folder of teams, members and settings. Older docs, and the folder layout (`~/.cadrei/<name>`, `cadrei.conf`), call it a cadrei.

## Concepts

| Term | What it is |
|---|---|
| **Crew** | Your folder, `~/.cadrei/<name>`, and a git repository. It holds your teams, members, playbook, projects list, settings and grants. |
| **Your orchestrator** | The Claude Code chat you talk to. `cadrei` opens it. It follows cadrei's instructions (a Claude Code skill, a set of instructions Claude Code loads) and your playbook, and hands work to your team instead of doing it itself. |
| **Team member** | A long-lived Claude Code session with one role, started from `members/<team>/<role>.md`. |
| **Team** | A folder of members, started together or one at a time. A new crew has one: `dev`, with an `engineer` and a `reviewer`. |
| **Project** | A git repository your crew works on. The crew records its repository; each machine records where its folder is. |

## How cadrei is built

Cadrei works like Docker. The cadrei program is the engine: it starts and stops your team, keeps their permissions, and checks your setup. Your crew (`~/.cadrei/<name>`) is like a Docker volume: your members, playbook, projects list and settings all live there, in a git repo you can back up.

Each member file is like an image recipe, and a running member is like a container: start it, stop it, start it again, and it picks up its chat. Where your projects live on this machine is like a bind mount. Your crew keeps that list in a file that stays on this machine.

So the engine can come and go. Upgrade it, uninstall it, or delete `~/.cadrei/config` and `~/.cadrei/framework`, and your crew is still there. Run `cadrei` again and it plugs back in, with its chats right where you left them.

## Your first run

The first `cadrei` checks your tools, shows the whole plan on one screen, and waits for one key. Run it inside a repo you want your team to work on, and the plan adds that repo too:

```text
  cadrei v0.2.1
  A team of Claude Code helpers you lead from one chat.
  ~/code/my-app

  ✓ Claude Code 2.1.300 (logged in), git and tmux are ready

  Here's the plan
  · Create your crew main in ~/.cadrei/main, with an engineer and a reviewer
  · Add my-app as your first project
  · Trust both in Claude Code
    Your team works there without Claude Code asking first.
  · Link cadrei's instructions into Claude Code (~/.claude/skills/cadrei)
  · Open your orchestrator in tmux (recommended)
    It keeps going if you close this window. Ctrl-b s switch, Ctrl-b d leave

  Press Enter to set this up.
  Or press p for the plain terminal, r to restore from GitHub, q to quit.
```

The header names the version and the folder you're in, with a small figure beside it in a color terminal. The check is one line when every tool is ready; when one is missing or out of date, it becomes a row per tool, with how to fix it.

| Key | What it does |
|---|---|
| Enter | sets up the whole plan |
| `p` | the same plan, but your orchestrator opens in this terminal instead of tmux |
| `r` | restores a crew from a GitHub backup instead ([Backup and restore](#backup-and-restore)) |
| `q` | quits. Nothing is changed. Ctrl-C and Ctrl-D do the same. |
| `a` | shows up when the repo has its own Claude Code settings: adds it to the plan anyway, and pressed again leaves it out |
| `o` | shows up when an archived setup is waiting: brings it back instead ([Bring back an archived setup](#bring-back-an-archived-setup)) |

- **Your first crew is called `main`.** There's no name question. `cadrei new <name>` makes more later.
- **Missing tools stop it.** If Claude Code, git or tmux 3.2 or newer is missing, the checks say how to install it, and nothing is changed. A Claude Code that isn't logged in doesn't stop it: the plan gets a login step, and before your orchestrator opens, the setup runs `claude auth login` (Claude Code's own login, which opens your browser) right in your terminal. If you cancel it, your crew is still made: log in with `claude auth login`, then run `cadrei` again.
- **A repo with its own Claude Code settings** (`.claude/settings.json`, `.claude/settings.local.json` or `.mcp.json` at its top, or anything in a `.claude/skills`, `agents` or `commands` folder anywhere in it, outside what git ignores) isn't in the plan. Trusting it would let those settings run commands for your whole team, so press `a` only if you trust where the repo came from. Outside a git repo, there's no project line at all.
- **No git name or email?** You get one line about it, once. `cadrei --check` shows how to set them.

After Enter you see one `✓` line per step, and your orchestrator opens. There's no second key: the tmux keys are in the plan, and they stay in the status bar at the bottom. The one exception: when Claude Code will ask to trust your crew's folder, cadrei says so and waits for Enter, so you can read it before tmux takes over the screen.

```text
  ✓ Created your crew main
  ✓ Added my-app as a project
  ✓ Trusted both in Claude Code
  ✓ Linked cadrei's instructions into Claude Code

  Opening your orchestrator in tmux.
```

The keys come from your own tmux prefix: with `C-a`, the plan says `Ctrl-a s switch, Ctrl-a d leave`.

With `p`, Claude Code opens right there with no extra key: `Opening your orchestrator here. Your team still runs in tmux, and you can watch any of them with cadrei attach.`

Your orchestrator then speaks first, with three ways to start: a read-only review of your repo, a small feature, or another team. In tmux it also tells you how to get around:

```text
Hi! I'm the orchestrator for your crew main. You tell me what you need, and
I hand the work to your team. Right now that's an engineer and a reviewer,
set up for my-app.

Three ways to start:
1. I'll have the reviewer read my-app and tell you the top 3 things to fix.
   Read-only, nothing changes.
2. I'll have the engineer add a small feature to my-app on a branch, and the
   reviewer check it.
3. I'll set up another team for you, like research, study or job search.

Type a number, or just tell me what you need.
New to tmux? Ask me how to get around.
```

More on that in [Your orchestrator](#your-orchestrator).

Run without a terminal (from a script or a pipe), cadrei sets nothing up and says `Run cadrei in a terminal to set one up.`

### Your crews are already here

When `~/.cadrei` holds crews but `~/.cadrei/config` is gone (after `cadrei uninstall` and a reinstall, or after you deleted it), the first run doesn't make a new crew. It offers to plug yours back in:

```text
  Found your crews in ~/.cadrei: life and work.

  Here's the plan
  · Plug them back in, with life as your default (press d to pick another)
  · Link cadrei's instructions into Claude Code (~/.claude/skills/cadrei)
  · Open your orchestrator in this terminal (life's setting)

  Press Enter to plug them back in.
  Or press p for the plain terminal, n for a new crew, r to restore, q to quit.
```

`d` picks another default crew, and `n` makes a new crew instead. After Enter, your projects and chats are as they were: `Opening your orchestrator. It picks up your last chat.`

## Where things live

```
~/.cadrei/
  <name>/                     a crew (a git repository)
    playbook.md               teams, pipelines, routing rules and your preferences
    projects.yaml             each project's repo, team and about (no local paths)
    cadrei.conf               settings (KEY=VALUE lines)
    protocol.md               optional house rules for every member
    members/<team>/<role>.md  one member per file
    teams/<team>/             working folders of teams without a project (tracked)
    .claude/
      member-settings.json    the grants every member starts with (tracked)
      member-settings.once    which grants are one-time (tracked)
      local/                  this machine's notes about the crew (ignored)
        places.json           where each project is on this machine
        state.json            your orchestrator's chat record
        cadrei.conf           optional: settings for this machine only
      build/                  generated: prompts, settings copies, chat records, member states, locks (ignored)
  config/                     this machine only, in no repository
    default                   the default crew's name
    projects-dir              where new clones go
    member-settings.sha256    fingerprints of the grants files
    state.json                the health check's notes and your one-time answers
    team-line-hint            there once the team line's one-time hint was shown
    every-chat-reminder       there once your orchestrator was reminded to offer the every-chat hook
  framework/                  cadrei's instructions (your orchestrator's skill), written out by the program, with a VERSION file
~/.cadrei-archive/            setups you packed away with cadrei uninstall --archive
```

`config` and `framework` are reserved names; a crew's name may use letters, digits, `-` and `_`.

Everything in `~/.cadrei/config` can be rebuilt or asked again, so deleting it loses nothing: the next `cadrei` plugs your crews back in.

### Everything cadrei puts on your machine

Here's the full list, and what `cadrei uninstall` does with each. If you've set `CLAUDE_CONFIG_DIR`, read that folder wherever this says `~/.claude` (it holds `.claude.json` too, then).

| What | Where | Added when | `cadrei uninstall` |
|---|---|---|---|
| The `cadrei` program | Homebrew's `bin`, or `~/.local/bin/cadrei` | you install it | With Homebrew, removed last (`--keep-program` keeps it). Otherwise kept: it prints the command that removes it |
| Your crews: members, playbook, projects list, `cadrei.conf`, permissions and `teams/` | `~/.cadrei/<name>/`, each one a git repo | the first run, `cadrei new`, or when you ask your orchestrator | Kept |
| This machine's notes about a crew: where its projects are, and its chat record | `~/.cadrei/<name>/.claude/local/`, left out of git | every crew | Kept, with the crew |
| Files made for each start: prompts, settings copies, chat records, member states, locks | `~/.cadrei/<name>/.claude/build/`, left out of git | every start | Kept, with the crew |
| A check that stops passwords, keys and files over 50 MB from going into your backup (a git pre-push hook) | `~/.cadrei/<name>/.git/hooks/pre-push` | the first run, and put back by every `cadrei` | Removed |
| This machine's settings: the default crew, the projects folder, the permissions fingerprint, the health check's notes, your one-time answers | `~/.cadrei/config/` | `cadrei` | Removed |
| cadrei's instructions for Claude Code (your orchestrator's skill) | `~/.cadrei/framework/`, rewritten when they don't match the program | every `cadrei` | Removed |
| A link so Claude Code can find those instructions | `~/.claude/skills/cadrei`, pointing at `~/.cadrei/framework/skills/cadrei` | the first run | Removed, if it points at this program |
| Optional: the every-chat hook, so every new Claude Code chat starts as your orchestrator (a SessionStart hook) | one entry in `~/.claude/settings.json` | your yes when your orchestrator offers it, or `cadrei hook on` | Removed, if it runs this program (your other hooks stay) |
| "Trusted folder" marks for your projects and new crews | entries in `~/.claude.json` | adding, linking or downloading a project; the first run and `cadrei new`, for the new crew's folder | Kept: your own Claude Code uses them too |
| Your projects | wherever you keep them; new downloads go in your projects folder | you, or your orchestrator | Kept |
| Members' chats | Claude Code's own folder | Claude Code | Kept |
| Archives of your setup | `~/.cadrei-archive/<date-time>/` | `cadrei uninstall --archive` | Kept: yours to delete |

`cadrei uninstall --all` also deletes your crews, `~/.cadrei` and `~/.cadrei-archive`, and your crews' trust marks. With Homebrew it removes the program too, last (`--keep-program` keeps it); otherwise it prints the command that removes it. It keeps your projects, their trust marks and Claude Code's chats ([Delete everything](#delete-everything)).

Your crews live in `~/.cadrei/<name>`. They're your setup, so don't delete them to reset cadrei. To reset the program, run `cadrei uninstall --keep-program` (or delete `~/.cadrei/config` and `~/.cadrei/framework`): your crews stay, and the next `cadrei` plugs them back in. To try cadrei as a brand-new user, run `cadrei uninstall --archive --keep-program`, which packs your crews away safely and keeps the program.

Want a plain Claude Code chat while the every-chat hook is on? Start it with `CADREI_OFF=1 claude`. [The every-chat hook](#the-every-chat-hook) has the rest.

Before cadrei first changes `~/.claude/settings.json` or `~/.claude.json`, it saves a copy next to it (`settings.json.bak-cadrei`, `.claude.json.bak-cadrei`) and never overwrites that copy.

The backup check, the every-chat hook, the team status line and the hooks in each member's settings only run the `cadrei` program when it sits in a folder only you can write to, which is where Homebrew and `install.sh` put it. Run it from a temporary folder and it sets none of them up; the health check tells you so.

The hooks cadrei writes into Claude Code's settings check that the program is still there first (`test -x <program> && <program> hook ... 2>/dev/null; exit 0`). So if the program is gone, for example after `brew uninstall cadrei` without `cadrei uninstall`, a leftover every-chat hook does nothing and shows nothing. Two checks stop instead, on purpose: each crew's pre-push hook stops a push and says cadrei is missing, and a running member's command check blocks its shell commands. Reinstall cadrei, or remove the crew's `.git/hooks/pre-push`.

## Writing a member

Ask your orchestrator ("add an SRE to an ops team"), or write the file yourself. A good member file is short and concrete:

```markdown
# Member: Site Reliability Engineer (ops team)

You are the SRE. You keep the services up and explain what broke.

- Read the logs and metrics before you form a theory.
- Change production only when the orchestrator's task says so.
- In your reply: what you found, what you changed, how you verified it.
```

- **One role, plainly stated.** What the member owns and what it doesn't do.
- **How to work**, in a few bullets. Concrete habits beat adjectives.
- **What the reply contains.** Your orchestrator relays it, so make it easy to relay.
- Leave out how to receive work and reply: the shared protocol covers that.

Team and member names may use letters, digits, `-` and `_`, and no member may be called `orchestrator`, a name kept for your orchestrator. Add the team to `playbook.md` so your orchestrator knows when to use it, and commit the change (your orchestrator does both when it adds a member).

### Models and effort

Each member can run on its own model and effort. Put this at the very top of
its file, `members/<team>/<role>.md`:

```markdown
---
model: sonnet
effort: high
---
```

`MODEL=` and `EFFORT=` in `cadrei.conf` set them for your whole crew, and a member's file wins over that. To try something once, ask your orchestrator ("start the writer on opus"), which runs `cadrei up` with `--model`. Effort is `low`, `medium`, `high`, `xhigh` or `max`. With nothing set, members use Claude Code's own defaults. Your orchestrator keeps its own model; switch it with `/model`, or set `ORCHESTRATOR_MODEL` in `cadrei.conf`.

### Working folders

- A member started for a project (`cadrei up dev my-app`) works in that project's folder.
- A team without a project works in `teams/<team>/`, unless `members/<team>/.workdir` names another folder.
- `members/<team>/<role>.workdir` pins one member to its own folder, for example a tutor per study track.

A `.workdir` file holds one path; `~` is expanded.

`teams/` is tracked in the crew's git, so the work members keep there is backed up with the crew. The crew's `.gitignore` leaves out dependency folders, logs and `.env` files (their `.env.example`-style templates stay), and the `.claude/build/` and `.claude/local/` folders.

## The playbook

`playbook.md` is read by your orchestrator at the start of every chat and wins over cadrei's instructions where they differ, with one exception, the same rule `protocol.md` has for members: it can add to cadrei's rules about your consent, members' permissions, stopping, uninstalling and restoring, but never loosen them. A playbook line that tries to is shown to you, not followed. Put there:

- **Teams**: who exists and what each team is for.
- **Pipelines**: multi-step flows, for example *engineer builds, reviewer reviews, engineer fixes*.
- **Routing rules**: which requests go where, and anything your orchestrator must never do itself.
- **Project notes** that don't fit elsewhere, such as how deploys work.
- **Preferences**, under a `## Preferences` heading, such as hiding the team line ([The team status line](#the-team-status-line)).

Rules that belong to one project go in that project's own `CLAUDE.md` instead, so every member working there follows them.

## House rules

`protocol.md` in your crew (optional) is added to the shared member protocol for every member. Use it for rules that apply to all members, for example a writing style. It can add rules, but it can't turn off the shared ones, like never writing a real-looking secret.

## Projects

`projects.yaml` is portable: it holds no local paths, so the same crew works on any machine.

```yaml
# repo: owner/repo, a git URL, or empty for a folder with no remote.
# team: the team that usually handles it.
# about: one line for the orchestrator.
my-app:
  repo: you/my-app
  team: dev
  about: The main product
```

It's a small part of YAML: a `name:` line, then indented `key: value` lines. A value runs to the end of its line, so put comments on their own lines, and don't put quotes around values.

Where each project is on this machine is kept in the crew, in `.claude/local/places.json`, which git leaves out. Projects stay wherever you keep them; nothing is cloned into a crew. Your orchestrator runs these commands when you ask in plain words ("work on github.com/you/app", "my blog is in ~/src/blog"):

| Command | What it does |
|---|---|
| `cadrei project add <name> <repo> [team] [about]` | clone into your projects folder, record the place, trust the folder |
| `cadrei project add <name> --path <dir> [--repo <repo>]` | register a git folder you already have (its repo is taken from `origin`) |
| `cadrei project link <name> <dir>` | set or change where a project's folder is on this machine |
| `cadrei project unlink <name> [--untrust]` | remove a project from the crew; **the folder is never touched** |
| `cadrei project sync` | clone every project that has a repo but no folder on this machine |
| `cadrei project trust <name> \| --all` | trust project folders in Claude Code |
| `cadrei project path <name>` | print a project's folder |
| `cadrei project dir [<dir>]` | show or set the projects folder (asked once per machine; `~/Developer` is suggested) |

`cadrei project` on its own lists these. Project names may use letters, digits, `.`, `-` and `_`. One project may belong to several crews.

**Refused folders.** Add, link and trust refuse `/`, your home folder, anything inside or containing `~/.cadrei`, a crew's team folder, `~/.claude`, and the top folder of a 0.1.x cadre (its `projects/<name>` folders can be linked). Trust also needs the top folder of a git repository.

**Missing projects.** When a project's folder is gone, cadrei never unlinks it by itself. `cadrei ls` marks it, and your orchestrator offers to clone it again, point to its new folder (suggesting a repository with the same `origin` in your projects folder), or unlink it. A folder on a drive that isn't connected says so. `cadrei up` and `cadrei attach` refuse a missing project.

**Trust.** Adding, linking and cloning a project marks its folder as trusted in Claude Code (`~/.claude.json`, or `$CLAUDE_CONFIG_DIR/.claude.json`), so members start there without the trust prompt. Cadrei records the physical path and, when different, the path as you wrote it. The edit keeps every other key and their order, keeps a backup of the original at `.claude.json.bak-cadrei` (the first one is never overwritten), writes atomically, checks again just before writing (a running session may rewrite the file), and skips with a warning on any doubt. Trust also lets that repository's own `.claude/settings.json` rules and hooks take effect, so add only repositories you trust, or pass `--no-trust`. That's why cadrei never trusts a repo with its own Claude Code settings (`.claude/settings.json`, `.claude/settings.local.json`, `.mcp.json`, or skills, agents or commands in any of its folders, and for a worktree or a submodule, the same in its main checkout or superproject) on its own: the first run asks you, and `cadrei project add`, `project link`, `project sync` and a GitHub restore add it without trust. Trust it yourself with `cadrei project trust <name>` once you've looked. Your orchestrator asks before it adds one too.

A new crew's own folder, `~/.cadrei/<name>`, is trusted the same way, with the same checks, so your orchestrator opens without a prompt: by the first run and by `cadrei new`. That only works once Claude Code has run at least once on this machine, since Claude Code makes `~/.claude.json` on its first launch and cadrei never creates that file. Until then, cadrei says Claude Code will ask, and you say yes once. A restored crew, from GitHub or from an archive, is never trusted in advance: its files came from somewhere else, so Claude Code asks, and you say yes only if it's your backup. A crew that already exists isn't trusted by these commands either.

## Sessions

| Command | What it does |
|---|---|
| `cadrei` | opens your orchestrator, in tmux or in this terminal as your crew's setting says ([tmux](#tmux)) |
| `cadrei --tmux` / `--no-tmux` | in tmux, or in this terminal, for this one run |
| `cadrei --detach` | starts your orchestrator in tmux without opening it |
| `cadrei --fresh` | starts your orchestrator with a fresh chat |
| `cadrei up <team>[/<role>] [project] [--fresh] [--model <model>] [--effort <level>]` | start a team or one member (your orchestrator does this); `--model` and `--effort` are for this start only |
| `cadrei attach [team] [project]` | watch or talk to a team; with no team, your orchestrator in tmux |
| `cadrei stop <team>[/<role>] [project] [--fresh]` | stop a team or one member |
| `cadrei stop [--all] [--yes]` | with no team, every member of this crew (`--all`: of every crew), after a confirmation; never your orchestrator |
| `cadrei ls [--all] [--json [--watch] [--json-version 1]]` | your crew, your orchestrator, running teams with what each member is doing, projects (missing ones marked) and other crews; `--watch` keeps printing each change, for programs |

Building something on top of cadrei? `cadrei ls --json` and its live stream are a stable interface: see [For tool builders](tools.md).

**What `cadrei ls` shows.** One block per running team, with the command that opens it, and one row per member:

```text
main  ~/.cadrei/main

Orchestrator  open in tmux since 14:02 · reopen with cadrei
Teams
  dev on my-app                                    cadrei attach dev my-app
    ● engineer   working for 4m
    ! reviewer   waiting on you · approve it with cadrei answer 7f3 yes
  research                                         cadrei attach research
    ○ scout      idle since 13:40
    ◌ writer     starting
    ? editor     running · can't tell what it's doing
  Not running: study (tutor), ops (sre)
Projects
  my-app   ~/code/my-app
  blog     not on this machine · ask your orchestrator to clone it
```

| Mark | State | What it means |
|---|---|---|
| `●` | working | busy on a task, `working for 4m` |
| `!` | waiting on you | it needs your approval, or asked you something ([Answering your team](#answering-your-team)) |
| `○` | idle | done, and waiting for its next task |
| `◌` | starting | its window is open, but it hasn't started work yet |
| `?` | running | it runs, but cadrei can't tell what it's doing (for example a member started by an older cadrei) |
| `✗` | stopped by safeguards | Claude's safeguards stopped its last reply, so it won't answer. Pick the model again in its window, or start it fresh. |

When a working or waiting member's chat hasn't changed for 10 minutes, the row adds `· no activity for 12m`: you may have stopped it with Esc. Members tell cadrei their state through Claude Code hooks in their settings copy; cadrei never reads their screen. The state is for you to look at. It's never a reason for cadrei or your orchestrator to stop or restart anyone, except a member stopped by safeguards, which your orchestrator offers to restart fresh. Without a terminal, `cadrei ls` uses plain marks (`* ! o . ? x`), and `cadrei ls --json` has the same states for scripts.

**Names.** A team runs in tmux session `cadrei-<crew>-<team>` (or `cadrei-<crew>-<team>-<project>`), one window per role, and your orchestrator in `cadrei-<crew>`. Each member's Claude Code session is named `<crew>-<team>[-<project>]-<role>`, which is the address your orchestrator sends work to. In tmux session names a `.` becomes `_`, as older tmux versions do it themselves: project `my.app` runs in `cadrei-w-dev-my_app`, while its members keep the dot (`w-dev-my.app-engineer`). `cadrei up` refuses when another crew already uses the name.

**One orchestrator per crew.** `cadrei` records your orchestrator in `.claude/build/orchestrator.lock`. When it's already open in another terminal, `cadrei` says so (`Your orchestrator is already open in another terminal`) and exits; when it runs in tmux, `cadrei` takes you back to it. A lock left by a session that ended is cleared. An orchestrator the every-chat hook started isn't detected.

**Chats resume.** Every start records the member's Claude Code session in `.claude/build/sessions/`, kept current across `/clear` and `/compact`. The next `cadrei up` (and the next `cadrei`) picks that chat up again. It starts a fresh chat, and says why, when there's no record, the working folder changed, the chat is gone, or it won't reopen. `--fresh` on `cadrei`, `cadrei up` and `cadrei stop` starts clean.

**Safeguard stops.** Sometimes Claude's safeguards stop a member's reply, and the member goes quiet. Cadrei notices: `cadrei up` warns after it starts such a member, `cadrei ls` shows it, and your orchestrator tells you right away. To fix it, open its window (`cadrei attach <team> [project]`), type `/model` and pick the model again. Or let your orchestrator restart it with a fresh chat. Your orchestrator never sends the stopped task again on its own.

**Sessions started by 0.1.x** show as legacy in the default crew's `cadrei ls`, and `cadrei stop` and `cadrei stop --all` stop them. They carry no crew, so on a tmux server another setup shares, that includes its 0.1.x sessions too. Every other session is stopped only when one of your crews started it.

## tmux

tmux is a terminal tool that keeps programs running after you close the window. Your team always runs in it. Your orchestrator does too, unless you pick the plain terminal.

**Your orchestrator in tmux** keeps going when you close the window, and your team is one key away. Run `cadrei` to come back: it reopens the same chat instead of starting a second one. To get around:

- **Ctrl-b then s** lists your orchestrator and every team. Pick one with the arrows, then Enter.
- **Ctrl-b then d** leaves. Everything keeps running, and cadrei says `Your orchestrator is still running. Run cadrei to come back.`
- **Scroll** with your mouse or trackpad. Press `q` to stop scrolling.
- **Copy text** by holding Option (Mac) or Shift (Linux) while you select.
- **A new line in a message**: type `\` then Enter, or press Ctrl-J. Shift+Enter sends the message in tmux.

These keys use your own tmux prefix, so with `C-a` as your prefix they say Ctrl-a. Cadrei's sessions show them in their status bar at the bottom: `Ctrl-b s switch · Ctrl-b d leave`. You can also ask your orchestrator "how do I get around?".

**Already in tmux?** Your orchestrator opens as its own session, and cadrei switches you to it, so tmux never opens inside tmux. Ctrl-b then L takes you back to the session you were in. If you're in tmux on another tmux server, it opens right in this pane that time.

**What cadrei sets.** On its own sessions only: the mouse on, the status bar, 50,000 lines of scrollback, and a few window options. It never binds keys, never sets server-wide options, and never touches your `~/.tmux.conf`.

**Picking tmux or this terminal.** It's a setting of your crew, so it travels with a backup:

| Command | What it does |
|---|---|
| `cadrei tmux` | says where your orchestrator opens, and whether that's for this machine only |
| `cadrei tmux on` / `off` | tmux, or this terminal, from now on (written to `cadrei.conf` and committed) |
| `cadrei tmux on --here` / `off --here` | the same, for this machine only (written to `.claude/local/cadrei.conf`) |

Or just ask your orchestrator: "use tmux" or "stop using tmux". The change applies the next time you run `cadrei`. `--tmux`, `--no-tmux` and `--detach` on `cadrei` override it for one run.

**In the plain terminal**, your team still runs in tmux in the background. Watch any of them with `cadrei attach <team> [project]` in another terminal tab, and leave it again with Ctrl-b then d.

## Your orchestrator

Your orchestrator speaks first every time cadrei opens it, so you always know which crew you're in. You'll see a short `(cadrei opened this chat)` message before it: that's cadrei waking it up, not you, and it never counts as your yes to anything.

- **On the first run**, it says hi, names your crew, and offers three ways to start: a read-only review of your repo (only the reviewer runs, and nothing changes), a small feature built on a branch, or another team.
- **Every time after**, it says up to three lines: who it is and which crew, the team line when someone's running, and the one thing that matters most right now (a member stopped by safeguards, leftover one-time grants, a project that isn't on this machine, a member waiting on you, or `Your team is ready. What's next?`).

When it opens, cadrei hands it a snapshot of your crew: projects, teams, who's running and what they're doing. So it doesn't need to run anything to greet you. It may run `cadrei ls`, `cadrei ls --json`, `cadrei ls --all` and `cadrei allow list` without asking you first, since they only read, and `cadrei hook skip`, which only records your no to the every-chat hook. Everything else still asks.

### The team status line

While anyone on your team is running, your orchestrator ends every reply with one short line:

```text
Started the reviewer on my-app. It's reading the code now and will report
the top 3 things to fix.

Team: reviewer working just now
```

Members waiting on you, or stopped by safeguards, come first and in bold: `Team: **! reviewer waiting on you** · engineer working 25m · designer idle`. With nobody running, there's no line. Cadrei sends the line fresh with every message, so it's always current and costs your orchestrator no extra work.

The first time the line shows on a machine, a hint comes right under it, once:

```text
Team: reviewer working just now
(Say "hide the team line" if you'd rather not see this.)
```

**To hide it**, ask: "hide the team line". Your orchestrator adds `- Don't end replies with the team status line.` under `## Preferences` in your crew's `playbook.md` and commits it, so it's a preference of your crew and travels with a backup. There's no setting or command for it. Say "show the team line" to bring it back. Hidden or not, your orchestrator still tells you in words when someone's waiting on you or stopped by safeguards, and `cadrei ls` shows your team anytime.

An orchestrator the every-chat hook started has no team line, since that hook can't add cadrei's settings to a chat. It runs `cadrei ls` when your team's status matters, and Claude Code asks you first.

### When a member doesn't answer

Your orchestrator asks Claude Code to tell it when each member it gives a task to goes idle (`notify_when_idle`), so a member that ends its turn without replying still wakes it. If your orchestrator leaves that out, cadrei's notify hook adds a note right after the send, and your orchestrator then asks for the notice on its own. The hook only adds that note: it never blocks, changes or approves a message. An orchestrator the every-chat hook started doesn't have it.

When a member's own reply is denied (by auto mode or a rule), the member sends one short status instead: that its reply was denied, naming only the rule, and that the content is in its window. Your orchestrator then tells you which window to open, with `cadrei attach`. It never reads the blocked content from that window, the member's chat record or a file, and never asks for it in other words, since that would get around the denial. Open the window yourself to read it.

When a member goes idle without replying at all, your orchestrator checks its state in `cadrei ls`, may ask it once for a short status, and otherwise tells you which window to open. It doesn't read that window itself either.

When a member runs in a different permission-mode class from your orchestrator, its replies wait for your approval in your orchestrator's window, and its idle notices may not reach your orchestrator at all (see [Troubleshooting](#troubleshooting)).

### Answering your team

A member that's `waiting` needs you: Claude Code is asking you something in its window. Either it wants to run something it isn't allowed to yet (a permission prompt), or it asked you a question. You can answer both from your orchestrator, without going to the member's window.

**A permission prompt.** Your orchestrator tells you who's waiting and what for, then runs `cadrei answer 7f3 yes`. Claude Code stops it and asks you, in its own prompt:

```text
 Bash command
 Answer the engineer's prompt
 cadrei answer 7f3 yes
 │ Hook PreToolUse:Bash requires confirmation for this command:
 │ Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash
 Do you want to proceed?
 ❯ 1. Yes
   2. No
```

Read the line that starts with `Cadrei:`. That's cadrei's own text, from Claude Code's record of what the member asked, and your orchestrator can't change it. The line above the command is Claude's own description of it. Press 1 and the member runs it, once. Press 2 and your orchestrator stops: tell it what you want instead. To tell the member no, your orchestrator runs `cadrei answer 7f3 no`, and Claude Code asks you again. A no stops the member's turn, and it waits until it hears what to do instead.

**A question.** A member can ask you something in its window, like "Upload resume.pdf to the signing site?". Members do this when only you can say yes: their instructions say so, or it touches personal data, money or something they can't undo. Your orchestrator shows you the question and its options exactly, each with a line to type. Type the one you pick, with `!` in front, right in your orchestrator: `! cadrei answer k2m 1`. A command you type with `!` is yours, so it runs at once.

**A block a grant can fix.** When auto mode or a rule blocks a member, your orchestrator asks you here first: `Let the engineer on my-app run gh pr merge 42 --squash once?`. On a yes it gives a one-time grant and restarts the member, which picks up its chat.

More:
- `cadrei answer` with no code shows who's waiting on you, with the line that answers each. You can run it, and `cadrei answer <code> <choice>`, in your own terminal too.
- Each request has its own short code. If the member's prompt changed in the meantime, the old code answers nothing.
- `yes` only ever picks the plain "Yes", the one-time allow. It never picks "always allow" or "switch to auto mode".
- In `dontAsk`, `bypassPermissions` or `plan` mode, Claude Code can't ask you about your orchestrator's answer, so it doesn't run it: type the `!` line yourself.
- Some things still need the member's window (`cadrei attach`): a question that takes more than one pick or holds several questions, a prompt with no plain "Yes", a member in `dontAsk` mode (it can't ask you at all), and the orchestrator the every-chat hook opens, where `cadrei answer` doesn't run.

## The every-chat hook

The every-chat hook is a SessionStart hook in `~/.claude/settings.json` that makes every new Claude Code chat open as your orchestrator. It isn't offered on the first run. Instead, your orchestrator offers it once, right after your first finished task. Say no, and it doesn't ask again (you can still ask it anytime).

| Command | What it does |
|---|---|
| `cadrei hook` | says whether it's on |
| `cadrei hook on` | adds it: new Claude Code chats open as your orchestrator |
| `cadrei hook off` | removes it: new chats are plain again |
| `cadrei hook skip` | records your no to the offer |

Its `cadrei` commands act on the crew for its folder (your default crew outside every crew and project). It runs `<cadrei program> hook orchestrator`, and stays silent in member sessions, in an orchestrator `cadrei` opened, and in chats started with `CADREI_OFF=1 claude`. An orchestrator started this way gets the same snapshot, but no team line and no read-only commands allowed in advance. Without the hook, run `cadrei`, or ask any chat to use cadrei's instructions.

The hook is added with a backup of the file (`settings.json.bak-cadrei`), keeping every other setting. `cadrei hook off` and `cadrei uninstall` remove it.

## Permissions for members

Each member is its own Claude Code session and asks for its own permissions. A message from your orchestrator never counts as your consent there. To grant something to every member, tell your orchestrator ("allow the members to run npm test"), which runs `cadrei allow`:

```bash
cadrei allow add 'Bash(git push origin HEAD:main)'      # an exact command
cadrei allow add --once 'Bash(npm publish)'             # for one task; removed afterwards
cadrei allow add --auto "Merging a reviewed branch into main in my projects is expected"
cadrei allow list                                       # numbered, with kind, once and wildcard marks
cadrei allow remove 'Bash(npm publish)'                 # by its exact text, or its list number
cadrei allow remove --once                              # every one-time grant
```

- A rule is a Claude Code permission rule: a tool name with an optional specifier, such as `Bash(npm test)`, `Read(./docs/**)` or `mcp__github__create_issue`.
- `--auto` adds a sentence (one line, at most 300 characters, plain ASCII letters) to the auto-mode classifier's allow list, after `"$defaults"`, so the built-in rules stay. Describe the work that's expected; a sentence about permissions, settings or grants, or one claiming your approval, is refused.
- **Refused**: blanket rules (`*`, a bare `Bash`, `Edit`, `Write`, `Read`, `WebFetch`, `NotebookEdit` or `PowerShell`, a specifier that's only a wildcard), whole MCP servers, wildcards in the program name, shells, interpreters and wrappers with a wildcard, programs that run whatever follows a subcommand (`docker run`, `npm exec`, `go run` and the like) with a wildcard, chained or backgrounded commands, commands built with shell syntax, `WebFetch` for every domain, `Edit` and `Read` paths that climb with `..`, startup files and files that run code outside a session (shell and git config, `~/.ssh`, launch agents, `~/.local/bin` and the like), and anything that reaches cadrei's own files: the grants file, `cadrei allow`, `cadrei.conf`, `~/.cadrei/config`. For `Edit` and `Read` rules, paths are read the way Claude Code reads them, so glob classes, escapes and braces can't hide a refused file. `Bash` rules are checked word by word: cadrei reads the paths, variables such as `$CADREI_HOME` and `..` climbs in them, but it can't see where a link on disk leads, so a shell rule is a weaker guard than an `Edit` rule. The full tables are in `internal/runtime/claude/allow`.
- **Warned**: other wildcards, `git` with a wildcard (it can run other programs), `make`, `npm run`, `pip install` and `./script` with a wildcard (they run code from files a member can change), and `Read` rules that reach secrets such as `~/.ssh` or `~/.aws`. Accepted wildcards are marked in `list`.
- Every change is committed in your crew (`git log -- .claude/` is the record) and travels with it. One-time grants are listed in `.claude/member-settings.once` with the time they were added. When some are left over, your orchestrator offers to remove them.
- Members can't add or remove grants.
- A running member keeps the grants it started with. `cadrei allow` lists the members to restart; a restart picks up the same chat.

### The grants file

Every member starts with the grants in `.claude/member-settings.json`, a Claude Code settings file. Members never get the file itself: before each start, cadrei validates it and writes a read-only copy to `.claude/build/`. The file may hold only `permissions.allow` and `deny` and `autoMode.allow` and `soft_deny`, with no duplicate keys, `"$defaults"` in every `autoMode` list, and the fixed entries. A file that breaks these rules gives members a copy with no grants, and a warning.

Every copy also gets fixed entries that keep members off cadrei's own files: `Edit` denies for `~/.cadrei/config`, `~/.cadrei/framework`, and each crew's `.claude` (which holds `local/` and `build/`), `cadrei.conf`, `members`, `playbook.md`, `protocol.md`, `projects.yaml`, `.git`, `CLAUDE.md`, `CLAUDE.local.md` and `.mcp.json`; `Read` and `Edit` denies for `~/.cadrei-archive`; denies for `cadrei allow`, `cadrei new` (and its old name `cadrei init`), `cadrei use`, `cadrei uninstall` and `cadrei restore`; and a `soft_deny` line telling the auto-mode classifier that only you change these, through your orchestrator. Team folders stay writable.

Every member's copy also runs `cadrei hook guard` before each Bash command. It refuses any command that may run cadrei's `uninstall` or `restore`, however it's written: through `env` or `sh -c`, by any path to the program, quoted or split up, or behind a variable. A command that only mentions them, like a `grep` for the word or a commit message about them, still runs. In members it also refuses `cadrei answer` and `cadrei compact`: only you answer for a member. It refuses a command it can't read, and if the program is gone.

Your orchestrator runs the same guard, from its own settings. There it lets uninstall and restore through (it runs them on your yes), asks you before `cadrei answer` ([Answering your team](#answering-your-team)), and refuses every other form of it. In both, it refuses typing into tmux windows: any `tmux` command outside a short read-only list (`capture-pane`, `list-sessions`, `list-windows`, `list-panes`, `list-clients`, `has-session`, `show-options`, `show-environment`, and `display-message -p`), on any tmux server, and `osascript` keystrokes. That's why your orchestrator compacts a member with `cadrei compact` instead of typing `/compact`. If cadrei is gone, your orchestrator's guard lets commands run, so an uninstalled cadrei doesn't lock your chat. The deny rules alone wouldn't do: Claude Code's own documentation says a prefix rule doesn't see through `env`, `sh -c` or a full path. A third check is in the commands: run inside a member's tmux window, `uninstall` and `restore` refuse, even when the member cleared its variables or named another tmux server. None of these is a sandbox: a member can still write a script and start it outside its own window. The member's instructions say never to, and in `default` mode it asks you first. [SECURITY.md](../SECURITY.md) has what each layer can and can't stop.

A fingerprint in `~/.cadrei/config/member-settings.sha256` flags a file changed outside `cadrei allow`. A change that arrives as a commit `cadrei allow` made (for example pulled from another machine) is accepted. If the fingerprint is gone, cadrei checks the file against its last commit instead. To undo a hand edit, run `git -C ~/.cadrei/<name> checkout -- .claude/member-settings.json`.

## Backup and restore

Your crew is a git repository, and your orchestrator commits every change to it. Say "back up my crew to GitHub": your orchestrator shows the repository it will create (`<your GitHub user>/cadrei-<name>`, private) and, on your yes, creates it with `gh` and pushes. From then on it pushes after each commit.

On a new machine, run `cadrei` and press `r` on the first run's plan. Type the repo (`you/cadrei-main`, or a URL), and cadrei shows a plan to copy it into `~/.cadrei/<name>` (named after the repo, so `cadrei-main` becomes `main`; it asks for a name only when that one's taken). After Enter, it checks the copy before anything takes effect:

- A backup that isn't a crew (no `members` folder), or one with a link where cadrei reads its files or runs sessions (members, `teams/`, `.claude/`, the top folder), isn't kept: cadrei would follow the link out of the crew.
- Files that Claude Code loads into your orchestrator once you trust the crew's folder (`.claude/settings.json` and `settings.local.json`, `.claude/commands`, `agents`, `skills` and `output-styles`, `.mcp.json`, `CLAUDE.md`, `CLAUDE.local.md`) are listed, and your orchestrator opens only after your yes (the default is no). Cadrei never writes these itself; on a no, look at them and remove any you didn't put there.
- The projects are listed with their repositories. Ones already in your projects folder are linked, and the rest are cloned, after your yes (`Press Enter for yes, type another folder, or press n to skip`). Only clones without their own Claude Code settings are trusted; the others are added but not trusted until you ask. On `n` they stay "not on this machine", for your orchestrator to offer later.
- The restored crew's folder isn't trusted in advance, so Claude Code asks once. Say yes only if it's your backup.

Projects with no repo are listed as missing until you link them. Your orchestrator greets you as on any later run, since the crew isn't new.

| Travels with the crew | Stays on each machine |
|---|---|
| members, playbook, `projects.yaml`, `cadrei.conf` (with your tmux choice), `protocol.md`, grants, `teams/` | project folders and where they are (`.claude/local/`), `~/.cadrei/config`, `.claude/build` |

Claude Code's chats and memory live in Claude Code's own folder, not in the crew, so on a new machine members start fresh chats with the same setup. Grants that name a local path travel as written and apply only where that path exists.

**No credentials in a backup.** Cadrei writes no tokens into a crew. A pre-push hook in each crew repository (`.git/hooks/pre-push`, running `cadrei hook pre-push`) refuses a push that carries files that look like credentials (`.credentials.json`, `.env` files, private keys, tokens such as `sk-ant-`, `ghp_` or `github_pat_`, and a password or token in a URL), files over 50 MB, or this machine's own `.claude/local/` files, and names each one. A pre-push hook of your own is left alone, and the health check says so. Members follow the same idea: they never write a real-looking secret, not even a fake one, and use a placeholder instead.

## Several crews

Most people need one crew. To make another, run `cadrei new <name>`, and `cadrei use <name>` makes it the default. `cadrei init <name>` still works too; it's the old name for cadrei new. A command acts on:

1. the crew in `CADREI_HOME`, when that's set;
2. otherwise the crew whose folder you're in (`~/.cadrei/<name>`);
3. otherwise the crew of the project you're in (when several crews link it, cadrei asks which one; without a terminal it refuses and says how to choose);
4. otherwise the default crew.

Cadrei reads configuration only from `~/.cadrei` or `CADREI_HOME`, never from the folder you happen to be in. Members and your orchestrator start with `CADREI_HOME` set, so changing folders never switches their crew. Session names carry the crew's name, so two crews can each run a `dev` team. `cadrei ls --all` and `cadrei stop --all` cover every crew.

## Configuration

`cadrei.conf` is read as `KEY=VALUE` lines, never run.

| Setting | Default | Meaning |
|---|---|---|
| `PERMISSION_MODE` | `default` | the permission mode of the members and your orchestrator |
| `ORCHESTRATOR` | `terminal` (the first run sets `tmux`, or `terminal` when you press `p`) | where `cadrei` opens your orchestrator: `tmux` or `terminal`. `cadrei tmux on\|off` sets it. |
| `MODEL`, `EFFORT` | none (Claude Code's own) | the members' model and effort, unless a member's file sets its own (see Models and effort) |
| `ORCHESTRATOR_MODEL`, `ORCHESTRATOR_EFFORT` | none (Claude Code's own) | your orchestrator's model and effort; `MODEL` and `EFFORT` don't apply to it |
| `FORMAT` | none (format 1) | set by cadrei, not by you: a later cadrei that changes how a crew is laid out writes its number here, and an older one then only reads the crew (see Upgrading). Read only from the crew's own `cadrei.conf`, never from `.claude/local/` |

**Settings for this machine only.** A crew's `.claude/local/cadrei.conf`, which git leaves out, wins over `cadrei.conf` on this machine. Today it's used for `ORCHESTRATOR`, through `cadrei tmux on|off --here`, and it may set the model and effort keys for this machine too. Crews from 0.2.0 have no `ORCHESTRATOR`, so they keep opening in the terminal until you ask. An old `ORCHESTRATOR_TMUX=1` still counts as `tmux`.

Keys cadrei doesn't know, and lines that aren't `KEY=VALUE`, are left as they are and ignored. `cadrei --check` lists them once.

Cross-session messages reach a member without your approval only when your orchestrator and the member run in the same permission-mode class; otherwise they wait for you in the member's window.

Environment variables: `CADREI_HOME` (use another crew for one command), `CADREI_PERMISSION_MODE` (override the mode), `CADREI_OFF=1` (a plain Claude Code chat, not an orchestrator), `CADREI_TMUX_SOCKET` (run sessions on a separate tmux server), `NO_COLOR` (no colors, plain marks), `FORCE_COLOR=1` (colors even through a pipe, for recordings), and Claude Code's own `CLAUDE_CONFIG_DIR`.

A switch such as `CADREI_OFF` is on when it's set to anything but an empty value, so `CADREI_OFF=0` turns it on too; leave it unset to turn it off. The other `CADREI_*` variables (`CADREI_MEMBER`, `CADREI_ORCHESTRATOR`, `CADREI_OPENS_IN`, `CADREI_OPENED_FROM`, `CADREI_OPENED_PROJECT`, and the ones the tests and development builds use) are cadrei's own, set in the sessions it starts: don't set them or rely on them. `CADRE_PERSONA`, which cadre 0.1.x's sessions carry, is still read, and a release note will say when it goes.

## The health check

Every `cadrei` checks the setup and stays silent unless something is wrong. Quick checks run every time; full checks run on the first run, after an upgrade, when a quick check fails, and on `cadrei --check`. They cover Claude Code (installed and logged in), tmux and git (and your git name and email), cadrei's instructions link and the every-chat hook pointing at this program, the `cadrei` first on your `PATH` being this one, files at the top of a crew that Claude Code loads into your orchestrator (`.claude/settings.json`, `.claude/settings.local.json`, `CLAUDE.md`, `CLAUDE.local.md`, `.mcp.json`), each crew's pre-push hook, and cadrei's instructions (put back when something changed them). Missing projects show in `cadrei ls` and your orchestrator's greeting instead. Leftovers from 0.1.x (the old hook, a skill link and a `cadre` link in `~/.local/bin`, both into the old clone) are recognized. Each finding says what's wrong (`!`) and how to fix it, a fixable one asks right away, and nothing outside cadrei's own files is changed without your yes. `cadrei --check` ends with `✓ Everything looks good. (cadrei 0.2.1)` when it is.

## Upgrading

`brew upgrade cadrei`, or run `install.sh` again. The next `cadrei` runs the full health check and rewrites `~/.cadrei/framework`. Running members keep the old protocol until they restart (`cadrei stop`, then let your orchestrator start them again; chats resume). Read the [CHANGELOG](../CHANGELOG.md) for anything to know.

Coming from 0.2.0, there's nothing to do: on its first run, 0.2.1 moves each crew's project places and orchestrator chat record from `~/.cadrei/config` into the crew's `.claude/local/`, adds that folder to the crew's `.gitignore`, and says so once.

**Going back to an older version** works, with one rule: an older cadrei never changes what a newer one wrote. A crew a newer cadrei changed the layout of (its `FORMAT`), a crew's `.claude/local` notes in a newer form, and an archive a newer cadrei made are refused with what to do (upgrade), and nothing is changed. On such a crew, `cadrei` doesn't open your orchestrator either, since its instructions would change the crew the older way; `cadrei ls`, `attach`, `stop`, `--check` and `uninstall` still work.

**Want to try a new version fresh**, the way a brand-new user sees it, and keep your setup? Run `cadrei uninstall --archive` before you upgrade, then install the new version (with Homebrew the archive removed the program, so `brew install rafi-ramdhani/cadrei/cadrei`) and run `cadrei`. When you're done trying, [bring your setup back](#bring-back-an-archived-setup).

## Uninstalling

`cadrei uninstall --dry-run` prints the plan. `cadrei uninstall` prints it, asks, then:

1. stops every team member, and your orchestrator in tmux last;
2. removes cadrei's instructions link and the every-chat hook, only where they point at this program (other hooks and settings stay; the backup made before cadrei first changed the settings file, `settings.json.bak-cadrei`, is kept, and no new one is written);
3. removes each crew's pre-push hook;
4. removes `~/.cadrei/config` and `~/.cadrei/framework`;
5. with Homebrew, removes the program itself, last: `remove the cadrei program (brew uninstall cadrei)` is the plan's last line, and the same yes covers it. Add `--keep-program` to keep the program.

`--archive` and `--all` end with the same step, covered by their own yes (the typed phrase, for `--all`).

It looks for your teams on every tmux server your crews' teams were started on, whatever `CADREI_TMUX_SOCKET` or `TMUX_TMPDIR` say now, and stops before changing anything while a team runs where it can't stop it, or where tmux can't say.

It keeps every crew and project, trust entries and backups, and ends like this:

```text
✓ Uninstalled. Your crews and projects are still here.
  Your crews in ~/.cadrei are your setup, so keep them. Reinstall and run
  cadrei to plug them back in.
✓ Removed the cadrei program (brew uninstall cadrei)
```

brew runs only after all of cadrei's own cleanup. If it fails, cadrei says why and prints `brew uninstall cadrei` for you to run; everything else is already done. With `--keep-program`, the last line is `To finish: brew uninstall cadrei` instead.

For an `install.sh` install, the last line is the `rm` that removes the program, for you to run: cadrei doesn't delete that file itself, since install.sh may have put it in a folder other programs share, or one that needs `sudo`. Since each crew keeps its own notes, a reinstall and `cadrei` plug your crews back in, with their projects and chats as they were. An orchestrator open in a terminal is named so you can close it. Members can't run it.

With Homebrew, let `cadrei uninstall` remove the program rather than running `brew uninstall cadrei` yourself: Homebrew can't run cadrei's cleanup for you. If you forgot, the every-chat hook does nothing, but each crew's pre-push check stops pushes until you reinstall cadrei. Reinstalling puts everything right.

### Archive everything

`cadrei uninstall --archive` does what `cadrei uninstall` does, and also moves every crew and `~/.cadrei/config` into one new folder, `~/.cadrei-archive/<date-time>/`. Nothing is deleted. Afterwards `~/.cadrei` is gone, so a reinstall and `cadrei` is exactly a brand-new user's first run. Use it to try cadrei fresh, or to start over and keep your old setup.

`cadrei uninstall --archive --dry-run` shows the plan first, with how each crew's GitHub backup stands:

```text
cadrei uninstall --archive will:
  · stop 3 team members: main-dev-my-app-engineer, main-dev-my-app-reviewer,
    main-research-scout
  · stop your orchestrator in tmux
  · remove cadrei's instructions link (~/.claude/skills/cadrei)
  · remove the every-chat hook from ~/.claude/settings.json (your other hooks
    stay)
  · remove cadrei's pre-push check from main, work and life
  · move your crews (main, work, life) and this machine's cadrei settings
    into ~/.cadrei-archive/2026-10-10-1432
  · remove ~/.cadrei/framework, then ~/.cadrei

Backups
  ✓ main is backed up to github.com/you/cadrei-main, and up to date
  ! work has 2 commits that aren't on github.com/you/cadrei-work yet
  ! life has no backup. After this, it's only in the archive.

It keeps your projects, Claude Code's trusted-folder marks, your Claude Code
chats, and your own Claude Code settings.

Bring it all back later with cadrei restore, or press o on cadrei's first run.
```

- Without `--dry-run`, it offers to commit and push each crew whose backup is behind, then asks `Archive and uninstall? [y/N]`. A push that fails doesn't stop it: the crew is still safe in the archive.
- `--yes` skips the question and pushes nothing.
- Run it in a terminal, not from your orchestrator, and close an orchestrator that's open in a terminal first: it moves your orchestrator's own folder.
- A crew outside `~/.cadrei` (one you reach only through `CADREI_HOME`) stays where it is.
- If it stops halfway, run the same command again to finish.
- Each archive has a `README.txt` that says what it is and how to bring it back. Members can't read or change the archive.

### Delete everything

`cadrei uninstall --all` does what `cadrei uninstall` does, and also deletes every crew, `~/.cadrei` (cadrei's settings and state) and `~/.cadrei-archive` with every archive in it, and removes your crews' trust marks in Claude Code. With Homebrew it removes the program too, last, as every uninstall does (`--keep-program` keeps it); otherwise it ends with the command that removes the program. Nothing of cadrei is left. It can't be undone, so it's never the default: plain `cadrei uninstall` keeps your crews.

- **Your projects stay.** Their folders are your work, and cadrei only links them, so `--all` never deletes one. If a project's folder is inside `~/.cadrei`, it stops before deleting anything and asks you to move it out first. It checks again after you type, and it stops too if it can't read a crew's notes of where its projects are. Claude Code's own chat history stays too.
- **Look first:** `cadrei uninstall --all --dry-run` lists each crew with what it holds, and warns about any crew with changes that aren't committed, commits that aren't in its backup, or no backup at all. Push or archive what you want to keep first.
- **Type to confirm.** In a terminal it asks you to type `delete everything`. Anything else stops it, and `--yes` isn't enough. Without a terminal it needs `--confirm "delete everything"`, which is how your orchestrator runs it after you type that phrase in the chat.
- It stops every member first, as `cadrei uninstall` does. If a team won't stop, tmux can't say which teams run, or an orchestrator is open in another terminal, nothing is deleted.
- A `~/.cadrei` or `~/.cadrei-archive` that is a link loses only the link: the folder it points to stays. From a linked `~/.cadrei`, the crews and cadrei's own settings go, and anything else there stays.
- It doesn't go with `--archive`, which keeps everything. Members can't run it.

### Bring back an archived setup

Run `cadrei restore`, or press `o` on the first run (it shows up whenever an archive is waiting). With several archives, `cadrei restore` lists them and you pick one by number; `cadrei restore <folder>` takes an archive folder directly. Then:

```text
  Bring back your setup from Oct 10, 14:32

  Here's the plan
  · Put main, work and life back in ~/.cadrei, with main as your default
  · Bring back this machine's cadrei settings
  · Link cadrei's instructions into Claude Code (~/.claude/skills/cadrei)
  · Turn the every-chat hook back on (it was on before)
  · Open your orchestrator in tmux, as before

  Press Enter to bring it back, or q to quit.
```

Everything goes back to the same paths, so your orchestrator picks up its last chat, and members pick up theirs on their next start. A restore writes no trust marks: your projects keep the ones they had, and Claude Code still knows your crews' folders from before.

- A restore needs an empty `~/.cadrei`. If you have a crew set up here (say, the one you made while trying a new version), it stops: `Archive it first, so nothing is lost: cadrei uninstall --archive`. Then run `cadrei restore` and pick the setup you want back. The trial gets its own archive, which you can bring back the same way.
- An archive that was already brought back isn't offered again.
- From another home folder, projects show as not on this machine until you link or clone them. For a second computer, a GitHub backup (`r`) is the better way.
- Once you're sure you don't want an archive, delete its folder. Claude Code doesn't need anything in it.

## Troubleshooting

- **A member never replies.** Run `cadrei ls`. `! waiting on you` means it needs your approval or an answer: ask your orchestrator, or run `cadrei answer` to see what for ([Answering your team](#answering-your-team)). Its window is `cadrei attach <team> [project]` (or Ctrl-b then s in tmux). `✗ stopped by safeguards` means Claude's safeguards stopped its reply: open its window, type `/model` and pick the model again, or ask your orchestrator to restart it fresh. Otherwise look at its window; a new folder may show Claude Code's trust prompt, and `cadrei project trust <name>` avoids it next time.
- **The trust prompt still shows for a project.** Run `cadrei project trust <name>`. A Claude Code session running during the change may have written its own copy of `~/.claude.json` over it; accept the prompt once, or run the command again with no sessions running. If you set `CLAUDE_CONFIG_DIR`, your orchestrator uses the one in the terminal you run `cadrei` from, and members use the one of whatever starts them (your orchestrator, or `cadrei up` in a terminal), even in a tmux server that started with another one.
- **Claude Code asks to trust your crew's folder.** Claude Code hadn't run on this machine before your first run, or the crew was restored. If it's your crew, say yes.
- **A member reports a blocked action.** Decide whether to allow it. If yes, tell your orchestrator to allow that exact action; it restarts the member when needed.
- **Messages wait for approval.** The member runs in a different permission-mode class from your orchestrator. Its idle notices may not reach your orchestrator at all, so it may not hear that the member finished. Set `PERMISSION_MODE` in `cadrei.conf` to the mode you want for both, and keep your orchestrator in that mode.
- **"Your orchestrator is already open in another terminal".** Switch to that one, or close it first. Or move it to tmux with `cadrei tmux on`, so `cadrei` always takes you back to it.
- **You left tmux and want back in.** Run `cadrei`.
- **A project shows as missing.** Ask your orchestrator to clone it again or point to its new folder, or run `cadrei project sync` or `cadrei project link <name> <dir>`.
- **After switching Claude accounts.** Run `cadrei stop`, close your orchestrator, and run `cadrei` again. Chats resume.
- **You want to start over.** Don't delete `~/.cadrei/<name>`. Run `cadrei uninstall --keep-program` to reset the program and keep your crews, or `cadrei uninstall --archive --keep-program` to start like a new user and keep everything in an archive. Without `--keep-program`, a Homebrew install removes the program too. To remove cadrei and every crew for good, run `cadrei uninstall --all` ([Delete everything](#delete-everything)).
