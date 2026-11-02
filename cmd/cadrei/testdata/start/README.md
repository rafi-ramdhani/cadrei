# Start-up screens

Claude Code's screens before its prompt that wait on the user to pick an option, as `tmux capture-pane -p` would show them in a member's window. None of them is a live capture yet: each is drawn from Claude Code 2.1.304's own text and the way its menus draw (the pointer `❯` at the current option; no numbers where the screen hides them). PR E's live run recaptures them.

- `trust.txt`: the folder trust screen ("Accessing workspace:"), "No, exit" first and picked.
- `trust-old.txt`: the older trust screen, with numbered options.
- `login.txt`: "Select login method:", numbered.
- `style.txt`: the first run's text style screen, a ✔ at the current style.
- `server.txt`: one new MCP server from the project's `.mcp.json`, in a frame.
- `servers.txt`: several new MCP servers at once, with boxes to tick. Only its window can answer it.
- `apikey.txt`: an API key found in the environment. The key, which the screen shows cut short, is written as a placeholder.
- `bypass.txt`: the warning before a session runs with no permission checks.
- `imports.txt`: instruction files that import files from outside the folder.
- `imports-home.txt`: the same screen, importing a file of the user's own outside any project (`~/.aws/credentials`), which a yes would load into the member at every start.
- `other.txt`: a menu cadrei doesn't know, under a question of its own.

Claude Code's own em dashes in these screens are written as hyphens.
