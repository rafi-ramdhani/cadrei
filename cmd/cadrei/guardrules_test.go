//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// answerCrew is a crew with two members waiting on the user: the engineer
// on my-app at a Bash prompt, and the writer in docs with a question. It
// returns the crew's folder and the two codes.
func answerCrew(t *testing.T) (c, eng, writer string) {
	t.Helper()
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c = home + "/.cadrei/work"
	os.MkdirAll(home+"/src/my-app", 0o755)
	register(t, home, "work", "my-app:\n  team: dev\n  path: ~/src/my-app\n")
	os.MkdirAll(c+"/members/docs", 0o755)
	os.WriteFile(c+"/members/docs/writer.md", []byte("# Member: writer\n"), 0o644)
	must(t, "up", "dev/engineer", "my-app")
	must(t, "up", "docs/writer")
	// cadrei on the PATH, which a full path in the strict form must be.
	writeStub(t, home+"/bin/cadrei", "#!/bin/sh\nexit 0\n")
	feed(t, c, "work-dev-my-app-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"gh pr merge 42 --squash"}}`)
	feed(t, c, "work-docs-writer", `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Upload resume.pdf to the signing site?","options":[{"label":"Yes"},{"label":"No"}],"multiSelect":false}]}}`)
	code := func(name string) string {
		st := session.ReadState(c + "/.claude/build/state/" + name + ".json")
		if st == nil || st.Request == nil {
			t.Fatalf("no request for %s", name)
		}
		return st.Request.Code
	}
	return c, code("work-dev-my-app-engineer"), code("work-docs-writer")
}

// guardAs runs cadrei hook guard on a Bash call in mode, as a member
// (member set) or as the orchestrator of crew c.
func guardAs(t *testing.T, c, member, mode, command string) (int, string, string) {
	t.Helper()
	if member != "" {
		t.Setenv("CADREI_MEMBER", member)
		defer os.Unsetenv("CADREI_MEMBER")
	} else {
		os.Unsetenv("CADREI_MEMBER")
	}
	t.Setenv("CADREI_HOME", c)
	in, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "permission_mode": mode, "tool_input": map[string]string{"command": command}})
	return callIn(string(in), "hook", "guard")
}

// 0.2.2, section 7: the guard's rules, as a member (M) and as the cadrei
// orchestrator (O). Blocked is exit 2 with the reason on stderr; ask is
// exit 0 with the ask alone on stdout; allowed is exit 0 with nothing.
// The guard never allows.
func TestTheGuardsRolesAndRules(t *testing.T) {
	c, eng, writer := answerCrew(t)
	fill := func(s string) string { return strings.NewReplacer("7f3", eng, "k2m", writer).Replace(s) }
	const member = "work-dev-my-app-engineer"
	blocked := func(who, mode, command, why string) {
		t.Helper()
		code, out, errOut := guardAs(t, c, who, mode, fill(command))
		if code != 2 || out != "" || (why != "" && strings.TrimSpace(errOut) != fill(why)) || errOut == "" {
			t.Errorf("%s %q: exit %d, %q %q", map[bool]string{true: "M", false: "O"}[who != ""], command, code, out, errOut)
		}
	}
	allowed := func(who, command string) {
		t.Helper()
		if code, out, errOut := guardAs(t, c, who, "default", fill(command)); code != 0 || out != "" || errOut != "" {
			t.Errorf("%s %q: exit %d, %q %q", map[bool]string{true: "M", false: "O"}[who != ""], command, code, out, errOut)
		}
	}
	asks := func(mode, command, why string) {
		t.Helper()
		code, out, errOut := guardAs(t, c, "", mode, fill(command))
		var got map[string]map[string]string
		json.Unmarshal([]byte(out), &got)
		want := map[string]map[string]string{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "ask", "permissionDecisionReason": fill(why)}}
		if code != 0 || errOut != "" || strings.Count(out, "\n") != 1 || len(got) != 1 || len(got["hookSpecificOutput"]) != 3 || got["hookSpecificOutput"]["permissionDecisionReason"] != want["hookSpecificOutput"]["permissionDecisionReason"] ||
			got["hookSpecificOutput"]["permissionDecision"] != "ask" || got["hookSpecificOutput"]["hookEventName"] != "PreToolUse" {
			t.Errorf("O %s %q: exit %d, %q %q", mode, command, code, out, errOut)
		}
	}

	// Blocked in both.
	for _, tc := range []struct{ command, memberWhy, orchWhy string }{
		{`/opt/homebrew/bin/cadrei answer 7f3 yes; ls`, memberAnswerRefused, strictRefused},
		{`env cadrei answer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`command cadrei answer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`nohup cadrei answer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`c"adrei" ans"wer" 7f3 yes`, memberAnswerRefused, strictRefused},
		{`cadrei an\swer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`sh -c 'cadrei answer 7f3 yes'`, memberAnswerRefused, strictRefused},
		{`bash -lc "cadrei answer 7f3 yes"`, memberAnswerRefused, strictRefused},
		{`echo 'cadrei answer 7f3 yes' | sh`, memberAnswerRefused, strictRefused},
		{`eval "cadrei answer 7f3 yes"`, memberAnswerRefused, strictRefused},
		{`x=answer; cadrei $x 7f3 yes`, memberAnswerRefused, strictRefused},
		{`cadrei answer $(cat code) yes`, memberAnswerRefused, strictRefused},
		{`python3 -c "import os; os.system('cadrei answer 7f3 yes')"`, memberAnswerRefused, strictRefused},
		{`cadrei answer 7f3 yes && cadrei answer k2m 1`, memberAnswerRefused, strictRefused},
		{`CADREI_HOME=/x cadrei answer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`./cadrei answer 7f3 yes`, memberAnswerRefused, strictRefused},
		{`cadrei answer 7f3 yes > out.txt`, memberAnswerRefused, strictRefused},
		{`tmux send-keys -t '=cadrei-main-dev:=engineer' 1`, typingRefused, typingRefused},
		{`tmux send -t x Enter`, typingRefused, typingRefused},
		{`tmux send-k -t x 1`, typingRefused, typingRefused},
		{`tmux -L other send-keys -t x 1`, typingRefused, typingRefused},
		{`tmux -S /tmp/tmux-501/default send-keys -t x 1`, typingRefused, typingRefused},
		{`tmux paste-buffer -t x`, typingRefused, typingRefused},
		{`tmux pasteb`, typingRefused, typingRefused},
		{`tmux load-buffer f`, typingRefused, typingRefused},
		{`tmux set-buffer x`, typingRefused, typingRefused},
		{`tmux capture-pane -p -t x \; send-keys -t x 1`, typingRefused, typingRefused},
		{`tmux source-file f`, typingRefused, typingRefused},
		{`tmux run-shell 'x'`, typingRefused, typingRefused},
		{`tmux if-shell true 'send-keys 1'`, typingRefused, typingRefused},
		{`tmux command-prompt`, typingRefused, typingRefused},
		{`tmux bind-key x send-keys 1`, typingRefused, typingRefused},
		{`tmux respawn-pane -k -t x`, typingRefused, typingRefused},
		{`tmux kill-pane -t x`, typingRefused, typingRefused},
		{`tmux -C attach`, typingRefused, typingRefused},
		{`tmux -C list-sessions`, typingRefused, typingRefused},
		{`tmux nosuchcmd -t x`, typingRefused, typingRefused},
		{`tmux $cmd -t x`, typingRefused, typingRefused},
		{`tmux "$(echo send-keys)" -t x 1`, typingRefused, typingRefused},
		{`tmux display-message 'hi'`, typingRefused, typingRefused},
		{`tmux`, typingRefused, typingRefused},
		{`tmux -c 'echo hi'`, typingRefused, typingRefused},
		{`sudo -u me tmux send-keys -t x 1`, typingRefused, typingRefused},
		{`echo 1 | xargs tmux send-keys -t x`, typingRefused, typingRefused},
		{`find . -name x -exec tmux send-keys -t x 1 \;`, typingRefused, typingRefused},
		{`sh -c 'tmux send-keys -t x 1'`, typingRefused, typingRefused},
		{`python3 -c "import os; os.system('tmux send-keys -t x 1')"`, typingRefused, typingRefused},
		{`t=tmux; $t send-keys -t x 1`, typingRefused, typingRefused},
		{`osascript -e 'tell application "System Events" to keystroke "1"'`, keystrokeRefused, keystrokeRefused},
		{`osascript -e 'tell application "System Events" to key code 36'`, keystrokeRefused, keystrokeRefused},
		// Review of 0.2.2: tmux after every wrapper, osascript with its
		// script on stdin or in JavaScript, code that runs tmux.
		{`exec tmux send-keys -t x 1`, typingRefused, typingRefused},
		{`time tmux send-keys -t x 1`, typingRefused, typingRefused},
		{`nohup tmux send-keys -t x 1 &`, typingRefused, typingRefused},
		{`env TMUX= tmux send-keys -t x 1`, typingRefused, typingRefused},
		{`watch 'tmux send-keys -t x 1'`, typingRefused, typingRefused},
		{`python3 -c "import subprocess; subprocess.run(['tmux', 'new-session', '-d'])"`, typingRefused, typingRefused},
		{"osascript <<'EOF'\ntell application \"System Events\" to keystroke \"1\"\nEOF", keystrokeRefused, keystrokeRefused},
		{`osascript -l JavaScript -e 'Application("System Events").keystroke("1")'`, keystrokeRefused, keystrokeRefused},
		{`watch 'cadrei answer 7f3 yes'`, memberAnswerRefused, strictRefused},
		// Review of 0.2.2 (M5): a format's #( starts a shell job in the
		// tmux server, so even the read-only commands refuse one. These
		// are text for the guard to read, never run.
		{`tmux display-message -p '#(true)'`, typingRefused, typingRefused},
		{`tmux list-panes -a -F '#{pane_id} #(true)'`, typingRefused, typingRefused},
		{`tmux list-sessions -F "#(echo x)"`, typingRefused, typingRefused},
		{`tmux display-message -I -t x`, typingRefused, typingRefused},
		{`tmux display-message -pI x`, typingRefused, typingRefused},
		{`tmux display-message -p -I -t x`, typingRefused, typingRefused},
	} {
		blocked(member, "default", tc.command, tc.memberWhy)
		blocked("", "default", tc.command, tc.orchWhy)
	}

	// By role.
	blocked(member, "default", `cadrei answer 7f3 yes`, memberAnswerRefused)
	asks("default", `cadrei answer 7f3 yes`, "Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash")
	asks("default", `cadrei answer 7f3 no`, "Cadrei: tell the engineer on my-app no? It wants to run: gh pr merge 42 --squash")
	blocked(member, "default", `/opt/homebrew/bin/cadrei answer k2m 1`, memberAnswerRefused)
	asks("default", filepath.Dir(filepath.Dir(c))+`/bin/cadrei answer k2m 1`, `Cadrei: answer the writer in docs with "1. Yes"? It asks: Upload resume.pdf to the signing site?`)
	// Review of 0.2.2 (L3): by a full path, it must be cadrei.
	blocked(member, "default", `/tmp/x/cadrei answer 7f3 yes`, memberAnswerRefused)
	blocked("", "default", `/tmp/x/cadrei answer 7f3 yes`, "Run cadrei answer <code> <choice> with cadrei by its name, so cadrei can ask you.")
	// A listing that runs into something else isn't the listing.
	blocked("", "default", `cadrei answer | cat`, strictRefused)
	blocked(member, "default", `cadrei answer zz9 yes`, memberAnswerRefused)
	blocked("", "default", `cadrei answer zz9 yes`, "Nothing's waiting under zz9 anymore. Run cadrei answer to see what's waiting now.")
	for _, mode := range []string{"bypassPermissions", "dontAsk", "plan", ""} {
		blocked(member, mode, `cadrei answer 7f3 yes`, memberAnswerRefused)
		blocked("", mode, `cadrei answer 7f3 yes`, "Claude Code can't ask the user in this permission mode. Ask them to type ! cadrei answer 7f3 yes themselves, or to answer in the member's window.")
	}
	for _, mode := range []string{"auto", "acceptEdits"} {
		blocked(member, mode, `cadrei answer 7f3 yes`, memberAnswerRefused)
		asks(mode, `cadrei answer 7f3 yes`, "Cadrei: allow once? The engineer on my-app wants to run: gh pr merge 42 --squash")
	}
	// A choice that can't answer the request is denied before any prompt.
	blocked("", "default", `cadrei answer 7f3 1`, "7f3 is a yes-or-no prompt. Use cadrei answer 7f3 yes or cadrei answer 7f3 no.")
	blocked("", "default", `cadrei answer k2m 3`, "k2m has options 1 to 2. Use cadrei answer k2m 1, for example.")
	blocked(member, "default", `cadrei answer`, memberAnswerRefused)
	allowed("", `cadrei answer`)
	for _, form := range []string{`cadrei compact dev/engineer my-app`, `env cadrei compact dev/engineer`, `sh -c 'cadrei compact dev/engineer'`, `/opt/homebrew/bin/cadrei compact dev/engineer`} {
		blocked(member, "default", form, "Team members can't run cadrei compact. Ask the user to do it from the orchestrator.")
	}
	allowed("", `cadrei compact dev/engineer my-app`)
	blocked(member, "default", `cadrei uninstall`, "Team members can't run cadrei uninstall or cadrei restore, in any form. Ask the user to do it from the orchestrator.")
	allowed("", `cadrei uninstall`)

	// Allowed in both.
	for _, command := range []string{
		`cadrei ls --json`, `cadrei allow list`,
		`git commit -m "Docs: cadrei answer and tmux send-keys"`,
		`grep -n "cadrei answer" docs/guide.md`, `gh pr create --body "use cadrei answer"`,
		"cat <<'EOF' > notes.md\ncadrei answer 7f3 yes\ntmux send-keys -t x 1\nEOF",
		`tmux capture-pane -p -t '=cadrei-main-dev:=engineer'`,
		`tmux list-windows -a`, `tmux has-session -t x`, `tmux show-options -g`, `tmux display-message -p '#S'`,
		`tmux capturep -p -t x \; lsw`, `tmux -L other list-sessions`, `tmux -V`,
		`brew install tmux`, `man tmux`, `which tmux`, `osascript -e 'display notification "done"'`,
		// Review of 0.2.2 (MH1): text that only mentions tmux or cadrei
		// answer, in arguments that run nothing.
		`rg -n "tmux new-session" cmd/`, `rg -n "cadrei answer" docs/`, `rg -n 'cadrei compact' docs/`,
		`python3 scripts/post.py --text "start tmux and attach later"`,
		`curl -s -d 'text=the tmux server will start soon' https://example.com/hook`,
		`perl -ne 'print if /tmux kill-server/' notes.md`,
		`python3 -c "print('see tmux if you need it')"`,
		`make docs MSG="tmux set the title"`,
		`awk '/tmux send-keys/' notes.md`, `sed -n '/cadrei answer/p' docs/guide.md`,
		`python3 -c "print('run cadrei answer 7f3 yes to answer')"`,
	} {
		allowed(member, command)
		allowed("", command)
	}
}

// Review of 0.2.2 (M3): a request cadrei can't check on the screen (a
// multi-select question, a tool it has no recorded screen for) is denied
// before any ask, as is any code when the guard doesn't know the crew.
func TestTheGuardDeniesWhatItCantAnswer(t *testing.T) {
	c, _, _ := answerCrew(t)
	feed(t, c, "work-docs-writer", `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?","options":[{"label":"a"},{"label":"b"}],"multiSelect":true}]}}`)
	feed(t, c, "work-dev-my-app-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"mcp__files__upload","tool_input":{"path":"/w/a.pdf"}}`)
	code := func(name string) string {
		return session.ReadState(c + "/.claude/build/state/" + name + ".json").Request.Code
	}
	for command, why := range map[string]string{
		"cadrei answer " + code("work-docs-writer") + " 1":           "This question needs more than one pick, so answer it in its window: cadrei attach docs",
		"cadrei answer " + code("work-dev-my-app-engineer") + " yes": "Cadrei can't check this kind of prompt on the screen, so answer it in its window: cadrei attach dev my-app",
	} {
		if n, out, errOut := guardAs(t, c, "", "default", command); n != 2 || out != "" || strings.TrimSpace(errOut) != why {
			t.Errorf("%s: %d %q %q", command, n, out, errOut)
		}
	}
	t.Setenv("CADREI_HOME", "")
	os.Unsetenv("CADREI_HOME")
	in := `{"hook_event_name":"PreToolUse","tool_name":"Bash","permission_mode":"default","tool_input":{"command":"cadrei answer ` + code("work-docs-writer") + ` 1"}}`
	if n, _, errOut := callIn(in, "hook", "guard"); n != 2 || !strings.Contains(errOut, "Nothing's waiting under") {
		t.Errorf("no crew: %d %q", n, errOut)
	}
}

// The ask's text comes from the state file only: a request with control
// or invisible characters shows them printable, and nothing on the
// command line gets into it.
func TestTheAskShowsOnlyTheRecord(t *testing.T) {
	c, eng, _ := answerCrew(t)
	feed(t, c, "work-dev-my-app-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf x\u001b[2J\u200b\nok"}}`)
	code := session.ReadState(c + "/.claude/build/state/work-dev-my-app-engineer.json").Request.Code
	if code == eng {
		t.Fatal("a new request kept its code")
	}
	_, out, _ := guardAs(t, c, "", "default", "cadrei answer "+code+" yes")
	var got struct {
		HookSpecificOutput struct{ PermissionDecisionReason string } `json:"hookSpecificOutput"`
	}
	json.Unmarshal([]byte(out), &got)
	if got.HookSpecificOutput.PermissionDecisionReason != "Cadrei: allow once? The engineer on my-app wants to run: rm -rf x?[2J?⏎ok" {
		t.Errorf("reason: %q", out)
	}
	// The old code names nothing now.
	if code, _, errOut := guardAs(t, c, "", "default", "cadrei answer "+eng+" yes"); code != 2 || !strings.Contains(errOut, "Nothing's waiting under "+eng) {
		t.Errorf("old code: %d %q", code, errOut)
	}
}
