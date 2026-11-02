//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// heldCrew is a crew with the dev team running, its engineer working, and
// cadrei answer run from the user's own terminal. It returns the crew's
// folder.
func heldCrew(t *testing.T) string {
	t.Helper()
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	ownTerminal(t)
	must(t, "up", "dev")
	writeStub(t, home+"/bin/cadrei", "#!/bin/sh\nexit 0\n")
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	return c
}

// failed is what cadrei prints on stderr for a failure that says what, on
// a stream with no terminal, on one line.
func failed(what string) string { return "cadrei: " + strings.ToLower(what[:1]) + what[1:] }

// heldOf is member's held records, oldest first.
func heldOf(t *testing.T, c, member string) []*session.Request {
	t.Helper()
	return session.ReadState(c + "/.claude/build/state/" + member + ".json").HeldOnes()
}

// lsRequest is the request ls --json shows for role, and its state.
func lsRequest(t *testing.T, role string) (state string, req *requestView) {
	t.Helper()
	var ls struct {
		Running []struct {
			Members []struct {
				Role    string
				State   string
				Request *requestView
			}
		}
	}
	if err := json.Unmarshal([]byte(must(t, "ls", "--json")), &ls); err != nil {
		t.Fatal(err)
	}
	for _, s := range ls.Running {
		for _, m := range s.Members {
			if m.Role == role {
				return m.State, m.Request
			}
		}
	}
	t.Fatalf("no %s in ls --json", role)
	return "", nil
}

// askOf is the reason in a guard's ask, or "" for none.
func askOf(out string) string {
	var got map[string]map[string]string
	json.Unmarshal([]byte(out), &got)
	if got["hookSpecificOutput"]["permissionDecision"] != "ask" {
		return ""
	}
	return got["hookSpecificOutput"]["permissionDecisionReason"]
}

// PR D (N6): a command the guard refuses in a member gets a record and a
// code, which the member's reason names. The member goes on working: it
// doesn't wait on it. The user sees it from the orchestrator (cadrei
// answer, ls --json, the orchestrator's note), and a yes by its code, which
// the orchestrator's guard asks the user about, lets that exact command by
// the guard once. Nothing else gets by, and it's once.
func TestARefusedCommandIsAllowedOnceByItsCode(t *testing.T) {
	c := heldCrew(t)
	const member, compact = "work-dev-engineer", "cadrei compact dev/reviewer"
	n, out, errOut := guardAs(t, c, member, "auto", compact)
	held := heldOf(t, c, member)
	if len(held) != 1 || held[0].Kind != session.ForGuard || held[0].Rule != ruleCompact || held[0].Summary != compact || held[0].Reason != compactRefused {
		t.Fatalf("record: %+v", held)
	}
	code := held[0].Code
	why := compactRefused + " Cadrei noted it for the user under code " + code + ": they can allow it once from the orchestrator. If they do, run the exact same command again, once."
	if n != 2 || out != "" || strings.TrimSpace(errOut) != why {
		t.Fatalf("refused: %d %q %q", n, out, errOut)
	}
	if state, req := lsRequest(t, "engineer"); state != session.Working || req == nil || req.Code != code || req.Kind != session.ForGuard || req.Summary != compact || req.Reason != compactRefused {
		t.Errorf("ls --json: %s %+v", state, req)
	}
	want := "Waiting on you:\n\n" +
		"  " + code + "  Cadrei's guard stopped the engineer in dev from running a command:\n" +
		"         " + compact + "\n" +
		"       Why:            " + compactRefused + "\n" +
		"       Allow it once:  cadrei answer " + code + " yes\n" +
		"       Say no:         cadrei answer " + code + " no\n"
	if n, out, errOut := call("answer"); n != 0 || out != want {
		t.Errorf("listing: %d\n%s\nwant:\n%s%s", n, out, want, errOut)
	}
	if ls := flat(must(t, "ls")); !strings.Contains(ls, "engineer working just now · cadrei's guard stopped a command: cadrei answer") {
		t.Errorf("ls: %q", ls)
	}
	// The orchestrator's guard asks the user, with what a yes does.
	n, out, errOut = guardAs(t, c, "", "default", "cadrei answer "+code+" yes")
	if ask := askOf(out); n != 0 || errOut != "" || ask != "Cadrei: allow once? The engineer in dev may run this command once, in the next 10 minutes, past cadrei's guard (its own permission mode and deny rules still apply): "+
		compact+" The guard stopped it: "+compactRefused {
		t.Errorf("the guard's ask: %d %q %q", n, out, errOut)
	}
	if n, out, errOut := call("answer", code, "1"); n != 1 || !strings.Contains(errOut, code+" takes yes or no.") {
		t.Errorf("a number: %d %q %q", n, out, errOut)
	}
	if n, out, errOut := call("answer", code, "yes"); n != 0 || flat(out) != "+ Allowed once: the engineer in dev may run that command once in the next 10 minutes. Tell it to run the exact same command again." {
		t.Errorf("yes: %d %q %q", n, out, errOut)
	}
	st := session.ReadState(c + "/.claude/build/state/" + member + ".json")
	if st.State != session.Working || len(st.HeldOnes()) != 0 || len(st.Allowed) != 1 || st.Allowed[0].Key != session.CommandKey(compact) {
		t.Fatalf("after the yes: %+v", st)
	}
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("listed after the yes: %q", out)
	}
	// Another command isn't let by, not even one that reads the same to a
	// shell.
	for _, other := range []string{"cadrei compact dev/pm", compact + " ", "cadrei  compact dev/reviewer"} {
		if n, _, _ := guardAs(t, c, member, "auto", other); n != 2 {
			t.Errorf("%q got by", other)
		}
	}
	if n, out, errOut := guardAs(t, c, member, "auto", compact); n != 0 || out != "" || errOut != "" {
		t.Errorf("the allowed run: %d %q %q", n, out, errOut)
	}
	if n, _, errOut := guardAs(t, c, member, "auto", compact); n != 2 || strings.Contains(errOut, code) {
		t.Errorf("a second run: %d %q", n, errOut)
	}
	// The prompt log: opened, and ended by its code.
	log, _ := os.ReadFile(session.PromptLogPath(c + "/.claude/build"))
	if !strings.Contains(string(log), `"code":"`+code+`","kind":"guard","tool":"Bash","source":"hook","event":"opened"`) ||
		!strings.Contains(string(log), `"code":"`+code+`","kind":"guard","tool":"Bash","source":"hook","event":"ended","ended":"by_code"`) {
		t.Errorf("prompt log:\n%s", log)
	}
}

// A refusal's yes runs out: AllowedFor after it, the guard refuses again.
// A record itself goes HeldFor after it opened.
func TestAnAllowanceRunsOut(t *testing.T) {
	c := heldCrew(t)
	const member, write = "work-dev-engineer", "echo note >> ~/.cadrei/work/.memory/notes.md"
	guardAs(t, c, member, "auto", write)
	held := heldOf(t, c, member)
	if len(held) != 1 || held[0].Rule != ruleMemory {
		t.Fatalf("record: %+v", held)
	}
	must(t, "answer", held[0].Code, "yes")
	path := c + "/.claude/build/state/" + member + ".json"
	st := session.ReadState(path)
	st.Allowed[0].Until = time.Now().Add(-time.Second)
	session.WriteState(path, st)
	if n, _, _ := guardAs(t, c, member, "auto", write); n != 2 {
		t.Error("an allowance past its time let it by")
	}
	st = session.ReadState(path)
	st.Open[0].At = time.Now().Add(-session.HeldFor)
	session.WriteState(path, st)
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("listed past HeldFor: %q", out)
	}
}

// The refusals a user can never allow (typing into tmux windows,
// keystrokes, uninstall and restore, a member's cadrei answer) get a
// record too, so the user sees them from the orchestrator. A yes says why
// cadrei can't, and gives the ! route where it makes sense; no closes it.
// Nothing ever lets them by.
func TestARefusalTheUserCantAllow(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	for _, tc := range []struct{ command, rule, why, yes, listed string }{
		{"tmux send-keys -t x 1", ruleTyping, typingRefused,
			"Cadrei never allows that for a member: it could press keys in another session's window, an approval too. Close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member: it could press keys in another session's window.\n"},
		{"osascript -e 'tell application \"System Events\" to keystroke \"1\"'", ruleKeys, keystrokeRefused,
			"Cadrei never allows that for a member: it could press keys in another session's window, an approval too. Close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member: it could press keys in another session's window.\n"},
		{"cadrei restore", ruleRemoval, removalRefused,
			"Cadrei never allows that for a member. If you want it run, run it yourself in the orchestrator: ! cd /src/app && cadrei restore. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member. To run it yourself:\n         ! cd /src/app && cadrei restore\n"},
		{"cadrei answer abc yes", ruleAnswer, memberAnswerRefused,
			"Cadrei never allows that for a member. If you want it run, run it yourself in the orchestrator: ! cd /src/app && cadrei answer abc yes. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member. To run it yourself:\n         ! cd /src/app && cadrei answer abc yes\n"},
	} {
		in, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "permission_mode": "auto", "cwd": "/src/app", "tool_input": map[string]string{"command": tc.command}})
		t.Setenv("CADREI_MEMBER", member)
		t.Setenv("CADREI_HOME", c)
		n, _, errOut := callIn(string(in), "hook", "guard")
		os.Unsetenv("CADREI_MEMBER")
		held := heldOf(t, c, member)
		if len(held) != 1 || held[0].Rule != tc.rule || held[0].Dir != "/src/app" {
			t.Fatalf("%s: record %+v", tc.command, held)
		}
		code := held[0].Code
		if n != 2 || strings.TrimSpace(errOut) != tc.why+" Cadrei noted it for the user under code "+code+", but it can't be allowed from the orchestrator." {
			t.Errorf("%s: refused %d %q", tc.command, n, errOut)
		}
		yes := strings.ReplaceAll(tc.yes, "CODE", code)
		if n, _, errOut := call("answer", code, "yes"); n != 1 || flat(errOut) != failed(yes) {
			t.Errorf("%s: yes %d %q\nwant %q", tc.command, n, flat(errOut), yes)
		}
		// The orchestrator's guard refuses the yes, saying the same.
		if n, out, errOut := guardAs(t, c, "", "default", "cadrei answer "+code+" yes"); n != 2 || out != "" || strings.TrimSpace(errOut) != yes {
			t.Errorf("%s: the guard's yes %d %q %q", tc.command, n, out, errOut)
		}
		if _, out, _ := call("answer"); !strings.Contains(out, tc.listed+"       Close it:       cadrei answer "+code+" no\n") {
			t.Errorf("%s: listing\n%s", tc.command, out)
		}
		if n, out, errOut := call("answer", code, "no"); n != 0 || flat(out) != "+ Closed "+code+". Tell the engineer in dev you said no, so it doesn't wait on it." {
			t.Errorf("%s: no %d %q %q", tc.command, n, out, errOut)
		}
		if len(heldOf(t, c, member)) != 0 {
			t.Errorf("%s: still held after no", tc.command)
		}
	}
}

// PR D (N6): auto mode's denial gets a record and a code, from its hook,
// with the classifier's reason word for word. The member doesn't wait on
// it. No closes it; a yes says cadrei can't override auto mode, with the
// one way that works: the user runs it with ! in the orchestrator, in the
// member's folder. Never its window.
func TestAnAutoModeDenialGetsACode(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	feed(t, c, member, `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_p","tool_input":{"command":"git push --force origin main"}}`)
	feed(t, c, member, `{"hook_event_name":"PermissionDenied","tool_name":"Bash","tool_use_id":"toolu_p","cwd":"/src/my app","tool_input":{"command":"git push --force origin main"},"reason":"Force-pushing main rewrites shared history."}`)
	feed(t, c, member, `{"hook_event_name":"Stop"}`)
	held := heldOf(t, c, member)
	if len(held) != 1 || held[0].Kind != session.ForDenied || held[0].Reason != "Force-pushing main rewrites shared history." {
		t.Fatalf("record: %+v", held)
	}
	code := held[0].Code
	if state, req := lsRequest(t, "engineer"); state != session.Working || req == nil || req.Code != code || req.Kind != session.ForDenied || req.Summary != "git push --force origin main" {
		t.Errorf("ls --json: %s %+v", state, req)
	}
	want := "Waiting on you:\n\n" +
		"  " + code + "  Auto mode denied the engineer in dev a command:\n" +
		"         git push --force origin main\n" +
		"       Why:            Force-pushing main rewrites shared history.\n" +
		"       Cadrei can't override auto mode from here. To run it yourself:\n" +
		"         ! cd '/src/my app' && git push --force origin main\n" +
		"       Close it:       cadrei answer " + code + " no\n"
	if _, out, _ := call("answer"); out != want {
		t.Errorf("listing:\n%s\nwant:\n%s", out, want)
	}
	yes := "Auto mode denied that, and cadrei can't override the engineer's auto mode from here. To run it yourself, type this in the orchestrator: ! cd '/src/my app' && git push --force origin main. Then close it with cadrei answer " + code + " no."
	if n, _, errOut := call("answer", code, "yes"); n != 1 || flat(errOut) != failed(yes) {
		t.Errorf("yes: %d %q", n, flat(errOut))
	}
	if n, _, errOut := guardAs(t, c, "", "default", "cadrei answer "+code+" yes"); n != 2 || strings.TrimSpace(errOut) != yes {
		t.Errorf("the guard's yes: %d %q", n, errOut)
	}
	if n, out, _ := guardAs(t, c, "", "default", "cadrei answer "+code+" no"); n != 0 ||
		askOf(out) != "Cadrei: close it? The engineer in dev won't get to run git push --force origin main. Auto mode said: Force-pushing main rewrites shared history." {
		t.Errorf("the guard's no: %d %q", n, out)
	}
	// Another prompt and turn later, it's still there.
	feed(t, c, member, `{"hook_event_name":"UserPromptSubmit"}`)
	feed(t, c, member, `{"hook_event_name":"Stop"}`)
	if n, out, errOut := call("answer", code, "no"); n != 0 || !strings.HasPrefix(out, "+ Closed "+code+".") {
		t.Errorf("no: %d %q %q", n, out, errOut)
	}
	// A call to a tool that isn't a shell command can't be run with !.
	feed(t, c, member, `{"hook_event_name":"PermissionDenied","tool_name":"WebFetch","tool_use_id":"toolu_w","tool_input":{"url":"https://example.com/x"},"reason":"Not this site."}`)
	held = heldOf(t, c, member)
	if len(held) != 1 {
		t.Fatalf("WebFetch: %+v", held)
	}
	if n, _, errOut := call("answer", held[0].Code, "yes"); n != 1 || flat(errOut) != "cadrei: auto mode denied that WebFetch call, and cadrei can't override the engineer's auto mode from here. Only a shell command can be run with !, so tell the engineer what to do instead. Then close it with cadrei answer "+held[0].Code+" no." {
		t.Errorf("WebFetch yes: %d %q", n, flat(errOut))
	}
}

// A no to a prompt the member waits on ends its turn and every prompt in
// it, but a held record stays: its call was over already.
func TestANoLeavesAHeldRecord(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	code := request(t, c, "engineer", mkdirRequest)
	guardAs(t, c, "work-dev-engineer", "auto", "cadrei compact dev/reviewer")
	held := heldOf(t, c, "work-dev-engineer")
	if len(held) != 1 {
		t.Fatalf("held: %+v", held)
	}
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "no"); n != 0 {
		t.Fatalf("no: %d %q %q", n, out, errOut)
	}
	st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json")
	if st.State != session.Idle || st.Current() != nil || len(st.HeldOnes()) != 1 || st.HeldOnes()[0].Code != held[0].Code {
		t.Errorf("after the no: %+v", st)
	}
}

// The orchestrator hears of a held record on its next prompt, with the
// code and what the user can do, while the member itself works or idles.
func TestTheOrchestratorHearsOfAHeldRecord(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	guardAs(t, c, member, "auto", "cadrei compact dev/reviewer")
	code := heldOf(t, c, member)[0].Code
	r, err := cadreis.Resolve(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	block := teamBlock(r, time.Now(), false)
	note := "- Cadrei's guard stopped the engineer in dev from running a command (" + code + "): cadrei compact dev/reviewer. The guard stopped it: " + compactRefused +
		" The user can allow it once with cadrei answer " + code + " yes, or close it with no. After a yes, tell the engineer to run the exact same command again.\n"
	if !strings.Contains(block, "Waiting on the user (cadrei's record of each request") || !strings.Contains(block, note) || !strings.Contains(block, "**! engineer: a stopped call waits on you**") {
		t.Errorf("team block:\n%s\nwant the note:\n%s", block, note)
	}
	rt, _ := runtime.Get(runtimeName())
	if snap := snapshotOf(rt, r, opening{}, time.Now()); !strings.Contains(snap, strings.TrimSuffix(strings.TrimPrefix(note, "- "), "\n")) {
		t.Errorf("snapshot:\n%s", snap)
	}
}
