//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
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
	if lines[3].Tool != "Bash" || len(lines[3].Codes) != 1 || lines[3].Codes[0] != st.Request.Code || lines[5].Notice != "permission_prompt" {
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
