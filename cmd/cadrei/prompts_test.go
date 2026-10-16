//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// 0.2.3 (PR A): how cadrei records what a member asks the user, so every
// prompt gets a code. The hook inputs are Claude Code 2.1.301's, as the
// hook spike captured them.

// traceOf is the engineer's trace lines in crew c.
func traceOf(t *testing.T, c string) []session.TraceLine {
	t.Helper()
	raw, _ := os.ReadFile(session.TracePath(c+"/.claude/build", "work-dev-engineer"))
	var lines []session.TraceLine
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var line session.TraceLine
		if l != "" && json.Unmarshal([]byte(l), &line) == nil {
			lines = append(lines, line)
		}
	}
	return lines
}

// Each hook run notes a start and an end line in the member's trace, with
// the hook's name, the tool, the call's id, the state before and after
// and the codes: never what the tool got.
func TestTheStateHookTracesEachRun(t *testing.T) {
	c := teamOf(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit","prompt":"deploy with TEST_TOKEN_PLACEHOLDER"}`)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"echo TEST_TOKEN_PLACEHOLDER"}}`)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`)
	lines := traceOf(t, c)
	if len(lines) != 6 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	for i, want := range []struct{ phase, hook, before, after string }{
		{session.TraceStart, "UserPromptSubmit", "", ""}, {session.TraceEnd, "UserPromptSubmit", "starting/0", "working/0"},
		{session.TraceStart, "PermissionRequest", "", ""}, {session.TraceEnd, "PermissionRequest", "working/0", "waiting/1"},
		{session.TraceStart, "Notification", "", ""}, {session.TraceEnd, "Notification", "waiting/1", "waiting/1"},
	} {
		l := lines[i]
		if l.Phase != want.phase || l.Hook != want.hook || l.Before != want.before || l.After != want.after || l.Run != lines[i/2*2].Run {
			t.Errorf("line %d: %+v", i, l)
		}
	}
	st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json")
	if lines[3].Tool != "Bash" || len(lines[3].Codes) != 1 || lines[3].Codes[0] != st.Current().Code || lines[5].Notice != "permission_prompt" {
		t.Errorf("the request's run: %+v %+v", lines[3], lines[5])
	}
	raw, _ := os.ReadFile(session.TracePath(c+"/.claude/build", "work-dev-engineer"))
	if strings.Contains(string(raw), "TEST_TOKEN_PLACEHOLDER") || strings.Contains(string(raw), "echo") || strings.Contains(string(raw), "needs your") {
		t.Errorf("the trace holds what the hook got:\n%s", raw)
	}
	if n := session.CountTrace(session.TracePath(c+"/.claude/build", "work-dev-engineer"), time.Now()); n != (session.TraceCounts{Runs: 3}) {
		t.Errorf("counts %+v", n)
	}
}

// A hook waits for the member's lock, which another hook holds while it
// changes the state file. One that can't get it in time changes nothing,
// and says so in the trace, which counts it as failed.
func TestTheStateHookTakesTheMembersLock(t *testing.T) {
	c := teamOf(t)
	build := c + "/.claude/build"
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	lock, err := fsx.Acquire(session.LockPath(build, "work-dev-engineer"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stateLockWait = 200 * time.Millisecond
	t.Cleanup(func() { stateLockWait = 2 * time.Second })
	feed(t, c, "work-dev-engineer", mkdirRequest)
	if st := session.ReadState(build + "/state/work-dev-engineer.json"); st.State != session.Working {
		t.Errorf("changed without the lock: %+v", st)
	}
	lines := traceOf(t, c)
	if last := lines[len(lines)-1]; last.Phase != session.TraceEnd || last.Hook != "PermissionRequest" || last.Err == "" {
		t.Errorf("trace: %+v", last)
	}
	if n := session.CountTrace(session.TracePath(build, "work-dev-engineer"), time.Now()); n.Failed != 1 {
		t.Errorf("counts %+v", n)
	}
	// Released, the next hook gets it.
	lock.Release()
	feed(t, c, "work-dev-engineer", mkdirRequest)
	if st := session.ReadState(build + "/state/work-dev-engineer.json"); st.State != session.Waiting {
		t.Errorf("after the lock: %+v", st)
	}
}

// The hook inputs of item 10's turn, with tool_use_id as 2.1.301 gives it:
// SendMessage and a question side by side.
const (
	sendStart   = `{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_use_id":"toolu_s","tool_input":{"to":"uds:/tmp/x.sock","message":"Waiting on the user's yes"}}`
	sendEnd     = `{"hook_event_name":"PostToolUse","tool_name":"SendMessage","tool_use_id":"toolu_s","tool_input":{"to":"uds:/tmp/x.sock","message":"Waiting on the user's yes"},"tool_response":{"success":true}}`
	shipInput   = `{"questions":[{"question":"Ship it?","header":"Ship","options":[{"label":"Yes","description":"Ship now."},{"label":"No","description":"Wait."}],"multiSelect":false}]}`
	shipStart   = `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_use_id":"toolu_q","tool_input":` + shipInput + `}`
	shipAsk     = `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":` + shipInput + `}`
	shipEnd     = `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"toolu_q","tool_input":` + shipInput + `}`
	mkdirStart  = `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_m","tool_input":{"command":"mkdir e2e-yes"}}`
	mkdirFailed = `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_use_id":"toolu_m","tool_input":{"command":"mkdir e2e-yes"},"error":"denied"}`
)

// stateOf is the engineer's state in crew c.
func stateOf(c string) *session.State {
	return session.ReadState(c + "/.claude/build/state/work-dev-engineer.json")
}

// Item 10: a question asked side by side with a SendMessage shows as
// waiting, with its code, from its call's start, in either order, and the
// code doesn't change when its permission request comes.
func TestAQuestionBesideASendMessageWaits(t *testing.T) {
	c := teamOf(t)
	for name, events := range map[string][]string{
		"one after the other": {sendStart, sendEnd, shipStart},
		"side by side":        {sendStart, shipStart, sendEnd},
	} {
		feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
		for _, ev := range events {
			feed(t, c, "work-dev-engineer", ev)
		}
		st := stateOf(c)
		if st.State != session.Waiting || st.Current() == nil || st.Current().Question != "Ship it?" {
			t.Fatalf("%s: %+v", name, st)
		}
		code := st.Current().Code
		if r := row(t, "engineer"); !strings.Contains(r, "waiting on you · it asked you something: cadrei answer") {
			t.Errorf("%s: row %q", name, r)
		}
		feed(t, c, "work-dev-engineer", shipAsk)
		if st := stateOf(c); len(st.Requests()) != 1 || st.Current().Code != code {
			t.Errorf("%s: after its permission request: %+v", name, st.Requests())
		}
		feed(t, c, "work-dev-engineer", shipEnd)
		if st := stateOf(c); st.State != session.Working || st.Current() != nil {
			t.Errorf("%s: answered in its window: %+v", name, st)
		}
	}
}

// Two prompts at once each get a code, and the listing shows both.
func TestTwoPromptsAtOnceBothShow(t *testing.T) {
	c := teamOf(t)
	for _, ev := range []string{`{"hook_event_name":"UserPromptSubmit"}`, mkdirStart, mkdirRequest, shipStart} {
		feed(t, c, "work-dev-engineer", ev)
	}
	st := stateOf(c)
	if len(st.Requests()) != 2 || st.Requests()[0].Code == st.Requests()[1].Code {
		t.Fatalf("requests: %+v", st.Requests())
	}
	ownTerminal(t)
	_, out, _ := call("answer")
	for _, r := range st.Requests() {
		if !strings.Contains(out, "  "+r.Code+"  ") {
			t.Errorf("the listing lacks %s:\n%s", r.Code, out)
		}
	}
	// The Bash prompt failing (a no in its window) ends it alone.
	feed(t, c, "work-dev-engineer", mkdirFailed)
	if st := stateOf(c); st.State != session.Waiting || len(st.Requests()) != 1 || st.Current().Kind != session.ForQuestion {
		t.Errorf("after the Bash call failed: %+v", st)
	}
}

// Hooks firing at once, as parallel tool calls make them, each get their
// change in: the member's lock keeps one from undoing another's.
func TestHooksAtOnceAllCount(t *testing.T) {
	c := teamOf(t)
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CADREI_HOME", c)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("toolu_%02d", i)
			in := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"` + id + `","tool_input":{"command":"make t` + id + `"}}`
			writeState(rt, c, "work-dev-engineer", []byte(in), time.Now())
			writeState(rt, c, "work-dev-engineer", []byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"make t`+id+`"}}`), time.Now())
		}(i)
	}
	wg.Wait()
	st := stateOf(c)
	if len(st.Calls) != 20 || len(st.Requests()) != session.MaxRequests {
		t.Errorf("%d calls, %d requests", len(st.Calls), len(st.Requests()))
	}
	for _, r := range st.Requests() {
		if r.Call == "" || !strings.HasSuffix(r.Summary, strings.TrimPrefix(r.Call, "toolu_")) {
			t.Errorf("request %+v isn't its call's", r)
		}
	}
}

// Answering one of two open prompts ends that one alone: the member still
// waits on the other, under its code.
func TestAnsweringOneOfTwoLeavesTheOther(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	for _, ev := range []string{mkdirStart, mkdirRequest, shipStart} {
		feed(t, c, "work-dev-engineer", ev)
	}
	st := stateOf(c)
	if len(st.Requests()) != 2 {
		t.Fatalf("requests: %+v", st.Requests())
	}
	bash, question := st.Requests()[0], st.Requests()[1]
	ownTerminal(t)
	if n, out, errOut := call("answer", bash.Code, "yes"); n != 0 {
		t.Fatalf("exit %d: %s%s", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
	if st := stateOf(c); st.State != session.Waiting || len(st.Requests()) != 1 || st.Current().Code != question.Code || st.WaitingFor != session.ForQuestion {
		t.Errorf("after the answer: %+v %+v", st, st.Requests())
	}
}
