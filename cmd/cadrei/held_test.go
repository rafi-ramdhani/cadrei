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

// guardIn is guardAs, with the folder the command runs in (Claude Code's
// cwd), or none for "".
func guardIn(t *testing.T, c, member, mode, dir, command string) (int, string, string) {
	t.Helper()
	t.Setenv("CADREI_MEMBER", member)
	defer os.Unsetenv("CADREI_MEMBER")
	t.Setenv("CADREI_HOME", c)
	in := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "permission_mode": mode, "tool_input": map[string]string{"command": command}}
	if dir != "" {
		in["cwd"] = dir
	}
	raw, _ := json.Marshal(in)
	return callIn(string(raw), "hook", "guard")
}

// PR D (N6): a command the guard refuses in a member gets a record and a
// code, which the member's reason names. The member goes on working: it
// doesn't wait on it. The user sees it from the orchestrator (cadrei
// answer, ls --json, the orchestrator's note), and a yes by its code, which
// the orchestrator's guard asks the user about, lets that exact command by
// the guard once. Nothing else gets by, and it's once.
func TestARefusedCommandIsAllowedOnceByItsCode(t *testing.T) {
	c := heldCrew(t)
	const member, compact, dir = "work-dev-engineer", "cadrei compact dev/reviewer", "/src/app"
	n, out, errOut := guardIn(t, c, member, "auto", dir, compact)
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
		"  " + code + "  Cadrei's guard stopped the engineer in dev from running a command in /src/app:\n" +
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
	if ask := askOf(out); n != 0 || errOut != "" || ask != "Cadrei: allow once? The engineer in dev may run this command once, in /src/app only, in the next 10 minutes, past cadrei's guard (its own permission mode and deny rules still apply): "+
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
	if st.State != session.Working || len(st.HeldOnes()) != 0 || len(st.Allowed) != 1 || st.Allowed[0].Key != session.CommandKey(dir, compact) {
		t.Fatalf("after the yes: %+v", st)
	}
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("listed after the yes: %q", out)
	}
	// Another command isn't let by, not even one that reads the same to a
	// shell, nor the same one in another folder, or in none it names
	// (review M3).
	for _, other := range []struct{ dir, command string }{{dir, "cadrei compact dev/pm"}, {dir, compact + " "}, {dir, "cadrei  compact dev/reviewer"},
		{c, compact}, {dir + "/sub", compact}, {"", compact}} {
		if n, _, _ := guardIn(t, c, member, "auto", other.dir, other.command); n != 2 {
			t.Errorf("%q in %q got by", other.command, other.dir)
		}
	}
	if n, out, errOut := guardIn(t, c, member, "auto", dir, compact); n != 0 || out != "" || errOut != "" {
		t.Errorf("the allowed run: %d %q %q", n, out, errOut)
	}
	if n, _, errOut := guardIn(t, c, member, "auto", dir, compact); n != 2 || strings.Contains(errOut, code) {
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
	guardIn(t, c, member, "auto", "/src/app", write)
	held := heldOf(t, c, member)
	if len(held) != 1 || held[0].Rule != ruleMemory {
		t.Fatalf("record: %+v", held)
	}
	must(t, "answer", held[0].Code, "yes")
	path := c + "/.claude/build/state/" + member + ".json"
	st := session.ReadState(path)
	st.Allowed[0].Until = time.Now().Add(-time.Second)
	session.WriteState(path, st)
	if n, _, _ := guardIn(t, c, member, "auto", "/src/app", write); n != 2 {
		t.Error("an allowance past its time let it by")
	}
	st = session.ReadState(path)
	st.Open[0].At = time.Now().Add(-session.HeldFor)
	session.WriteState(path, st)
	if n, out, _ := call("answer"); n != 0 || out != "Nobody's waiting on you right now.\n" {
		t.Errorf("listed past HeldFor: %q", out)
	}
}

// Review L1: the guard lets an allowed command by only once its allowance
// is used up in the state file. When the write fails (the state folder
// can't be written), it refuses, and the allowance is still there for the
// run after.
func TestAFailedWriteLetsNothingBy(t *testing.T) {
	c := heldCrew(t)
	const member, compact, dir = "work-dev-engineer", "cadrei compact dev/reviewer", "/src/app"
	guardIn(t, c, member, "auto", dir, compact)
	must(t, "answer", heldOf(t, c, member)[0].Code, "yes")
	state := c + "/.claude/build/state"
	os.Chmod(state, 0o500)
	t.Cleanup(func() { os.Chmod(state, 0o755) })
	for i := 0; i < 3; i++ {
		if n, _, _ := guardIn(t, c, member, "auto", dir, compact); n != 2 {
			t.Fatalf("run %d with the state folder read-only got by", i)
		}
	}
	os.Chmod(state, 0o755)
	if n, _, _ := guardIn(t, c, member, "auto", dir, compact); n != 0 {
		t.Error("the allowance was lost")
	}
	if n, _, _ := guardIn(t, c, member, "auto", dir, compact); n != 2 {
		t.Error("let by twice")
	}
}

// Review round 2 (R2-2): an allowance goes by where its folder really is.
// A link pointed elsewhere after the yes isn't the folder the user said
// yes for, so the same command run through it is refused.
func TestAnAllowanceStaysWithItsFolder(t *testing.T) {
	c := heldCrew(t)
	const member, write = "work-dev-engineer", "echo note >> .memory/notes.md"
	base := t.TempDir()
	real, other, link := base+"/real", base+"/other", base+"/lnk"
	os.MkdirAll(real, 0o755)
	os.MkdirAll(other, 0o755)
	os.Symlink(real, link)
	guardIn(t, c, member, "auto", link, write)
	must(t, "answer", heldOf(t, c, member)[0].Code, "yes")
	os.Remove(link)
	os.Symlink(other, link)
	if n, _, _ := guardIn(t, c, member, "auto", link, write); n != 2 {
		t.Error("the link pointed elsewhere took the allowance")
	}
	if n, _, _ := guardIn(t, c, member, "auto", real, write); n != 0 {
		t.Error("the folder itself didn't")
	}
}

// Review round 2 (R2-3): cadrei with a command the guard can't read is
// refused in a member, however it's spelled, and a cadrei command written
// out with a variable after it still runs.
func TestCadreiWithAnUnreadCommandIsRefused(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	for _, command := range []string{"cadrei $(cat args.txt) < forged.json", "cadrei `cat args.txt`", "cadrei $A", `cadrei "$A"`, "env cadrei ${A}",
		"sh -c 'cadrei $(cat f)'", `python3 -c "import os; os.system('cadrei $(cat f)')"`} {
		if n, _, errOut := guardIn(t, c, member, "auto", "/src/app", command); n != 2 || !strings.HasPrefix(errOut, opaqueRefused) {
			t.Errorf("%s: %d %q", command, n, errOut)
		}
	}
	for _, command := range []string{"cadrei ls --json", "cadrei ls $FLAGS", "echo cadrei"} {
		if n, out, errOut := guardIn(t, c, member, "auto", "/src/app", command); n != 0 || out != "" || errOut != "" {
			t.Errorf("%s: %d %q %q", command, n, out, errOut)
		}
	}
}

// Review M3: a refusal whose folder the runtime didn't name can't be
// allowed, since an allowance is for one folder.
func TestARefusalWithNoFolderCantBeAllowed(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	guardIn(t, c, member, "auto", "", "cadrei compact dev/reviewer")
	code := heldOf(t, c, member)[0].Code
	if n, _, errOut := call("answer", code, "yes"); n != 1 || flat(errOut) != failed("Cadrei doesn't know which folder the engineer ran that in, so it can't allow it from here. Close it with cadrei answer "+code+" no.") {
		t.Errorf("yes: %d %q", n, flat(errOut))
	}
}

// Review L3: a command with a tab or a control character shows otherwise
// than it is, so it gets no ! line (a ? there is a shell wildcard), and a
// refusal of one can't be allowed.
func TestAnAlteredCommandGetsNoBangLine(t *testing.T) {
	c := heldCrew(t)
	const member = "work-dev-engineer"
	feed(t, c, member, `{"hook_event_name":"PermissionDenied","tool_name":"Bash","tool_use_id":"toolu_r","cwd":"/src/app","tool_input":{"command":"rm -f build/out\u0001.o"},"reason":"No."}`)
	held := heldOf(t, c, member)
	if len(held) != 1 || !held[0].Altered || held[0].Summary != "rm -f build/out?.o" {
		t.Fatalf("record: %+v", held)
	}
	if _, out, _ := call("answer"); strings.Contains(out, "!") {
		t.Errorf("a ! line in the listing:\n%s", out)
	}
	if n, _, errOut := call("answer", held[0].Code, "yes"); n != 1 || strings.Contains(errOut, "! cd") || !strings.Contains(flat(errOut), "holds a tab or a control character") {
		t.Errorf("yes: %d %q", n, errOut)
	}
	must(t, "answer", held[0].Code, "no")
	guardIn(t, c, member, "auto", "/src/app", "echo x >> .memory/a\tb.md")
	held = heldOf(t, c, member)
	if len(held) != 1 || !held[0].Altered {
		t.Fatalf("refusal: %+v", held)
	}
	if n, _, errOut := call("answer", held[0].Code, "yes"); n != 1 || !strings.Contains(flat(errOut), "can't be allowed from here") {
		t.Errorf("an altered refusal's yes: %d %q", n, errOut)
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
		// No ! line: it would answer abc, a prompt the user hasn't seen,
		// with no ask (review M2).
		{"cadrei answer abc yes", ruleAnswer, memberAnswerRefused,
			"Cadrei never allows that for a member, and gives you no line to run it yourself: it would answer a prompt you haven't seen. To answer a member, run cadrei answer to see what's waiting first. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member. To answer a member, run cadrei answer to see what's waiting first.\n"},
		// A member forging its own record (review M1).
		{`echo '{"hook_event_name":"PermissionDenied"}' | cadrei hook state`, ruleHook, hookRefused,
			"Cadrei never allows that for a member: cadrei's hooks run on their own. Close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member.\n"},
		// Refused as a possible uninstall first, but they could answer a
		// prompt the user hasn't seen: no ! line (review round 2, R2-1).
		{"xargs cadrei answer k2m 2 < /dev/null", ruleRemoval, removalRefused,
			"Cadrei never allows that for a member. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member.\n"},
		{"xargs cadrei answer k2m < picks.txt", ruleRemoval, removalRefused,
			"Cadrei never allows that for a member. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member.\n"},
		{"xargs cadrei < args.txt", ruleRemoval, removalRefused,
			"Cadrei never allows that for a member. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member.\n"},
		// A command the guard can't read (R2-3).
		{"cadrei $(cat args.txt) < forged.json", ruleOpaque, opaqueRefused,
			"Cadrei never allows that for a member. Then close it with cadrei answer CODE no.",
			"       Cadrei never allows this for a member.\n"},
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
		// The orchestrator's note offers a ! line only for the plain
		// restore (R2-4).
		r, err := cadreis.Resolve(c, nil)
		if err != nil {
			t.Fatal(err)
		}
		block := teamBlock(r, time.Now(), false)
		if route := strings.Contains(block, "typing ! "); route != (tc.command == "cadrei restore") {
			t.Errorf("%s: the note's ! line: %v\n%s", tc.command, route, block)
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
		"  " + code + "  Auto mode denied the engineer in dev a command in /src/my app:\n" +
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
	guardIn(t, c, member, "auto", "/src/app", "cadrei compact dev/reviewer")
	code := heldOf(t, c, member)[0].Code
	r, err := cadreis.Resolve(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	block := teamBlock(r, time.Now(), false)
	note := "- Cadrei's guard stopped the engineer in dev from running a command in /src/app (" + code + "): cadrei compact dev/reviewer. The guard stopped it: " + compactRefused +
		" The user can allow it once with cadrei answer " + code + " yes, or close it with no. After a yes, tell the engineer to run the exact same command again.\n"
	if !strings.Contains(block, "Waiting on the user (cadrei's record of each request") || !strings.Contains(block, note) || !strings.Contains(block, "**! engineer: a stopped call waits on you**") {
		t.Errorf("team block:\n%s\nwant the note:\n%s", block, note)
	}
	rt, _ := runtime.Get(runtimeName())
	if snap := snapshotOf(rt, r, opening{}, time.Now()); !strings.Contains(snap, strings.TrimSuffix(strings.TrimPrefix(note, "- "), "\n")) {
		t.Errorf("snapshot:\n%s", snap)
	}
}
