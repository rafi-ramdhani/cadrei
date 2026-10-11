//go:build !windows

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	writeState(rt, home, name, input)
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
// call, so it reads one small file, takes no locks and uses no network.
// It prints nothing (the output of some hooks goes into the member's
// context) and always exits 0 (exit 1 shows a hook error in the member's
// window, and exit 2 would block the tool).
func runHookState(e *env) int {
	defer func() { recover() }()
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
	writeState(rt, home, name, input)
	return 0
}

// writeState applies a hook's event to a member's state file, in the
// cadrei's build folder when cadrei made it.
func writeState(rt runtime.Runtime, home, name string, input []byte) {
	ev := rt.Sessions().StateEvent(bytes.NewReader(input))
	build := rt.BuildDir(paths.Real(home))
	path := session.StatePath(build, name)
	if ev.Name == "" || path == "" {
		return
	}
	if st, err := os.Stat(build); err != nil || !st.IsDir() {
		return
	}
	// Most tool events change nothing (working stays working): no write.
	prev := session.ReadState(path)
	if next := session.Apply(prev, ev, time.Now()); prev == nil || next == nil || *next != *prev {
		session.WriteState(path, next)
	}
}
