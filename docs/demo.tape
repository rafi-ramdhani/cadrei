# The README's demo: from a fresh machine to a team at work, in about a
# minute of screen time. Recorded with VHS (https://github.com/charmbracelet/vhs):
#
#   vhs docs/demo.tape        # writes docs/demo.gif
#
# Before recording:
# - Use a throwaway HOME or a separate user account. The first run creates
#   ~/.cadrei, links cadrei's instructions into ~/.claude and trusts folders
#   in ~/.claude.json, so a recording must never run against a setup someone
#   uses. It must not hold ~/.cadrei or ~/.cadrei-archive either, or the
#   first run offers to plug those back in instead. A development build of
#   cadrei runs only with HOME in a temporary folder; for a throwaway HOME
#   elsewhere, set CADREI_DEV_REAL_HOME=1.
# - In that HOME: cadrei 0.2.2 on PATH, tmux 3.2 or newer, git with a name
#   and email set, and Claude Code installed and logged in. Run claude once
#   there and log in: that makes ~/.claude.json, so the first run can trust
#   the new crew's folder and your orchestrator opens without a trust prompt.
# - A small git repository at ~/src/notes-app with an origin remote and no
#   Claude Code settings of its own (no .claude/settings.json,
#   .claude/settings.local.json or .mcp.json), so Enter adds it as the first
#   project. Its name can change; keep it small so the review finishes
#   within the hidden wait.
# - Run it outside tmux, so your orchestrator opens in cadrei's tmux.
# - Afterwards, cadrei stop --yes in that HOME stops the team, and
#   tmux kill-session -t cadrei-main stops your orchestrator.
# - The Sleep lengths depend on the machine and on Claude Code's speed;
#   tune them after a first take.

Output docs/demo.gif

Require cadrei
Require claude
Require tmux
Require git

Set Shell "bash"
Set FontSize 18
Set Width 1280
Set Height 760
Set Padding 24
Set TypingSpeed 45ms
Set Theme "Catppuccin Mocha"

# Always the styled screens (colors and check marks), in the brand's
# truecolor clay and with the mascot, whatever the recording's terminal
# tells cadrei about itself.
Env FORCE_COLOR "1"
Env COLORTERM "truecolor"

Hide
Type "cd ~/src/notes-app && export PS1='$ ' && clear"
Enter
Show

# The first run: the checks, then the whole plan on one screen.
Type "cadrei"
Sleep 600ms
Enter
Sleep 3s

# Enter sets it all up: the crew main, notes-app as its first project,
# cadrei's instructions, and your orchestrator in tmux.
Enter
Sleep 2.5s

# Your orchestrator speaks first: the greeting and three ways to start.
Sleep 12s

# Pick the read-only review. Only the reviewer runs, and nothing changes.
Type "1"
Sleep 600ms
Enter
Sleep 15s

# The reply ends with the team line, and its one-time hint under it.
Sleep 4s

# The reviewer works; skip the wait.
Hide
Sleep 180s
Show

# The reviewer's top 3, relayed. Then look at the team, from your
# orchestrator: Ctrl-b then s lists every session.
Sleep 6s
Ctrl+B
Type "s"
Sleep 3s
Escape
Sleep 1s

# Leave tmux (Ctrl-b then d). Everything keeps running.
Ctrl+B
Type "d"
Sleep 2s

# The status screen: who's working, who's idle, and your projects.
Type "cadrei ls"
Sleep 500ms
Enter
Sleep 5s

# Back to your orchestrator, right where you left it.
Type "cadrei"
Sleep 500ms
Enter
Sleep 4s
