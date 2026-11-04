//go:build !windows

package main

import (
	"path/filepath"
	"strconv"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The badge: the orchestrator's tmux status bar says "! 2 waiting" while
// members' requests wait on the user. Whatever changes a member's open
// requests (its hooks, the guard, an answer, a start or a stop) sets the
// count on the orchestrator's session, which its bar shows. A running
// wait and the team status set it too, for a held record that ran out of
// time with no write.

func init() { session.RequestsChanged = refreshBadge }

// refreshBadge sets the waiting count on the tmux session of build's
// orchestrator, when it runs in tmux. It changes nothing else, and fails
// quietly: the bar is a hint, never the record.
func refreshBadge(build string) {
	if name, home := badgeOf(build); name != "" {
		setBadge(name, badgeCount(home, session.Default()))
	}
}

// showBadge sets the count to n, one already counted the way badgeCount
// does.
func showBadge(build string, n int) {
	if name, _ := badgeOf(build); name != "" {
		setBadge(name, n)
	}
}

// badgeOf is the tmux session of build's orchestrator and its crew's
// folder, or "" when it doesn't run in tmux.
func badgeOf(build string) (name, home string) {
	l := orchestrator.ReadLock(orchestrator.LockPath(build))
	if l == nil || l.Mode != orchestrator.Tmux {
		return "", ""
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return "", ""
	}
	t := session.Default()
	home = t.Option(l.Session, "@cadrei_home")
	if t.Option(l.Session, "@cadrei_role") != "orchestrator" || home == "" || rt.BuildDir(home) != build {
		return "", ""
	}
	return l.Session, home
}

// setBadge sets session name's count to n, when it changed.
func setBadge(name string, n int) {
	t, count := session.Default(), strconv.Itoa(n)
	if t.Option(name, session.WaitingOption) != count {
		t.SetOption(name, session.WaitingOption, count)
	}
}

// badgeCount is how many requests wait on the user in the crew at home,
// counted as cadrei answer lists them: running members' only, and held
// records past their time left out. It reads no member's screen.
func badgeCount(home string, t session.Tmux) int {
	c := cadreis.Cadrei{Name: filepath.Base(home), Path: home}
	return len(waitingIn(viewsOf(c, cadreis.Default(), t, false)))
}
