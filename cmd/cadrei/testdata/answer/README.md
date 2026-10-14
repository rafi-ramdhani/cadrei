# Prompt screens for cadrei answer

Claude Code's permission prompts and questions, as `tmux capture-pane -p` shows them in a member's window. The tests in `answer_test.go` read them to check which key `cadrei answer` presses, and that it presses none when the screen doesn't match.

- `bash-prompt.txt`: a Bash permission prompt in default mode, with the "switch to auto mode" choice (spike S4, Claude Code 2.1.296). The tip line's dash is written as a semicolon. As written down, it has no dashed rules around the command, which every live capture has, so `cadrei answer` presses nothing for it.
- `question.txt`: an `AskUserQuestion` question with two options, then `Type something.` and `Chat about this` (spike S5, Claude Code 2.1.296). It has no solid rule above it, so the tests that read it put one there.
- `bash-prompt-live.txt`: a member's Bash prompt, captured in the 0.2.2 end-to-end run (Claude Code 2.1.296): the command between dashed rules, a long option that wraps, and the member's reply above it. The folder is written `<work>` and the tip's dash as a hyphen.
- `question-live.txt`: a member's question, captured in the 0.2.2 end-to-end run: a rule between its own options and the two Claude Code adds, and the window's status line under the footer.
- `multi-live.txt`: a multi-select question (checkboxes and a Submit tab), captured in the 0.2.2 end-to-end run. `cadrei answer` refuses it: only its window can answer.
- `write-live.txt`: a member's Write prompt, captured in the 0.2.2 end-to-end run. It names the file from the member's folder, while the record has its full path; its second choice switches to accept edits.
- `wrapped-live.txt`: a member's Bash prompt for a long command, captured in the 0.2.2 end-to-end run: the command wraps onto a second framed (│) line.
- `edit-live.txt`: a member's Edit prompt, captured in the second end-to-end run: "Edit file", the file from the member's folder, then the change between dashed rules.
- `fetch-live.txt`: a WebFetch prompt: no "Esc to cancel" line, and the URL shown as `url:` with a slash added. `cadrei answer` leaves these to the member's window.
- `mcp-live.txt`: an MCP tool's prompt (a stand-in server): it shows the tool's input as `path: "resume.pdf"`, not the JSON cadrei records, so it's left to the window too.
- `plan-live.txt`: a plan approval (ExitPlanMode): no plain "Yes" and no "Esc to cancel" line.
- `para-live.txt`: a member's Bash prompt for a command with a blank line in it (a heredoc), framed with │.
- `tall-live.txt`: a Bash prompt taller than the member's 80x24 window, captured in the round 2 run: the whole window, with no solid rule. Claude Code draws on the alternate screen, so the prompt's top isn't kept anywhere tmux can read. Read as it is, not with a rule above it.
- `longpath-live.txt`: a Write prompt for a file outside the member's folder whose path is longer than the window: Claude Code cuts its start, so the line under "Create file" starts with `…`. The path the user typed is written `/Users/me/e2e`.

Claude Code's own em dashes in these screens are written as hyphens.
