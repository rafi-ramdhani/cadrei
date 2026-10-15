//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/memory"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// deletePhrase is what the user types, or passes to --confirm, to delete
// everything: --yes isn't enough.
const deletePhrase = "delete everything"

// runUninstallAll is cadrei uninstall --all: uninstall, and also delete
// every crew, cadrei's config and state, and every archive, so nothing of
// cadrei is left. Project folders are never deleted: they're the user's
// work, which cadrei only links. A folder cadrei reaches through a link
// loses the link only.
func runUninstallAll(e *env) int {
	dry, confirmed, keep := false, false, false
	for i := 0; i < len(e.args); i++ {
		switch a := e.args[i]; a {
		case "--all":
		case "--dry-run":
			dry = true
		case keepFlag:
			keep = true
		case "--archive":
			return allWithArchive(e)
		case "--yes", "-y":
			return e.failWith("--yes isn't enough to delete every crew.",
				"Run "+cmd("cadrei uninstall --all")+" in a terminal and type "+deletePhrase+", or add "+cmd("--confirm \""+deletePhrase+"\"")+".")
		case "--confirm":
			if i+1 >= len(e.args) || e.args[i+1] != deletePhrase {
				return e.failWith("--confirm takes exactly \"" + deletePhrase + "\".")
			}
			confirmed = true
			i++
		default:
			return e.failWith("Use " + cmd("cadrei uninstall --all") + ", with --dry-run first.")
		}
	}
	if dry && confirmed {
		return e.failWith("Pick one: --dry-run or --confirm.")
	}
	if e.memberAnyway("uninstall cadrei") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	list, err := allCrews()
	if err != nil {
		return e.failWith("Couldn't list your crews: "+strings.TrimSuffix(err.Error(), ".")+".", "Nothing was deleted.")
	}
	root, archives := cadreis.Root(), cadreis.ArchiveRoot()
	gone := deletes(list)
	projects, why, fix := crewProjects(list, gone)
	if why != "" {
		return e.failWith(why, fix)
	}
	td := planTeardown(rt, list)

	e.say("cadrei uninstall --all will:")
	step := func(text string) { e.note("  ", markBullet, text) }
	if len(td.members) > 0 {
		step(fmt.Sprintf("stop %d team members: %s", len(td.members), strings.Join(td.members, ", ")))
	}
	if len(td.orchestrators) > 0 {
		step("stop your orchestrator in tmux")
	}
	td.linkSteps(step)
	var risky []string
	for _, c := range list {
		step("delete the crew " + c.Name + " (" + unbroken(display(c.Path)) + "): " + holds(c))
		b := checkBackup(c)
		switch {
		case b.err != "":
			risky = append(risky, c.Name+"'s backup couldn't be checked ("+b.err+")")
		case b.url == "":
			risky = append(risky, c.Name+" has no backup, so nothing of it is kept anywhere")
		default:
			if b.uncommitted || len(b.untracked) > 0 {
				risky = append(risky, c.Name+" has changes that aren't committed")
			}
			if b.ahead > 0 {
				risky = append(risky, c.Name+" has "+commits(b.ahead, "that isn't", "that aren't")+" in its backup ("+b.remote+")")
			}
		}
	}
	rootLinked, archivesLinked := linked(root), linked(archives)
	if _, err := os.Lstat(root); err == nil {
		if rootLinked {
			step("remove the link " + unbroken(cadreis.Tilde(root)) + ", and cadrei's settings, state and program files in its folder " + unbroken(display(root)) + " (anything else there stays)")
		} else {
			step("delete " + unbroken(cadreis.Tilde(root)) + ": cadrei's settings, state and program files" + others(root, list))
		}
	}
	archived := len(cadreis.Archives())
	if _, err := os.Lstat(archives); err == nil {
		if archivesLinked {
			step("remove the link " + unbroken(cadreis.Tilde(archives)) + " (its folder " + unbroken(display(archives)) + " stays)")
		} else {
			what := "every archive"
			if archived == 1 {
				what = "your archive"
			} else if archived > 1 {
				what = fmt.Sprintf("your %d archives", archived)
			}
			step("delete " + unbroken(cadreis.Tilde(archives)) + ", with " + what + " in it")
		}
	}
	if len(list) > 0 {
		step("remove your crews' trust in " + rt.Title())
	}
	prog := planProgram(keep)
	if line := prog.step(); line != "" {
		step(line)
	}
	e.say("")
	if len(projects) > 0 {
		e.show("", "It keeps every project's folder: "+strings.Join(projects, ", ")+". "+rt.Title()+"'s own chat history stays too.")
	} else {
		e.show("", "It keeps every project's folder. "+rt.Title()+"'s own chat history stays too.")
	}
	td.leftRunning(e)
	for _, r := range risky {
		e.note("", markWarn, r+".")
	}
	// An orchestrator open in another terminal would go on in a deleted
	// folder: it's named, and stops a real run. The one running this
	// command, which this process runs under, is closed by the user after.
	open := openElsewhere(rt, list)
	for _, o := range open {
		e.note("", markWarn, o+" So this stops before it deletes anything.")
	}
	if !dry && td.unlistedRefusal() != "" {
		return td.refuseUnlisted(e)
	}
	if !dry && len(open) > 0 {
		return e.failWith("An orchestrator is open in another terminal. Nothing was deleted.", "Close it first, then run this again.")
	}
	if dry {
		e.show("", "Nothing was deleted. To go ahead, run "+cmd("cadrei uninstall --all")+" in a terminal and type "+deletePhrase+"."+strings.TrimSuffix(" "+prog.then(td.bin), " "))
		return 0
	}
	if !confirmed {
		if !e.interactive() {
			return e.failWith("Deleting everything needs your typed yes, and this isn't a terminal. Nothing was deleted.",
				"Run "+cmd("cadrei uninstall --all")+" in a terminal, or add "+cmd("--confirm \""+deletePhrase+"\"")+" once you've read the dry run.")
		}
		e.say("")
		if strings.TrimSpace(e.ask("This can't be undone. Type "+deletePhrase+" to go on, or press Enter to stop: ")) != deletePhrase {
			e.say("Nothing was deleted.")
			return 1
		}
	}

	code := 0
	warn := func(what string, err error) {
		e.warn("", "Couldn't "+what+": "+strings.TrimSuffix(err.Error(), ".")+".")
		code = 2
	}
	st := td.stop(e, e.stdout, true)
	if len(st.Failed) > 0 {
		// A team that still runs writes into its crew: nothing is deleted.
		e.warn("", "Nothing was deleted, since a team is still running. Stop it, then run this again.")
		return 2
	}
	// What changed while the user read the plan and typed, such as a crew
	// made, a project linked into one or an orchestrator opened, is
	// checked again. Each crew's places stay locked from here to its
	// delete, so none is linked in meanwhile.
	for _, c := range list {
		l, err := cadreis.LockPlaces(c)
		if err != nil {
			e.warn("", "Nothing was deleted: "+strings.TrimSuffix(err.Error(), ".")+".")
			return 2
		}
		defer l.Release()
	}
	again, err := allCrews()
	if err == nil && !slices.Equal(again, list) {
		err = errors.New("your crews changed while you read the plan")
	}
	if err == nil && len(openElsewhere(rt, list)) > 0 {
		err = errors.New("an orchestrator opened in another terminal while you read the plan")
	}
	if err != nil {
		e.warn("", "Nothing was deleted: "+strings.TrimSuffix(err.Error(), ".")+". Run "+cmd("cadrei uninstall --all")+" again to see the plan as it is now.")
		return 2
	}
	if _, why, fix := crewProjects(list, gone); why != "" {
		e.warn("", why+" Nothing was deleted.")
		e.show("  ", fix)
		return 2
	}
	if beforeDeletesForTests != nil {
		beforeDeletesForTests(list)
	}
	for _, line := range td.unlink(e) {
		e.warn("", line)
		code = 2
	}
	if len(list) > 0 {
		var folders []runtime.Folder
		for _, c := range list {
			folders = append(folders, runtime.Folder{Name: c.Name, Dir: c.Path})
		}
		if _, note := rt.Trust().Unmark(folders); note != "" {
			e.note("", markWarn, note)
		}
		// The pointers to each crew's memory, only where they're exactly
		// cadrei's.
		for _, c := range list {
			for _, d := range memoryFolders(rt, c) {
				memory.RemovePointer(d)
			}
		}
	}
	var deleted []string
	for _, c := range list {
		if err := os.RemoveAll(c.Path); err != nil {
			warn("delete the crew "+c.Name, err)
		} else {
			deleted = append(deleted, c.Name)
		}
	}
	// A link loses only itself: in a linked ~/.cadrei, cadrei's own
	// folders go too, and everything else there stays.
	remove := func(d string) bool {
		var err error
		switch st, lerr := os.Lstat(d); {
		case lerr != nil:
			return true
		case st.Mode()&os.ModeSymlink != 0:
			err = os.Remove(d)
		default:
			err = os.RemoveAll(d)
		}
		if err != nil {
			warn("delete "+unbroken(cadreis.Tilde(d)), err)
		}
		return err == nil
	}
	settings := true
	if rootLinked {
		real := paths.Real(root)
		for _, own := range []string{"config", "framework"} {
			settings = remove(filepath.Join(real, own)) && settings
		}
	}
	settings = remove(root) && settings
	archivesGone := remove(archives)
	var what []string
	if len(deleted) > 0 {
		what = append(what, andList(deleted))
	}
	if settings {
		what = append(what, "cadrei's settings")
	}
	switch {
	case archivesLinked && archivesGone:
		what = append(what, "the link "+unbroken(cadreis.Tilde(archives)))
	case archived > 0 && archivesGone:
		what = append(what, "your archives")
	}
	if len(what) > 0 {
		e.note("", markDone, "Deleted "+andList(what))
	}
	switch {
	case code != 0:
		e.note("", markWarn, "Uninstalled, but not all of cadrei could be deleted: see above. Your projects are still here.")
	case archivesLinked:
		e.note("", markDone, "Uninstalled. Your archives stay in "+unbroken(display(archives))+", and your projects are still here.")
	case prog.remove:
		// The program goes next, the plan's last step.
		e.note("", markDone, "Uninstalled. Your projects are still here.")
	default:
		e.note("", markDone, "Uninstalled. Nothing of cadrei is left here but the program. Your projects are still here.")
	}
	// A cleanup that partly failed keeps the program, so the same command
	// can run again to finish, and remove it last.
	if code != 0 && prog.remove {
		e.note("", markWarn, "The cadrei program stays, since not all of cadrei could be removed. Fix what's above, then run the same command again: it removes the program last.")
	} else if _, c := e.finishProgram(prog, td.bin); c != 0 {
		code = c
	}
	st.Last(e.stdout)
	return code
}

// beforeDeletesForTests, when set, runs after the last check and before
// anything is deleted, with the crews' places still locked.
var beforeDeletesForTests func(list []cadreis.Cadrei)

// allCrews lists the crews --all deletes: cadreis.List's, each with a
// cadrei.conf or a playbook.md beside its members folder, so a folder of
// the user's own in a linked ~/.cadrei isn't taken for one.
func allCrews() ([]cadreis.Cadrei, error) {
	list, err := cadreis.List()
	if err != nil {
		return nil, err
	}
	var out []cadreis.Cadrei
	for _, c := range list {
		if exists(filepath.Join(c.Path, "cadrei.conf")) || exists(filepath.Join(c.Path, "playbook.md")) {
			out = append(out, c)
		}
	}
	return out, nil
}

// linked reports whether p is a link.
func linked(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// deletes lists the folders --all deletes, which no project may be in:
// ~/.cadrei whole, or for a linked one, each crew and cadrei's own
// folders in it; and ~/.cadrei-archive unless it's a link, which only
// loses itself.
func deletes(list []cadreis.Cadrei) []string {
	root, archives := cadreis.Root(), cadreis.ArchiveRoot()
	var out []string
	if linked(root) {
		for _, c := range list {
			out = append(out, c.Path)
		}
		out = append(out, filepath.Join(paths.Real(root), "config"), filepath.Join(paths.Real(root), "framework"))
	} else {
		out = append(out, root)
	}
	if !linked(archives) {
		out = append(out, archives)
	}
	return out
}

// openElsewhere names each crew's orchestrator open in a terminal that
// this process doesn't run under.
func openElsewhere(rt runtime.Runtime, list []cadreis.Cadrei) []string {
	var out []string
	for _, c := range list {
		if l := orchestrator.ReadLock(orchestrator.LockPath(rt.BuildDir(c.Path))); l != nil && l.Mode == orchestrator.Terminal && !runsUnder(l.PID) {
			out = append(out, "The orchestrator of "+c.Name+" is open in another terminal (open since "+l.Since.Format("15:04")+").")
		}
	}
	return out
}

// allWithArchive refuses --all with --archive, in either order.
func allWithArchive(e *env) int {
	return e.failWith("--all and --archive don't go together: --archive packs your crews away to bring back later, and --all deletes them.",
		"Pick one: "+cmd("cadrei uninstall --archive")+" or "+cmd("cadrei uninstall --all")+".")
}

// crewProjects reads every crew's projects for --all, and the places 0.2.0
// kept for them, since each project's folder stays: none may sit in what
// --all deletes (gone). It returns the projects for the plan,
// or why --all can't go on and what to do: a project folder inside, or
// notes it can't read, which could hide one.
func crewProjects(list []cadreis.Cadrei, gone []string) (projects []string, why, fix string) {
	unreadable := func(file string, err error) (string, string) {
		// The file is named already: its error alone.
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return "Couldn't read " + unbroken(cadreis.Tilde(file)) + ", so this can't check that no project's folder is in what it deletes: " +
				strings.TrimSuffix(err.Error(), ".") + ". Nothing was deleted.",
			"Fix the file or its permissions, then run this again."
	}
	inside := func(name, d string) (string, string) {
		for _, g := range gone {
			if paths.Inside(paths.Real(d), paths.Real(g)) {
				return "The project " + printable(name) + "'s folder, " + display(d) + ", is inside " + unbroken(cadreis.Tilde(g)) + ", which this deletes.",
					"Move it out and link it again with " + cmd("cadrei project link "+printable(name)+" <folder>") + ", then run this again."
			}
		}
		return "", ""
	}
	for _, c := range list {
		reg, err := registry.Load(c.Registry())
		if err != nil {
			why, fix = unreadable(c.Registry(), err)
			return nil, why, fix
		}
		places, err := cadreis.ReadPlaces(c)
		if err != nil {
			why, fix = unreadable(cadreis.PlacesFile(c), err)
			return nil, why, fix
		}
		// Every place, a project's or not: what --all keeps is the folders.
		for _, name := range slices.Sorted(maps.Keys(places)) {
			if why, fix = inside(name, cadreis.PlaceDir(places[name])); why != "" {
				return nil, why, fix
			}
		}
		for _, en := range reg.Entries() {
			if d := cadreis.PlaceDir(places[en.Name]); d != "" && !slices.Contains(projects, printable(en.Name)+" ("+display(d)+")") {
				projects = append(projects, printable(en.Name)+" ("+display(d)+")")
			}
		}
	}
	old, err := cadreis.OldPlaces()
	if err != nil {
		return nil, "Couldn't read cadrei 0.2.0's notes of where projects are, so this can't check that no project's folder is in what it deletes: " +
			strings.TrimSuffix(err.Error(), ".") + ". Nothing was deleted.", "Fix the file or its permissions, then run this again."
	}
	for _, file := range slices.Sorted(maps.Keys(old)) {
		for _, name := range slices.Sorted(maps.Keys(old[file])) {
			if why, fix = inside(name, cadreis.PlaceDir(old[file][name])); why != "" {
				return nil, why, fix
			}
		}
	}
	return projects, "", ""
}

// holds says what a crew holds, for the plan: its teams, members and
// projects.
func holds(c cadreis.Cadrei) string {
	teams, members := 0, 0
	entries, _ := os.ReadDir(filepath.Join(c.Path, "members"))
	for _, en := range entries {
		if en.IsDir() && !strings.HasPrefix(en.Name(), ".") {
			teams++
			members += len(session.Roles(c.Path, en.Name()))
		}
	}
	n := 0
	if reg, err := registry.Load(c.Registry()); err == nil {
		n = len(reg.Entries())
	}
	out := count(teams, "team") + ", " + count(members, "member") + ", " + count(n, "project") + " (their folders stay)"
	if _, err := os.Stat(memory.Dir(c.Path)); err == nil {
		out += ", and your orchestrator's memory (.memory/, " + count(memory.Count(c.Path), "note") + ")"
	}
	return out
}

// count is n with its noun: "1 team", "2 teams".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// others names what ~/.cadrei holds besides the crews and cadrei's own
// folders, for the plan: it all goes.
func others(root string, list []cadreis.Cadrei) string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, en := range entries {
		n := en.Name()
		if n == "config" || n == "framework" || slices.ContainsFunc(list, func(c cadreis.Cadrei) bool { return c.Name == n }) {
			continue
		}
		out = append(out, printable(n))
	}
	if len(out) == 0 {
		return ""
	}
	return ", and everything else in it (" + strings.Join(out, ", ") + ")"
}
