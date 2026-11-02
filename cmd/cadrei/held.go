//go:build !windows

package main

import (
	"regexp"
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

// heldHead is a held record's first line in the listing and the note,
// with the folder the member ran it in, when the record has it.
func heldHead(w *waitingOn) string {
	in := ""
	if w.req.Dir != "" {
		in = " in " + display(w.req.Dir)
	}
	if w.req.Kind == session.ForGuard {
		return "Cadrei's guard stopped " + w.place() + " from running " + heldWhat(w.req) + in
	}
	return "Auto mode denied " + w.place() + " " + heldWhat(w.req) + in
}

// heldAllowable reports whether a yes can allow w's held record: a guard's
// refusal by a rule the user can allow, of a command cadrei shows whole
// and as it is, in a folder it knows (the allowance is for that folder).
func heldAllowable(w *waitingOn) bool {
	return w.req.Kind == session.ForGuard && allowable[w.req.Rule] && w.req.Key != "" && w.req.Dir != "" &&
		!w.req.Altered && !strings.HasSuffix(w.req.Summary, "…")
}

// noRoute are the refusals cadrei offers no ! line for. Typing and
// keystrokes could press keys in another session's window. A member's
// cadrei answer names a code for a prompt the user hasn't seen: a ! line
// would answer it with no ask. cadrei hook is Claude Code's to run.
var noRoute = setOf(ruleTyping, ruleKeys, ruleAnswer, ruleHook, ruleOpaque)

// heldRoute is bangRoute for w's held record, but none for a refusal in
// noRoute, nor for a command any of those rules finds, whatever rule the
// record names (xargs cadrei answer ... < f is refused as a possible
// uninstall first), nor for an uninstall or restore its line can't show.
func heldRoute(w *waitingOn) (line string, ok bool) {
	if w.req.Kind == session.ForGuard && noRoute[w.req.Rule] || routeBlocked(w.req.Summary) {
		return "", false
	}
	return bangRoute(w)
}

// routeBlocked reports whether command could answer for a member, type or
// press keys in a window, run cadrei's hooks, or run cadrei with a command
// its line doesn't show, or is an uninstall or restore that isn't the
// plain one, with its words written out.
func routeBlocked(command string) bool {
	for _, r := range []guardRule{answerRule, hookRule, opaqueRule{}, tmuxRule{}, keystrokeRule{}} {
		if guardLine(r, command, 0) {
			return true
		}
	}
	return removesCrews(command) && !plainRemoval.MatchString(command)
}

// plainRemoval is cadrei uninstall or restore as one simple command, by
// cadrei's name, with literal words: its line shows all it runs.
var plainRemoval = regexp.MustCompile(`^\s*cadrei[ \t]+(uninstall|restore)([ \t]+[A-Za-z0-9_./=:@-]+)*\s*$`)

// bangRoute is the line the user types in the orchestrator to run w's
// command themselves, in the folder the member ran it in: for a shell
// command of one line that cadrei shows whole and as it is (the line is
// the command itself). ok is false for any other.
func bangRoute(w *waitingOn) (line string, ok bool) {
	s := w.req.Summary
	if w.req.Tool != "Bash" || w.req.Altered || s == "" || strings.Contains(s, "⏎") || strings.HasSuffix(s, "…") {
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
	closeNow := " Close it with " + c("cadrei answer "+code+" no") + "."
	route, ok := heldRoute(w)
	switch {
	case heldAllowable(w):
		return ""
	case w.req.Kind == session.ForDenied && ok:
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. To run it yourself, type this in the orchestrator: " +
			c(route) + "." + closeIt
	case w.req.Kind == session.ForDenied && routeBlocked(w.req.Summary):
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. The command could answer or type for a member, so there's no ! line for it either: tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Kind == session.ForDenied && w.req.Altered:
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. The command holds a tab or a control character, which cadrei can't show you as it is, so there's no ! line for it either: tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Kind == session.ForDenied && w.req.Tool == "Bash":
		return "Auto mode denied that, and cadrei can't override the " + w.role + "'s auto mode from here. It's too long or has several lines, so it can't be run with ! either: tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Kind == session.ForDenied:
		return "Auto mode denied that " + w.req.Tool + " call, and cadrei can't override the " + w.role + "'s auto mode from here. Only a shell command can be run with !, so tell the " +
			w.role + " what to do instead." + closeIt
	case w.req.Rule == ruleTyping || w.req.Rule == ruleKeys:
		return "Cadrei never allows that for a member: it could press keys in another session's window, an approval too." + closeNow
	case w.req.Rule == ruleAnswer:
		return "Cadrei never allows that for a member, and gives you no line to run it yourself: it would answer a prompt you haven't seen. To answer a member, run " +
			c("cadrei answer") + " to see what's waiting first." + closeIt
	case w.req.Rule == ruleHook:
		return "Cadrei never allows that for a member: cadrei's hooks run on their own." + closeNow
	case allowable[w.req.Rule] && w.req.Dir == "":
		return "Cadrei doesn't know which folder the " + w.role + " ran that in, so it can't allow it from here." + closeNow
	case allowable[w.req.Rule] && w.req.Altered:
		return "That command holds a tab or a control character, which cadrei can't show you as it is, so it can't be allowed from here." + closeNow
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
		return "Cadrei: allow once? " + capital(w.place()) + " may run this command once, in " + display(w.req.Dir) + " only, in the next " + minutes(session.AllowedFor) +
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
	if route, ok := heldRoute(w); ok {
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
	switch route, ok := heldRoute(w); {
	case w.req.Kind == session.ForDenied && ok:
		e.say("%s", "       Cadrei can't override auto mode from here. To run it yourself:")
		e.say("%s", "         "+cmd(route))
	case w.req.Kind == session.ForDenied:
		e.say("%s", "       Cadrei can't override auto mode from here. Tell the "+w.role+" what to do instead.")
	case w.req.Rule == ruleTyping || w.req.Rule == ruleKeys:
		e.say("%s", "       Cadrei never allows this for a member: it could press keys in another session's window.")
	case w.req.Rule == ruleAnswer:
		e.say("%s", "       Cadrei never allows this for a member. To answer a member, run "+cmd("cadrei answer")+" to see what's waiting first.")
	case ok:
		e.say("%s", "       Cadrei never allows this for a member. To run it yourself:")
		e.say("%s", "         "+cmd(route))
	default:
		e.say("%s", "       Cadrei never allows this for a member.")
	}
	e.say("%s", "       "+pad("Close it:")+cmd("cadrei answer "+code+" no"))
}
