//go:build !windows

package main

import (
	"strings"
	"testing"
)

// The guard refuses cadrei's uninstall and restore in every form a member
// could type, and lets everything else run.
func TestTheGuardSeesEveryWayToUninstall(t *testing.T) {
	for _, c := range []string{
		`cadrei uninstall --all --confirm "delete everything"`,
		`env -u CADREI_MEMBER cadrei uninstall --all`,
		`CADREI_MEMBER= cadrei uninstall --yes`,
		`sh -c "cadrei uninstall --all"`,
		`bash -c 'cadrei restore'`,
		`/opt/homebrew/bin/cadrei uninstall --archive --yes`,
		`./cadrei uninstall`, `../bin/cadrei restore ~/x`,
		`cadre uninstall`,
		`cad"rei" unin'st'all`, `cadrei un\install`, `CADREI UNINSTALL`,
		`ls; cadrei uninstall --yes`, `cadrei ls && cadrei restore`,
		`c=cadrei; $c uninstall`, `x=$(which cadrei); "$x" uninstall --all`,
		"`which cadrei` restore",
		`printf 'uninstall --all' | xargs cadrei`,
		`echo uninstall | bash`,
		`cadrei unin${x}stall --all`,
		"cadrei\tuninstall", "cadrei\nuninstall", "cadrei \\\nuninstall",
		// Wrapped, chained and nested.
		`bash -c '"$0" uninstall' cadrei`, `zsh -lc 'cadrei restore'`, `$SHELL -c 'cadrei uninstall'`,
		"bash <<'EOF'\ncadrei uninstall --all\nEOF", "sh <<< 'cadrei restore'",
		"cat <<'EOF' | sh\ncadrei uninstall\nEOF", `printf 'cadrei uninstall' | sh`,
		`eval "cadrei uninstall"`, `eval 'c=cadrei; $c restore'`,
		`echo "$(cadrei uninstall --yes)"`, "cat <<EOF\n$(cadrei uninstall)\nEOF", "cat <<EOF\n`cadrei restore`\nEOF",
		`x=uninstall; cadrei $x`, `for s in restore; do cadrei "$s"; done`,
		`python3 -c 'import os; os.system("cadrei uninstall --all")'`,
		"python3 - <<'EOF'\nimport subprocess\nsubprocess.run([\"cadrei\", \"restore\"])\nEOF",
		`find . -name x -exec cadrei uninstall \;`, `sudo -u me cadrei uninstall`, `sudo -u me bash -c 'cadrei restore'`,
		`timeout 5 cadrei restore`, `nohup cadrei uninstall &`, `command cadrei uninstall`, `exec cadrei uninstall`,
		`(cadrei uninstall)`, `{ cadrei restore; }`, `if true; then cadrei uninstall; fi`, `true || cadrei restore`,
		`echo "cadrei uninstall" > x.sh && sh x.sh`, `bash < x.sh # cadrei uninstall`, `source <(echo cadrei uninstall)`,
		`git -c alias.x='!cadrei uninstall' x`, `git config alias.x '!cadrei restore'`, `gh alias set x 'cadrei uninstall' --shell`,
		`tmux send-keys -t x 'cadrei uninstall' Enter`, `npx cadrei uninstall`, `cadrei $'\x75ninstall'`,
		`echo cadrei uninstall | xargs -0 sh -c`, `xargs cadrei < args.txt`, `cadrei 'uninstall`,
		`BASH -c 'cadrei restore'`, `cadrei uninstall 2>&1 | tee log`, `cadrei uninstall &>/dev/null`,
		`bash -c "$(printf 'cadrei uninstall')"`, `eval "$(echo cadrei restore)"`, `$(echo cadrei) $(echo uninstall)`,
		`cadrei "$(echo uninstall)"`, `/usr/bin/env cadrei uninstall`, `nice -n 5 cadrei restore`, `env -S 'cadrei uninstall'`,
		"node <<'EOF'\nrequire('child_process').execSync('cadrei uninstall')\nEOF", `bash <(echo cadrei uninstall)`,
		`alias x='cadrei uninstall'; x`, `a=(cadrei restore); "${a[@]}"`, `sh -c "cadrei uninst""all"`, `xargs -I{} cadrei {} <<< uninstall`,
	} {
		if !removesCrews(c) {
			t.Errorf("let through: %q", c)
		}
	}
	for _, c := range []string{
		`cadrei ls --json`, `cadrei up dev my-app`, `cadrei stop dev`, `git status`, `git restore notes.md`,
		`npm uninstall left-pad`, `echo restore`, `ls -la ~/.cadrei`, `go test ./...`,
	} {
		if removesCrews(c) {
			t.Errorf("refused: %q", c)
		}
	}
}

// The guard reads a command as a shell does: words that only go to a
// program that never runs them, and code that names restore but not
// cadrei's program, aren't a removal. These are the commands it blocked
// before.
func TestTheGuardPassesTextThatOnlyMentionsRemoval(t *testing.T) {
	for _, c := range []string{
		// Go code with restore() and term.Restore, through a heredoc to
		// python, in a folder named after cadrei, beside an *env.
		"cd ~/Documents/cadre-worktree-x && python3 - <<'EOF'\n" +
			"p = 'cmd/cadrei/orchestrator.go'\n" +
			"s = open(p).read().replace('x', '''func (e *env) lendTerminal() (back func()) {\n" +
			"\trestore := func() { term.Restore(fd, saved) }\n\tdefer restore()\n}''')\n" +
			"open(p, 'w').write(s)\nEOF",
		`sed -i '' 's/defer term.Restore(fd, saved)/defer restore()/' cmd/cadrei/orchestrator.go`,
		// A grep.
		`grep -rn "restore\|uninstall" cmd/cadrei/ docs/`, `grep -n "cadrei restore" README.md | head`,
		`cd ~/src/cadre && grep -rn 'restore()' cmd/cadrei | wc -l`,
		// Heredocs.
		"cat > notes.md <<'EOF'\nRun cadrei uninstall, then cadrei restore. It can't hurt.\nEOF",
		"git commit -q -F - <<'EOF'\nKeep cadrei restore and cadrei uninstall away from members\nEOF",
		"cat <<-EOF > x.md\n\tcadrei restore $HOME\n\tEOF",
		// A gh pr body.
		`gh pr create --title "Guard fix" --body "The guard blocked any command that said cadrei uninstall or cadrei restore."`,
		"gh pr create --title x --body \"$(cat <<'EOF'\nIt blocked cadrei restore, so it can't be used.\nEOF\n)\"",
		"gh pr edit 42 --body-file - <<'EOF'\ncadrei uninstall --all\nEOF",
		// File contents and commit messages.
		`git commit -m "Say cadrei restore in the guide"`, `git -C ~/src/cadre commit -m "cadrei uninstall docs"`,
		`echo 'cadrei uninstall' >> notes.md`, `go test -run 'TestRestore|Uninstall' ./cmd/cadrei/`,
		`sed -n 1,40p cmd/cadrei/uninstall.go`, `go test -run TestTheGuard ./cmd/cadrei/`,
	} {
		if removesCrews(c) {
			t.Errorf("refused: %q", c)
		}
	}
}

// cadrei hook guard reads Claude Code's tool call: a Bash call it refuses
// ends with exit 2 and why, any other call goes on, and input it can't
// read is refused.
func TestTheGuardHook(t *testing.T) {
	sandbox(t)
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	bash := func(command string) string {
		return `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":` + strconvQuote(command) + `}}`
	}
	for input, want := range map[string]int{
		bash("cadrei uninstall --all"):         2,
		bash(`sh -c "cadrei restore"`):         2,
		bash("git status"):                     0,
		`{"tool_name":"Edit","tool_input":{}}`: 0,
		`{"tool_name":"Bash","tool_input":{}}`: 2,
		`{"tool_input":{"command":"ls"}}`:      2,
		"not json":                             2,
		"":                                     2,
	} {
		code, out, errOut := callIn(input, "hook", "guard")
		if code != want || out != "" || (want == 2 && errOut == "") {
			t.Errorf("%q: exit %d, %q %q", input, code, out, errOut)
		}
	}
	if _, _, errOut := callIn(bash("cadrei uninstall --all"), "hook", "guard"); !strings.Contains(errOut, "Team members can't run cadrei uninstall or cadrei restore, in any form.") {
		t.Errorf("why: %q", errOut)
	}
}

// strconvQuote quotes s as a JSON string.
func strconvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

// The guard reads any line without failing: a half-written quote,
// heredoc or expansion is read the blunt way.
func FuzzTheGuardReadsAnyLine(f *testing.F) {
	for _, s := range []string{"cadrei ls", "a $'\\x7", "b ${", "c <<", "d \"$(", "e `", "f <(", "g $'\\u'", "h | |& ;; &>"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { removesCrews(s) })
}
