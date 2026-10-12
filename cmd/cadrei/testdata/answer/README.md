# Prompt screens for cadrei answer

Claude Code's permission prompts and questions, as `tmux capture-pane -p` shows them in a member's window. The tests in `answer_test.go` read them to check which key `cadrei answer` presses, and that it presses none when the screen doesn't match.

- `bash-prompt.txt`: a Bash permission prompt in default mode, with the "switch to auto mode" choice (spike S4, Claude Code 2.1.296). The tip line's dash is written as a semicolon.
- `question.txt`: an `AskUserQuestion` question with two options, then `Type something.` and `Chat about this` (spike S5, Claude Code 2.1.296).
- `bash-prompt-live.txt`: a member's Bash prompt, captured in the 0.2.2 end-to-end run (Claude Code 2.1.296): the command between dashed rules, a long option that wraps, and the member's reply above it. The folder is written `<work>` and the tip's dash as a hyphen.
- `question-live.txt`: a member's question, captured in the 0.2.2 end-to-end run: a rule between its own options and the two Claude Code adds, and the window's status line under the footer.
- `multi-live.txt`: a multi-select question (checkboxes and a Submit tab), captured in the 0.2.2 end-to-end run. `cadrei answer` refuses it: only its window can answer.
- `write-live.txt`: a member's Write prompt, captured in the 0.2.2 end-to-end run. It names the file from the member's folder, while the record has its full path; its second choice switches to accept edits.
- `wrapped-live.txt`: a member's Bash prompt for a long command, captured in the 0.2.2 end-to-end run: the command wraps onto a second framed (│) line.
