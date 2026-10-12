//go:build !windows

package main

import (
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// compactWait is how long /compact may take to show in the member's input
// line before cadrei gives up without pressing Enter.
var compactWait = 2 * time.Second

// runCompact is cadrei compact <team>/<role> [project]: it types /compact
// into an idle member whose prompt is ready, waits for it to show in the
// input line, then presses Enter as a key of its own. It carries no
// consent, so the orchestrator runs it with no prompt; the guard keeps
// sessions from typing into tmux themselves.
func runCompact(e *env) int {
	if len(e.args) < 1 || len(e.args) > 2 || !strings.Contains(e.args[0], "/") {
		return e.failWith("Use " + cmd("cadrei compact <team>/<role> [project]") + ".")
	}
	if e.memberAnyway("compact a member") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	team, role, err := splitTarget(r, e.args[0])
	if err != nil {
		return e.failErr(err)
	}
	project := ""
	if len(e.args) == 2 {
		project = e.args[1]
	}
	place := placeName(printable(role), printable(team), printable(project))
	t := session.Default()
	name := session.SessionName(r.Name, session.Key(team, project))
	if !t.HasWindow(name, role) || t.Option(name, "@cadrei_home") != r.Path {
		return e.failWith(capital(place) + " isn't running.")
	}
	member := session.MemberName(r.Name, session.Key(team, project), role)
	ops := rt.Sessions()
	st := statusOfMember(rt, r.Path, member, time.Now(), true)
	if st.State != session.Idle || !ops.Ready(t.Screen(name, role)) {
		return e.failWith(capital(place) + " is busy. Try again when it's idle.")
	}
	if err := t.SendKeys(name, role, true, "/compact"); err != nil {
		return e.fail("%s", err)
	}
	for start := time.Now(); !typed(t.Screen(name, role), "/compact"); time.Sleep(100 * time.Millisecond) {
		if time.Since(start) >= compactWait {
			return e.failWith("I typed /compact, but it didn't show in "+place+"'s input, so I didn't press Enter.",
				"Check its window: "+cmd("cadrei attach "+strings.TrimSpace(team+" "+project)))
		}
	}
	if err := t.SendKeys(name, role, false, "Enter"); err != nil {
		return e.fail("%s", err)
	}
	e.note("", markDone, "Compacting "+place+"'s conversation.")
	return 0
}

// typed reports whether text sits alone in the input line at the bottom
// of a member's screen: the last line that starts with the prompt mark.
func typed(screen, text string) bool {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-8; i-- {
		line := strings.TrimSpace(strings.Trim(lines[i], "│ "))
		if rest, ok := strings.CutPrefix(line, "❯"); ok {
			return strings.TrimSpace(rest) == text
		}
		if rest, ok := strings.CutPrefix(line, ">"); ok {
			return strings.TrimSpace(rest) == text
		}
	}
	return false
}
