//go:build !windows

package main

import (
	"os/exec"
	"strings"
	"testing"

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
