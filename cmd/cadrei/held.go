//go:build !windows

package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Held records: a member's call that didn't run, because cadrei's guard
// refused it (session.ForGuard) or the runtime's auto mode denied it
// (session.ForDenied). The member doesn't wait on it: it went on, or
// stopped to say so. The record keeps it in front of the user, with a code,
// until they answer it, the member starts again, or session.HeldFor
// passes.
//
// The code takes yes or no, and presses no key. No closes the record. Yes
// to a guard's refusal the user can allow (allowable) lets that exact
// command by the guard once, within session.AllowedFor: the orchestrator
// tells the member to run it again, and the member's own permission mode
// and deny rules still decide. Yes to any other says plainly why cadrei
// can't, and how the user can run it themselves, with ! in the orchestrator.

// heldWhat says what a held record is about: "a command", or "a call to
// <tool>".
func heldWhat(req *session.Request) string {
	if req.Tool == "Bash" {
		return "a command"
	}
	return "a call to " + req.Tool
}

// heldHead is a held record's first line in the listing and the note.
func heldHead(w *waitingOn) string {
	if w.req.Kind == session.ForGuard {
		return "Cadrei's guard stopped " + w.place() + " from running " + heldWhat(w.req)
	}
	return "Auto mode denied " + w.place() + " " + heldWhat(w.req)
}

// heldAllowable reports whether a yes can allow w's held record: a guard's
// refusal by a rule the user can allow, of a command cadrei shows whole.
func heldAllowable(w *waitingOn) bool {
	return w.req.Kind == session.ForGuard && allowable[w.req.Rule] && w.req.Key != "" && !strings.HasSuffix(w.req.Summary, "…")
}

// bangRoute is the line the user types in the orchestrator to run w's
// command themselves, in the folder the member ran it in: for a shell
// command of one line that cadrei shows whole. ok is false for any other.
func bangRoute(w *waitingOn) (line string, ok bool) {
	s := w.req.Summary
	if w.req.Tool != "Bash" || s == "" || strings.Contains(s, "⏎") || strings.HasSuffix(s, "…") {
		return "", false
	}
	dir := w.req.Dir
	if dir == "" {
		dir = w.dir
	}
	if dir == "" {
		return "! " + s, true
	}
	return "! cd " + shellPath(dir) + " && " + s, true
}

// heldYes is why a yes can't answer w's held record, or "" when it can.
func heldYes(w *waitingOn, code string, c func(string) string) string {
	closeIt := " Then close it with " + c("cadrei answer "+code+" no") + "."
	route, ok := bangRoute(w)
	switch {
	case heldAllowable(w):
		return ""
	case w.req.Kind == session.ForDenied && ok:
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. To run it yourself, type this in the orchestrator: " +
			c(route) + "." + closeIt
	case w.req.Kind == session.ForDenied && w.req.Tool == "Bash":
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. It's too long or has several lines, so it can't be run with ! either: tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Kind == session.ForDenied:
		return "Auto mode denied that " + w.req.Tool + " call, and cadrei can't override the " + w.role + "'s auto mode from here. Only a shell command can be run with !, so tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Rule == ruleTyping || w.req.Rule == ruleKeys:
		return "Cadrei never allows that for a member: it could press keys in another session's window, an approval too. Close it with " + c("cadrei answer "+code+" no") + "."
	case allowable[w.req.Rule]:
		return "That command is too long for cadrei to show you whole, so it can't be allowed from here. Close it with " + c("cadrei answer "+code+" no") +
			", and ask the " + w.role + " to split it up."
	case ok:
		return "Cadrei never allows that for a member. If you want it run, run it yourself in the orchestrator: " + c(route) + "." + closeIt
	}
	return "Cadrei never allows that for a member." + closeIt
}

// checkHeld says why choice can't answer w's held record, or "".
func checkHeld(w *waitingOn, code, choice string, c func(string) string) string {
	switch choice {
	case "no":
		return ""
	case "yes":
		return heldYes(w, code, c)
	}
	if heldAllowable(w) {
		return code + " takes yes or no. Use " + c("cadrei answer "+code+" yes") + " or " + c("cadrei answer "+code+" no") + "."
	}
	return code + " takes no, which closes it. Use " + c("cadrei answer "+code+" no") + "."
}

// heldAsk is the guard's text for answering w's held record with choice.
func heldAsk(w *waitingOn, choice string) string {
	if choice == "yes" {
		return "Cadrei: allow once? " + capital(w.place()) + " may run this command once, in the next " + minutes(session.AllowedFor) +
			", past cadrei's guard (its own permission mode and deny rules still apply): " + w.req.Summary + " The guard stopped it: " + w.req.Reason
	}
	return "Cadrei: close it? " + capital(w.place()) + " won't get to run " + w.req.Summary + ". " + heldWhy(w.req)
}

// heldWhy says why a held record's call didn't run.
func heldWhy(req *session.Request) string {
	if req.Kind == session.ForGuard {
		return "The guard stopped it: " + req.Reason
	}
	return "Auto mode said: " + req.Reason
}

// minutes says a duration of whole minutes: "10 minutes".
func minutes(d time.Duration) string { return strconv.Itoa(int(d/time.Minute)) + " minutes" }

// answerHeld answers w's held record with choice, its mark of an answer by
// code already on: no closes it, and yes lets its command by the guard
// once. It never presses a key, and leaves the member's own state as it
// is.
func (e *env) answerHeld(w *waitingOn, build, choice string) int {
	allowed, found := false, false
	now := time.Now()
	prev, next, err := session.ChangeState(build, w.name, 2*time.Second, func(st *session.State) *session.State {
		if st == nil {
			return st
		}
		next := *st
		next.Open = append([]*session.Request(nil), st.Open...)
		next.Allowed = append([]session.Allowance(nil), st.Allowed...)
		if choice == "yes" {
			allowed = next.Allow(w.req.Code, now)
			found = allowed
		} else {
			found = next.Drop(w.req.Code)
		}
		if !found {
			return st
		}
		return &next
	})
	switch {
	case err != nil:
		answering(build, w, false)
		return e.failWith("The " + w.role + "'s record is busy right now, so I didn't change anything. Try again in a moment.")
	case !found:
		return e.failWith(nothingWaiting(w.req.Code, cmd))
	}
	session.LogPrompts(build, w.name, prev, next, func(code string) string {
		if code == w.req.Code {
			return session.EndedByCode
		}
		return session.EndedCancelled
	}, now)
	if allowed {
		e.note("", markDone, "Allowed once: "+w.place()+" may run that command once in the next "+minutes(session.AllowedFor)+". Tell it to run the exact same command again.")
		return 0
	}
	e.note("", markDone, "Closed "+w.req.Code+". Tell "+w.place()+" you said no, so it doesn't wait on it.")
	return 0
}

// heldNote is a held record's note for the orchestrator.
func heldNote(w *waitingOn) string {
	who := heldHead(w) + " (" + w.req.Code + "): " + noteText(w.req.Summary) + ". " + heldWhy(w.req)
	if heldAllowable(w) {
		return who + " The user can allow it once with cadrei answer " + w.req.Code + " yes, or close it with no. After a yes, tell the " + w.role + " to run the exact same command again."
	}
	if route, ok := bangRoute(w); ok && !(w.req.Rule == ruleTyping || w.req.Rule == ruleKeys) {
		return who + " Cadrei can't allow it from here. The user can run it themselves by typing " + route + ", and close it with cadrei answer " + w.req.Code + " no."
	}
	return who + " Cadrei can't allow it from here. The user closes it with cadrei answer " + w.req.Code + " no."
}

// listHeld is a held record in cadrei answer's listing.
func (e *env) listHeld(w *waitingOn, pad func(string) string) {
	code := w.req.Code
	e.say("%s", "  "+code+"  "+heldHead(w)+":")
	for _, l := range strings.Split(w.req.Summary, "⏎") {
		e.say("%s", "         "+l)
	}
	e.say("%s", "       "+pad("Why:")+w.req.Reason)
	if heldAllowable(w) {
		e.say("%s", "       "+pad("Allow it once:")+cmd("cadrei answer "+code+" yes"))
		e.say("%s", "       "+pad("Say no:")+cmd("cadrei answer "+code+" no"))
		return
	}
	switch route, ok := bangRoute(w); {
	case w.req.Kind == session.ForDenied && ok:
		e.say("%s", "       Cadrei can't override auto mode from here. To run it yourself:")
		e.say("%s", "         "+cmd(route))
	case w.req.Kind == session.ForDenied:
		e.say("%s", "       Cadrei can't override auto mode from here. Tell the "+w.role+" what to do instead.")
	case w.req.Rule == ruleTyping || w.req.Rule == ruleKeys:
		e.say("%s", "       Cadrei never allows this for a member: it could press keys in another session's window.")
	case ok:
		e.say("%s", "       Cadrei never allows this for a member. To run it yourself:")
		e.say("%s", "         "+cmd(route))
	default:
		e.say("%s", "       Cadrei never allows this for a member.")
	}
	e.say("%s", "       "+pad("Close it:")+cmd("cadrei answer "+code+" no"))
}
