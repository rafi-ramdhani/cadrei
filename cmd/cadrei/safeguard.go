//go:build !windows

package main

import (
	"slices"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Safeguard stops: the runtime's safeguards stopped a member's last
// reply, so it won't answer until the user picks its model again or
// starts it fresh. Cadrei gives the stop a code once the runtime's own
// retry had its chance. A fresh start is answered from here: the member
// stops and starts again in a new chat. Picking the model again stays in
// its window: Claude Code 2.1.304 saves a model picked by /model as the
// default for every new session (its picker does too, unless s is
// pressed), which would change the user's own settings.

// lookAtStop records the safeguard stop status m shows for member name,
// in build, as a request with a code, or ends the one m no longer shows.
// It reports whether the member's state changed.
func lookAtStop(build, name string, m memberStatus) bool {
	stopped := m.State == session.SafeguardStopped
	prev := session.ReadState(session.StatePath(build, name))
	if session.SeeStop(prev, m.Since, stopped) == prev {
		return false
	}
	session.SeeStopOf(build, name, m.Since, stopped)
	return true
}

// modelName is the runtime's model's name, whose safeguards stop a reply.
func modelName() string {
	if rt, err := runtime.Get(runtimeName()); err == nil {
		return rt.Model()
	}
	return "The model"
}

// pickInWindow says how to pick the model again in w's member's window,
// and why cadrei doesn't.
func pickInWindow(w *waitingOn, c func(string) string) string {
	return "run /model in its window and press s to keep the pick to its session (a pick there is saved as your default for new sessions otherwise): " + c(w.attach())
}

// checkStop says why choice can't answer w's safeguard stop, or "": only
// a fresh start is answered from here.
func checkStop(w *waitingOn, code, choice string, c func(string) string) string {
	switch choice {
	case "2":
		return ""
	case "1":
		return "Cadrei doesn't pick its model for you. To pick it again, " + pickInWindow(w, c)
	}
	return code + " takes 2 (start it fresh) from here. Use " + c("cadrei answer "+code+" 2") + ", or pick its model again in its window: " + c(w.attach())
}

// stopAsk is the guard's text for starting w's member fresh.
func stopAsk(w *waitingOn) string {
	return "Cadrei: start " + w.place() + " fresh? It stops, and starts again in a new chat that won't remember this one, after " +
		modelName() + "'s safeguards stopped its last reply."
}

// answerStop starts w's member fresh, its safeguard stop's request ended
// as answered by its code first: as cadrei stop --fresh and cadrei up do.
func (e *env) answerStop(w *waitingOn, build string) int {
	answered(build, w, "2", time.Now())
	member := w.team + "/" + w.role
	stop, up := []string{member}, []string{member}
	if w.project != "" {
		stop = append(stop, w.project)
		up = append(up, w.project)
		if target := session.Default().Option(w.session, "@cadrei_target"); target != "" {
			up[1] = target
		}
	}
	saved := e.args
	defer func() { e.args = saved }()
	e.args = append(slices.Clone(stop), "--fresh")
	if code := runStop(e); code != 0 {
		return code
	}
	e.args = up
	return runUp(e)
}
