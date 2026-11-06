//go:build !windows

package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// N8: the orchestrator's tmux status bar shows how many requests wait on
// the user, set by whatever opens or ends one, and nothing once none
// waits.
func TestTheOrchestratorsBarShowsWhatsWaiting(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	must(t, "--tmux", "--detach")
	tm, name := session.Default(), orchestrator.SessionName("work")
	right := func() string {
		t.Helper()
		out, err := exec.Command("tmux", "-L", tm.Socket, "display", "-p", "-t", "="+name+":", "#{E:status-right}").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got := tm.Option(name, session.WaitingOption); got != "0" || strings.Contains(right(), "waiting") {
		t.Errorf("at the start: %q, bar %q", got, right())
	}
	must(t, "up", "dev")
	request(t, c, "engineer", mkdirRequest)
	if got := tm.Option(name, session.WaitingOption); got != "1" || !strings.Contains(right(), " ! 1 waiting ") {
		t.Errorf("one waiting: %q, bar %q", got, right())
	}
	request(t, c, "reviewer", uploadRequest)
	if got := tm.Option(name, session.WaitingOption); got != "2" || !strings.Contains(right(), " ! 2 waiting ") {
		t.Errorf("two waiting: %q, bar %q", got, right())
	}
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	if got := tm.Option(name, session.WaitingOption); got != "1" {
		t.Errorf("one ended: %q", got)
	}
	// Review L1: it counts what cadrei answer lists. A state file left by
	// a member that isn't running doesn't count, nor does a held record
	// past its time.
	build := c + "/.claude/build"
	gone := &session.State{State: session.Waiting, WaitingFor: session.ForQuestion, Since: time.Now(),
		Open: []*session.Request{{Code: "q7x", Kind: session.ForQuestion, Tool: "AskUserQuestion", Question: "Still here?", Source: session.FromHook}}}
	if err := session.WriteState(session.StatePath(build, "work-old-tester"), gone); err != nil {
		t.Fatal(err)
	}
	held := func(at time.Time) {
		t.Helper()
		_, _, err := session.ChangeState(build, "work-dev-engineer", time.Second, func(prev *session.State) *session.State {
			next := *prev
			next.Open = append(slices.Clone(prev.Open), &session.Request{Kind: session.ForGuard, Tool: "Bash", Summary: "git push", At: at, Source: session.FromHook})
			return &next
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	held(time.Now().Add(-session.HeldFor))
	if got := tm.Option(name, session.WaitingOption); got != "1" {
		t.Errorf("with a gone member's file and a held record past its time: %q", got)
	}
	// One that runs out of time later is taken off by a wait's look, as
	// nothing writes for it.
	held(time.Now())
	if got := tm.Option(name, session.WaitingOption); got != "2" {
		t.Errorf("a held record: %q", got)
	}
	path := session.StatePath(build, "work-dev-engineer")
	st := session.ReadState(path)
	for _, r := range st.Open {
		r.At = time.Now().Add(-session.HeldFor)
	}
	session.WriteState(path, st)
	ownTerminal(t)
	n, out, errOut := call("answer", "--wait", "--timeout", "100ms")
	if got := tm.Option(name, session.WaitingOption); got != "1" {
		t.Errorf("after a wait's look: %q (%d %q %q)", got, n, out, errOut)
	}
	must(t, "stop", "dev", "--yes")
	if got := tm.Option(name, session.WaitingOption); got != "0" || strings.Contains(right(), "waiting") {
		t.Errorf("stopped: %q, bar %q", got, right())
	}
}

// The badge goes first on the right of the bar, its styles kept out of
// the conditional's commas, and plain without colors.
func TestTheBadgeLook(t *testing.T) {
	look := session.Badged([]session.Option{{Name: "status-right", Value: "keys"}, {Name: "status-right-length", Value: "60"}}, false)
	if look[0].Value != "#{?@cadrei_waiting,#[bold bg=#A9502F fg=#FAF8F4] ! #{@cadrei_waiting} waiting #[default] ,}keys" || look[1].Value != "80" {
		t.Errorf("look: %+v", look)
	}
	plain := session.Badged([]session.Option{{Name: "status-right", Value: "keys"}}, true)
	if plain[0].Value != "#{?@cadrei_waiting,#[bold]! #{@cadrei_waiting} waiting#[nobold] · ,}keys" {
		t.Errorf("plain: %+v", plain)
	}
}
