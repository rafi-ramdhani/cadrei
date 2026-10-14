//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
	"github.com/rafi-ramdhani/cadrei/internal/memory"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// installLine is how to install cadrei again with install.sh.
const installLine = "curl -fsSL --proto '=https' https://raw.githubusercontent.com/rafi-ramdhani/cadrei/main/install.sh | sh"

// crewBackup is what the archive checks about a crew's backup.
type crewBackup struct {
	crew        cadreis.Cadrei
	remote      string // its origin, as people read it
	url         string // its origin, as git takes it
	uncommitted bool
	untracked   []string // files git doesn't track yet: never committed for the user
	ahead       int
	err         string // why it couldn't be checked
	commit      bool   // commit and push it first
	push        bool   // push it first
}

// readable writes a remote as people read it, with no secret in it:
// github.com/you/repo.
func readable(url string) string {
	s := strings.TrimSuffix(project.Redact(url), ".git")
	for _, p := range []string{"https://", "http://", "ssh://git@", "ssh://"} {
		s = strings.TrimPrefix(s, p)
	}
	if rest, ok := strings.CutPrefix(s, "git@"); ok {
		s = strings.Replace(rest, ":", "/", 1)
	}
	return s
}

// checkBackup looks at a crew's backup: its origin, changes not committed,
// and commits not pushed.
func checkBackup(c cadreis.Cadrei) crewBackup {
	b := crewBackup{crew: c}
	git := func(args ...string) (string, error) {
		out, err := gitx.Reading(c.Path, args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// Changes first, so a crew with no remote still says it has them.
	if out, err := git("status", "--porcelain"); err == nil && out != "" {
		b.uncommitted = true
	}
	if out, err := git("ls-files", "--others", "--exclude-standard"); err == nil && out != "" {
		b.untracked = strings.Split(out, "\n")
	}
	// Where a push really goes: origin's push URL, which can differ from
	// the one it fetches from.
	url, err := git("remote", "get-url", "--push", "origin")
	if err != nil || url == "" {
		return b
	}
	b.url, b.remote = url, readable(url)
	out, err := git("rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		line, _, _ := strings.Cut(out, "\n")
		b.err = strings.TrimSuffix(strings.TrimPrefix(line, "fatal: "), ".")
		return b
	}
	b.ahead, _ = strconv.Atoi(out)
	return b
}

// runArchive is cadrei uninstall --archive: uninstall, and also move every
// crew and ~/.cadrei/config into one new archive, so a reinstall is a true
// first run and cadrei restore can bring it all back.
func runArchive(e *env, yes, dry, keep bool) int {
	if os.Getenv("CADREI_ORCHESTRATOR") != "" {
		return e.failWith("Run this in a terminal, not from your orchestrator. It moves the orchestrator's own folder.")
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	list, _ := cadreis.List()
	for _, c := range list {
		if l := orchestrator.ReadLock(orchestrator.LockPath(rt.BuildDir(c.Path))); l != nil && l.Mode == orchestrator.Terminal {
			return e.failWith("Your orchestrator is open in another terminal (open since " + l.Since.Format("15:04") + "). Close it first, then run this again.")
		}
	}
	td := planTeardown(rt, list)
	bin := td.bin
	var names []string
	for _, c := range list {
		names = append(names, c.Name)
	}
	now := time.Now()
	stamp := now.Format("2006-01-02-1504")
	archiveAt := cadreis.Tilde(filepath.Join(paths.Real(filepath.Dir(cadreis.ArchiveRoot())), ".cadrei-archive", stamp))
	// A run that stopped halfway is finished, into the same archive.
	pending := stoppedArchive()
	if pending != nil {
		archiveAt = cadreis.Tilde(pending.Dir)
		e.note("", markWarn, "An archive stopped halfway on "+pending.Manifest.ArchivedAt.Format("Jan 2 at 15:04")+". This finishes it.")
	}

	e.say("cadrei uninstall --archive will:")
	step := func(text string) { e.note("  ", markBullet, text) }
	if len(td.members) > 0 {
		step(fmt.Sprintf("stop %d team members: %s", len(td.members), strings.Join(td.members, ", ")))
	}
	if len(td.orchestrators) > 0 {
		step("stop your orchestrator in tmux")
	}
	td.linkSteps(step)
	if len(names) > 0 {
		step("move your crews (" + strings.Join(names, ", ") + ") and this machine's cadrei settings into " + unbroken(archiveAt))
	} else {
		step("move this machine's cadrei settings into " + unbroken(archiveAt))
	}
	step("remove " + unbroken(cadreis.Tilde(framework.Dir())) + ", then " + unbroken(cadreis.Tilde(paths.Real(cadreis.Root()))))
	prog := planProgram(keep)
	if line := prog.step(); line != "" {
		step(line)
	}
	if r, err := cadreis.Resolve(wdOrHome(), nil); err == nil && !cadreis.Inside(r.Path) {
		e.note("", markWarn, unbroken(display(r.Path))+" isn't in ~/.cadrei, so it stays where it is.")
	}

	var backups []crewBackup
	if len(list) > 0 {
		e.say("")
		e.say("%s", render(bold("Backups"), streamStyled(e.stdout)))
	}
	for _, c := range list {
		b := checkBackup(c)
		switch {
		case b.remote == "":
			e.note("  ", markWarn, c.Name+" has no backup. After this, it's only in the archive.")
		case b.err != "":
			e.note("  ", markWarn, c.Name+"'s backup couldn't be checked: "+b.err+".")
		case b.uncommitted:
			e.note("  ", markWarn, c.Name+" has changes that aren't committed yet.")
		case b.ahead > 0:
			e.note("  ", markWarn, c.Name+" has "+commits(b.ahead, "that isn't", "that aren't")+" on "+b.remote+" yet")
		default:
			e.note("  ", markDone, c.Name+" is backed up to "+b.remote+", and up to date")
		}
		backups = append(backups, b)
	}
	e.say("")
	e.show("", "It keeps your projects, "+rt.Title()+"'s trusted-folder marks, your "+rt.Title()+" chats, and your own "+rt.Title()+" settings.")
	e.say("")
	e.show("", "Bring it all back later with "+cmd("cadrei restore")+", or press o on cadrei's first run.")
	if r := td.unlistedRefusal(); r != "" {
		e.note("", markWarn, r+" So this stops before it changes anything.")
	}
	if dry {
		return 0
	}
	if td.unlistedRefusal() != "" {
		return td.refuseUnlisted(e)
	}

	// What to push first: asked now, done after the last yes.
	for i := range backups {
		b := &backups[i]
		if b.remote == "" || b.err != "" || (!b.uncommitted && b.ahead == 0) {
			continue
		}
		switch {
		case yes && b.uncommitted:
			e.note("", markWarn, b.crew.Name+"'s changes weren't committed (--yes doesn't push).")
		case yes:
			e.note("", markWarn, b.crew.Name+"'s "+commits(b.ahead, "wasn't", "weren't")+" pushed (--yes doesn't push).")
		case b.uncommitted && !hasIdentity(b.crew.Path):
			e.note("", markWarn, b.crew.Name+"'s changes stay in the archive: git has no name or email to commit them.")
		case b.uncommitted:
			b.commit = e.yes("Commit "+b.crew.Name+"'s changes and push to "+b.remote+" first?", true)
		default:
			b.push = e.yes(fmt.Sprintf("Push %s's %d commits to %s first?", b.crew.Name, b.ahead, b.remote), true)
		}
	}
	if !yes {
		if !e.interactive() {
			return e.failWith("Archiving needs a yes.", "Run it again with --yes.")
		}
		if !e.yes("Archive and uninstall?", false) {
			e.say("Nothing was changed.")
			return 1
		}
	}

	for _, b := range backups {
		if !b.commit && !b.push {
			continue
		}
		if b.commit {
			// Only what git already tracks: a file it doesn't track could
			// be anything, so it stays out of the backup, safe in the archive.
			if len(b.untracked) > 0 {
				e.note("", markWarn, b.crew.Name+" has files git doesn't track yet ("+andList(b.untracked)+"). They aren't committed or pushed, and they're still safe in the archive.")
			}
			if out, err := commitChanges(b.crew.Path); err != nil {
				e.note("", markWarn, "Couldn't push "+b.crew.Name+": "+firstLine(string(out))+". It's still safe in the archive.")
				continue
			}
		}
		out, err := pushWithin(b.crew.Path, pushLimit)
		if err != nil {
			reason := firstLine(string(out))
			if reason == "" {
				reason = err.Error()
			}
			e.note("", markWarn, "Couldn't push "+b.crew.Name+": "+strings.TrimSuffix(reason, ".")+". It's still safe in the archive.")
			continue
		}
		e.note("", markDone, "Pushed "+b.crew.Name+" to "+b.remote)
	}

	man := cadreis.Manifest{Version: cadreis.ManifestVersion, ArchivedAt: now, CadreiVersion: version, Home: paths.Home(), Steps: []string{}}
	man.Machine.EveryChatHook = "off"
	if td.hookOurs {
		man.Machine.EveryChatHook = "on"
	}
	man.Machine.SkillLinked = td.skillOurs
	// A linked ~/.cadrei (on another disk) comes back as the same link, so
	// every crew keeps its physical path, and with it its chats.
	if fi, err := os.Lstat(cadreis.Root()); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if to, err := os.Readlink(cadreis.Root()); err == nil {
			if !filepath.IsAbs(to) {
				to = filepath.Join(filepath.Dir(cadreis.Root()), to)
			}
			man.RootLink = filepath.Clean(to)
		}
	}
	def := cadreis.Default()
	for _, b := range backups {
		cb := cadreis.ArchivedCrew{Name: b.crew.Name, Path: b.crew.Path, Default: b.crew.Name == def}
		cb.Backup = cadreis.Backup{Remote: project.Redact(b.url), HasUncommitted: b.uncommitted}
		if again := checkBackup(b.crew); again.remote != "" && again.err == "" {
			cb.Backup.IsPushed, cb.Backup.HasUncommitted = again.ahead == 0, again.uncommitted
		}
		if b.crew.Name == def {
			man.Machine.Orchestrator, _ = cadreis.OpensIn(b.crew)
		}
		man.Cadreis = append(man.Cadreis, cb)
	}
	var a *cadreis.Archive
	if pending != nil {
		a = pending
		for _, cb := range man.Cadreis {
			if !a.HasCrew(cb.Name) {
				a.Manifest.Cadreis = append(a.Manifest.Cadreis, cb)
			}
		}
		if err := a.Save(); err != nil {
			return e.failWith("Couldn't finish the archive: " + err.Error() + ".")
		}
	} else if a, err = cadreis.NewArchive(man); err != nil {
		return e.failWith("Couldn't make the archive: " + err.Error() + ".")
	}

	// Nothing moves while a session still runs in a crew, or a hook or
	// link still points at the program the user is told to remove.
	again := "Nothing was moved. Run the same command again to finish."
	st := td.stop(e, discard{}, false)
	if len(st.Failed) > 0 {
		return e.failWith("Couldn't stop "+andList(st.Failed)+".", "Stop "+itOrThem(len(st.Failed))+" yourself, then run the same command again to finish. Nothing was moved.")
	}
	if len(td.sessions) > 0 || len(td.orchestrators) > 0 {
		var what []string
		if len(td.members) > 0 {
			what = append(what, fmt.Sprintf("%d team members", len(td.members)))
		}
		if len(td.orchestrators) > 0 {
			what = append(what, "your orchestrator")
		}
		e.note("", markDone, "Stopped "+strings.Join(what, " and "))
	}
	a.Step("stopped")
	if failed := td.unlink(e); len(failed) > 0 {
		for _, line := range failed {
			e.warn("", line)
		}
		return e.failWith("Cadrei's links and hooks aren't all removed yet.", again)
	}
	a.Step("unlinked")

	root := paths.Real(cadreis.Root())
	for _, c := range list {
		if err := cadreis.Move(c.Path, filepath.Join(a.Dir, "cadreis", c.Name)); err != nil {
			return e.failWith("Couldn't move "+c.Name+" into the archive: "+err.Error()+".", "Run the same command again to finish.")
		}
		a.Step("moved " + c.Name)
	}
	if _, err := os.Lstat(cadreis.ConfigDir()); err == nil {
		// A linked config is moved as the link (7.8.1); the manifest keeps
		// its target, so a restore knows the link is the one moved here.
		if to, err := os.Readlink(cadreis.ConfigDir()); err == nil {
			a.Manifest.ConfigLink = to
			if err := a.Save(); err != nil {
				return e.failWith("Couldn't note the config's link in the archive: "+err.Error()+".", "Run the same command again to finish.")
			}
		}
		if err := cadreis.Move(cadreis.ConfigDir(), filepath.Join(a.Dir, "config")); err != nil {
			return e.failWith("Couldn't move this machine's cadrei settings into the archive: "+err.Error()+".", "Run the same command again to finish.")
		}
	}
	a.Step("moved config")
	if moved := crewNames(*a, false); len(moved) > 0 {
		e.note("", markDone, "Moved "+andList(moved)+" into "+unbroken(cadreis.Tilde(a.Dir)))
	}
	os.RemoveAll(framework.Dir())
	if entries, err := os.ReadDir(root); err == nil {
		for _, en := range entries {
			cadreis.Move(filepath.Join(root, en.Name()), filepath.Join(a.Dir, "other", en.Name()))
		}
	}
	// What couldn't be moved stays, named, and so does a link to it.
	if err := os.Remove(root); err != nil && !errors.Is(err, fs.ErrNotExist) {
		var left []string
		if entries, err := os.ReadDir(root); err == nil {
			for _, en := range entries {
				left = append(left, en.Name())
			}
		}
		return e.failWith("Couldn't remove "+unbroken(cadreis.Tilde(root))+": it still holds "+andList(left)+". Nothing in it was deleted.",
			"Move those out yourself, then run the same command again to finish.")
	}
	if info, err := os.Lstat(cadreis.Root()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		os.Remove(cadreis.Root()) // a linked ~/.cadrei
	}
	e.note("", markDone, "Removed "+unbroken(cadreis.Tilde(root)))
	readme := "This is your cadrei setup from " + now.Format("Jan 2, 2006 at 15:04") + ", packed away by\ncadrei uninstall --archive. Nothing in it was deleted.\n\n" +
		"To bring it back, install cadrei and run cadrei restore, or press o on\ncadrei's first run. It goes back to the same places, so your projects and\nchats pick up where they left off.\n\n" +
		rt.Title() + " doesn't need anything in here. You can delete this folder once\nyou're sure you don't want it.\n"
	os.WriteFile(filepath.Join(a.Dir, "README.txt"), []byte(readme), 0o600)
	a.Step(cadreis.StepDone)

	removed, code := false, 0
	if prog.remove {
		removed, code = e.removeProgram()
	}
	e.say("")
	e.say("Your setup is archived. To start over like a new user:")
	switch installKind() {
	case "homebrew":
		if removed {
			e.say("  %s", render(cmd("brew install rafi-ramdhani/cadrei/cadrei"), streamStyled(e.stdout)))
		} else {
			e.say("  %s", render(cmd(brewRemove+" && brew install rafi-ramdhani/cadrei/cadrei"), streamStyled(e.stdout)))
		}
	case "dev":
		e.say("  %s", finalStep(bin))
	default:
		e.say("  %s", render(cmd(finalStep(bin)+" && "+installLine), streamStyled(e.stdout)))
	}
	e.say("  %s", render(cmd("cadrei"), streamStyled(e.stdout)))
	e.say("To bring it back: %s", render(cmd("cadrei restore"), streamStyled(e.stdout)))
	st.Last(e.stdout)
	return code
}

// stoppedArchive is the newest archive whose run stopped halfway, or nil.
func stoppedArchive() *cadreis.Archive {
	for _, a := range cadreis.Archives() {
		if !a.Finished() && a.Manifest.RestoredAt == nil {
			return &a
		}
	}
	return nil
}

// pushLimit is how long a push before archiving may take.
const pushLimit = 60 * time.Second

// commitChanges commits the changes to files git already tracks in crew,
// and the orchestrator's memory, new notes too, with none of the crew's
// own hooks or fsmonitor: they're the user's.
func commitChanges(crew string) ([]byte, error) {
	if _, err := os.Lstat(memory.Dir(crew)); err == nil {
		if out, err := gitx.Reading(crew, "add", "-A", "--", memory.Folder).CombinedOutput(); err != nil {
			return out, err
		}
	}
	if out, err := gitx.Reading(crew, "add", "-u").CombinedOutput(); err != nil {
		return out, err
	}
	// Nothing to commit is not an error.
	gitx.Reading(crew, "commit", "-qm", "Changes before archiving").Run()
	return nil, nil
}

// pushWithin pushes repo, through its pre-push check, with no prompts. At
// the limit the push and everything it started (the check, ssh) are
// stopped, so a hung push can't hold the archive.
func pushWithin(repo string, limit time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	c := gitx.Pushing(ctx, repo, "push", "-q", "origin", "HEAD")
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 2 * time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("it took longer than %d seconds", int(limit.Seconds()))
	}
	return out, err
}

// discard is a writer that keeps nothing.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimPrefix(strings.TrimSpace(line), "fatal: ")
}

// unrestored are the archives that haven't been brought back.
func unrestored() []cadreis.Archive {
	var out []cadreis.Archive
	for _, a := range cadreis.Archives() {
		if a.Manifest.RestoredAt == nil && a.Finished() {
			out = append(out, a)
		}
	}
	return out
}

// crewNames lists an archive's crews, its default marked when asked.
func crewNames(a cadreis.Archive, markDefault bool) []string {
	var out []string
	for _, c := range a.Manifest.Cadreis {
		if markDefault && c.Default {
			out = append(out, c.Name+" (default)")
		} else {
			out = append(out, c.Name)
		}
	}
	return out
}

// runRestore is cadrei restore [<archive>]: bring an archived setup back to
// the same paths, then open the orchestrator.
func runRestore(e *env) int {
	if len(e.args) > 1 {
		return e.failWith("Use " + cmd("cadrei restore") + ", or " + cmd("cadrei restore <archive folder>") + ".")
	}
	if e.memberAnyway("restore an archived setup") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	var a cadreis.Archive
	if len(e.args) == 1 {
		if a, err = cadreis.ReadArchive(e.args[0]); errors.Is(err, fs.ErrNotExist) {
			return e.failWith("There's no archived setup in " + unbroken(display(e.args[0])) + ".")
		} else if err != nil {
			return e.failWith(strings.TrimSuffix(err.Error(), ".")+".", "Nothing was changed.")
		}
	} else {
		picked, ok := e.pickArchive()
		if !ok {
			return 1
		}
		a = picked
	}
	if a.Manifest.RestoredAt != nil {
		return e.failWith("That setup was already brought back on " + a.Manifest.RestoredAt.Format("Jan 2") + ". Its folder only holds its notes now.")
	}
	if !a.Finished() {
		return e.stoppedHalfway(a)
	}
	if !e.restoreArchive(rt, a) {
		return 1
	}
	e.args = nil
	return runPlain(e)
}

// unreadableNote names the archives cadrei can't read or restore from.
func unreadableNote(bad []string) string {
	return unbroken(cadreis.Tilde(cadreis.ArchiveRoot())) + " holds " + andList(bad) + ", which cadrei can't read or restore from (its manifest.json is missing or broken, or a folder in it is a link). The crews in it are still there, in its cadreis folder."
}

// stoppedHalfway says an archive's uninstall stopped before it finished,
// and how to finish it first.
func (e *env) stoppedHalfway(a cadreis.Archive) int {
	return e.failWith("The archive from "+a.Manifest.ArchivedAt.Format("Jan 2, 15:04")+" stopped halfway, so it can't be brought back yet.",
		"Run "+cmd("cadrei uninstall --archive")+" to finish it, then "+cmd("cadrei restore")+".")
}

// pickArchive picks the archive to restore: the only one, or the user's
// choice from the list.
func (e *env) pickArchive() (cadreis.Archive, bool) {
	all := cadreis.Archives()
	open := unrestored()
	if bad := cadreis.Unreadable(); len(bad) > 0 {
		e.note("", markWarn, unreadableNote(bad))
		if len(all) == 0 {
			return cadreis.Archive{}, false
		}
	}
	switch {
	case len(open) == 0 && len(all) == 0:
		e.failWith("There's no archived setup in "+unbroken(cadreis.Tilde(cadreis.ArchiveRoot()))+".",
			"To restore from a GitHub backup, run "+cmd("cadrei")+" on a fresh setup and press r.")
		return cadreis.Archive{}, false
	case len(open) == 0 && all[0].Manifest.RestoredAt == nil:
		// The newest stopped halfway: finishing it comes first.
		e.stoppedHalfway(all[0])
		return cadreis.Archive{}, false
	case len(open) == 0:
		fix := []string{}
		// An older one that stopped halfway is the one to finish.
		for _, a := range all {
			if !a.Finished() && a.Manifest.RestoredAt == nil {
				fix = append(fix, "The archive from "+a.Manifest.ArchivedAt.Format("Jan 2, 15:04")+" stopped halfway. Run "+cmd("cadrei uninstall --archive")+" to finish it, then "+cmd("cadrei restore")+".")
				break
			}
		}
		e.failWith("That setup was already brought back on "+all[0].Manifest.RestoredAt.Format("Jan 2")+". Its folder only holds its notes now.", fix...)
		return cadreis.Archive{}, false
	case len(open) == 1 && len(all) == 1:
		return open[0], true
	}
	e.say("Archived setups in %s", cadreis.Tilde(cadreis.ArchiveRoot()))
	n := 0
	var choices []cadreis.Archive
	for _, a := range all {
		crews := strings.Join(crewNames(a, true), ", ")
		when := a.Manifest.ArchivedAt.Format("Jan 2, 15:04")
		if a.Manifest.RestoredAt != nil || !a.Finished() {
			note := "not finished"
			if a.Manifest.RestoredAt != nil {
				note = "restored on " + a.Manifest.RestoredAt.Format("Jan 2")
			}
			e.say("  -  %-15s  %-30s %s", when, crews, note)
			continue
		}
		n++
		choices = append(choices, a)
		e.say("  %d  %-15s  %s", n, when, crews)
	}
	for tries := 0; tries < 3; tries++ {
		answer, ok := e.answer("Which one? Type its number: ")
		if !ok {
			break
		}
		if i, err := strconv.Atoi(answer); err == nil && i >= 1 && i <= len(choices) {
			return choices[i-1], true
		}
	}
	e.say("Nothing was changed.")
	return cadreis.Archive{}, false
}

// restoreArchive shows an archive's plan and, on Enter, puts it back. It
// never merges into a setup that's here: every path comes back exact.
func (e *env) restoreArchive(rt runtime.Runtime, a cadreis.Archive) bool {
	if list, _ := cadreis.List(); len(list) > 0 && !restoring(a, list) {
		var names []string
		for _, c := range list {
			names = append(names, c.Name)
		}
		e.failWith("You already have a crew set up here ("+strings.Join(names, ", ")+").",
			"Archive it first, so nothing is lost: "+cmd("cadrei uninstall --archive"),
			"Then run "+cmd("cadrei restore")+" and pick the setup you want back.")
		return false
	}
	// 7.8.3: ~/.cadrei must be empty or missing, cadrei's own files aside,
	// so the restore never moves anything onto what's there.
	if extra := inTheWay(a); len(extra) > 0 {
		e.failWith(unbroken(cadreis.Tilde(cadreis.Root()))+" already holds "+andList(extra)+", which isn't part of this setup.",
			"Move "+itOrThem(len(extra))+" out of "+unbroken(cadreis.Tilde(cadreis.Root()))+" first, then run "+cmd("cadrei restore")+" again. Nothing was changed.")
		return false
	} // The same check a GitHub restore makes: a crew with links where
	// cadrei reads files or runs sessions would point them outside it.
	for _, c := range a.Manifest.Cadreis {
		if links := crewLinks(rt, filepath.Join(a.Dir, "cadreis", c.Name)); len(links) > 0 {
			e.failWith("The archive's crew "+c.Name+" has links where cadrei reads or runs files ("+strings.Join(links, ", ")+"), so nothing was restored.",
				"Put the real files there, then run "+cmd("cadrei restore")+" again.")
			return false
		}
	}
	// What else comes back into ~/.cadrei gets the same care: nothing in
	// other/ may take the place of cadrei's own folders or come back as a
	// crew that skipped the checks above.
	if bad := strayOthers(a); len(bad) > 0 {
		other := unbroken(cadreis.Tilde(filepath.Join(a.Dir, "other")))
		e.failWith("The archive's other folder holds "+andList(bad)+", which cadrei won't put back in "+unbroken(cadreis.Tilde(cadreis.Root()))+", so nothing was restored.",
			"Take "+itOrThem(len(bad))+" out of "+other+" if you trust "+itOrThem(len(bad))+", then run "+cmd("cadrei restore")+" again.")
		return false
	}

	m := a.Manifest
	names := crewNames(a, false)
	def := ""
	for _, c := range m.Cadreis {
		if c.Default {
			def = c.Name
		}
	}
	e.say("")
	e.screen(bold("Bring back your setup from " + m.ArchivedAt.Format("Jan 2, 15:04")))
	e.say("")
	e.screen(bold("Here's the plan"))
	// Where the crews go: ~/.cadrei, or the place it linked to before,
	// when the archive's record of it can be trusted.
	link, why := rootLink(a)
	dest := unbroken(cadreis.Tilde(paths.Real(cadreis.Root())))
	if _, err := os.Lstat(cadreis.Root()); errors.Is(err, fs.ErrNotExist) && link != "" {
		dest = unbroken(cadreis.Tilde(link)) + ", linked from " + unbroken(cadreis.Tilde(cadreis.Root())) + " as before"
	}
	line := "Put " + andList(names) + " back in " + dest
	if def != "" {
		line += ", with " + def + " as your default"
	}
	e.bullet(line)
	e.bullet("Bring back this machine's cadrei settings")
	for _, c := range m.Cadreis {
		if set := modelSettings(filepath.Join(a.Dir, "cadreis", c.Name)); len(set) > 0 {
			e.bullet("Keep " + c.Name + "'s model and effort settings: " + strings.Join(set, ", "))
		}
	}
	if others := a.Others(); len(others) > 0 {
		e.bullet("Put back the other things that were in " + unbroken(cadreis.Tilde(paths.Real(cadreis.Root()))) + ": " + strings.Join(others, ", "))
	}
	e.bullet("Link cadrei's instructions into " + rt.Title() + " (" + unbroken(cadreis.Tilde(rt.Instructions().Path())) + ")")
	if m.Machine.EveryChatHook == "on" {
		e.bullet("Turn the every-chat hook back on (it was on before)")
	}
	e.loginItem()
	if m.Machine.Orchestrator == cadreis.OpensInTmux {
		e.bullet("Open your orchestrator in tmux, as before")
	} else {
		e.bullet("Open your orchestrator in this terminal, as before")
	}
	if why != "" {
		e.say("")
		e.note("  ", markWarn, why)
	}
	// A crew's notes on this machine name where its projects are: a place
	// a project can't be (from an archive made elsewhere) is left out.
	// 0.2.0 kept them in the config's places folder, which MoveIn merges
	// in later: those are checked too.
	badPlaces, badOld := map[string][]string{}, map[string][]string{}
	for _, c := range m.Cadreis {
		archived := cadreis.Cadrei{Name: c.Name, Path: filepath.Join(a.Dir, "cadreis", c.Name)}
		var named []string
		for _, name := range slices.Sorted(maps.Keys(cadreis.Places(archived))) {
			if d := cadreis.Place(archived, name); d != "" {
				if why := placeRefusal(d); why != "" {
					badPlaces[c.Name] = append(badPlaces[c.Name], name)
					named = append(named, name+" ("+unbroken(cadreis.Tilde(d))+": "+why+")")
				}
			}
		}
		old := oldPlaces(filepath.Join(a.Dir, "config", "places", c.Name+".json"))
		for _, name := range slices.Sorted(maps.Keys(old)) {
			if why := placeRefusal(old[name]); why != "" {
				badOld[c.Name] = append(badOld[c.Name], name)
				named = append(named, name+" ("+unbroken(cadreis.Tilde(old[name]))+": "+why+")")
			}
		}
		if len(named) > 0 {
			e.say("")
			e.note("  ", markWarn, c.Name+"'s notes put projects where no project can be: "+strings.Join(named, ", ")+". Those places are left out. Link the projects again with "+cmd("cadrei project link <name> <folder>")+".")
		}
	}
	// An archive from elsewhere may come with git settings that run
	// programs: git runs them whenever it's used in the crew, cadrei's
	// own commits included, so they're named before the user agrees.
	for _, c := range m.Cadreis {
		if !exists(filepath.Join(a.Dir, "cadreis", c.Name, ".git")) {
			e.say("")
			e.note("  ", markWarn, c.Name+" has no git repo of its own, so cadrei won't commit its changes, and it has no backup.")
		}
	}
	if !madeHere(a) {
		for _, c := range m.Cadreis {
			if run := gitRunners(filepath.Join(a.Dir, "cadreis", c.Name)); len(run) > 0 {
				e.say("")
				e.note("  ", markWarn, c.Name+"'s git settings could run programs ("+strings.Join(run, ", ")+"). Git would run them whenever it's used in "+c.Name+", by cadrei too. Look at them in "+unbroken(cadreis.Tilde(filepath.Join(a.Dir, "cadreis", c.Name, ".git")))+" before you bring it back.")
			}
		}
	}
	if m.Home != "" && paths.Real(m.Home) != paths.Real(paths.Home()) {
		e.say("")
		e.note("  ", markWarn, "This setup comes from another home folder ("+unbroken(m.Home)+"). Projects will show as not on this machine until you link or clone them, and chats start fresh. For a second computer, a GitHub backup (r) is the better way.")
	}
	e.say("")
	key, _ := e.readKey(e.wrapScreen("Press Enter to bring it back, or q to quit."), "q")
	if key != keyEnter {
		e.say("")
		e.screen("Nothing was changed.")
		return false
	}
	if !e.prepareRoot(link) {
		return false
	}
	a.Step(stepRestoring)
	root := paths.Real(filepath.Dir(cadreis.ConfigDir()))
	e.say("")
	// ReadArchive checked the names and that the archive's folders aren't
	// links; this checks again, with every link resolved, so every move
	// stays inside the archive and ~/.cadrei whatever a name holds.
	for _, d := range cadreis.ArchiveFolders {
		if !realFolder(a, d) && !(d == "config" && a.LinkedConfig()) {
			e.failWith("The archive's "+d+" folder is a link or a file, so it can't be put back safely.", "Nothing more was changed.")
			return false
		}
	}
	for _, c := range m.Cadreis {
		src, dst := filepath.Join(a.Dir, "cadreis", c.Name), filepath.Join(root, c.Name)
		if !paths.Within(src, filepath.Join(a.Dir, "cadreis")) || !paths.Within(dst, root) || src == filepath.Join(a.Dir, "cadreis") || dst == root ||
			paths.Real(filepath.Dir(src)) != filepath.Join(paths.Real(a.Dir), "cadreis") {
			e.failWith("The archive names a crew (" + c.Name + ") that can't be put back safely.")
			return false
		}
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if err := cadreis.Move(src, dst); err != nil {
			e.failWith("Couldn't put " + c.Name + " back: " + err.Error() + ".")
			return false
		}
	}
	for crew, bad := range badPlaces {
		for _, name := range bad {
			cadreis.SetPlace(cadreis.Cadrei{Name: crew, Path: filepath.Join(root, crew)}, name, "")
		}
	}
	var marked []string
	for _, n := range names {
		marked = append(marked, accent(n))
	}
	e.work(markDone, "Put "+andList(marked)+" back in "+unbroken(cadreis.Tilde(root)))
	if _, err := os.Lstat(filepath.Join(a.Dir, "config")); err == nil {
		// A re-run that got to this step finishes its own move (Move never
		// drops the only whole copy). A config from anything else, also on
		// a re-run that stopped before this step, is kept aside, never
		// deleted.
		if _, err := os.Lstat(cadreis.ConfigDir()); err == nil && !a.Has(stepConfig) {
			aside := cadreis.ConfigDir() + ".before-restore-" + time.Now().Format("2006-01-02-150405")
			if err := os.Rename(cadreis.ConfigDir(), aside); err != nil {
				e.failWith("Couldn't move this machine's current cadrei settings aside: " + err.Error() + ".")
				return false
			}
			e.note("  ", markWarn, "This machine's cadrei settings from before are kept in "+unbroken(cadreis.Tilde(aside)))
		}
		a.Step(stepConfig)
		if err := cadreis.Move(filepath.Join(a.Dir, "config"), cadreis.ConfigDir()); err != nil {
			e.failWith("Couldn't bring back this machine's cadrei settings: " + err.Error() + ".")
			return false
		}
	}
	// What can't come back (the same name is there already) stays in the
	// archive, named, and the archive isn't marked as brought back.
	var stayed []string
	if entries, err := os.ReadDir(filepath.Join(a.Dir, "other")); err == nil {
		for _, en := range entries {
			if err := cadreis.Move(filepath.Join(a.Dir, "other", en.Name()), filepath.Join(root, en.Name())); err != nil {
				stayed = append(stayed, en.Name())
			}
		}
	}
	if len(stayed) > 0 {
		e.note("  ", markWarn, andList(stayed)+" stayed in "+unbroken(cadreis.Tilde(filepath.Join(a.Dir, "other")))+": "+unbroken(cadreis.Tilde(root))+" already has something by that name. Nothing was deleted.")
	}
	// 0.2.0 places for a crew the archive doesn't hold were never checked:
	// MoveIn would take them into any crew made later by that name.
	if entries, err := os.ReadDir(filepath.Join(cadreis.ConfigDir(), "places")); err == nil {
		for _, en := range entries {
			if crew, ok := strings.CutSuffix(en.Name(), ".json"); ok && !slices.ContainsFunc(m.Cadreis, func(c cadreis.ArchivedCrew) bool { return c.Name == crew }) {
				os.Remove(filepath.Join(cadreis.ConfigDir(), "places", en.Name()))
			}
		}
	}
	for crew, bad := range badOld {
		file := filepath.Join(cadreis.ConfigDir(), "places", crew+".json")
		if raw, err := os.ReadFile(file); err == nil {
			places := map[string]string{}
			if json.Unmarshal(raw, &places) == nil {
				for _, name := range bad {
					delete(places, name)
				}
				if out, err := json.MarshalIndent(places, "", "  "); err == nil {
					fsx.WriteFile(file, append(out, '\n'), 0o600)
				}
			}
		}
	}
	e.work(markDone, "Brought back this machine's cadrei settings")
	framework.Sync(cadrei.Assets, version)
	list, _ := cadreis.List()
	for _, c := range list {
		e.guard(c)
	}
	// A crew that came back but isn't one cadrei sees (a link, say) means
	// the archive isn't marked as brought back.
	var unseen []string
	for _, c := range m.Cadreis {
		if _, ok := cadreis.Find(c.Name); !ok {
			unseen = append(unseen, c.Name)
		}
	}
	if len(unseen) > 0 {
		e.note("  ", markWarn, andList(unseen)+" came back to "+unbroken(cadreis.Tilde(root))+", but cadrei doesn't see "+itOrThem(len(unseen))+" as a crew (a link, or a folder without a crew's files). The archive isn't marked as brought back.")
		stayed = append(stayed, unseen...)
	}
	if err := rt.Instructions().Link(framework.SkillDir()); err == nil {
		e.work(markDone, "Linked cadrei's instructions into "+rt.Title())
	}
	if m.Machine.EveryChatHook == "on" && e.hookPlaced(framework.Binary()) == nil {
		if _, err := rt.Hooks().Set(framework.Binary()); err == nil {
			e.work(markDone, "Turned the every-chat hook back on")
		}
	}
	now := time.Now()
	// The orchestrator that opens: the default crew's, on its own setting.
	// A crew that didn't come back means only the rest of it did.
	last, tmux := "Your setup is back.", false
	if len(unseen) > 0 {
		last = "The rest of your setup is back."
	}
	if d, ok := cadreis.Find(def); ok {
		last += " Opening your orchestrator."
		where, _ := cadreis.OpensIn(d)
		tmux = where == cadreis.OpensInTmux
		if resumes(rt, d) {
			last += " It picks up your last chat."
		}
	}
	if len(stayed) > 0 {
		return e.finish(tmux, last, "", false)
	}
	a.Manifest.RestoredAt = &now
	a.Save()
	for _, d := range []string{"cadreis", "config", "other"} {
		os.Remove(filepath.Join(a.Dir, d))
	}
	// The note goes only into the archive's own README: one that's a link
	// (in an archive from elsewhere) could lead to any file.
	readme := filepath.Join(a.Dir, "README.txt")
	if st, err := os.Lstat(readme); err == nil && st.Mode().IsRegular() {
		if raw, err := os.ReadFile(readme); err == nil {
			fsx.WriteFile(readme, append(raw, []byte("\nBrought back on "+now.Format("Jan 2, 2006 at 15:04")+".\n")...), 0o600)
		}
	}
	return e.finish(tmux, last, "", false)
}

// rootLink is where ~/.cadrei linked to when the archive was made, when
// that can be trusted: an absolute, clean path, from an archive in
// ~/.cadrei-archive (made on this machine, not a folder handed to
// restore), at a place that's missing or empty. Otherwise it's "", with
// why when the archive named one.
func rootLink(a cadreis.Archive) (link, why string) {
	v := a.Manifest.RootLink
	if v == "" {
		return "", ""
	}
	ignored := "This archive says your crews were on " + unbroken(v) + ". They come back into " + unbroken(cadreis.Tilde(cadreis.Root())) + " instead: cadrei goes back to such a place only for an archive in " + unbroken(cadreis.Tilde(cadreis.ArchiveRoot())) + ", and only when the place is empty."
	if !filepath.IsAbs(v) || filepath.Clean(v) != v || !madeHere(a) || appPlace(v) {
		return "", ignored
	}
	if st, err := os.Lstat(v); err == nil {
		entries, _ := os.ReadDir(v)
		if !st.IsDir() || len(entries) > 0 {
			return "", ignored
		}
	}
	return v, ""
}

// oldPlaces reads a 0.2.0 places file (project name to folder, ~/...
// under the home folder), with ~ expanded as cadreis.Place does (a bare ~
// too); entries that aren't absolute are left out.
func oldPlaces(file string) map[string]string {
	raw, err := os.ReadFile(file)
	places := map[string]string{}
	if err != nil || json.Unmarshal(raw, &places) != nil {
		return nil
	}
	out := map[string]string{}
	for name, p := range places {
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = paths.Home() + p[1:]
		}
		if filepath.IsAbs(p) {
			out[name] = filepath.Clean(p)
		}
	}
	return out
}

// madeHere reports whether this machine's uninstall --archive made an
// archive (cadreis.Archive.MadeHere), rather than it being a folder handed
// to restore, or copied into ~/.cadrei-archive.
func madeHere(a cadreis.Archive) bool { return a.MadeHere() }

// gitRunners lists what a crew's own git settings would run once it's
// back: the config keys and hooks gitx.Runners finds, but cadrei's own
// pre-push hook, which cadrei writes again for this machine.
func gitRunners(crew string) []string {
	var out []string
	for _, r := range gitx.Runners(crew) {
		// cadrei's own check, for this program: one in its form for any
		// other program could run anything.
		if r == "hooks/pre-push" && backup.OwnHook(filepath.Join(crew, ".git", "hooks", "pre-push"), framework.Binary()) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// appPlace reports whether p is, with every link on the way resolved, the
// home folder itself, or inside its Library or one of its dot-folders:
// places programs read from (launch agents, shell and tool settings), never
// a home for crews.
func appPlace(p string) bool {
	home := paths.Real(paths.Home())
	for _, q := range []string{p, paths.Real(p)} {
		rel, err := filepath.Rel(home, q)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		first, _, _ := strings.Cut(rel, "/")
		if rel == "." || strings.EqualFold(first, "Library") || strings.HasPrefix(first, ".") {
			return true
		}
	}
	return false
}

// prepareRoot makes ~/.cadrei ready for a restore: the link to the place
// the crews were on (link), or the folder itself. It never makes more than
// the last folder of a place a link points at, so a disk that isn't
// connected is never stood in for by a new folder at its mount point.
func (e *env) prepareRoot(link string) bool {
	root := cadreis.Root()
	gone := func(place string) bool {
		e.failWith("Your crews were on "+unbroken(place)+", and that place isn't there now.", "Connect the disk it's on, then run "+cmd("cadrei restore")+" again.")
		return false
	}
	st, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist) && link != "":
		if _, err := os.Stat(filepath.Dir(link)); err != nil {
			return gone(link)
		}
		if err := os.Mkdir(link, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			e.failWith("Couldn't make " + unbroken(link) + ": " + err.Error() + ".")
			return false
		}
		if err := os.Symlink(link, root); err != nil {
			e.failWith("Couldn't link " + unbroken(cadreis.Tilde(root)) + " to " + unbroken(link) + ": " + err.Error() + ".")
			return false
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(root, 0o755); err != nil {
			e.failWith("Couldn't make " + unbroken(cadreis.Tilde(root)) + ": " + err.Error() + ".")
			return false
		}
	case err == nil && st.Mode()&fs.ModeSymlink != 0:
		if _, err := os.Stat(root); err == nil {
			return true
		}
		// A link left behind, to a place that's gone.
		target, err := os.Readlink(root)
		if err != nil {
			return gone(cadreis.Tilde(root))
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(root), target)
		}
		if _, err := os.Stat(filepath.Dir(target)); err != nil {
			return gone(target)
		}
		if err := os.Mkdir(target, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			e.failWith("Couldn't make " + unbroken(target) + ": " + err.Error() + ".")
			return false
		}
	}
	return true
}

// inTheWay lists what's in ~/.cadrei that a restore of a would move things
// onto, or would leave mixed in: anything but cadrei's own files (the
// framework, the config it sets aside, Finder's .DS_Store). A stopped
// restore of a, run again, finishes what it already put back there.
func inTheWay(a cadreis.Archive) []string {
	if a.Has(stepRestoring) {
		return nil
	}
	entries, _ := os.ReadDir(cadreis.Root())
	ours := map[string]bool{"framework": true, "config": true, ".DS_Store": true}
	var out []string
	for _, en := range entries {
		if !ours[en.Name()] && !strings.HasPrefix(en.Name(), "config.before-restore-") {
			out = append(out, en.Name())
		}
	}
	return out
}

// crewLinks lists the links in a crew folder where cadrei would follow
// them, committed or not. A crew that is itself a link has none here (it
// comes back as the link, and isn't seen as a crew).
func crewLinks(rt runtime.Runtime, crew string) []string {
	if st, err := os.Lstat(crew); err != nil || !st.IsDir() {
		return nil
	}
	rtDir, _ := filepath.Rel(crew, filepath.Dir(rt.BuildDir(crew)))
	links := cadreis.LinksIn(crew, rtDir)
	if committed, err := cadreis.CommittedLinks(crew, rtDir); err == nil {
		links = append(links, committed...)
	}
	sort.Strings(links)
	return slices.Compact(links)
}

// strayOthers lists what in an archive's other folder a restore won't
// put back, each with why. Uninstall never puts these there: config and
// framework are cadrei's own names (config has its own folder, framework
// is made again), a crew goes to cadreis, links only come from this
// machine's ~/.cadrei, in an archive made here, and ~/.cadrei is no repo.
func strayOthers(a cadreis.Archive) []string {
	entries, _ := os.ReadDir(filepath.Join(a.Dir, "other"))
	var out []string
	for _, en := range entries {
		name, p := en.Name(), filepath.Join(a.Dir, "other", en.Name())
		st, err := os.Lstat(p)
		switch {
		case err != nil:
			continue
		case strings.EqualFold(name, "config") || strings.EqualFold(name, "framework"):
			out = append(out, name+" (cadrei's own name)")
		case strings.EqualFold(name, ".git"):
			// A repo above the crews: git would take its settings for any
			// crew without a repo of its own.
			out = append(out, name+" (a git folder)")
		case st.Mode()&fs.ModeSymlink != 0 && !madeHere(a):
			out = append(out, name+" (a link)")
		case st.IsDir() && cadreis.CheckName(name) == nil && exists(filepath.Join(p, "members")):
			// Any members entry: a link that leads nowhere in the archive
			// may lead somewhere from ~/.cadrei.
			out = append(out, name+" (a crew)")
		}
	}
	return out
}

// exists reports whether there's anything at p, a link to nothing too.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// itOrThem is "it" for one, else "them".
func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// realFolder reports whether an archive's sub folder is a real folder in
// it, every link resolved, or missing.
func realFolder(a cadreis.Archive, sub string) bool {
	p := filepath.Join(a.Dir, sub)
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && st.IsDir() && paths.Real(p) == filepath.Join(paths.Real(a.Dir), sub)
}

// The manifest steps a restore records: when it starts, and when it starts
// moving the config back.
const (
	stepRestoring = "restoring"
	stepConfig    = "restoring config"
)

// restoring reports whether the crews here are the ones a restore of a
// moved back before it stopped, so running it again finishes it.
func restoring(a cadreis.Archive, here []cadreis.Cadrei) bool {
	if _, err := os.Lstat(filepath.Join(a.Dir, "cadreis")); err != nil || !a.Has(stepRestoring) {
		return false
	}
	for _, c := range here {
		if !a.HasCrew(c.Name) {
			return false
		}
	}
	return true
}

// runUninstallArchive is cadrei uninstall --archive [--dry-run | --yes].
func runUninstallArchive(e *env) int {
	yes, dry, keep := false, false, false
	for _, a := range e.args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--dry-run":
			dry = true
		case keepFlag:
			keep = true
		case "--all":
			return allWithArchive(e)
		default:
			return e.failWith("Use " + cmd("cadrei uninstall --archive") + " with --dry-run or --yes, or neither, and " + cmd(keepFlag) + " if you like.")
		}
	}
	if yes && dry {
		return e.failWith("Pick one: --dry-run or --yes.")
	}
	if e.memberAnyway("uninstall cadrei") {
		return 1
	}
	return runArchive(e, yes, dry, keep)
}

// commits counts commits with the verb that goes with them: "1 commit
// that isn't", "2 commits that aren't".
func commits(n int, one, many string) string {
	if n == 1 {
		return "1 commit " + one
	}
	return strconv.Itoa(n) + " commits " + many
}
