//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	rtpkg "github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The first run: a header, a check of the tools, one plan and one key.
// Enter sets everything up; nothing is made before that key.

// firstCrew is the name of the first crew on a machine.
const firstCrew = "main"

// screen prints a screen line: marked text, wrapped at the width, with a
// 2-space margin.
func (e *env) screen(text string) { e.show("  ", text) }

// bullet prints a list item of a screen.
func (e *env) bullet(text string) { e.note("  ", markBullet, text) }

// opener is the top of every first-run screen: the compact header (7.12.1)
// with the version, the tagline and the folder the user is in.
func (e *env) opener() {
	wd, err := paths.Getwd()
	if err != nil {
		wd = paths.Home()
	}
	e.say("")
	e.header(productLine(), "A team of "+e.title()+" helpers you lead from one chat.", display(wd))
	e.say("")
}

// title is the runtime's name, for screens.
func (e *env) title() string {
	if rt, err := rtpkg.Get(runtimeName()); err == nil {
		return rt.Title()
	}
	return "the runtime"
}

// probeWithin runs a version command with the same time limit as the
// runtime's probes, and returns its first line.
func probeWithin(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, err
}

// installHint is how to install a tool here: brew when it's there, else
// the package manager.
func installHint(formula string) string {
	switch {
	case hasBrew():
		return "Install it with " + cmd("brew install "+formula)
	case runtime.GOOS == "darwin":
		return "Install Homebrew from https://brew.sh, then run " + cmd("brew install "+formula)
	}
	return "Install it with your package manager (for example " + cmd("sudo apt install "+formula) + ")"
}

// upgradeHint is how to get a newer tool here, as installHint.
func upgradeHint(formula string) string {
	switch {
	case hasBrew():
		return "Update it with " + cmd("brew upgrade "+formula)
	case runtime.GOOS == "darwin":
		return "Install Homebrew from https://brew.sh, then run " + cmd("brew install "+formula)
	}
	return "Update it with your package manager"
}

// hasBrew reports whether Homebrew is on PATH.
func hasBrew() bool {
	_, err := exec.LookPath("brew")
	return err == nil
}

// checkRow is one tool check's result, as a 3.5 row.
type checkRow struct {
	mark mark
	text string
	fix  []string
}

// checks runs the tool checks and reports whether setup can go on. When
// all pass they're one line (3.1); a warning or a failure shows every row
// under "Checking your setup" (3.5). In a terminal, a line says the checks
// are running and is replaced by the result.
func (e *env) checks(rt rtpkg.Runtime) bool {
	live := e.terminal() && streamStyled(e.stdout)
	if live {
		fmt.Fprint(e.stdout, "  Checking your setup...")
	}
	var rows []checkRow
	ok, quiet := true, true
	add := func(m mark, text string, fix ...string) {
		rows = append(rows, checkRow{m, text, fix})
		if m == markFail {
			ok = false
		}
		if m != markDone {
			quiet = false
		}
	}
	title := rt.Title()
	p := rt.Probe()
	switch {
	case !p.Installed:
		help := rt.Help()
		hint := "Install it from " + help.URL
		if hasBrew() && help.Install != "" {
			hint = "Install it with " + cmd(help.Install) + ", or see " + help.URL
		}
		add(markFail, p.Says(title), hint)
	case !p.Runs:
		hint := "Reinstall it, see " + rt.Help().URL
		if help := rt.Help(); help.Reinstall != "" {
			hint = "Reinstall it with " + cmd(help.Reinstall)
		}
		add(markFail, p.Says(title), hint)
	case p.Login == rtpkg.LoggedIn:
		add(markDone, p.Says(title))
	default:
		add(markWarn, p.Says(title))
	}
	for _, c := range []toolCheck{gitCheck(), tmuxCheck()} {
		var fix []string
		if c.fix != "" {
			fix = append(fix, c.fix)
		}
		add(c.mark, c.what, fix...)
	}
	if e.hookPlaced(framework.Binary()) != nil {
		e.shown = true // the guard and the health check don't repeat it
		add(markWarn, "This copy of cadrei is in a folder others can change, so some features are off: the password check before backups, team status, and picking up chats after /clear.",
			"Install it with Homebrew or install.sh to turn them on.")
	}
	if _, found := pathFinding(); found {
		exe, _ := os.Executable()
		first, _ := exec.LookPath("cadrei")
		add(markWarn, "Another cadrei comes first on your PATH ("+unbroken(display(first))+").",
			"Remove it, or put "+unbroken(display(filepath.Dir(exe)))+" earlier in your PATH.")
	}
	if live {
		fmt.Fprint(e.stdout, "\r\x1b[2K")
	}
	if quiet {
		e.note("  ", markDone, title+" "+p.Version+" (logged in), git and tmux are ready")
		return true
	}
	e.screen(bold("Checking your setup"))
	for _, r := range rows {
		e.note("  ", r.mark, r.text)
		for _, f := range r.fix {
			e.show("    ", f)
		}
	}
	if !ok {
		e.say("")
		e.screen("Nothing was changed. Run cadrei again once that's fixed.")
	}
	return ok
}

// terminal reports whether stdout is a real terminal, where a line can be
// replaced or a block redrawn in place.
func (e *env) terminal() bool { return isTerminal(e.stdout) }

// redraws reports whether a plan is drawn again in place: in a styled
// terminal only. Plain mode (NO_COLOR, TERM=dumb) uses no cursor codes, so
// a changed plan is printed again below (criteria 12 and 100). Test runs
// that read keys as from a terminal print below, unless a test asks for a
// terminal.
func (e *env) redraws() bool {
	return e.terminal() && streamStyled(e.stdout) && (terminalForTests || !testTTY())
}

// firstProject is the folder the first run offers as the first project:
// the git repository the user is in, unless it's inside ~/.cadrei.
type firstProject struct {
	name, dir string
	valid     bool     // its name can be a project's
	refused   string   // why it can't be linked (a home folder under git, say), or ""
	risky     []string // its own settings that would run for the team
}

func (e *env) projectHere(rt rtpkg.Runtime) *firstProject {
	wd, err := paths.Getwd()
	if err != nil || cadreis.Inside(paths.Real(wd)) {
		return nil
	}
	out, err := exec.Command("git", "-C", wd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil
	}
	top := paths.Real(strings.TrimSpace(string(out)))
	p := &firstProject{name: filepath.Base(top), dir: top}
	p.valid = project.CheckName(p.name) == nil
	// A folder that can't be linked (one cadrei looks through no further,
	// like a home folder under git) is left out before its files are read.
	if p.refused = project.LinkRefusal(top, rt.Trust().Protected()); p.refused == "" && p.valid {
		p.risky = rt.Trust().Risky(top)
	}
	return p
}

// tmuxItem is the plan's tmux item and its why line, with the keys from
// the user's real tmux prefix.
func (e *env) tmuxItem() (item, why string) {
	t := session.Default()
	// The server reads the user's tmux.conf, so the keys shown are theirs.
	t.StartServer()
	long, _ := t.Prefix()
	if t.InTmux() == session.SameServer {
		return "Open your orchestrator as its own tmux session (recommended)",
			"It keeps going on its own. " + long + " s switch, " + long + " L back to here"
	}
	return "Open your orchestrator in tmux (recommended)",
		"It keeps going if you close this window. " + long + " s switch, " + long + " d leave"
}

// firstRun sets up this machine: a new crew, a restore from GitHub, or
// the crews already in ~/.cadrei plugged back in. It reports whether plain
// cadrei goes on to open the orchestrator.
func (e *env) firstRun(rt rtpkg.Runtime) bool {
	list, _ := cadreis.List()
	if !e.interactive() {
		if len(list) > 0 {
			e.failWith("Your crews in ~/.cadrei aren't plugged in yet.", "Run cadrei in a terminal to plug them back in.")
			return false
		}
		e.failWith("There's no crew on this machine yet.", "Run cadrei in a terminal to set one up.")
		return false
	}
	e.opener()
	if !e.checks(rt) {
		return false
	}
	if len(list) > 0 {
		return e.plugIn(rt, list)
	}
	return e.newCrew(rt, firstCrew)
}

// newCrew shows the plan for a new crew named name and sets it up on
// Enter. In a terminal, a pressed a draws the plan again in place.
func (e *env) newCrew(rt rtpkg.Runtime, name string) bool {
	title := rt.Title()
	here := e.projectHere(rt)
	addProject := here != nil && here.valid && here.refused == "" && len(here.risky) == 0
	dest := cadreis.Tilde(filepath.Join(paths.Real(cadreis.Root()), name))
	tmuxItem, tmuxWhy := e.tmuxItem()
	inPlace := e.redraws()
	drawn := 0
	for {
		if inPlace && drawn > 0 {
			fmt.Fprintf(e.stdout, "\x1b[%dA\r\x1b[J", drawn)
		}
		out := e.stdout
		counter := &countWriter{w: out}
		e.stdout = counter
		e.say("")
		e.screen(bold("Here's the plan"))
		e.bullet("Create your crew " + accent(name) + " in " + unbroken(dest) + ", with an engineer and a reviewer")
		if addProject {
			e.bullet("Add " + here.name + " as your first project")
			e.bullet("Trust both in " + title)
			if len(here.risky) > 0 {
				e.note("    ", markWarn, here.name+"'s own settings ("+strings.Join(here.risky, ", ")+") will run for your team too.")
			} else {
				e.show("    ", dim("Your team works there without "+title+" asking first."))
			}
		} else {
			e.bullet("Trust it in " + title)
			e.show("    ", dim("Your orchestrator opens there without "+title+" asking first."))
		}
		e.bullet("Link cadrei's instructions into " + title + " (" + unbroken(cadreis.Tilde(rt.Instructions().Path())) + ")")
		e.bullet(tmuxItem)
		e.show("    ", dim(tmuxWhy))
		keys := "prq"
		prompt := "Or press p for the plain terminal, r to restore from GitHub, q to quit."
		switch {
		case here != nil && here.refused != "":
			e.say("")
			e.note("  ", markWarn, unbroken(display(here.dir))+" can't be a project: "+here.refused+". So it's left out.")
		case here != nil && !here.valid:
			e.say("")
			e.note("  ", markWarn, "This folder's name ("+here.name+") can't be a project name. Ask your orchestrator to add it under another name.")
		case here != nil && len(here.risky) > 0:
			keys = "aprq"
			if addProject {
				prompt = "Or press a to leave it out, p for the plain terminal, r to restore, q to quit."
			} else {
				e.say("")
				e.note("  ", markWarn, here.name+" has its own "+title+" settings ("+strings.Join(here.risky, ", ")+"), which would run for your team once it's trusted. So it's left out.")
				prompt = "Or press a to add it, p for the plain terminal, r to restore, q to quit."
			}
		}
		e.unreadableArchives()
		lines := []string{"Press Enter to set this up.", prompt}
		if offer := archiveOffer(); offer != nil {
			keys += "o"
			lines = append(append(lines, ""), offer...)
		}
		e.say("")
		key, _ := e.readKey(e.wrapScreen(lines...), keys)
		e.stdout = out
		drawn = counter.lines
		switch key {
		case 'o':
			return e.restoreFromArchive(rt)
		case 'a':
			addProject = !addProject
			continue
		case 'q':
			e.say("")
			e.screen("Nothing was changed.")
			return false
		case 'r':
			return e.restoreCrew(rt, "")
		}
		tmux := key == keyEnter
		c, ok := e.makeCrew(rt, name, tmux)
		if !ok {
			return false
		}
		crewTrusted := e.trustCrew(rt, c)
		projectTrusted := true
		if addProject {
			addProject, projectTrusted = e.addFirstProject(rt, c, here)
		}
		// The trust line, or in its place what Claude Code will ask (T9).
		switch {
		case crewTrusted && addProject && projectTrusted:
			e.work(markDone, "Trusted both in "+title)
		case crewTrusted:
			e.work(markDone, "Trusted it in "+title)
		default:
			e.work(markWarn, title+" will ask to trust "+unbroken(display(c.Path))+" when it opens. That's your crew's own folder, so say yes.")
		}
		if addProject && !projectTrusted {
			e.work(markWarn, title+" will ask to trust "+here.name+" the first time it opens there.")
		}
		e.linkSkill(rt)
		e.identityOnce(c.Path)
		if old := cadreis.OldHome(); old != "" {
			e.work(markWarn, "You have a cadre from 0.1.x at "+unbroken(display(old))+". To bring it in, tell your orchestrator: bring in my old cadre from "+unbroken(display(old)))
		}
		return e.finish(tmux, lastLine(tmux, false), "", !crewTrusted)
	}
}

// wrapScreen wraps prompt lines for a screen, in marked text.
func (e *env) wrapScreen(lines ...string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, wrap(l, streamWidth(e.stdout), "  ", "  ")...)
	}
	return out
}

// makeCrew creates a crew from the template, trusted in the runtime, as
// the default, with where its orchestrator opens.
func (e *env) makeCrew(rt rtpkg.Runtime, name string, tmux bool) (cadreis.Cadrei, bool) {
	c, _, err := cadreis.Create(name, cadrei.Assets)
	if err != nil {
		e.failWith(sentence(err.Error()))
		return c, false
	}
	e.guard(c)
	if err := cadreis.SetDefault(c.Name); err != nil {
		e.failWith(sentence(err.Error()))
		return c, false
	}
	where := cadreis.OpensInTerminal
	if tmux {
		where = cadreis.OpensInTmux
	}
	cadreis.SetOpensIn(c, where, false)
	cadreis.SetState(cadreis.FirstTaskPending, "yes")
	e.say("")
	e.work(markDone, "Created your crew "+accent(c.Name))
	return c, true
}

// trustCrew trusts a crew cadrei just made from its template, so the
// orchestrator opens without the runtime asking. A crew from anywhere else
// (a restore) is never trusted in advance.
func (e *env) trustCrew(rt rtpkg.Runtime, c cadreis.Cadrei) bool {
	results, _ := rt.Trust().Mark([]rtpkg.Folder{{Name: c.Name, Dir: c.Path}})
	return len(results) == 1 && (results[0].State == "trusted" || results[0].State == "already")
}

// addFirstProject links the folder the user is in as the crew's first
// project, and trusts it. It reports whether it was added, and whether
// the trust went through.
func (e *env) addFirstProject(rt rtpkg.Runtime, c cadreis.Cadrei, here *firstProject) (added, trusted bool) {
	if _, err := project.Link(c, project.Spec{Name: here.name, Team: "dev"}, here.dir, rt.Trust().Protected()); err != nil {
		e.work(markWarn, "Couldn't add "+here.name+": "+strings.TrimSuffix(err.Error(), ".")+".")
		return false, true
	}
	e.work(markDone, "Added "+here.name+" as a project")
	results, _ := project.Trust(rt, []rtpkg.Folder{{Name: here.name, Dir: here.dir}})
	return true, len(results) == 1 && (results[0].State == "trusted" || results[0].State == "already")
}

// linkSkill writes cadrei's instructions and links them into the runtime.
func (e *env) linkSkill(rt rtpkg.Runtime) {
	if _, err := framework.Sync(cadrei.Assets, version); err != nil {
		e.note("  ", markWarn, "Couldn't write cadrei's instructions: "+err.Error()+".")
		return
	}
	if err := rt.Instructions().Link(framework.SkillDir()); err != nil {
		e.note("  ", markWarn, "Couldn't link cadrei's instructions into "+rt.Title()+": "+err.Error()+".")
		return
	}
	e.work(markDone, "Linked cadrei's instructions into "+rt.Title())
}

// identityOnce says, once on this machine, that git has no identity.
func (e *env) identityOnce(dir string) {
	if hasIdentity(dir) || cadreis.GetState(cadreis.IdentityWarned) != "" {
		return
	}
	cadreis.SetState(cadreis.IdentityWarned, "yes")
	e.work(markWarn, "Git has no name or email set, so history isn't saved. See "+cmd("cadrei --check"))
}

// restoreName is the name a restored crew gets on this machine: the
// repository's name, without the cadrei- a backup's name starts with.
func restoreName(repo string) string {
	name := strings.TrimSuffix(filepath.Base(strings.TrimRight(repo, "/")), ".git")
	if n := strings.TrimPrefix(name, "cadrei-"); n != "" {
		name = n
	}
	return name
}

// restoreCrew restores a crew from its backup repository (3.4). repo, when
// set, skips the question.
func (e *env) restoreCrew(rt rtpkg.Runtime, repo string) bool {
	title := rt.Title()
	e.say("")
	e.screen(bold("Restore your crew from GitHub"))
	e.say("")
	for tries := 0; tries < 3 && repo == ""; tries++ {
		answer, ok := e.answer(render(wrap("Which repo holds it? (owner/repo, or a URL) ", streamWidth(e.stdout), "  ", "  ")[0], false) + " ")
		if !ok {
			break
		}
		if strings.HasPrefix(answer, "-") {
			e.note("  ", markWarn, "A repo name can't start with -.")
			continue
		}
		repo = answer
	}
	if repo == "" {
		e.say("")
		e.screen("Nothing was changed.")
		return false
	}
	name, ok := e.crewName(restoreName(repo), "Name for this one?")
	if !ok {
		return false
	}
	c := cadreis.Cadrei{Name: name, Path: filepath.Join(cadreis.Root(), name)}
	e.say("")
	e.screen(bold("Here's the plan"))
	e.bullet("Copy " + repo + " into " + unbroken(cadreis.Tilde(c.Path)))
	e.bullet("Link cadrei's instructions into " + title + " (" + unbroken(cadreis.Tilde(rt.Instructions().Path())) + ")")
	item, why := e.tmuxItem()
	e.bullet(item)
	e.show("    ", dim(why))
	e.say("")
	key, _ := e.readKey(e.wrapScreen("Press Enter to set this up.", "Or press p for the plain terminal, q to quit."), "pq")
	if key == 'q' {
		e.say("")
		e.screen("Nothing was changed.")
		return false
	}
	tmux := key == keyEnter
	e.say("")
	e.screen("Copying " + repo + "...")
	if err := os.MkdirAll(cadreis.Root(), 0o755); err != nil {
		e.failWith(sentence(err.Error()))
		return false
	}
	if err := project.Clone(repo, c.Path); err != nil {
		reason, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
		e.note("  ", markFail, "Couldn't copy "+repo+": "+strings.TrimSuffix(reason, ".")+".")
		e.show("    ", "Check the name and that you can open it on GitHub, then run cadrei again.")
		return false
	}
	c.Path = paths.Real(c.Path)
	if !c.Present() {
		// Only what this command just cloned is removed.
		os.RemoveAll(c.Path)
		e.note("  ", markFail, repo+" isn't a crew (it has no members folder). Nothing was kept.")
		return false
	}
	// Git checks out a committed link as a link, which cadrei would follow
	// out of the crew when it reads members or the playbook, writes its
	// generated files, or starts a team in its folder.
	rtDir, _ := filepath.Rel(c.Path, filepath.Dir(rt.BuildDir(c.Path)))
	links, err := cadreis.CommittedLinks(c.Path, rtDir)
	if err != nil || len(links) > 0 {
		os.RemoveAll(c.Path)
		if err != nil {
			e.note("  ", markFail, sentence(err.Error())+" Nothing was kept.")
		} else {
			e.note("  ", markFail, repo+" has links where cadrei reads or runs files ("+strings.Join(links, ", ")+"), so nothing was kept. Put the real files in the backup, then restore again.")
		}
		return false
	}
	e.guard(c)
	cadreis.IgnoreLocal(c)
	if err := cadreis.SetDefault(c.Name); err != nil {
		e.failWith(sentence(err.Error()))
		return false
	}
	where := cadreis.OpensInTerminal
	if tmux {
		where = cadreis.OpensInTmux
	}
	// The backup's own choice stands unless the user picked otherwise, and
	// then it's the crew's setting, committed, as p does on a plug-in.
	if was, _ := cadreis.OpensIn(c); was != where {
		cadreis.SetOpensIn(c, where, false)
	}
	e.work(markDone, "Restored your crew "+accent(c.Name)+" into "+unbroken(display(c.Path)))
	if set := modelSettings(c.Path); len(set) > 0 {
		e.note("  ", markWarn, "Its cadrei.conf sets the model and effort your team and orchestrator start with: "+strings.Join(set, ", ")+". Change them there if you'd rather not.")
	}
	e.linkSkill(rt)
	// What the backup brings into the orchestrator once its folder is
	// trusted is shown, and the orchestrator opens only on a yes.
	if loaded := rt.Trust().Loaded(c.Path); len(loaded) > 0 {
		e.say("")
		e.note("  ", markWarn, "Your backup has files "+title+" loads into your orchestrator:")
		for _, f := range loaded {
			e.note("    ", markBullet, unbroken(cadreis.Tilde(filepath.Join(c.Path, f))))
		}
		e.show("    ", "Only go on if you put them there.")
		e.say("")
		if !e.yes("  Open your orchestrator with them?", false) {
			e.say("")
			e.screen("Your orchestrator wasn't opened. Look at those files and remove any you didn't add (" + cmd("git -C "+cadreis.Tilde(c.Path)+" rm <file>") + ", then commit). Then run " + cmd("cadrei") + ".")
			return false
		}
	}
	e.offerProjects(rt, c)
	return e.finish(tmux, lastLine(tmux, resumes(rt, c)), title+" will ask you to trust "+unbroken(cadreis.Tilde(c.Path))+". It holds what you restored from "+repo+", so say yes only if that's your backup.", true)
}

// offerProjects offers to clone a restored crew's projects that aren't on
// this machine (linking those already in the projects folder), and trusts
// only those without their own settings.
func (e *env) offerProjects(rt rtpkg.Runtime, c cadreis.Cadrei) {
	var todo []projectView
	for _, p := range projectsOf(c, true) {
		if p.State != "present" && p.Repo != "" {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		return
	}
	dir := cadreis.ProjectsDir()
	if dir == "" {
		dir = cadreis.SuggestProjectsDir()
	}
	e.say("")
	e.screen(bold("Its projects"))
	width, rwidth := 0, 0
	for _, p := range todo {
		width, rwidth = max(width, len(p.Name)), max(rwidth, len(p.Repo))
	}
	found := 0
	for _, p := range todo {
		line := fmt.Sprintf("%-*s   %s", width, p.Name, p.Repo)
		if p.Found != "" {
			line = fmt.Sprintf("%-*s   %-*s   found in %s", width, p.Name, rwidth, p.Repo, display(p.Found))
			found++
		} else if found > 0 || anyFound(todo) {
			line = fmt.Sprintf("%-*s   %-*s   will be cloned", width, p.Name, rwidth, p.Repo)
		}
		e.bullet(line)
	}
	e.say("")
	question := "Clone them into " + cadreis.Tilde(dir) + "?"
	switch {
	case anyFound(todo) && found < len(todo):
		question = "Link the ones that are here, clone the others into " + cadreis.Tilde(dir) + "?"
		if found == 1 && len(todo)-found == 1 {
			question = "Link the one that's here, clone the other into " + cadreis.Tilde(dir) + "?"
		}
	case anyFound(todo):
		question = "Link them?"
		if len(todo) == 1 {
			question = "Link it?"
		}
	case len(todo) == 1:
		question = "Clone it into " + cadreis.Tilde(dir) + "?"
	}
	e.screen(question)
	answer, ok := e.answer("  Press Enter for yes, type another folder, or press n to skip: ")
	switch {
	case !ok:
		return
	case strings.EqualFold(answer, "n"):
		e.screen("They'll stay off this machine. Ask your orchestrator to clone them anytime.")
		return
	case answer != "":
		if _, ok := e.setProjectsDir(answer, rt.Trust().Protected()); !ok {
			return
		}
	default:
		if cadreis.ProjectsDir() == "" {
			if err := cadreis.SetProjectsDir(dir); err != nil {
				e.note("  ", markWarn, sentence(err.Error()))
				return
			}
		}
	}
	for _, p := range todo {
		if p.Found != "" {
			cadreis.SetPlace(c, p.Name, p.Found)
		}
	}
	results, err := project.Sync(c, cadreis.ProjectsDir(), rt.Trust().Protected())
	if err != nil {
		e.note("  ", markWarn, sentence(err.Error()))
		return
	}
	e.cloneResults(rt, results)
}

func anyFound(list []projectView) bool {
	for _, p := range list {
		if p.Found != "" {
			return true
		}
	}
	return false
}

// cloneResults prints what a sync did, and trusts the clean clones: a
// clone with its own settings is registered but not trusted. In a team
// member's session nothing is trusted: only the user trusts folders.
func (e *env) cloneResults(rt rtpkg.Runtime, results []project.SyncResult) {
	title := rt.Title()
	var untrusted []string
	for _, r := range results {
		switch r.State {
		case "cloned", "found":
			what := cloneLine(r)
			if inMember() {
				e.note("  ", markDone, what)
				untrusted = append(untrusted, r.Name)
				continue
			}
			if risky := rt.Trust().Risky(r.Dir); len(risky) > 0 {
				e.note("  ", markWarn, what+". It has its own "+title+" settings ("+strings.Join(risky, ", ")+"), so it isn't trusted yet. Ask your orchestrator to trust it if you trust it.")
				continue
			}
			if res, _ := project.Trust(rt, []rtpkg.Folder{{Name: r.Name, Dir: r.Dir}}); res[0].State == "trusted" || res[0].State == "already" {
				e.note("  ", markDone, what+" and trusted it")
			} else {
				e.note("  ", markDone, what)
				line, _ := project.Line(res[0], title)
				e.note("  ", markWarn, line)
			}
		case "failed":
			reason := ""
			if r.Err != nil {
				reason = r.Err.Error()
			}
			e.note("  ", markWarn, "Couldn't clone "+r.Name+": "+strings.TrimSuffix(reason, ".")+".")
		}
	}
	if len(untrusted) > 0 {
		e.note("  ", markWarn, untrustedByMember(untrusted, title))
	}
}

// plugIn is the plan for crews already in ~/.cadrei with no default set
// (after an uninstall and a reinstall): plug them back in, as they were.
func (e *env) plugIn(rt rtpkg.Runtime, list []cadreis.Cadrei) bool {
	title := rt.Title()
	def := lastUsed(rt, list)
	inPlace := e.redraws()
	drawn := 0
	for {
		var names []string
		for _, c := range list {
			names = append(names, c.Name)
		}
		// d draws the plan again in place, in a terminal.
		if inPlace && drawn > 0 {
			fmt.Fprintf(e.stdout, "\x1b[%dA\r\x1b[J", drawn)
		}
		out := e.stdout
		counter := &countWriter{w: out}
		e.stdout = counter
		where, _ := cadreis.OpensIn(list[def])
		openLine := "Open your orchestrator in tmux (" + list[def].Name + "'s setting)"
		if where == cadreis.OpensInTerminal {
			openLine = "Open your orchestrator in this terminal (" + list[def].Name + "'s setting)"
		}
		e.say("")
		keys := "pnrq"
		if len(list) == 1 {
			e.screen("Found your crew " + list[0].Name + " in " + unbroken(display(list[0].Path)) + ".")
			e.say("")
			e.screen(bold("Here's the plan"))
			e.bullet("Plug " + list[0].Name + " back in, with its projects and chats as they were")
		} else {
			keys += "d"
			e.screen("Found your crews in " + unbroken(cadreis.Tilde(paths.Real(cadreis.Root()))) + ": " + andList(names) + ".")
			e.say("")
			e.screen(bold("Here's the plan"))
			e.bullet("Plug them back in, with " + list[def].Name + " as your default (press d to pick another)")
		}
		e.bullet("Link cadrei's instructions into " + title + " (" + unbroken(cadreis.Tilde(rt.Instructions().Path())) + ")")
		e.bullet(openLine)
		e.say("")
		lead := "Press Enter to plug it back in."
		if len(list) > 1 {
			lead = "Press Enter to plug them back in."
		}
		e.unreadableArchives()
		lines := []string{lead, "Or press p for the plain terminal, n for a new crew, r to restore, q to quit."}
		if offer := archiveOffer(); offer != nil {
			keys += "o"
			lines = append(append(lines, ""), offer...)
		}
		key, _ := e.readKey(e.wrapScreen(lines...), keys)
		e.stdout = out
		drawn = counter.lines
		switch key {
		case 'o':
			return e.restoreFromArchive(rt)
		case 'd':
			def = (def + 1) % len(list)
			continue
		case 'q':
			e.say("")
			e.screen("Nothing was changed.")
			return false
		case 'r':
			return e.restoreCrew(rt, "")
		case 'n':
			name, ok := e.crewName(firstCrew, "Name for the new one?")
			if !ok {
				return false
			}
			return e.newCrew(rt, name)
		case 'p':
			cadreis.SetOpensIn(list[def], cadreis.OpensInTerminal, false)
		}
		if err := cadreis.SetDefault(list[def].Name); err != nil {
			e.failWith(sentence(err.Error()))
			return false
		}
		for _, c := range list {
			e.guard(c)
		}
		e.say("")
		if len(list) == 1 {
			e.work(markDone, "Plugged "+accent(list[0].Name)+" back in")
		} else {
			var marked []string
			for _, n := range names {
				marked = append(marked, accent(n))
			}
			e.work(markDone, "Plugged "+andList(marked)+" back in ("+accent(list[def].Name)+" is your default)")
		}
		e.linkSkill(rt)
		where, _ = cadreis.OpensIn(list[def])
		last := "Opening your orchestrator."
		if resumes(rt, list[def]) {
			last += " It picks up your last chat."
		}
		return e.finish(where == cadreis.OpensInTmux, last, "", false)
	}
}

// crewName asks for a new crew's name until it's one a crew can have: a
// valid name, not taken in any case, with nothing at its place. Each ask
// says why the name before won't do, and an empty answer keeps it. After
// three answers, or when the input ends, nothing is changed.
func (e *env) crewName(name, ask string) (string, bool) {
	for tries := 0; ; tries++ {
		why := ""
		dest := filepath.Join(cadreis.Root(), name)
		if err := cadreis.CheckName(name); err != nil {
			why = sentence(err.Error())
		} else if c, taken := cadreis.Find(name); taken {
			why = "You already have a crew called " + c.Name + "."
		} else if other, clash := cadreis.Clash(name, dest); clash {
			why = "You already have a crew called " + other.Name + "."
		} else if _, err := os.Lstat(dest); err == nil {
			why = unbroken(cadreis.Tilde(dest)) + " is already there."
		}
		if why == "" {
			return name, true
		}
		var answer string
		ok := tries < 3
		if ok {
			answer, ok = e.answer("  " + why + " " + ask + " ")
		}
		if !ok {
			e.say("")
			e.screen("Nothing was changed.")
			return "", false
		}
		if answer != "" {
			name = answer
		}
	}
}

// lastUsed is the crew whose orchestrator ran last (its newest session
// record), else the first by name.
func lastUsed(rt rtpkg.Runtime, list []cadreis.Cadrei) int {
	best, newest := 0, time.Time{}
	for i, c := range list {
		files, _ := filepath.Glob(filepath.Join(rt.BuildDir(c.Path), "sessions", "*.json"))
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && st.ModTime().After(newest) {
				best, newest = i, st.ModTime()
			}
		}
	}
	return best
}

// andList writes names as "a, b and c".
func andList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// cloneLine says what a sync did with a project's folder.
func cloneLine(r project.SyncResult) string {
	if r.State == "found" {
		return "Found " + r.Name + " at " + unbroken(display(r.Dir))
	}
	return "Cloned " + r.Name + " into " + unbroken(display(r.Dir))
}

// unreadableArchives says, on the first run, which archives cadrei can't
// read, so a setup that's still there isn't silently passed over.
func (e *env) unreadableArchives() {
	if bad := cadreis.Unreadable(); len(bad) > 0 {
		e.say("")
		e.note("  ", markWarn, unreadableNote(bad))
	}
}

// archiveOffer is the lines under a first-run plan that offer an archived
// setup waiting to come back (key o), or none. Both first-run screens use
// it, and it reads the manifests once.
func archiveOffer() []string {
	open := unrestored()
	switch len(open) {
	case 0:
		return nil
	case 1:
		return []string{"You have an archived setup from " + open[0].Manifest.ArchivedAt.Format("Jan 2, 15:04") + " (" + strings.Join(crewNames(open[0], false), ", ") + ").",
			"Press o to bring it back instead."}
	}
	return []string{fmt.Sprintf("You have %d archived setups. Press o to pick one.", len(open))}
}

// restoreFromArchive is o on the first run's plan: pick an archive and
// bring it back.
func (e *env) restoreFromArchive(rt rtpkg.Runtime) bool {
	a, ok := e.pickArchive()
	if !ok {
		return false
	}
	return e.restoreArchive(rt, a)
}
