//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Acceptance 44: the line for each row of the spec's table.
func TestTeamLine(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	m := func(role, place, state string, since time.Duration) teamMember {
		return teamMember{role: role, place: place, status: memberStatus{State: state, Since: ago(since)}}
	}
	working := func(role, place string, d time.Duration) teamMember { return m(role, place, session.Working, d) }
	idle := func(role, place string) teamMember { return m(role, place, session.Idle, time.Hour) }
	waiting := func(role, place string) teamMember { return m(role, place, session.Waiting, time.Minute) }
	quiet := working("engineer", "app", 25*time.Minute)
	quiet.status.Quiet = 12 * time.Minute
	many := []teamMember{waiting("reviewer", "my-app"), m("reviewer", "blog", session.Working, 3*time.Minute)}
	for i, r := range []string{"a", "b", "c", "d", "e"} {
		many = append(many, working("engineer-"+r+"-with-a-long-name", "p", time.Duration(i+1)*time.Minute))
	}
	for _, r := range []string{"x", "y", "z"} {
		many = append(many, idle("designer-"+r+"-with-a-long-name", "p"))
	}
	urgent := []teamMember{waiting("reviewer", "a"), waiting("editor", "b"), waiting("tutor", "c"), waiting("coach", "d"), waiting("scout", "e"),
		working("engineer", "f", time.Minute), working("designer", "g", 2*time.Minute), working("writer", "h", 3*time.Minute)}
	for _, tc := range []struct {
		members []teamMember
		want    string
	}{
		{nil, ""},
		{[]teamMember{working("engineer", "app", 4*time.Minute)}, "Team: engineer working 4m"},
		{[]teamMember{idle("engineer", "app")}, "Team: engineer idle"},
		{[]teamMember{idle("designer", "app"), working("reviewer", "app", 3*time.Minute), working("engineer", "app", 25*time.Minute)},
			"Team: engineer working 25m · reviewer working 3m · designer idle"},
		{[]teamMember{working("engineer", "app", 25*time.Minute), idle("designer", "app"), waiting("writer", "docs")},
			"Team: **! writer waiting on you** · engineer working 25m · designer idle"},
		{[]teamMember{working("engineer", "app", 25*time.Minute), idle("designer", "app"), m("scout", "research", session.SafeguardStopped, 0)},
			"Team: **✗ scout stopped by safeguards** · engineer working 25m · designer idle"},
		{[]teamMember{working("engineer", "app", 25*time.Minute), waiting("writer", "docs"), m("scout", "research", session.SafeguardStopped, 0)},
			"Team: **✗ scout stopped by safeguards** · **! writer waiting on you** · engineer working 25m"},
		{[]teamMember{working("engineer", "my-app", 25*time.Minute), idle("engineer", "blog")}, "Team: engineer (my-app) working 25m · engineer (blog) idle"},
		{[]teamMember{quiet, idle("designer", "app")}, "Team: engineer working 25m (quiet 12m) · designer idle"},
		{[]teamMember{m("engineer", "app", session.Starting, 0), m("writer", "app", session.Unknown, 0)}, "Team: engineer starting · writer running"},
		{many, "Team: **! reviewer (my-app) waiting on you** · 6 working · 3 idle"},
		{urgent, "Team: **! reviewer waiting on you** · **! editor waiting on you** · **! tutor waiting on you** · +2 more need you · 3 working"},
	} {
		if got := teamLine(tc.members, now); got != tc.want {
			t.Errorf("got  %q\nwant %q", got, tc.want)
		}
	}
}

// Acceptance 44, 45 and 50: the blocks the hook prints, the one-time
// hint, and silence outside an orchestrator.
func TestHookTeamStatus(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	block := func() string {
		t.Helper()
		code, out, errOut := call("hook", "team-status")
		if code != 0 || errOut != "" {
			t.Fatalf("exit %d %q", code, errOut)
		}
		return out
	}
	if out := block(); out != "" {
		t.Errorf("outside an orchestrator: %q", out)
	}
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	head := "## Team status (from cadrei, not the user)\n"
	if out := block(); out != head+"No member is running, so no team line.\n" {
		t.Errorf("nobody running: %q", out)
	}
	must(t, "up", "dev/engineer")
	// The hide rule: no hint, no marker.
	playbook := readFile(t, c+"/playbook.md")
	os.WriteFile(c+"/playbook.md", []byte(playbook+"\n## Preferences\n\n- Don't end replies with the team status line.\n"), 0o644)
	if out := block(); out != head+"playbook.md hides the team line, so don't add it. Status only:\nTeam: engineer starting\n" {
		t.Errorf("hidden: %q", out)
	}
	if _, err := os.Stat(home + "/.cadrei/config/team-line-hint"); err == nil {
		t.Error("the hint's marker was made while the line is hidden")
	}
	os.WriteFile(c+"/playbook.md", []byte(playbook), 0o644)
	// A config folder that can't be written: no hint.
	os.Chmod(home+"/.cadrei/config", 0o500)
	if out := block(); strings.Contains(out, "First time") {
		t.Errorf("a hint with no marker: %q", out)
	}
	os.Chmod(home+"/.cadrei/config", 0o700)
	first := head + "End your reply with this line, exactly, after a blank line:\nTeam: engineer starting\nFirst time only: right under it, add exactly:\n" +
		`(Say "hide the team line" if you'd rather not see this.)` + "\n"
	if out := block(); out != first {
		t.Errorf("first: %q", out)
	}
	if out := block(); out != head+"End your reply with this line, exactly, after a blank line:\nTeam: engineer starting\n" {
		t.Errorf("later: %q", out)
	}
	// Once a member has finished a task, a reminder of the every-chat hook
	// offer, once.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	t.Setenv("CADREI_HOME", c)
	if out := block(); !strings.HasSuffix(out, "\n"+offerAsk) || strings.Count(out, "every-chat") != 2 {
		t.Errorf("reminder: %q", out)
	}
	if out := block(); strings.Contains(out, "every-chat") {
		t.Errorf("reminded twice: %q", out)
	}
	t.Setenv("CADREI_HOME", "")
	if out := block(); out != "" {
		t.Errorf("without CADREI_HOME: %q", out)
	}
}

// The reminder never comes when the user said no to the hook already.
func TestNoOfferReminderAfterANo(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "up", "dev/engineer")
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	must(t, "hook", "skip")
	if _, out, _ := call("hook", "team-status"); strings.Contains(out, "every-chat") {
		t.Errorf("after a no: %q", out)
	}
}

// Audit qual-tests-06 and perf-hooks-2, acceptance 46: hook team-status
// takes under 50 ms (the median of 100 runs, the cache warm) with 10 and
// with 30 working members in 10 teams, each with a 256 KB transcript.
func TestHookTeamStatusBudget(t *testing.T) {
	if raceOn {
		t.Skip("the race detector's program isn't the real one")
	}
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"working on it"}]}}` + "\n"
	big := []byte(strings.Repeat(line, 256*1024/len(line)))
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	os.MkdirAll(c+"/.claude/build", 0o755)
	for _, per := range []int{1, 3} {
		for k := 0; k < 10; k++ {
			team := fmt.Sprintf("t%d", k)
			name := "cadrei-work-" + team
			os.MkdirAll(c+"/members/"+team, 0o755)
			tmuxIn(socket, "kill-session", "-t", "="+name)
			for j := 0; j < per; j++ {
				role := fmt.Sprintf("m%d", j)
				member := "work-" + team + "-" + role
				os.WriteFile(c+"/members/"+team+"/"+role+".md", []byte("# Member\n"), 0o644)
				if j == 0 {
					tmuxIn(socket, "new-session", "-d", "-s", name, "-n", role, "sleep", "300")
					tmuxIn(socket, "set-option", "-t", "="+name+":", "@cadrei_home", c)
					tmuxIn(socket, "set-option", "-t", "="+name+":", "@cadrei_team", team)
				} else {
					tmuxIn(socket, "new-window", "-d", "-t", "="+name+":", "-n", role, "sleep", "300")
				}
				tmuxIn(socket, "set-option", "-w", "-t", "="+name+":"+role, "@cadrei_member", member)
				transcript := filepath.Join(t.TempDir(), member+".jsonl")
				os.WriteFile(transcript, big, 0o600)
				feed(t, c, member, `{"hook_event_name":"UserPromptSubmit","transcript_path":"`+transcript+`"}`)
				t.Setenv("CADREI_HOME", c)
			}
		}
		if out := must(t, "hook", "team-status"); strings.Count(out, "working") == 0 {
			t.Fatalf("no one working:\n%s", out)
		}
		var took []time.Duration
		for i := 0; i < 100; i++ {
			start := time.Now()
			must(t, "hook", "team-status")
			took = append(took, time.Since(start))
		}
		slices.Sort(took)
		t.Logf("%d members: median %s", 10*per, took[50])
		if took[50] > 50*time.Millisecond {
			t.Errorf("%d members: median %s, over 50 ms", 10*per, took[50])
		}
	}
}

// Audit qual-tests-13, acceptance 45: past the 2-second limit, or with
// members' states it can't read, the hook prints nothing and exits 0.
func TestHookTeamStatusSaysNothingWhenItCant(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "up", "dev/engineer")
	t.Setenv("CADREI_HOME", c)
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	if out := must(t, "hook", "team-status"); !strings.Contains(out, "Team: engineer") {
		t.Fatalf("before: %q", out)
	}
	defer func(limit time.Duration) { teamStatusLimit, teamStatusStallForTests = limit, 0 }(teamStatusLimit)
	teamStatusLimit, teamStatusStallForTests = 50*time.Millisecond, 400*time.Millisecond
	if code, out, errOut := call("hook", "team-status"); code != 0 || out != "" || errOut != "" {
		t.Errorf("past the limit: %d %q %q", code, out, errOut)
	}
	teamStatusLimit, teamStatusStallForTests = 2*time.Second, 0
	os.Chmod(c+"/.claude/build/state", 0o000)
	t.Cleanup(func() { os.Chmod(c+"/.claude/build/state", 0o755) })
	if code, out, errOut := call("hook", "team-status"); code != 0 || out != "" || errOut != "" {
		t.Errorf("states it can't read: %d %q %q", code, out, errOut)
	}
}
