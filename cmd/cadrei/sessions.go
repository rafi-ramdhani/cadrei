//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// scope is the resolved cadrei as the session code sees it.
func scope(r *cadreis.Resolved) session.Scope {
	return session.Scope{Name: r.Name, Path: r.Path, Default: r.Name == r.Default, T: session.Default()}
}

// tmuxReady checks that tmux 3.2 or newer is there, as the first run and
// the health check do (tmuxCheck).
func (e *env) tmuxReady() bool {
	if c := tmuxCheck(); c.mark == markFail {
		e.failWith(c.what, c.fix)
		return false
	}
	return true
}

// splitTarget reads team or team/role, and checks the team exists.
func splitTarget(r *cadreis.Resolved, target string) (team, role string, err error) {
	team, role, _ = strings.Cut(target, "/")
	if st, serr := os.Stat(filepath.Join(r.Path, "members", team)); team == "" || team == "." || team == ".." || serr != nil || !st.IsDir() {
		return "", "", &lookupError{what: "There's no team called " + printable(team) + " in your crew " + printable(r.Name) + ".", fix: []string{"Ask your orchestrator to add it."}}
	}
	return team, role, nil
}

// startable refuses a team or member whose name cannot be part of a
// session name (0.1.x allowed dots, as in ml.ops), naming what to rename.
func startable(cadrei, team, role string) error {
	if !session.CheckName(team) {
		return &lookupError{what: "The team " + printable(team) + " can't start: " + session.NameRule + ".", fix: []string{"Rename its folder, " + unbroken(display(filepath.Join(cadrei, "members", team))) + "."}}
	}
	roles := []string{role}
	if role == "" {
		roles = session.Roles(cadrei, team)
	}
	for _, ro := range roles {
		if !session.CheckName(ro) {
			return &lookupError{what: "The " + printable(ro) + " can't start: " + session.NameRule + ".", fix: []string{"Rename its file, " + unbroken(display(filepath.Join(cadrei, "members", team, ro+".md"))) + "."}}
		}
		// A member's address is <crew>-<team>-<role>: a role called
		// orchestrator would share one with crew <crew>-<team>'s.
		if strings.EqualFold(ro, "orchestrator") {
			return &lookupError{what: "The " + printable(team) + " team's member orchestrator can't start: orchestrator is a name kept for your orchestrator.", fix: []string{"Rename its file, " + unbroken(display(filepath.Join(cadrei, "members", team, ro+".md"))) + "."}}
		}
	}
	return nil
}

// mode is the permission mode sessions start with: CADREI_PERMISSION_MODE,
// else cadrei.conf's PERMISSION_MODE, else default.
func (e *env) mode(values map[string]string) string { return modeOf(values) }

func modeOf(values map[string]string) string {
	if m := os.Getenv("CADREI_PERMISSION_MODE"); m != "" {
		return m
	}
	if m := values["PERMISSION_MODE"]; m != "" {
		return m
	}
	return "default"
}

// picker gives every role the cadrei's runtime, refused when it cannot
// enforce the fixed denies, has no messaging or lacks the permission mode,
// and prepares what its members start with, once.
func (e *env) picker(r *cadreis.Resolved, mode string) func(string) (runtime.Runtime, string, error) {
	var rt runtime.Runtime
	var grants string
	return func(role string) (runtime.Runtime, string, error) {
		if rt != nil {
			return rt, grants, nil
		}
		got, err := runtime.Get(runtimeName())
		if err != nil {
			return nil, "", err
		}
		if err := runtime.Usable(got, mode); err != nil {
			return nil, "", err
		}
		// Prepare writes the settings copy into the build folder, so the
		// folder is checked first.
		if err := session.EnsureBuild(got.BuildDir(r.Path)); err != nil {
			return nil, "", err
		}
		pl := places(r)
		// The session hook names this binary only when it is safely
		// placed; without it, resume uses the id from the launch.
		if e.hookPlaced(pl.Binary) != nil {
			pl.Binary = ""
		}
		p := got.Permissions().Prepare(r.Path, pl)
		e.notes(p.Notes)
		for _, w := range p.Warnings {
			e.warn("", w)
		}
		rt, grants = got, p.Grants
		return rt, grants, nil
	}
}

// locate turns a project name or a folder into the key's project part, the
// folder and the target recorded for restarts.
func locate(r *cadreis.Resolved, arg string) (proj, dir, target string, err error) {
	d, perr := projectDir(r, arg)
	switch {
	case perr == nil:
		return arg, d, arg, nil
	case perr != errNotRegistered:
		return "", "", "", perr
	}
	st, serr := os.Stat(arg)
	if serr != nil || !st.IsDir() {
		return "", "", "", &lookupError{what: printable(arg) + " isn't a project or a folder.", fix: []string{"See your projects with " + cmd("cadrei ls")}}
	}
	abs, _ := filepath.Abs(arg)
	name := strings.Map(func(c rune) rune {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return c
		}
		return '-'
	}, filepath.Base(abs))
	return name, abs, abs, nil
}

func runUp(e *env) int {
	fresh := contains(e.args, "--fresh")
	e.args = without(e.args, "--fresh")
	override, rest, ok := e.choiceFlags(e.args)
	if !ok {
		return 1
	}
	e.args = rest
	if len(e.args) < 1 || len(e.args) > 2 || strings.HasPrefix(e.args[0], "-") {
		return e.failWith("Which team? For example: "+cmd("cadrei up dev my-app"), "Or ask your orchestrator to start one.")
	}
	r, ok := e.resolve()
	if !ok || !e.tmuxReady() {
		return 1
	}
	if err := cadreis.CheckName(r.Name); err != nil {
		return e.failWith("This crew's folder name (" + printable(r.Name) + ") can't be used in session names. Rename the folder to letters, digits, - and _.")
	}
	team, role, err := splitTarget(r, e.args[0])
	if err != nil {
		return e.failErr(err)
	}
	if err := startable(r.Path, team, role); err != nil {
		return e.failErr(err)
	}
	u := session.Up{Scope: scope(r), Team: team, Role: role, Dir: session.DefaultDir(r.Path, team)}
	if len(e.args) == 2 {
		if u.Project, u.Dir, u.Target, err = locate(r, e.args[1]); err != nil {
			return e.failErr(err)
		}
		u.Explicit = true
	}
	if err := u.CheckSession(team, u.Project); err != nil {
		return e.fail("%s", err)
	}
	values := e.conf(r)
	u.Mode = e.mode(values)
	u.Pick = e.picker(r, u.Mode)
	u.Wait = startWait()
	u.Fresh = fresh
	u.Version = version
	u.StateHooks = e.hookPlaced(framework.Binary()) == nil
	protocol, _ := cadrei.Assets.ReadFile("protocol.md")
	u.Protocol = protocol
	u.Override, u.Defaults = override, crewChoice(r.Path, false)
	// Every value this start could use is checked before anything starts.
	if rt, err := runtime.Get(runtimeName()); err == nil {
		if err := checkChoices(rt, r.Path, u); err != nil {
			return e.failErr(err)
		}
	}
	u.Report = e.reportUp(u)
	if !e.recordServer(r.Cadrei, u.T) {
		return 1
	}
	failed, started := u.Start(e.stdout)
	if len(started) > 0 {
		// A member started: the first run's greeting has done its job.
		cadreis.SetState(cadreis.FirstTaskPending, "")
	}
	e.safeguardCheck(r, team, u.Project, started)
	if failed {
		return 1
	}
	return 0
}

// reportUp says what happened to each role cadrei up started.
func (e *env) reportUp(u session.Up) func(session.Result) {
	on := ""
	if u.Target != "" {
		on = " on " + printable(u.Target)
		if filepath.IsAbs(u.Target) {
			on = " in " + unbroken(display(u.Target))
		}
	}
	return func(res session.Result) {
		who := "the " + res.Role
		member := filepath.Join("members", u.Team, res.Role+".md")
		switch {
		case res.Err != nil:
			e.exitedOr(res.Err, "The "+res.Role+" didn't start", fixModel(res.Choice, u.Path, member))
		case res.Legacy != "":
			e.show("", "The "+res.Role+" is already running, in the 0.1.x session "+res.Legacy+". It keeps that name until it restarts.")
		case res.Running && u.Override != (session.Choice{}):
			// An override can't reach a running member (7.11.2).
			runs := ""
			if d := describe(res.Was); d != "" {
				runs = " on " + d
			}
			stop := strings.TrimSpace("cadrei stop " + u.Team + "/" + res.Role + " " + u.Project)
			e.show("", "The "+res.Role+" is already running"+runs+". Stop it first to start it on "+describe(u.Override)+": "+cmd(stop)+".")
		case res.Running:
			e.show("", "The "+res.Role+" is already running"+on+".")
		case res.Resumed:
			now := ""
			if d := changed(res.Choice, res.Was); d != "" {
				now = ", now on " + d
			}
			e.note("", markDone, "Started "+who+on+" (picked up where it left off"+now+")")
		default:
			e.note("", markDone, "Started "+who+on+" (fresh chat)")
			if why := freshWhy(res.Note); why != "" {
				e.show("  ", why)
			}
		}
		if res.Waiting {
			// Logged out, say: up started it, but it sits there.
			where := strings.TrimSpace(u.Team + " " + u.Project)
			e.show("  ", "It's waiting on a login or setup screen and won't get going until you answer it: "+cmd("cadrei attach "+where))
		}
	}
}

// exitedOr reports err after what: a command that ended at once says its
// last line, with check (where its model is set) when there's one, or how
// to run it by hand, kept whole.
func (e *env) exitedOr(err error, what string, check string) {
	var ex *session.ExitedError
	if errors.As(err, &ex) {
		last := clause(strings.TrimSuffix(ex.Last, "."))
		if ex.Last != "" && check != "" {
			e.failWith(what+": "+last+".", check)
			return
		}
		if ex.Last != "" {
			e.failWith(what+": "+last+".",
				"To see more, run this in "+unbroken(display(ex.Dir))+":", unbroken(tildeLine(ex.Line)))
			return
		}
		e.failWith(what+": it stopped right after it started.",
			"To see why, run this in "+unbroken(display(ex.Dir))+":", unbroken(tildeLine(ex.Line)))
		return
	}
	e.failWith(what + ": " + strings.TrimSuffix(err.Error(), ".") + ".")
}

// tildeLine writes the home folder as ~ in a shell command, where the
// shell expands it again: at the start of an unquoted word, and after =.
func tildeLine(line string) string {
	home := paths.Real(paths.Home())
	for _, h := range []string{home, paths.Home()} {
		line = strings.ReplaceAll(line, " "+h+"/", " ~/")
		line = strings.ReplaceAll(line, "="+h+"/", "=~/")
		if strings.HasPrefix(line, h+"/") {
			line = "~/" + line[len(h)+1:]
		}
	}
	return line
}

// confirm asks question, unless --yes was given. Without a terminal it
// says how to confirm and fails.
func (e *env) confirm(question, needs string, yes bool) bool {
	if yes {
		return true
	}
	if !e.interactive() {
		e.warn("", needs+" needs a yes. Run it again with --yes.")
		return false
	}
	switch strings.ToLower(e.ask(question + " [y/N] ")) {
	case "y", "yes":
		return true
	}
	e.say("Nothing was changed.")
	return false
}

// showSessions prints the running teams with their members, under a
// heading per crew when known is set (a listing of every crew).
func (e *env) showSessions(t session.Tmux, list []session.Info, known map[string]string) {
	last := ""
	for _, i := range list {
		indent := "  "
		if known != nil {
			if g := session.Group(i, known); g != last {
				e.say("  %s", printable(g))
				last = g
			}
			indent = "    "
		}
		e.say("%s%s: %s", indent, runningLabel(i), strings.Join(roleNames(t, i), ", "))
	}
}

// runningLabel names a running team as people read it: dev, or dev on my-app.
// A session cadrei didn't mark goes by its tmux name. It comes from tmux,
// so it's printable.
func runningLabel(i session.Info) string {
	if i.Team == "" {
		return printable(i.Name)
	}
	switch {
	case i.Target != "" && filepath.IsAbs(i.Target):
		return printable(i.Team) + " in " + display(i.Target)
	case i.Target != "":
		return printable(i.Team + " on " + i.Target)
	case i.Project != "":
		return printable(i.Team + " on " + i.Project)
	}
	return printable(i.Team)
}

// roleNames lists the roles running in a team's session, printable: a
// member can rename its own window.
func roleNames(t session.Tmux, i session.Info) []string {
	prefix := strings.TrimPrefix(i.Name, "cadrei-") + "-"
	var out []string
	for _, m := range t.MemberNames(i) {
		out = append(out, printable(strings.TrimPrefix(m, prefix)))
	}
	return out
}

// stopReporter says only what needs saying while stopping: a session that
// wouldn't stop, and the one this command runs in, stopped last.
func (e *env) stopReporter(labels map[string]string) func(string, bool, bool) {
	return func(name string, stopped, last bool) {
		label := labels[name]
		if label == "" {
			label = printable(name)
		}
		switch {
		case last:
			e.show("", "Stopping "+label+" last, since this command runs in it.")
		case !stopped:
			e.warn("", "Couldn't stop "+label+".")
		}
	}
}

func runStop(e *env) int {
	var pos []string
	all, yes, fresh := false, false, false
	for _, a := range e.args {
		switch a {
		case "--all":
			all = true
		case "--yes", "-y":
			yes = true
		case "--fresh":
			fresh = true
		default:
			if strings.HasPrefix(a, "-") {
				return e.failWith("Use " + cmd("cadrei stop [team[/role]] [project] [--all] [--yes] [--fresh]") + ".")
			}
			pos = append(pos, a)
		}
	}
	if len(pos) > 2 || (all && len(pos) > 0) {
		return e.failWith("Use " + cmd("cadrei stop [team[/role]] [project] [--all] [--yes] [--fresh]") + ".")
	}
	if len(pos) > 0 {
		r, ok := e.resolve()
		if !ok {
			return 1
		}
		team, role, err := splitTarget(r, pos[0])
		if err != nil {
			return e.failErr(err)
		}
		project := ""
		if len(pos) == 2 {
			project = pos[1]
		}
		s := scope(r)
		key := session.Key(team, project)
		roles := []string{role}
		what := "the " + printable(team) + " team"
		var res session.StopResult
		if role == "" {
			res = s.StopTeam(team, project)
			roles = session.Roles(r.Path, team)
		} else {
			what = "the " + printable(role)
			res = s.StopRole(team, project, role)
		}
		on := ""
		if project != "" {
			on = " on " + printable(project)
		}
		switch {
		case res.Stopped:
			e.note("", markDone, "Stopped "+what+on)
		case res.Unmarked != "":
			e.note("", markWarn, sentence(printable(res.Unmarked)))
		default:
			e.show("", strings.ToUpper(what[:1])+what[1:]+" isn't running"+on+".")
			if res.Instead != "" {
				e.show("  ", printable(res.Instead)+" runs under that name, and was left as it is.")
			}
		}
		var names []string
		for _, ro := range roles {
			names = append(names, session.MemberName(r.Name, key, ro))
		}
		dropStates(r.Path, names)
		if fresh {
			e.forget(r.Cadrei, names)
		}
		return 0
	}
	if e.member("stop the whole crew") {
		return 1
	}
	t := session.Default()
	var list []session.Info
	var known map[string]string
	var targets []cadreis.Cadrei
	// done ends the stop; with --fresh, the stopped cadreis' members start
	// new conversations next time.
	done := func(code int) int {
		if fresh && code == 0 {
			for _, c := range targets {
				e.forget(c, nil)
			}
		}
		return code
	}
	summary := "Stopped everything in all your crews"
	if all {
		known = knownCadreis()
		targets, _ = cadreis.List()
		list = ofCrews(session.All(t, known), known, cadreis.Default() != "")
		if len(list) == 0 {
			e.say("Nothing's running in any of your crews.")
			return done(0)
		}
		e.say("Running in all your crews:")
	} else {
		r, ok := e.resolve()
		if !ok {
			return 1
		}
		targets = []cadreis.Cadrei{r.Cadrei}
		list = scope(r).Running()
		if len(list) == 0 {
			e.say("Nothing's running in your crew %s.", r.Name)
			return done(0)
		}
		e.say("Running in your crew %s:", r.Name)
		summary = "Stopped everything in your crew " + r.Name
	}
	e.showSessions(t, list, known)
	if !e.confirm("Stop all of it?", "Stopping everything", yes) {
		return 1
	}
	var names []string
	labels := map[string]string{}
	for _, i := range list {
		names = append(names, i.Name)
		labels[i.Name] = "the " + runningLabel(i) + " team"
		if i.Team == "" {
			labels[i.Name] = printable(i.Name)
		}
		if i.Home != "" {
			defer dropStates(i.Home, t.MemberNames(i))
		}
	}
	st := &session.Stopping{T: t, Report: e.stopReporter(labels)}
	st.Stop(e.stdout, names)
	if len(st.Failed) == 0 {
		e.note("", markDone, summary)
	}
	if len(st.Failed) > 0 {
		st.Last(e.stdout)
		return 2
	}
	code := done(0)
	st.Last(e.stdout)
	return code
}

func knownCadreis() map[string]string {
	known := map[string]string{}
	list, _ := cadreis.List()
	for _, c := range list {
		known[c.Path] = c.Name
	}
	// A crew outside ~/.cadrei that CADREI_HOME names is this setup's too.
	if os.Getenv("CADREI_HOME") != "" {
		if r, err := cadreis.Resolve(wdOrHome(), nil); err == nil {
			known[r.Path] = r.Name
		}
	}
	return known
}

// ofCrews keeps the sessions of this machine's crews, by their recorded
// home and never by name alone (8.3.5), so another setup's crews on the
// same tmux server are left alone. With legacy, cadre 0.1.x's sessions
// stay too, as they do for the default crew.
func ofCrews(list []session.Info, known map[string]string, legacy bool) []session.Info {
	var out []session.Info
	for _, i := range list {
		if known[i.Home] != "" || (legacy && i.Legacy()) {
			out = append(out, i)
		}
	}
	return out
}

func runAttach(e *env) int {
	if len(e.args) > 2 {
		return e.failWith("Use " + cmd("cadrei attach [team] [project]") + ".")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	if len(e.args) == 0 {
		return e.attachOrchestrator(r)
	}
	proj := ""
	if len(e.args) == 2 {
		proj = e.args[1]
		// A registered project that is missing on this machine is refused,
		// with how to get it back.
		if _, err := projectDir(r, proj); err != nil && !errors.Is(err, errNotRegistered) {
			return e.fail("%s", err)
		}
	}
	key := session.Key(e.args[0], proj)
	s := scope(r)
	live := s.Live(e.args[0], proj)
	if live == "" {
		if m := s.Unmarked(e.args[0], proj); m != "" {
			return e.fail("%s", printable(m))
		}
		on := ""
		if proj != "" {
			on = " on " + printable(proj)
		}
		if other := s.Instead(e.args[0], proj); other != "" {
			return e.failWith("The "+printable(e.args[0])+" team isn't running"+on+". "+printable(other)+" runs under its name.", "Ask your orchestrator to start it.")
		}
		_ = key
		return e.failWith("The "+printable(e.args[0])+" team isn't running"+on+".", "Ask your orchestrator to start it.")
	}
	if !e.interactive() {
		return e.failWith("Run " + cmd("cadrei attach") + " in a terminal.")
	}
	if _, err := s.T.Attach(live); err != nil {
		return e.fail("%s", err)
	}
	return 0
}

// projectsOf lists a cadrei's projects with whether each is on this
// machine. With find, a project that isn't is looked for in the projects
// folder, which runs git in each folder there: only for what shows found.
func projectsOf(c cadreis.Cadrei, find bool) []projectView {
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return nil
	}
	var out []projectView
	finder := project.NewFinder(cadreis.ProjectsDir())
	if !find {
		finder = nil
	}
	for _, entry := range reg.Entries() {
		d := cadreis.ProjectDir(c, entry)
		v := projectView{Name: printable(entry.Name), Repo: printable(project.Redact(entry.Get("repo"))), Team: printable(entry.Get("team")),
			About: printable(entry.Get("about")), Path: d, State: cadreis.Where(d)}
		v.Cloned = v.State == "present"
		if !v.Cloned && finder != nil {
			v.Found = finder.Find(v.Repo)
		}
		out = append(out, v)
	}
	return out
}

// display writes a path with ~ for the home folder.
func display(p string) string { return printable(cadreis.Tilde(paths.Real(p))) }

// forget drops the conversation records of members (names, or every
// member's for nil) so their next start is a new conversation; the
// orchestrator's record stays.
func (e *env) forget(c cadreis.Cadrei, names []string) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return
	}
	dir := filepath.Join(rt.BuildDir(c.Path), "sessions")
	if names == nil {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, f := range files {
			if n := strings.TrimSuffix(filepath.Base(f), ".json"); n != orchestrator.Name(c.Name) {
				names = append(names, n)
			}
		}
	}
	for _, n := range names {
		if p := session.RecordPath(rt.BuildDir(c.Path), n); p != "" {
			session.Forget(p)
		}
	}
	e.show("  ", "Their next start is a fresh chat.")
}

// dropStates removes the state files of members that were stopped.
func dropStates(home string, names []string) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return
	}
	for _, n := range names {
		if p := session.StatePath(rt.BuildDir(home), n); p != "" {
			os.Remove(p)
		}
	}
}

// safeguardCheck looks at each member up just started or resumed: when the
// runtime's safeguards stopped its last reply, it won't answer, and the
// user has to pick the model again in its window or start it fresh. Each
// one gets a block after all the start lines. There's no wait for the
// runtime's own retry: nothing retries in a chat that was just resumed.
func (e *env) safeguardCheck(r *cadreis.Resolved, team, project string, roles []string) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return
	}
	build := rt.BuildDir(r.Path)
	key := session.Key(team, project)
	where := strings.TrimSpace(team + " " + project)
	for _, role := range roles {
		name := session.MemberName(r.Name, key, role)
		rec := session.ReadRecord(session.RecordPath(build, name))
		if rec == nil {
			continue
		}
		if _, stopped := session.Safeguard(rt.Sessions(), build, name, rt.Sessions().TranscriptFile(rec.ID, rec.Dir)); !stopped {
			continue
		}
		member := strings.TrimSpace(team + "/" + role + " " + project)
		e.note("", markWarn, rt.Model()+"'s safeguards stopped the "+role+"'s last reply, so it won't answer.")
		e.show("  ", "Open its window and pick the model again: "+cmd("cadrei attach "+where))
		e.show("  ", "Or start it fresh: "+cmd("cadrei stop "+member+" --fresh"))
		e.show("  ", "then "+cmd("cadrei up "+member))
	}
}
