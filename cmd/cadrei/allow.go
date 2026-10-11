//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// ago writes how long ago t was: "3d ago", "2h ago", "5m ago", "just now".
func ago(t time.Time) string {
	s := int(time.Since(t).Seconds())
	for _, u := range []struct {
		unit string
		n    int
	}{{"d", 86400}, {"h", 3600}, {"m", 60}} {
		if s >= u.n {
			return fmt.Sprintf("%d%s ago", s/u.n, u.unit)
		}
	}
	return "just now"
}

func runAllowList(e *env) int {
	if len(e.args) > 1 || (len(e.args) == 1 && e.args[0] != "list") {
		return e.failWith("Use " + cmd("cadrei allow") + ", or " + cmd("cadrei allow list") + ".")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	perms := rt.Permissions()
	file := perms.GrantsFile(r.Path)
	var list []runtime.Grant
	if _, err := os.Stat(file); err == nil {
		store, err := perms.Open(r.Path, false)
		if err != nil {
			e.warn("", "Your team starts without "+unbroken(display(file))+", because "+strings.TrimSuffix(err.Error(), ".")+".")
			return 1
		}
		if !perms.Unchanged(r.Path) {
			e.warn("", unbroken(display(file))+" was changed outside cadrei allow.", "See what changed with "+cmd("git -C "+display(r.Path)+" log -p -- "+relTo(r.Path, file)))
		}
		list = store.List()
	}
	e.say("%s", render(bold("What your team can do without asking"), streamStyled(e.stdout)))
	if len(list) == 0 {
		e.say("  Nothing extra yet.")
	}
	for i, g := range list {
		var marks []string
		if g.Once {
			marks = append(marks, "once, added "+ago(g.Added))
		}
		if g.Wide {
			marks = append(marks, "wide: contains *")
		}
		mark := ""
		if len(marks) > 0 {
			mark = "   [" + strings.Join(marks, "; ") + "]"
		}
		e.say("  %d. %-4s  %s%s", i+1, g.Kind, g.Entry, mark)
	}
	if b := perms.BuiltIn(); b != "" {
		e.say("%s", render(dim(b), streamStyled(e.stdout)))
	}
	return 0
}

// allowLock serializes cadrei allow changes in one cadrei (read, change,
// write, commit). The lock file stays in the build folder, which git
// ignores, so the cadrei's repository stays clean.
func allowLock(build, file string) (*fsx.Lock, error) {
	if err := session.EnsureBuild(build); err != nil {
		return nil, err
	}
	l, err := fsx.Acquire(filepath.Join(build, "allow.lock"), 10*time.Second)
	if errors.Is(err, fsx.ErrBusy) {
		return nil, fmt.Errorf("another cadrei allow is changing %s. Try again", display(file))
	}
	return l, err
}

func runAllowAdd(e *env) int    { return allowChange(e, "add") }
func runAllowRemove(e *env) int { return allowChange(e, "remove") }

func allowChange(e *env, op string) int {
	if inMember() {
		return e.failWith("Team members can't change what the team is allowed to do.", "Tell your orchestrator what was blocked instead.")
	}
	kind, once := "rule", false
	var args []string
	for _, a := range e.args {
		switch a {
		case "--once":
			once = true
		case "--auto":
			kind = "auto"
		default:
			if strings.HasPrefix(a, "--") {
				return e.failWith("There's no option "+a+" for "+cmd("cadrei allow "+op)+".", "See the options with "+cmd("cadrei help advanced"))
			}
			args = append(args, a)
		}
	}
	switch {
	case op == "add" && len(args) != 1:
		return e.failWith("Allow what? For example:", cmd("cadrei allow add \"Bash(npm test)\""), cmd("cadrei allow add --auto \"running the test suite\""))
	case op == "remove" && (kind == "auto" || (once && len(args) != 0) || (!once && len(args) != 1)):
		return e.failWith("Remove what? For example:", cmd("cadrei allow remove 2"), cmd("cadrei allow remove --once"))
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	perms := rt.Permissions()
	file := perms.GrantsFile(r.Path)
	_, existed := os.Stat(file)
	// Create the file if needed; it is read again under the lock below.
	if _, err := perms.Open(r.Path, true); err != nil {
		return e.failWith(unbroken(display(file))+" can't be changed: "+strings.TrimSuffix(err.Error(), ".")+".",
			"Restore it with "+cmd("git -C "+display(r.Path)+" checkout -- "+relTo(r.Path, file)))
	}
	_ = existed
	lock, err := allowLock(rt.BuildDir(r.Path), file)
	if err != nil {
		return e.fail("%s", err)
	}
	defer lock.Release()
	// Read under the lock: another cadrei allow may have just written.
	store, err := perms.Open(r.Path, false)
	if err != nil {
		return e.failWith(unbroken(display(file)) + " can't be changed: " + strings.TrimSuffix(err.Error(), ".") + ".")
	}
	if !perms.Unchanged(r.Path) {
		e.warn("", unbroken(display(file))+" was changed outside cadrei allow. This change keeps those edits.", "See what changed with "+cmd("git -C "+display(r.Path)+" log -p -- "+relTo(r.Path, file)))
	}
	var msg string
	if op == "add" {
		entry := args[0]
		warning, err := perms.Validate(r.Path, knownPaths(), entry, kind == "auto")
		if err != nil {
			return e.fail("%s", err)
		}
		switch err := store.Add(kind, entry, once); {
		case errors.Is(err, runtime.ErrGranted):
			e.show("", entry+" is already allowed. Nothing was changed.")
			return 0
		case err != nil:
			return e.fail("%s", err)
		}
		msg = "Allow for members: " + entry
		if once {
			msg = "Allow for members once: " + entry
		}
		if once {
			e.note("", markDone, "Allowed once: "+entry)
		} else {
			e.note("", markDone, "Allowed: "+entry)
		}
		if warning != "" {
			e.note("", markWarn, sentence(strings.TrimPrefix(warning, "warning: ")))
		}
	} else {
		target := "--once"
		if !once {
			target = args[0]
		}
		removed, stale, err := store.Remove(target)
		switch {
		case errors.Is(err, runtime.ErrNoOnce):
			e.show("", "There's nothing allowed just once. Nothing was changed.")
			return 0
		case err != nil:
			return e.fail("%s", err)
		}
		switch {
		case len(removed) == 0:
			msg = "Drop stale one-time grant records"
		case once:
			msg = "Remove one-time grants for members"
		default:
			msg = "Remove grant for members: " + removed[0]
		}
		for _, x := range removed {
			e.note("", markDone, "Removed: "+x)
		}
		for _, x := range stale {
			e.show("", x+" was already gone.")
		}
	}
	perms.Record(r.Path)
	// Without a git identity it stays uncommitted, silently (D3).
	cadreis.Commit(r.Path, msg, store.Files()...)
	e.restartNote(r)
	return 0
}

// relTo writes path relative to dir, for a git command run in dir.
func relTo(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return path
	}
	return rel
}

// knownPaths lists every known cadrei's folder.
func knownPaths() []string {
	var out []string
	list, _ := cadreis.List()
	for _, c := range list {
		out = append(out, c.Path)
	}
	return out
}

// restartNote says which running members still have the old grants, with
// the commands that restart each (the design assumes a running session
// does not reload its grants).
func (e *env) restartNote(r *cadreis.Resolved) {
	s := scope(r)
	list := s.Running()
	if len(list) == 0 {
		e.show("", "Team members you start from now on get this change.")
		return
	}
	e.show("", "Team members already running keep what they started with until they restart:")
	e.showSessions(s.T, list, nil)
	// The commands carry the crew, since the user may run them anywhere.
	pin := "CADREI_HOME=" + shellPath(r.Path) + " "
	e.say("Restart one with:")
	generic := false
	for _, i := range list {
		if i.Team == "" {
			generic = true
			continue
		}
		for _, role := range s.T.Windows(i.Name) {
			target := ""
			if i.Target != "" {
				target = " " + session.Quote(i.Target)
			}
			project := ""
			if i.Project != "" {
				project = " " + i.Project
			}
			e.say("  %scadrei stop %s/%s%s && %scadrei up %s/%s%s", pin, i.Team, role, project, pin, i.Team, role, target)
		}
	}
	if generic {
		e.say("  %scadrei stop <team> [project] && %scadrei up <team> [project]", pin, pin)
		e.say("  (a 0.1.x session cadre-<team>-<project> is team <team>, project <project>)")
	}
}

// shellPath writes a folder for a shell command: with ~ when that's safe to
// type unquoted, else quoted in full.
func shellPath(p string) string {
	d := display(p)
	for _, r := range d {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("~/._-", r)) {
			return session.Quote(p)
		}
	}
	return d
}
