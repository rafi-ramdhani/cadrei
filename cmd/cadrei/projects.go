//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// projectsDir returns where new clones go. It is asked once and remembered
// (N.2): on a terminal cadrei asks; without one it refuses, so the
// orchestrator asks the user in the chat instead.
func (e *env) projectsDir(protected []string) (string, bool) {
	if d := cadreis.ProjectsDir(); d != "" {
		return d, true
	}
	suggest := cadreis.Tilde(cadreis.SuggestProjectsDir())
	if !e.interactive() {
		e.failWith("Your projects folder isn't set yet.", "Set it with "+cmd("cadrei project dir <folder>")+", for example "+cmd("cadrei project dir "+suggest))
		return "", false
	}
	answer := e.ask(fmt.Sprintf("Where do you keep your projects? [%s] ", suggest))
	if answer == "" {
		answer = suggest
	}
	dir, ok := e.setProjectsDir(answer, protected)
	return dir, ok
}

func (e *env) setProjectsDir(answer string, protected []string) (string, bool) {
	dir := answer
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		dir = paths.Home() + dir[1:]
	}
	if !filepath.IsAbs(dir) {
		dir, _ = filepath.Abs(dir)
	}
	if why := project.DestRefusal(paths.Real(dir), protected); why != "" {
		e.failWith(unbroken(display(dir)) + " can't hold projects: " + strings.TrimSuffix(why, ".") + ".")
		return "", false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if !paths.Within(paths.Real(dir), paths.Home()) {
			e.failWith(unbroken(display(dir))+" doesn't exist.", "Create it first, or pick a folder in your home folder.")
			return "", false
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			e.fail("%s", err)
			return "", false
		}
	}
	if err := cadreis.SetProjectsDir(dir); err != nil {
		e.fail("%s", err)
		return "", false
	}
	e.note("", markDone, "New clones go to "+unbroken(cadreis.Tilde(paths.Real(dir))))
	return paths.Real(dir), true
}

func runProjectDir(e *env) int {
	switch len(e.args) {
	case 0:
		if d := cadreis.ProjectsDir(); d != "" {
			e.say("%s", cadreis.Tilde(d))
		} else {
			e.show("", "Your projects folder isn't set yet. Set it with "+cmd("cadrei project dir <folder>")+", for example "+cmd("cadrei project dir "+cadreis.Tilde(cadreis.SuggestProjectsDir())))
		}
		return 0
	case 1:
		if e.member("change where projects are cloned") {
			return 1
		}
		// Every runtime's protected folders; in 0.2.0, Claude Code's.
		rt, err := runtime.Get(runtime.Default())
		if err != nil {
			return e.fail("%s", err)
		}
		if _, ok := e.setProjectsDir(e.args[0], rt.Trust().Protected()); !ok {
			return 1
		}
		return 0
	}
	return e.failWith("Use " + cmd("cadrei project dir [<folder>]") + ".")
}

// addExamples are the two ways to add a project, for its errors.
var addExamples = []string{cmd("cadrei project add blog you/blog"), cmd("cadrei project add app --path ~/code/app")}

func runProjectAdd(e *env) int {
	var pos []string
	var path, repo string
	trust := true
	for i := 0; i < len(e.args); i++ {
		switch a := e.args[i]; {
		case a == "--no-trust":
			trust = false
		case (a == "--path" || a == "--repo") && i+1 < len(e.args):
			if a == "--path" {
				path = e.args[i+1]
			} else {
				repo = e.args[i+1]
			}
			i++
		case strings.HasPrefix(a, "-"):
			return e.failWith("There's no option "+a+" for "+cmd("cadrei project add")+". For example:", addExamples...)
		default:
			pos = append(pos, a)
		}
	}
	linking := path != ""
	s := project.Spec{Team: "dev"}
	switch {
	case linking && len(pos) >= 1 && len(pos) <= 3:
		s.Name, s.Repo = pos[0], repo
		pos = pos[1:]
	case !linking && repo == "" && len(pos) >= 2 && len(pos) <= 4:
		s.Name, s.Repo = pos[0], pos[1]
		pos = pos[2:]
	default:
		return e.failWith("Add which project? For example:", addExamples...)
	}
	if len(pos) > 0 {
		s.Team = pos[0]
	}
	if len(pos) > 1 {
		s.About = pos[1]
	}
	// Only the user, through the orchestrator, adds projects: a project's
	// folder is trusted and members work in it.
	if e.member("add projects") {
		return 1
	}
	if err := project.CheckName(s.Name); err != nil {
		return e.fail("%s", err)
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	protected := rt.Trust().Protected()
	var added project.Added
	var err error
	if linking {
		added, err = project.Link(r.Cadrei, s, path, protected)
	} else {
		dir, ok := e.projectsDir(protected)
		if !ok {
			return 1
		}
		added, err = project.Add(r.Cadrei, s, dir, protected)
	}
	if err != nil {
		return e.fail("%s", err)
	}
	where := unbroken(display(added.Dir))
	head := map[string]string{"cloned": "Cloned " + s.Name + " into " + where, "already": "Added " + s.Name + ", already at " + where,
		"linked": "Added " + s.Name + " from " + where}[added.Where]
	risky := rt.Trust().Risky(added.Dir)
	switch {
	case !trust:
		e.note("", markDone, head)
	case len(risky) > 0:
		// Its own settings would run for the team once it's trusted:
		// only an explicit cadrei project trust does that.
		e.note("", markDone, head)
		e.note("", markWarn, untrustedRisky(rt, s.Name, risky))
	default:
		results, note := project.Trust(rt, []runtime.Folder{{Name: s.Name, Dir: added.Dir}})
		switch res := results[0]; res.State {
		case "trusted":
			e.note("", markDone, head+", and trusted it in "+rt.Title())
		case "already":
			e.note("", markDone, head+". It was already trusted in "+rt.Title()+".")
		default:
			e.note("", markDone, head)
			line, _ := project.Line(res, rt.Title())
			e.note("", markWarn, line)
			if note != "" {
				e.show("  ", note)
			}
		}
	}
	e.notes(added.Notes)
	return 0
}

func runProjectSync(e *env) int {
	trust := true
	switch {
	case len(e.args) == 1 && e.args[0] == "--no-trust":
		trust = false
	case len(e.args) != 0:
		return e.failWith("Use " + cmd("cadrei project sync [--no-trust]") + ".")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	dir := cadreis.ProjectsDir()
	if dir == "" && needsProjectsDir(r) {
		if dir, ok = e.projectsDir(rt.Trust().Protected()); !ok {
			return 1
		}
	}
	for _, w := range skippedEntries(r.Cadrei) {
		e.warn("", strings.TrimSuffix(w, ".")+".")
	}
	results, err := project.Sync(r.Cadrei, dir, rt.Trust().Protected())
	if err != nil {
		return e.fail("%s", err)
	}
	var cloned []project.SyncResult
	code := 0
	for _, res := range results {
		switch res.State {
		case "present":
			e.note("  ", markDone, res.Name+" is on this machine")
		case "cloned", "found":
			cloned = append(cloned, res)
		case "drive":
			e.note("  ", markWarn, res.Name+" is on a drive that isn't connected ("+unbroken(display(res.Dir))+"). Connect the drive.")
		case "no repo":
			e.note("  ", markWarn, res.Name+" isn't on this machine, and has no repo to clone. Link its folder with "+cmd("cadrei project link "+res.Name+" <folder>"))
		case "no folder":
			e.note("  ", markWarn, res.Name+" has nowhere to go. Set your projects folder with "+cmd("cadrei project dir <folder>"))
		default:
			e.note("  ", markWarn, "Couldn't clone "+res.Name+": "+strings.TrimSuffix(fmt.Sprint(res.Err), ".")+".")
			code = 1
		}
	}
	if trust {
		// Only the clean ones are trusted: a clone with its own settings
		// would run them for every member.
		e.cloneResults(rt, cloned)
	} else {
		for _, res := range cloned {
			e.note("  ", markDone, cloneLine(res))
		}
	}
	return code
}

// needsProjectsDir reports whether some project with a repo has no place
// on this machine, so sync would clone it into the projects folder.
func needsProjectsDir(r *cadreis.Resolved) bool {
	reg, err := registry.Load(r.Registry())
	if err != nil {
		return false
	}
	for _, entry := range reg.Entries() {
		if entry.Get("repo") != "" && cadreis.ProjectDir(r.Cadrei, entry) == "" {
			return true
		}
	}
	return false
}

// runProjectLink is cadrei project link <name> <dir>: where a project's
// folder is on this machine, for a folder that moved or a clone made
// elsewhere. The folder is trusted, as with project add.
func runProjectLink(e *env) int {
	trust := true
	var pos []string
	for _, a := range e.args {
		switch {
		case a == "--no-trust":
			trust = false
		case strings.HasPrefix(a, "-"):
			return e.failWith("Use " + cmd("cadrei project link <name> <folder> [--no-trust]") + ".")
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		return e.failWith("Use " + cmd("cadrei project link <name> <folder> [--no-trust]") + ".")
	}
	if e.member("link folders to the crew") {
		return 1
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	dir, err := project.Relink(r.Cadrei, pos[0], pos[1], rt.Trust().Protected())
	if err != nil {
		return e.fail("%s", err)
	}
	e.note("", markDone, pos[0]+" is at "+unbroken(display(dir))+" on this machine")
	if risky := rt.Trust().Risky(dir); trust && len(risky) > 0 {
		e.note("", markWarn, untrustedRisky(rt, pos[0], risky))
	} else if trust {
		e.trustReport(rt, []runtime.Folder{{Name: pos[0], Dir: dir}})
	}
	return 0
}

// untrustedRisky says a project wasn't trusted because it has settings of
// its own that would run for the team.
func untrustedRisky(rt runtime.Runtime, name string, risky []string) string {
	return name + " has its own " + rt.Title() + " settings (" + strings.Join(risky, ", ") + "), which would run for your team once it's trusted. So it isn't trusted yet. If you trust where it came from, run " + cmd("cadrei project trust "+name)
}

// runProjectUnlink is cadrei project unlink <name> [--untrust]: the project
// leaves the registry and this machine's places; its folder is kept.
func runProjectUnlink(e *env) int {
	untrust := false
	var pos []string
	for _, a := range e.args {
		switch {
		case a == "--untrust":
			untrust = true
		case strings.HasPrefix(a, "-"):
			return e.failWith("Use " + cmd("cadrei project unlink <name> [--untrust]") + ".")
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 1 {
		return e.failWith("Use " + cmd("cadrei project unlink <name> [--untrust]") + ".")
	}
	if e.member("unlink projects") {
		return 1
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	dir, notes, err := project.Unlink(r.Cadrei, pos[0])
	if err != nil {
		return e.fail("%s", err)
	}
	e.notes(notes)
	if dir != "" {
		e.note("", markDone, "Removed "+pos[0]+" from this crew. Its folder "+unbroken(display(dir))+" stays.")
	} else {
		e.note("", markDone, "Removed "+pos[0]+" from this crew")
	}
	if untrust && dir != "" {
		results, note := rt.Trust().Unmark([]runtime.Folder{{Name: pos[0], Dir: dir}})
		for _, res := range results {
			e.show("  ", res.Name+": "+res.State+" in "+rt.Title())
		}
		if note != "" {
			e.show("  ", note)
		}
	}
	return 0
}

// untrustedByMember says that a member's projects aren't trusted, and the
// commands the user runs to trust them: project trust takes one name.
func untrustedByMember(names []string, title string) string {
	verb := " isn't"
	if len(names) > 1 {
		verb = " aren't"
	}
	var runs []string
	for _, n := range names {
		runs = append(runs, cmd("cadrei project trust "+n))
	}
	return andList(names) + verb + " trusted in " + title + ": team members can't trust folders. Ask the user to run " + strings.Join(runs, ", then ") + "."
}

// trustReport trusts folders and prints a line each, then any note.
func (e *env) trustReport(rt runtime.Runtime, folders []runtime.Folder) {
	if inMember() {
		var names []string
		for _, f := range folders {
			names = append(names, f.Name)
		}
		e.note("", markWarn, untrustedByMember(names, rt.Title()))
		return
	}
	results, note := project.Trust(rt, folders)
	for _, f := range folders {
		for _, res := range results {
			if res.Name == f.Name {
				line, ok := project.Line(res, rt.Title())
				if ok {
					e.note("", markDone, line)
				} else {
					e.note("", markWarn, line)
				}
			}
		}
	}
	if note != "" {
		e.show("  ", note)
	}
}

func runProjectTrust(e *env) int {
	if e.member("trust project folders") {
		return 1
	}
	if len(e.args) != 1 || (strings.HasPrefix(e.args[0], "-") && e.args[0] != "--all") {
		return e.failWith("Use " + cmd("cadrei project trust <name>") + ", or " + cmd("cadrei project trust --all") + ".")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	rt, ok := e.cadreiRuntime(r)
	if !ok {
		return 1
	}
	reg, err := registry.Load(r.Registry())
	if err != nil {
		return e.fail("%s", err)
	}
	var folders []runtime.Folder
	if e.args[0] == "--all" {
		for _, entry := range reg.Entries() {
			d := cadreis.ProjectDir(r.Cadrei, entry)
			if st, err := os.Stat(d); d == "" || err != nil || !st.IsDir() {
				e.note("", markWarn, entry.Name+" isn't on this machine, so it was skipped. Get it with "+cmd("cadrei project sync"))
				continue
			}
			folders = append(folders, runtime.Folder{Name: entry.Name, Dir: d})
		}
	} else {
		entry := reg.Get(e.args[0])
		if entry == nil {
			return e.failWith("There's no project called "+e.args[0]+" in this crew.", "See your projects with "+cmd("cadrei ls"))
		}
		d := cadreis.ProjectDir(r.Cadrei, entry)
		if st, err := os.Stat(d); d == "" || err != nil || !st.IsDir() {
			return e.failWith(e.args[0]+" isn't on this machine.", "Get it with "+cmd("cadrei project sync"))
		}
		folders = []runtime.Folder{{Name: entry.Name, Dir: d}}
	}
	if len(folders) > 0 {
		e.trustReport(rt, folders)
	}
	return 0
}
