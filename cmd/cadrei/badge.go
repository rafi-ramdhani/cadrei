//go:build !windows

package main

import (
	"strconv"

	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The badge: the orchestrator's tmux status bar says "! 2 waiting" while
// members' requests wait on the user. Whatever changes a member's open
// requests (its hooks, the guard, an answer, a start or a stop) sets the
// count on the orchestrator's session, which its bar shows.

func init() { session.RequestsChanged = refreshBadge }

// refreshBadge sets the waiting count on the tmux session of build's
// orchestrator, when it runs in tmux. It changes nothing else, and fails
// quietly: the bar is a hint, never the record.
func refreshBadge(build string) {
	l := orchestrator.ReadLock(orchestrator.LockPath(build))
	if l == nil || l.Mode != orchestrator.Tmux {
		return
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return
	}
	t := session.Default()
	home := t.Option(l.Session, "@cadrei_home")
	if t.Option(l.Session, "@cadrei_role") != "orchestrator" || home == "" || rt.BuildDir(home) != build {
		return
	}
	count := strconv.Itoa(session.WaitingCount(build))
	if t.Option(l.Session, session.WaitingOption) != count {
		t.SetOption(l.Session, session.WaitingOption, count)
	}
}
