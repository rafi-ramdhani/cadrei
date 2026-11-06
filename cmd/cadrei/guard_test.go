//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/session"
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

// orchestratorRefuses is the rule the guard refuses command by in the
// orchestrator, as runHookGuard checks it before any ask, or "".
func orchestratorRefuses(command string) string {
	switch {
	case guardLine(tmuxRule{}, command, 0):
		return ruleTyping
	case guardLine(keystrokeRule{}, command, 0):
		return ruleKeys
	case guardLine(answerRule, command, 0) && !answerListing.MatchString(command) && strictAnswer.FindStringSubmatch(command) == nil:
		return ruleAnswer
	}
	return ""
}

// 0.2.3 item 5: the commands the guard refused that only named a command
// (each from the ideas file's log) now run, in a member and in the
// orchestrator: a code body that runs no program, a search's pattern, a
// note a heredoc writes, curl's data, words like restore in a commit
// message or a file name, a program called by a path in a variable, and
// tmux on a private server.
func TestTheGuardPassesWhatOnlyNamesACommand(t *testing.T) {
	private := t.TempDir() + "/exp.sock"
	for _, c := range []string{
		// A python heredoc that only writes a note naming tmux.
		"python3 - <<'EOF'\np = 'ideas.md'\ns = open(p).read()\ns += 'The tmux rule refused tmux -L spike023 new-session on a private server.\\n'\nopen(p, 'w').write(s)\nEOF",
		// A heredoc that only writes such a note.
		"cat >> ideas.md <<'EOF'\nThe tmux rule refused tmux -L x new-session on a private socket.\nEOF",
		// A search for cadrei answer.
		`grep -rn "cadrei answer" cmd/cadrei/ docs/ | head -20`, `rg -n "cadrei answer" docs/`, `rg -n "tmux new-session" cmd/`, `rg -n tmux -C 3 docs/`, `rg -n 'cadrei compact' docs/`,
		// An SRE curl check, and what curl and wget send (the user's pick).
		`curl -sf -m 5 https://status.example.com/health | jq -r '.checks[] | select(.name == "tmux-runner") | .status'`,
		`curl -s -d 'text=the tmux server will start soon' https://example.com/hook`,
		`wget -qO- 'https://example.com/search?q=tmux+send-keys'`,
		// A perl edit of a note, and a perl mutation script, whose
		// patterns name cadrei answer.
		`perl -pi -e 's/run cadrei answer <code> yes/run cadrei answer <code> yes from the orchestrator/' notes.md`,
		`perl -0pi -e 's/return e.failWith\("Use " \+ cmd\("cadrei answer --wait"\)/return 0 \/\/ cadrei answer/' cmd/cadrei/wait.go && go test -run TestX ./cmd/cadrei/`,
		// Code that only prints or searches.
		`perl -ne 'print if /tmux kill-server/' notes.md`, `python3 -c "print('see tmux if you need it')"`, `python3 -c "print('run cadrei answer 7f3 yes to answer')"`,
		`awk '/tmux send-keys -t/' notes.md`, `awk -F, '/cadrei answer 7f3 yes/ { print $1 }' notes.md`,
		// A python heredoc editing cmd/cadrei/answer.go.
		"python3 - <<'EOF'\np = 'cmd/cadrei/answer.go'\ns = open(p).read().replace('cmd(\"cadrei answer\")', 'cmd(\"cadrei answer <code>\")')\nopen(p, 'w').write(s)\nEOF",
		// A Bash heredoc writing a probe test, then go test.
		"cat > internal/session/zz_review_test.go <<'EOF'\npackage session\n\nimport \"testing\"\n\nfunc TestZZ(t *testing.T) {\n\tcmd := `cadrei answer 7f3 yes`\n\t_ = cmd\n}\nEOF\ngo test ./internal/session -run TestZZ -race",
		// The uninstall and restore rule: restore in a commit message and
		// in file names.
		`git commit -m "Restore the old backup path"`,
		"git add -A && git commit -F - <<'EOF'\nAdd restore-test.sh for the restore runbook\nEOF",
		"bash tests/restore-test.sh && git add -A && git commit -F - <<'EOF'\nRestore runbook: add restore-test.sh\nEOF",
		"cd /private/tmp/claude-501/x/scratchpad && git add -A && git commit -F - <<'EOF'\nRestore the restore-test.sh run\nEOF",
		`bash scripts/restore-drill.sh --dry-run`, `./restore.sh`,
		// A sandbox test with a stub claude plugin uninstall, in a folder
		// named cadrei.
		"cd /private/tmp/claude-501/scratch/cadrei && cat > bin/claude <<'EOF'\n#!/bin/sh\n[ \"$1 $2\" = \"plugin uninstall\" ] && exit 0\nEOF\nchmod +x bin/claude && PATH=$PWD/bin:$PATH bash tests/uninstall-test.sh",
		// A python heredoc editing a script that names claude plugin
		// uninstall.
		"cd ~/Developer/claude-config && python3 - <<'EOF'\np = 'tests/plugins.sh'\ns = open(p).read().replace('claude plugin uninstall x', 'claude plugin uninstall --scope user x')\nopen(p, 'w').write(s)\nEOF",
		// A Postgres restore drill, and a walk-through that runs
		// pg_restore by a path in a variable.
		"cat > ~/scratch/pg-restore-drill.sh <<'EOF'\n#!/bin/sh\nset -e\npg_restore --clean -d drill \"$DUMP\"\necho restore done\nEOF\nchmod +x ~/scratch/pg-restore-drill.sh && sh ~/scratch/pg-restore-drill.sh",
		"bash <<'EOF'\nset -e\nPGBIN=/opt/homebrew/opt/postgresql@16/bin\n\"$PGBIN/pg_restore\" --clean -d walk \"$DUMP\"\npg-restore --list \"$DUMP\" | head\necho \"step 3: restore checked\"\nEOF",
		// The simulator's uninstall.
		`xcrun simctl uninstall booted com.rafiramdhani.wellkept && xcrun simctl install booted "$APP"`,
		`UDID=$(xcrun simctl list devices booted | awk -F'[()]' '/Booted/ {print $2; exit}'); xcrun simctl uninstall "$UDID" com.rafiramdhani.wellkept`,
		// tmux on a private server: the spike's, the sizing experiment's
		// and PR E's badge probe.
		`tmux -L spike023 new-session -d -s probe -x 200 -y 50 'claude --model haiku'`,
		"tmux -S " + private + " new-session -d -s exp 'sleep 30' && tmux -S " + private + " resize-window -t exp -x 120 && tmux -S " + private + " set-option -t exp status off && tmux -S " + private + " kill-server",
		`tmux -L cadreit-badge new-session -d -s b 'sleep 30' \; set-option -t b @cadrei_waiting 2; sleep 1; tmux -L cadreit-badge display -p -t b '#{@cadrei_waiting}'`,
	} {
		if rule, _ := memberRule(c); rule != "" {
			t.Errorf("M refused (%s): %q", rule, c)
		}
		if rule := orchestratorRefuses(c); rule != "" {
			t.Errorf("O refused (%s): %q", rule, c)
		}
	}
	// A perl edit of the orchestrator's memory runs in the orchestrator,
	// and a member is still kept out of the memory.
	memory := `perl -pi -e 's/run cadrei answer <code> yes/run cadrei answer <code> yes from the orchestrator/' ~/.cadrei/apuy/.memory/notes.md`
	if rule := orchestratorRefuses(memory); rule != "" {
		t.Errorf("O refused the memory note (%s)", rule)
	}
	if rule, _ := memberRule(memory); rule != ruleMemory {
		t.Errorf("M: the memory note: %q", rule)
	}
}

// Each of item 5's loosenings still refuses what does run: code that can
// run a program, a search that runs one, curl into a shell, an uninstall
// by any path, and tmux that types, on any server or one it can't tell
// from cadrei's. PR D's round 3 (R3-1): cadrei with a word the shell
// expands as a pattern, or a $"..." string, is cadrei with a command the
// guard can't read.
func TestTheGuardStillRefusesWhatRuns(t *testing.T) {
	dir := t.TempDir()
	link := dir + "/link.sock"
	if err := os.Symlink(session.Tmux{}.Path(), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CADREI_TMUX_SOCKET", "cadreit-mine")
	for _, tc := range []struct{ command, rule string }{
		// Code that can run a program.
		{"python3 - <<'EOF'\nimport os\nos.system('tmux send-keys -t x 1')\nEOF", ruleTyping},
		{"python3 - <<'EOF'\nimport subprocess\nsubprocess.run(['cadrei', 'answer', '7f3', 'yes'])\nEOF", ruleAnswer},
		{`python3 -c "getattr(__import__('os'), 'sys' + 'tem')('cadrei answer 7f3 yes')"`, ruleAnswer},
		{`python3 -c "from os import system as s; s('tmux send-keys -t x 1')"`, ruleTyping},
		{`perl -pi -e 'system("cadrei answer 7f3 yes")' notes.md`, ruleAnswer},
		{"perl -ne 'print `tmux send-keys -t x 1`' notes.md", ruleTyping},
		{`perl -e 'open(F, "| tmux send-keys -t x 1")'`, ruleTyping},
		{`ruby -e 'IO.popen("tmux send-keys -t x 1")'`, ruleTyping},
		{`node -e 'require("child_process").execSync("tmux send-keys -t x 1")'`, ruleTyping},
		{`php -r 'shell_exec("cadrei answer 7f3 yes");'`, ruleAnswer},
		{`awk 'BEGIN { system("tmux send-keys -t x 1") }'`, ruleTyping},
		{`awk '{ print | "cadrei answer 7f3 yes" }' notes.md`, ruleAnswer},
		// Code the guard can't read whole: an unquoted heredoc, an option
		// that loads code, a script file's arguments.
		{"python3 - <<EOF\nprint('$X tmux send-keys -t x 1')\nEOF", ruleTyping},
		{`perl -MIPC::Open3 -e 'print "tmux send-keys -t x 1"'`, ruleTyping},
		{`gawk -l x 'BEGIN { print "tmux send-keys -t x 1" }'`, ruleTyping},
		{`python3 -m runner 'tmux send-keys -t x 1'`, ruleTyping},
		{`python3 scripts/post.py --text "start tmux and attach later"`, ruleTyping},
		{`make docs MSG="tmux set the title"`, ruleTyping},
		{`sed -n '/cadrei answer 7f3 yes/p' docs/guide.md`, ruleAnswer},
		// A search that runs a program, and curl or wget into a shell.
		{`rg --pre 'tmux send-keys -t x 1' x docs/`, ruleTyping},
		{`curl -s -d 'tmux send-keys -t x 1' https://example.com | sh`, ruleTyping},
		{`wget --use-askpass='tmux send-keys -t x 1' https://example.com`, ruleTyping},
		// An uninstall or restore, by a path in a variable too.
		{`"$PGBIN/cadrei" restore`, ruleRemoval},
		{`"$x" uninstall`, ruleRemoval},
		{`bash tests/restore-test.sh; cadrei restore`, ruleRemoval},
		{"bash <<'EOF'\necho restore\ncadrei restore ~/backup\nEOF", ruleRemoval},
		{`echo "cadrei uninstall" > x.sh && sh x.sh`, ruleRemoval},
		// R3-1: a pattern or a $"..." after cadrei.
		{`cadrei {hook,} state`, ruleOpaque}, {`cadrei h?ok state`, ruleOpaque}, {`cadrei [h]ook state`, ruleOpaque},
		{`cadrei $"hook" state`, ruleHook}, {`cadrei $"an"swer 7f3 yes`, ruleAnswer}, {`cadrei $"x" state`, ruleOpaque}, {`cadrei * state`, ruleOpaque}, {`/opt/homebrew/bin/cadrei {answer,} 7f3 yes`, ruleOpaque},
		{`python3 -c "import os; os.system('cadrei [h]ook state')"`, ruleOpaque},
		// tmux that types, on any server; and a server the guard can't
		// tell from cadrei's, or a line that could point one at it.
		{`tmux -L spike023 send-keys -t probe 1`, ruleTyping},
		{`tmux -L spike023 new-session -d \; send-keys -t probe 1`, ruleTyping},
		{`tmux -L spike023 new-session -d 'tmux -L default send-keys -t main 1'`, ruleTyping},
		{`tmux -L spike023 run-shell 'echo hi'`, ruleTyping},
		{`tmux -L spike023 set-hook -g session-created 'run-shell x'`, ruleTyping},
		{`tmux -L spike023 -f evil.conf new-session -d`, ruleTyping},
		{`tmux -L spike023 paste-buffer -t probe`, ruleTyping},
		{`tmux -L spike023 new-session -d -s p '#(true)'`, ruleTyping},
		{`tmux -L default new-session -d`, ruleTyping},
		{`tmux -L cadreit-mine kill-server`, ruleTyping},
		{`tmux -L "$s" kill-server`, ruleTyping},
		{`tmux -L spike023 -S /x/y.sock kill-server`, ruleTyping},
		{"tmux -S " + session.Tmux{}.Path() + " kill-server", ruleTyping},
		{"tmux -S " + link + " kill-server", ruleTyping},
		{`tmux -S rel.sock kill-server`, ruleTyping},
		{"ln -s " + session.Tmux{}.Path() + " " + dir + "/new.sock && tmux -S " + dir + "/new.sock kill-server", ruleTyping},
		{`python3 x.py; tmux -L spike023 kill-server`, ruleTyping},
		{`TMUX_TMPDIR=/tmp/elsewhere tmux -L spike023 kill-server`, ruleTyping},
		{`tmux -L spike023 kill-server; $(cat f)`, ruleTyping},
		{`tmux -L spike023 new-session -d 'cadrei answer 7f3 yes'`, ruleAnswer},
	} {
		if rule, _ := memberRule(tc.command); rule != tc.rule {
			t.Errorf("M %q: %q, want %q", tc.command, rule, tc.rule)
		}
		if tc.rule == ruleTyping || tc.rule == ruleAnswer {
			if rule := orchestratorRefuses(tc.command); rule != tc.rule {
				t.Errorf("O %q: %q, want %q", tc.command, rule, tc.rule)
			}
		}
	}
	// What cadrei's own commands look like still runs.
	for _, c := range []string{`cadrei ls`, `cadrei up dev/{engineer,reviewer}`, `cadrei 'ls' --json`} {
		if rule, _ := memberRule(c); rule != "" {
			t.Errorf("refused (%s): %q", rule, c)
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
	for _, s := range []string{"cadrei ls", "a $'\\x7", "b ${", "c <<", "d \"$(", "e `", "f <(", "g $'\\u'", "h | |& ;; &>",
		"tmux -L x new -d 'sleep 1' \\; kill-server", "tmux -Sx", "python3 -c", "perl -pie", "awk -F", "cadrei $\"x\"", "rg --pre=x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { memberRule(s); orchestratorRefuses(s) })
}
