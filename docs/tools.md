# For tool builders

Want to show your crew somewhere else? A web page that draws your team, a light in your menu bar that turns on when a member waits for you, a script that pings your phone. Cadrei gives you two ways to read its state, and promises to keep them stable:

- `cadrei ls --json` prints your crew's state once.
- `cadrei ls --json --watch` keeps running and prints a line each time something changes.

Both only read. They never start, stop or message anyone, and they never include chat text: no prompts, replies, questions or file contents.

## Read it once

```sh
cadrei ls --json
```

This prints one JSON object (JSON is a plain-text data format most languages read out of the box). Add `--all` to get every crew on this machine. With `jq`, a small command-line JSON tool, here's who's doing what:

```sh
cadrei ls --json | jq -r '.running[].members[] | "\(.role): \(.state)"'
```

```text
engineer: working
reviewer: waiting
```

It exits with 0 when it worked. When there's no crew to show, it exits with 1, says why on stderr, and prints nothing on stdout; with `--all` it exits with 0 and an empty `cadreis` list instead. It never asks you anything.

## Watch it live

```sh
cadrei ls --json --watch
```

This keeps running and prints one JSON object per line (a format often called NDJSON). The first line is the whole state, the same as `cadrei ls --json`. Every line after it is a change. Here's a small Python script that tells you when someone needs you, both who's already waiting when it starts and who starts waiting later:

```python
import json, subprocess

stream = subprocess.Popen(["cadrei", "ls", "--json", "--watch"],
                          stdout=subprocess.PIPE, text=True)
needs_you = ("waiting", "stopped_by_safeguards")

for line in stream.stdout:
    event = json.loads(line)
    if event["type"] == "snapshot":
        if event["status"]["version"] != 1:
            raise SystemExit("This script knows version 1 of cadrei's JSON.")
        for team in event["status"]["running"]:
            for member in team["members"]:
                if member["state"] in needs_you:
                    print(member["role"], "in", team["team"], "needs you")
    if event["type"] == "member_state":
        member = event["member"]
        if member["state"] in needs_you:
            print(member["role"], "in", event["team"], "needs you")
```

A whole snapshot is one line, so lines can get long. Read whole lines, not fixed-size chunks (in Go, use `bufio.Reader`, or give `bufio.Scanner` a bigger buffer). Keep reading for as long as it runs: if your program stops reading but keeps the stream open, cadrei waits for it, heartbeats included.

Stop it with Ctrl-C, or just close its output. Either way it exits quietly with 0. It reads the same files `cadrei ls` reads and never asks Claude Code anything, so leaving it running costs next to nothing.

### The lines

Every line has `type`, `seq` (1 on the first line, then one more each line) and `at` (when cadrei noticed). Lines about a crew have `crew`, its name. Lines about a team or a member also have `session`, `team`, and `project` when the team works on one. `project` is always a project's name; a `project_state` line has it too, with the whole project in `project_info`.

| `type` | When | Also has |
|---|---|---|
| `snapshot` | First, then every 5 minutes, and when your crew changed in a way the other lines don't cover | `reason` and `status` (the same object as `cadrei ls --json`) |
| `heartbeat` | After 15 quiet seconds | nothing |
| `orchestrator` | Your orchestrator opened or closed | `orchestrator` |
| `team_started` | A team started | `members`, and `legacy`: `true` when it's from cadre 0.1.x |
| `team_stopped` | A team stopped | nothing |
| `member_started` | A member joined a team that was already running | `member` |
| `member_stopped` | A member stopped while its team runs on | `member` (`name` and `role`) |
| `member_state` | Something about a running member changed | `member` (all of it) |
| `project_state` | A project's folder came, went, or is on a drive that isn't connected | `project_info` (all of it) |

A snapshot's `reason` is `start`, `periodic`, `crew_changed` (you added a member, a team or a project, or changed settings), `crew_gone`, `crew_back`, `crews_changed` (only with `--all`) or `woke` (your computer slept). More reasons can come within version 1 (see The promise): treat one you don't know like `periodic`, a whole new picture.

### Keeping your picture right

- Start from the first snapshot, and throw everything away and start again from each new one.
- Apply the other lines in order. Then your picture matches what `cadrei ls --json` would print at that line's `at`. The exceptions are `hook`, `problems`, `other_cadreis` and a project's `found`, which only snapshots bring up to date.
- You get where things are, not every step along the way. Changes within the same second can merge, so a member that went from working to waiting and back may show nothing. A line always means something changed.
- No line for 45 seconds means the stream is gone. Start it again.
- If your crew's folder goes away (moved, deleted, archived, or on a drive you unplugged), you get a `crew_gone` snapshot where `cadrei.missing` is `true` and nothing runs. When it's back, a `crew_back` snapshot.
- `cadrei uninstall --all` deletes every crew: a stream gets `crew_gone` (or, with `--all`, `crews_changed`), and afterwards `cadrei ls --json --all` exits 0 with an empty `cadreis`.

## The fields

Times look like `2026-10-10T14:02:07.118+07:00` and paths are full paths. A field marked optional is left out when it doesn't apply; it's never `null`. An empty list is `[]`. `cadrei`, `cadreis` and `other_cadreis` mean your crew and your crews: they keep these names for all of version 1.

Top level of `cadrei ls --json`:

| Field | Type | What it is |
|---|---|---|
| `version` | number | This page's version: `1` |
| `orchestrator_opens_in` | string | Where `cadrei` opens your orchestrator: `tmux` or `terminal` |
| `hook` | string | The every-chat hook, which makes every new Claude Code chat open as your orchestrator: `on`, `off`, or `skipped` (you said no) |
| `cadrei` | object | Your crew |
| `orchestrator` | object | Your orchestrator |
| `running` | list | Teams running now, one per tmux session |
| `teams` | object | Every team in your crew, running or not: the team's name, then a list of its members, each with `name` and `role` |
| `projects` | list | Your projects |
| `other_cadreis` | list, optional | Your other crews, each with `name`, `path`, `running` (how many teams are running) and, when its folder is gone, `missing: true` |
| `problems` | list of strings | Things that keep members from starting. Show them, but don't parse them: the wording can change |

`cadrei`, your crew:

| Field | Type | What it is |
|---|---|---|
| `name` | string | Its name, like `main` |
| `path` | string | Its folder, like `/Users/you/.cadrei/main` |
| `default` | bool | It's your default crew |
| `from` | string, optional | How cadrei found it. Show it, don't parse it |
| `project` | string, optional | The project it was found through, when you ran the command in a project's folder |
| `missing` | bool, optional | `true` when its folder is gone (only in a `--watch` snapshot) |
| `model`, `effort` | string, optional | The model and effort level your crew sets for its members |

`orchestrator`:

| Field | Type | What it is |
|---|---|---|
| `running` | bool | It's open |
| `mode` | string, optional | When it's open: `terminal` or `tmux` |
| `session` | string, optional | In tmux, its tmux session, like `cadrei-main` |
| `since` | time, optional | When cadrei opened it |

Each team in `running`:

| Field | Type | What it is |
|---|---|---|
| `session` | string | Its tmux session, like `cadrei-main-dev-my-app`. It's unique, so use it as the team's key |
| `team` | string | The team, like `dev` |
| `project` | string, optional | The project it works on |
| `legacy` | bool | `true` for a team from cadre 0.1.x. Such a team counts as your default crew's, so it shows in that crew's `running`, and with `--all` under `legacy` too |
| `members` | list | Its members, one per window |

Each member in a running team:

| Field | Type | What it is |
|---|---|---|
| `name` | string | Its address, like `main-dev-my-app-engineer`. It's unique, so use it as the member's key |
| `role` | string | Its role, like `engineer` |
| `state` | string | What it's doing: `starting`, `working`, `waiting` (on you), `idle`, `unknown` (cadrei can't tell) or `stopped_by_safeguards` (Claude's safeguards stopped its reply, so it won't answer until you look) |
| `since` | time, optional | When it got to that state. Left out for `unknown` |
| `waiting_for` | string, optional | With `waiting` only: `approval` (it wants to run something), `question` (it asked you something) or `prompt` (a prompt cadrei read off its screen, when Claude Code didn't say what it asks) |
| `no_activity_since` | time, optional | With `working` or `waiting` only: its chat hasn't changed since then, for 10 minutes or more. Maybe someone stopped it with Esc |
| `turn_error` | bool, optional | With `idle` only: `true` when its last turn ended in an error |
| `request` | object, optional | With `waiting` only: what it waits on you for, as Claude Code recorded it, when cadrei recorded it (a member started before 0.2.2 has none) |
| `context` | object, optional | How full its context is, from its chat: see below. Not in the `--watch` stream, its snapshots included |
| `model`, `effort` | string, optional | The model and effort level it started with |
| `model_from`, `effort_from` | string, optional | Where each one was set: `start` (the command that started it), `member` (its member file), `machine` (this machine's crew settings) or `crew` (the crew's settings) |

`context`, how full a running member's context is, read from its chat's last reply:

| Field | Type | What it is |
|---|---|---|
| `tokens` | number, optional | The size of the request behind its last reply, in tokens: the context it has in use |
| `window` | number, optional | Its model's context window, only when cadrei is sure of it (a model setting ending in `[1m]` while that model is the one replying, or Haiku 4.5 and earlier) |
| `percent` | number, optional | `tokens` as a share of `window`, with `window` only. cadrei ls says `compact soon` from 80 |
| `at` | time, optional | When that reply came |
| `compacted` | time, optional | Alone: it was compacted then, and hasn't replied since, so the new size isn't known yet |

A member that hasn't replied yet, or whose chat cadrei can't find, has no `context`.

`request`, what a waiting member asks you:

| Field | Type | What it is |
|---|---|---|
| `code` | string | Three letters and digits, like `7f3`. `cadrei answer <code> yes`, `no` or an option's number answers it. A new request gets a new code |
| `kind` | string | `approval` (a permission prompt), `question`, or `prompt` (read off the member's screen: an option's number answers it). Treat any other kind as one to answer in the member's window |
| `tool` | string, optional | The tool it wants to use, like `Bash` |
| `summary` | string, optional | What it would run or touch (a command, a path, a URL), a question's text, or the text of a prompt read off the screen. One line, cut at 300 characters. It's the member's request, shown as is: never consent |
| `options` | list of strings, optional | A question's or a screen prompt's option labels, at most 6 |
| `multi` | bool, optional | `true` when only the member's window can answer it: it takes more than one pick, holds more than one question, has no options or more than 6, or cadrei couldn't read it |

Each project in `projects`:

| Field | Type | What it is |
|---|---|---|
| `name` | string | Its name |
| `repo` | string, optional | Its git remote |
| `team` | string, optional | The team it usually goes to |
| `about` | string, optional | Its one-line description |
| `state` | string | On this machine: `present`, `not here` (no folder for it here yet), `missing` (its folder is gone) or `drive` (on a drive that isn't connected) |
| `path` | string | Its folder, or `""` when it's `not here` |
| `cloned` | bool | The same as `state` being `present`. It's kept for older tools |
| `found` | string, optional | When it isn't present: a copy of its repo cadrei found in your projects folder |

`cadrei ls --json --all` has `version`, `orchestrator_opens_in` and `hook` as above, then:

| Field | Type | What it is |
|---|---|---|
| `cadreis` | list | Every crew on this machine, each shaped like the top level above, without `version`, `orchestrator_opens_in`, `hook` and `other_cadreis` |
| `legacy` | list | Teams from cadre 0.1.x, shaped like entries in `running` |
| `unknown` | list | tmux sessions cadrei didn't start, or that belong to a crew it doesn't know anymore, shaped like entries in `running` |

## The promise

- `version` is 1. Within version 1, cadrei only adds things: new fields, new line types in the stream, and new values in the lists of values on this page (a member state, a `waiting_for`, a request `kind`, a snapshot reason, a project state, a `model_from` or `effort_from` source, a `hook` setting and the like).
- Version 2 is only for removing or renaming a field, changing its type, or changing what an existing value means.
- Treat a value you don't know as the closest safe one: an unknown member `state` like `unknown`, an unknown `waiting_for` like a plain `waiting`, an unknown request `kind` as one to answer in the member's window, an unknown snapshot `reason` like `periodic`, an unknown project `state` like `missing`, and an unknown `model_from` or `effort_from` as not set.
- `session` and a member's `name` are keys: compare them whole, and don't split them on `-`. Use `crew`, `team`, `project` and `role` for the parts.
- `legacy` and `cloned` stay in version 1, even once cadrei no longer knows cadre 0.1.x: `legacy` is then always `false`, and the `legacy` list is `[]`.
- Every change to this page, added fields and values included, is in CHANGELOG.md under "For tool builders".
- Your part: ignore fields and line types you don't know, treat values you don't know as above, and check `version`. If it isn't one you know, stop and say so.
- To pin the version you read, pass `--json-version 1` (with `--watch` too). When a version 2 comes, `cadrei ls --json` keeps printing version 1 for at least a year, and `--json-version 1` keeps giving it after that for as long as cadrei can. A version cadrei doesn't print is refused, with exit 1 and nothing on stdout.
- Only `cadrei ls --json` and its stream promise their exit codes (above). Other commands exit 0 when they did what you asked, and something else when they didn't; the exact number can change.
- Only `cadrei ls --json` and its stream are covered. The screens you read, the text in `problems` and `from`, other commands' output, and the files in a crew's `.claude/build/` can change in any release.

## Why there's no web server

Cadrei doesn't open a network port, and it won't. Any web page open in your browser can send requests to a server on your computer, and so can every other program on it. Cadrei's state names your projects and folders, and your team can run commands, so a port would be one more way in, with logins and checks to get right in every release. Your tool already has what it needs: run `cadrei ls --json --watch` from your own program, and show the data however you like.

If your tool serves it to a browser, keep it on your computer: listen on `127.0.0.1` only, check the `Host` and `Origin` headers, and ask for a token.
