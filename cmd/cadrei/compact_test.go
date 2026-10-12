//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// inputMember starts the engineer with a stub Claude Code whose screen
// shows ready (unless hidden), then an input line, and notes in
// home/typed the line it gets. With echo off, what's typed doesn't show.
func inputMember(t *testing.T, home string, ready, echo bool) {
	t.Helper()
	footer := ""
	if ready {
		footer = stubReady
	}
	quiet := ""
	if !echo {
		quiet = "stty -echo\n"
	}
	writeStub(t, home+"/bin/claude", "#!/bin/sh\n"+stubAnswers+
		"[ -n \"$TMUX\" ] || exit 0\n"+footer+quiet+"printf '❯ '\nIFS= read -r line\nprintf '%s\\n' \"$line\" > \""+home+"/typed\"\nexec sleep 300\n")
	must(t, "up", "dev/engineer")
	t.Cleanup(func() { session.Default().KillSession("cadrei-work-dev") })
	for i := 0; i < 100 && !strings.Contains(session.Default().Screen(session.SessionName("work", "dev"), "engineer"), "❯"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
}

// typedLine is the line the stub member got, after up to a second.
func typedLine(home string) string {
	for i := 0; i < 20; i++ {
		if raw, err := os.ReadFile(home + "/typed"); err == nil {
			return string(raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

// 0.2.2 (6.6): cadrei compact types /compact into an idle member whose
// prompt is ready, sees it in the input line, then presses Enter.
func TestCompactTypesItIntoAnIdleMember(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	inputMember(t, home, true, true)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	if n, out, errOut := call("compact", "dev/engineer"); n != 0 || out != "+ Compacting the engineer in dev's conversation.\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if got := typedLine(home); got != "/compact\n" {
		t.Errorf("typed %q", got)
	}
}

// A member that's working or waiting, or whose prompt isn't ready, gets
// no keys; nor does one that isn't running, or a run in a member.
func TestCompactRefusesABusyMember(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	inputMember(t, home, true, true)
	busy := "cadrei: the engineer in dev is busy. Try again when it's idle."
	for _, payload := range []string{`{"hook_event_name":"UserPromptSubmit"}`, mkdirRequest} {
		feed(t, c, "work-dev-engineer", payload)
		if n, _, errOut := call("compact", "dev/engineer"); n != 1 || flat(errOut) != busy {
			t.Errorf("%s: exit %d %q", payload, n, errOut)
		}
	}
	if n, _, errOut := call("compact", "dev/reviewer"); n != 1 || flat(errOut) != "cadrei: the reviewer in dev isn't running." {
		t.Errorf("not running: %d %q", n, errOut)
	}
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	t.Setenv("CADREI_MEMBER", "work-dev-reviewer")
	if n, _, errOut := call("compact", "dev/engineer"); n != 1 || flat(errOut) != "cadrei: team members can't compact a member. Ask the user to do it from the orchestrator." {
		t.Errorf("member: %d %q", n, errOut)
	}
	os.Unsetenv("CADREI_MEMBER")
	if typedLine(home) != "" {
		t.Error("something was typed")
	}
	if n, _, errOut := call("compact", "dev"); n != 1 || flat(errOut) != "cadrei: use cadrei compact <team>/<role> [project]." {
		t.Errorf("usage: %d %q", n, errOut)
	}
}

// Not ready (no footer): idle or not, nothing is typed.
func TestCompactWaitsForAReadyPrompt(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	waitForTests = time.Second
	t.Cleanup(func() { waitForTests = 0 })
	inputMember(t, home, false, true)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	if n, _, errOut := call("compact", "dev/engineer"); n != 1 || !strings.Contains(flat(errOut), "is busy") {
		t.Errorf("exit %d %q", n, errOut)
	}
	if typedLine(home) != "" {
		t.Error("something was typed")
	}
}

// /compact that doesn't show in the input line gets no Enter.
func TestCompactPressesNoEnterUnseen(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	inputMember(t, home, true, false)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	compactWait = 300 * time.Millisecond
	t.Cleanup(func() { compactWait = 2 * time.Second })
	if n, _, errOut := call("compact", "dev/engineer"); n != 1 || flat(errOut) != "cadrei: I typed /compact, but it didn't show in the engineer in dev's input, so I didn't press Enter. Check its window: cadrei attach dev" {
		t.Errorf("exit %d %q", n, errOut)
	}
	if typedLine(home) != "" {
		t.Error("Enter was pressed")
	}
}
