//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// runUninstall is cadrei uninstall: it removes what this cadrei made outside
// the user's cadreis and projects, after showing the plan and getting a
// yes. It stops cadrei's sessions; removes the skill link and the
// orchestrator hook only where they point at this cadrei, each cadrei's
// pre-push guard (which would stop every push once cadrei is gone),
// ~/.cadrei/config and ~/.cadrei/framework; and keeps every cadrei and
// project. The program itself is removed last, by the user.
func runUninstall(e *env) int {
	// Flags in another order: --all or --archive after --dry-run, say.
	switch {
	case contains(e.args, "--all"):
		return runUninstallAll(e)
	case contains(e.args, "--archive"):
		e.args = without(e.args, "--archive")
		return runUninstallArchive(e)
	}
	yes, dry, keep := false, false, false
	for _, a := range e.args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dry = true
		case keepFlag:
			keep = true
		default:
			return e.failWith("Use " + cmd("cadrei uninstall") + ", with --dry-run or --yes, and " + cmd(keepFlag) + ", if you like.")
		}
	}
	if e.memberAnyway("uninstall cadrei") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	list, _ := cadreis.List()

	// The plan: only what this cadrei made, and where it still points here.
	td := planTeardown(rt, list)
	var terminals []string
	for _, c := range list {
		if l := orchestrator.ReadLock(orchestrator.LockPath(rt.BuildDir(c.Path))); l != nil && l.Mode == orchestrator.Terminal {
			terminals = append(terminals, c.Name)
		}
	}
	skills := rt.Instructions()
	bin := td.bin
	var folders []string
	for _, d := range []string{cadreis.ConfigDir(), framework.Dir()} {
		if _, err := os.Lstat(d); err == nil {
			folders = append(folders, d)
		}
	}

	e.say("cadrei uninstall will:")
	steps := 0
	step := func(text string) {
		steps++
		e.note("  ", markBullet, text)
	}
	if len(td.sessions) > 0 {
		step("stop your running teams: " + strings.Join(td.teams, ", "))
	}
	if len(td.orchestrators) > 0 {
		step("stop your orchestrator in tmux")
	}
	td.linkSteps(step)
	// 0.2.0 notes cadrei couldn't move into their crews stay, so a
	// reinstall still finds where those crews' projects are.
	leftovers := cadreis.Leftovers()
	for _, d := range folders {
		// A link is removed as a link; the folder it points to stays.
		if st, err := os.Lstat(d); err == nil && st.Mode()&os.ModeSymlink != 0 {
			step("remove the link " + unbroken(cadreis.Tilde(d)) + " (its folder " + unbroken(display(d)) + " stays)")
			continue
		}
		if d == cadreis.ConfigDir() && len(leftovers) > 0 {
			step("remove " + unbroken(display(d)) + ", except the notes of " + andList(leftovers) + " that aren't in their crews yet")
			continue
		}
		step("remove " + unbroken(display(d)))
	}
	// The program goes last, after everything that names it.
	prog := planProgram(keep)
	if line := prog.step(); line != "" {
		step(line)
	}
	if steps == 0 {
		e.show("  ", "Nothing: cadrei has nothing set up here.")
	}
	e.say("")
	if len(list) > 0 {
		var names []string
		for _, c := range list {
			names = append(names, c.Name)
		}
		e.show("", "It keeps your crews ("+joinNames(names)+", in "+unbroken(display(cadreis.Root()))+") and every project.")
	} else {
		e.show("", "It keeps every project.")
	}
	if _, err := skills.Target(); err == nil && !td.skillOurs {
		e.show("", "It keeps "+unbroken(cadreis.Tilde(skills.Path()))+", which points at another cadrei program.")
	}
	if td.hookOthers {
		e.show("", "It keeps every-chat hooks that run another cadrei program.")
	}
	td.leftRunning(e)
	for _, name := range terminals {
		e.note("", markWarn, "The orchestrator of "+name+" is open in a terminal. Close it yourself.")
	}
	if !dry && td.unlistedRefusal() != "" {
		return td.refuseUnlisted(e)
	}
	if dry || steps == 0 {
		if then := prog.then(bin); then != "" {
			e.show("", then)
		}
		return 0
	}
	if !e.confirm("Uninstall cadrei?", "Uninstalling", yes) {
		return 1
	}

	code := 0
	warn := func(what string, err error) {
		e.warn("", "Couldn't "+what+": "+strings.TrimSuffix(err.Error(), ".")+".")
		code = 2
	}
	st := td.stop(e, e.stdout, true)
	if len(st.Failed) > 0 {
		code = 2
	}
	if stopped := len(td.sessions) + len(td.orchestrators) - len(st.Failed); stopped > 0 {
		var what []string
		if len(td.sessions) > 0 {
			what = append(what, "your teams")
		}
		if len(td.orchestrators) > 0 {
			what = append(what, "your orchestrator")
		}
		e.note("", markDone, "Stopped "+strings.Join(what, " and "))
	}
	for _, line := range td.unlink(e) {
		e.warn("", line)
		code = 2
	}
	var removed []string
	for _, d := range folders {
		what := unbroken(display(d))
		// A link goes as a link, before anything else: the folder it points
		// to is the user's, and nothing in it is touched (the plan says so).
		if st, err := os.Lstat(d); err == nil && st.Mode()&os.ModeSymlink != 0 {
			what = "the link " + unbroken(cadreis.Tilde(d))
			if err := os.Remove(d); err != nil {
				warn("remove "+what, err)
			} else {
				removed = append(removed, what)
			}
			continue
		}
		if d == cadreis.ConfigDir() && len(leftovers) > 0 {
			entries, _ := os.ReadDir(d)
			for _, en := range entries {
				if en.Name() != "places" && en.Name() != "state.json" {
					os.RemoveAll(filepath.Join(d, en.Name()))
				}
			}
			e.note("", markWarn, "Kept the notes of "+andList(leftovers)+" in "+what+": cadrei couldn't move them into their crews.")
			continue
		}
		if err := os.RemoveAll(d); err != nil {
			warn("remove "+what, err)
		} else {
			removed = append(removed, what)
		}
	}
	if len(removed) > 0 {
		e.note("", markDone, "Removed "+andList(removed))
	}
	e.note("", markDone, "Uninstalled. Your crews and projects are still here.")
	e.show("  ", "Your crews in "+unbroken(cadreis.Tilde(paths.Real(cadreis.Root())))+" are your setup, so keep them. Reinstall and run "+cmd("cadrei")+" to plug them back in.")
	// A cleanup that partly failed keeps the program, so the same command
	// can run again to finish, and remove it last.
	if code != 0 && prog.remove {
		e.note("", markWarn, "The cadrei program stays, since not all of cadrei could be removed. Fix what's above, then run the same command again: it removes the program last.")
	} else if _, c := e.finishProgram(prog, bin); c != 0 {
		code = c
	}
	st.Last(e.stdout)
	return code
}

// leftRunning says which sessions a teardown leaves running.
func (td teardown) leftRunning(e *env) {
	if r := td.unlistedRefusal(); r != "" {
		e.note("", markWarn, r+" So this stops before it changes anything.")
	}
	if len(td.legacy) > 0 {
		e.show("", "It leaves cadre 0.1.x's teams running: "+joinNames(td.legacy)+".")
	}
	if len(td.unmarked) > 0 {
		e.show("", "It leaves tmux sessions cadrei didn't start running: "+joinNames(td.unmarked)+".")
	}
	if len(td.others) > 0 {
		e.show("", "It leaves another setup's teams running: "+strings.Join(td.others, ", ")+".")
	}
}

// finalStep is how the user removes the program itself, by install kind.
func finalStep(bin string) string {
	if installKind() == "dev" {
		return "delete the cadrei you built (" + display(bin) + ")"
	}
	return finalCommand(bin)
}

// finishText is finalStep marked for output: a command kept whole, or the
// built program's path kept whole.
func finishText(bin string) string {
	if installKind() == "dev" {
		return "delete the cadrei you built (" + unbroken(display(bin)) + ")"
	}
	return cmd(finalCommand(bin))
}

// finalCommand is the command that removes the installed program.
func finalCommand(bin string) string {
	switch installKind() {
	case "homebrew":
		return brewRemove
	}
	return "rm " + display(bin)
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
