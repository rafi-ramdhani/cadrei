//go:build !windows

package main

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// runHookOrchestrator is the hidden cadrei hook orchestrator: the session
// start hook that makes a new session the orchestrator. It stays silent
// in a member's session, with CADREI_OFF, and in an orchestrator cadrei
// started (which has the text in its prompt already), and it never fails:
// a hook that errors would get in the way of every session.
func runHookOrchestrator(e *env) int {
	if inMember() || os.Getenv("CADREI_OFF") != "" || os.Getenv("CADREI_ORCHESTRATOR") != "" {
		return 0
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return 0
	}
	text, _ := cadrei.Assets.ReadFile("orchestrator.md")
	out := strings.TrimSpace(string(text))
	// The same snapshot cadrei's own launch gives, for the cadrei of the
	// folder the chat started in.
	func() {
		defer func() { recover() }()
		wd, _ := paths.Getwd()
		if r, err := cadreis.Resolve(wd, nil); err == nil {
			out += "\n\n" + snapshotOf(rt, r, opening{in: "hook", from: wd}, time.Now())
		}
	}()
	e.stdout.Write(rt.Hooks().Output(out))
	return 0
}

// runHookPrePush is the hidden cadrei hook pre-push, which git runs before a
// cadrei repository is pushed (its backup): it refuses a push that carries
// a file that looks like a credential or is over 50 MB, naming each one.
// When it cannot check, it refuses too: a backup can wait, a leaked token
// cannot be taken back.
func runHookPrePush(e *env) int {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return e.failWith("The pre-push check runs in a git repository.")
	}
	repo := strings.TrimSpace(string(out))
	remote := ""
	if len(e.args) > 0 {
		remote = e.args[0]
	}
	findings, err := backup.Scan(repo, remote, backup.ReadUpdates(e.stdin))
	if err != nil {
		return e.failWith("Couldn't check this push for passwords and keys (" + strings.TrimSuffix(err.Error(), ".") + "), so it was stopped.")
	}
	if len(findings) == 0 {
		return 0
	}
	var others []backup.Finding
	for i := range findings {
		findings[i].Path = printable(findings[i].Path)
	}
	for _, f := range findings {
		if f.Why == backup.MachineOnly {
			e.warn("", unbroken(f.Path)+" is this machine's own file. Take it out of the commits you're pushing (the history, not only the last commit), for example with "+cmd("git rm --cached "+f.Path)+", then push again.")
		} else {
			others = append(others, f)
		}
	}
	if len(others) == 0 {
		return 1
	}
	e.failWith("This push was stopped. It carries files that must not leave this machine:")
	for _, f := range others {
		e.warn("  ", unbroken(f.Path)+": "+f.Why)
	}
	e.show("  ", "Take them out of the commits you're pushing (the history, not only the last commit), then push again.")
	return 1
}

// runHookSession is the hidden cadrei hook session, the session start hook
// in every member's settings copy: when the member's session starts, or
// starts over after /clear or /compact, it records the conversation id it
// now has, so the next start resumes the right conversation. It writes
// only a record of a cadrei that cadrei knows, for a name cadrei makes, and it
// never fails.
func runHookSession(e *env) int {
	name, home := os.Getenv("CADREI_MEMBER"), os.Getenv("CADREI_HOME")
	if name == "" || home == "" {
		return 0
	}
	own := hookCrew(home)
	rt, err := runtime.Get(runtimeName())
	// Only a member's record: the orchestrator's is cadrei's own, written
	// at launch, and a name that is no team and role of this cadrei is no
	// member's.
	if own == nil || err != nil || name == orchestrator.Name(own.Name) || !memberOf(*own, name) {
		return 0
	}
	input, _ := io.ReadAll(io.LimitReader(e.stdin, 1<<20))
	// A start is a state too: idle, in its new conversation.
	writeState(rt, home, name, input, time.Now())
	record := session.RecordPath(rt.BuildDir(paths.Real(home)), name)
	id := rt.Sessions().FromHook(bytes.NewReader(input))
	if record == "" || id == "" {
		return 0
	}
	dir, _ := paths.Getwd()
	if r := session.ReadRecord(record); r != nil {
		if r.ID == id {
			return 0
		}
		dir = r.Dir // the folder it was started in
	}
	session.WriteRecord(record, session.Record{ID: id, Dir: dir, Since: time.Now()})
	return 0
}

// hookCrew is the crew a member's hook names with CADREI_HOME, when cadrei
// knows it: one in ~/.cadrei, or a crew outside it (a crew is a folder
// with members, named as a crew can be), which up starts members of too.
func hookCrew(home string) *cadreis.Cadrei {
	real := paths.Real(home)
	list, _ := cadreis.List()
	for i, c := range list {
		if c.Path == real {
			return &list[i]
		}
	}
	c := cadreis.Cadrei{Name: filepath.Base(real), Path: real}
	if !cadreis.Inside(real) && cadreis.CheckName(c.Name) == nil && c.Present() {
		return &c
	}
	return nil
}

// memberOf reports whether name is a member session name of cadrei c:
// <cadrei>-<team>-<role> or <cadrei>-<team>-<project>-<role>, for a team
// folder and a role file that exist.
func memberOf(c cadreis.Cadrei, name string) bool {
	teams, _ := os.ReadDir(filepath.Join(c.Path, "members"))
	for _, t := range teams {
		if !t.IsDir() || strings.HasPrefix(t.Name(), ".") {
			continue
		}
		prefix := c.Name + "-" + t.Name() + "-"
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		for _, role := range session.Roles(c.Path, t.Name()) {
			rest := strings.TrimPrefix(name, prefix)
			if rest == role || strings.HasSuffix(rest, "-"+role) && len(rest) > len(role)+1 {
				return true
			}
		}
	}
	return false
}

// runHook is cadrei hook [on|off|skip]: the every-chat hook, which makes
// every new chat of the runtime open as your orchestrator. skip records
// the user's no, so the orchestrator doesn't offer it again.
func runHook(e *env) int {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	hooks := rt.Hooks()
	if len(e.args) == 0 {
		switch s, err := everyChat(rt); {
		case err != nil:
			return e.fail("%s", err)
		case s == hookOn:
			e.say("The every-chat hook is on.")
		case s == hookSkipped:
			e.say("The every-chat hook is off (you said no to it).")
		default:
			e.say("The every-chat hook is off.")
		}
		return 0
	}
	if len(e.args) != 1 || (e.args[0] != "on" && e.args[0] != "off" && e.args[0] != "skip") {
		return e.failWith("Use " + cmd("cadrei hook on") + ", " + cmd("cadrei hook off") + " or " + cmd("cadrei hook skip") + ".")
	}
	if e.member("change the every-chat hook") {
		return 1
	}
	bin := framework.Binary()
	switch e.args[0] {
	case "on":
		if e.hookPlaced(bin) != nil {
			return e.failWith("This copy of cadrei isn't installed in a safe place, so it can't add the hook.",
				"Install it with Homebrew or install.sh, then try again.")
		}
		if _, err := hooks.Set(bin); err != nil {
			return e.fail("%s", err)
		}
		cadreis.SetState(cadreis.HookOffer, "")
		e.note("", markDone, "New "+rt.Title()+" chats now open as your orchestrator.")
	case "off":
		if _, err := hooks.Remove(bin); err != nil {
			return e.fail("%s", err)
		}
		e.note("", markDone, "New "+rt.Title()+" chats are plain again.")
	case "skip":
		cadreis.SetState(cadreis.HookOffer, hookSkipped)
	}
	return 0
}

// runHookState is the hidden cadrei hook state, the hook a member's
// settings copy runs on each event that changes what it is doing: it
// writes the member's state file for cadrei ls. It runs on every tool
// call, so it reads one small file, holds the member's lock only around
// that read and write, notes two lines in the member's trace, and uses no
// network. It prints nothing (the output of some hooks goes into the
// member's context) and always exits 0 (exit 1 shows a hook error in the
// member's window, and exit 2 would block the tool).
func runHookState(e *env) int {
	defer func() { recover() }()
	start := time.Now()
	name, home := os.Getenv("CADREI_MEMBER"), os.Getenv("CADREI_HOME")
	if name == "" || home == "" {
		return 0
	}
	// The same checks as hook session: a member of a cadrei cadrei knows.
	if c := hookCrew(home); c == nil || name == orchestrator.Name(c.Name) || !memberOf(*c, name) {
		return 0
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return 0
	}
	input, _ := io.ReadAll(io.LimitReader(e.stdin, 1<<20))
	writeState(rt, home, name, input, start)
	return 0
}

// stateLockWait is how long a state hook waits for the member's lock: well
// under the runtime's hook timeout, so a run that can't get it still ends
// and says so in the trace.
var stateLockWait = 2 * time.Second

// writeState applies a hook's event to a member's state file, in the
// cadrei's build folder when cadrei made it, under the member's lock: hooks
// of tool calls that run at once fire at once, and an unlocked read and
// write of one would undo another's. start is when the hook began, for the
// trace.
func writeState(rt runtime.Runtime, home, name string, input []byte, start time.Time) {
	ev := rt.Sessions().StateEvent(bytes.NewReader(input))
	build := rt.BuildDir(paths.Real(home))
	path := session.StatePath(build, name)
	if (ev.Name == "" && ev.Hook == "") || path == "" {
		return
	}
	if st, err := os.Stat(build); err != nil || !st.IsDir() {
		return
	}
	trace := session.TracePath(build, name)
	line := session.TraceLine{Run: traceRun(), Hook: cmp.Or(ev.Hook, ev.Name), Tool: runtime.TraceWord(ev.Tool), ID: runtime.TraceWord(ev.ToolID), Notice: ev.Notice}
	note := func(phase string, prev, next *session.State, err error) {
		line.T, line.Phase = time.Now(), phase
		if phase == session.TraceEnd {
			line.MS = time.Since(start).Milliseconds()
			line.Before, line.After = session.TraceState(prev), session.TraceState(next)
			for _, r := range next.Requests() {
				line.Codes = append(line.Codes, r.Code)
			}
			if err != nil {
				line.Err = err.Error()
			}
		}
		session.AppendTrace(trace, line)
	}
	note(session.TraceStart, nil, nil, nil)
	prev, next, err := session.ChangeState(build, name, stateLockWait, func(prev *session.State) *session.State {
		session.RotateTrace(trace)
		if ev.Name == "" {
			return prev
		}
		// The catch-all: the runtime says a prompt is up, and no hook
		// recorded one, so cadrei reads it off the member's own screen.
		if ev.Name == runtime.EvPromptShown && len(prev.Requests()) == 0 {
			ev.Request = screenRequest(session.OwnScreen())
		}
		// One a hook recorded: its choices, which no hook gives.
		if ev.Name == runtime.EvPromptShown && len(prev.Requests()) > 0 {
			return withChoices(build, name, prev, session.OwnScreen())
		}
		// An idle reminder over an open request: a no or Esc in the
		// member's window ends a prompt with no hook to say so, and the
		// screen tells whether one still shows.
		if ev.Name == runtime.EvIdleReminder && len(prev.Requests()) > 0 {
			ev.NoPrompt = noPrompt(session.OwnScreen())
		}
		// The end of a call a request may be one of several calls': with no
		// prompt on the screen, the request's prompt is gone too, so it isn't
		// handed on to a call still running.
		if ev.Name == runtime.EvToolDone && slices.ContainsFunc(prev.Requests(), func(r *session.Request) bool { return slices.Contains(r.Maybe, ev.ToolID) }) {
			ev.NoPrompt = noPrompt(session.OwnScreen())
		}
		return session.Apply(prev, ev, time.Now())
	})
	if err == nil {
		session.LogPrompts(build, name, prev, next, session.Ending(ev.Name), time.Now())
	}
	note(session.TraceEnd, prev, next, err)
}

// screenLimit is how much of a screen prompt's box a request keeps: more
// than a hook's summary, since an answer checks the box whole.
const screenLimit = 2000

// screenRequest is the prompt at the bottom of a member's screen as a
// request: its box's text and its options' labels, or nil when no prompt
// shows. One cadrei can't read whole (its top off the screen, its box
// past screenLimit, or more options than it keeps), one that takes more
// than one key, or one with no option cadrei may press (picks) is
// recorded for the member's window to answer.
func screenRequest(screen string) *runtime.Request {
	opts, box, whole := screenPrompt(screen)
	if len(opts) == 0 {
		return nil
	}
	var lines []string
	for _, l := range box {
		lines = append(lines, strings.TrimRight(l, " "))
	}
	text := strings.Trim(strings.Join(lines, "\n"), "\n ")
	r := &runtime.Request{Question: runtime.RequestText(text, screenLimit)}
	for i, o := range opts {
		if i == runtime.MaxOptions {
			r.Multi = true
			break
		}
		r.Options = append(r.Options, runtime.RequestText(o.label, runtime.OptionLimit))
	}
	if !whole || text == "" || strings.HasSuffix(r.Question, "…") || takesMore(box, opts) || len(picks(r.Options)) == 0 {
		r.Multi = true
	}
	return r
}

// choiceLimit is how much of an approval's option a request keeps: the
// user picks one word for word, and some run past a hundred characters.
const choiceLimit = 200

// withChoices is st with the options of the approval its member's screen
// shows, read off the screen, since no hook gives them: the one open
// approval with none yet whose prompt the screen shows, as cadrei answer
// checks it. It's st itself when there's none, or more than one, or more
// options than a request keeps.
func withChoices(build, name string, st *session.State, screen string) *session.State {
	dir := ""
	if rec := session.ReadRecord(session.RecordPath(build, name)); rec != nil {
		dir = rec.Dir
	}
	found := -1
	var opts []promptOption
	for i, r := range st.Requests() {
		if r.Kind != session.ForApproval || len(r.Options) > 0 {
			continue
		}
		w := &waitingOn{req: r, dir: dir}
		if full := session.ReadFull(build, name, r); full != "" {
			whole := *r
			whole.Summary = full
			w.req = &whole
		}
		if !promptShows(screen, w) {
			continue
		}
		if found >= 0 {
			return st
		}
		found = i
		opts, _, _ = promptOf(screen, w)
	}
	if found < 0 || len(opts) > runtime.MaxOptions {
		return st
	}
	next := *st
	next.Open = slices.Clone(st.Open)
	r := *st.Open[found]
	for _, o := range opts {
		r.Options = append(r.Options, runtime.RequestText(o.label, choiceLimit))
	}
	next.Open[found] = &r
	return &next
}

// traceRun is a new id for one hook run in a member's trace.
func traceRun() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
