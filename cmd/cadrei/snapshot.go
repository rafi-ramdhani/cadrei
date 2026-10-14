//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The orchestrator's snapshot: a "## Right now" block it reads on startup
// and on resume, so it can speak first without running anything. Cadrei
// passes what only the launch knows in the environment.
const (
	envOpenedFrom    = "CADREI_OPENED_FROM"    // the folder the user ran cadrei in
	envOpenedProject = "CADREI_OPENED_PROJECT" // the linked project that folder is in
	envOpensIn       = "CADREI_OPENS_IN"       // tmux, tmux-inside, terminal or hook
)

// seed is the orchestrator's first message: cadrei sent it, not the user.
const seed = "(cadrei opened this chat)"

// readOnly are the commands the orchestrator runs without asking, exactly
// these forms (the skill names them).
var readOnly = []string{"cadrei ls", "cadrei ls --json", "cadrei ls --all", "cadrei ls --json --all", "cadrei ls --all --json", "cadrei allow", "cadrei allow list",
	// It only records the user's no to the every-chat hook.
	"cadrei hook skip",
	// The listing of who's waiting: it answers nothing.
	"cadrei answer"}

// opening is what only the launch knows: where the orchestrator opens
// (tmux, tmux-inside, terminal, or hook for a chat the every-chat hook
// made one), the folder the user ran cadrei in, and its linked project.
type opening struct {
	in, from, proj string
}

// openingFromEnv is what cadrei passed the orchestrator at launch.
func openingFromEnv() opening {
	return opening{in: os.Getenv(envOpensIn), from: os.Getenv(envOpenedFrom), proj: os.Getenv(envOpenedProject)}
}

// snapshotOf builds the block for cadrei r.
func snapshotOf(rt runtime.Runtime, r *cadreis.Resolved, o opening, now time.Time) string {
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "- %s: %s\n", label, printable(value)) }
	b.WriteString("## Right now\n\n")
	by := "cadrei"
	if o.in == "hook" {
		by = "the every-chat hook"
	}
	line("Crew", r.Name+" ("+display(r.Path)+")")
	line("Opened by", by+", at "+now.Format("15:04")+" on "+now.Format("2006-01-02"))
	// Until a first member starts, so a first chat cut short (or spent
	// logging in) still gets the first-run greeting next time.
	line("First run", yesNo(cadreis.GetState(cadreis.FirstTaskPending) != ""))
	line("Orchestrator opens in", opensIn(o.in))
	line("Opened from", openedFrom(r, o))
	// Only what the snapshot prints: no projects-folder search (found), no
	// orchestrator lock or problems, and one tmux listing.
	st := struct {
		Projects []projectView
		Teams    map[string][]memberView
		Running  []sessionView
	}{projectsOf(r.Cadrei, false), teamsOf(r.Cadrei), runningOf(r.Cadrei, r.Default, session.Default())}
	addContext(st.Running)
	var projects []string
	for _, p := range st.Projects {
		desc := p.State
		if p.Team != "" {
			desc += ", team " + p.Team
		}
		projects = append(projects, p.Name+" ("+desc+")")
	}
	line("Projects", orNone(strings.Join(projects, ", ")))
	var teams []string
	names := make([]string, 0, len(st.Teams))
	for team := range st.Teams {
		names = append(names, team)
	}
	sort.Strings(names)
	for _, team := range names {
		var roles []string
		for _, m := range st.Teams[team] {
			roles = append(roles, m.Role)
		}
		teams = append(teams, team+" ("+strings.Join(roles, ", ")+")")
	}
	var running, members, stopped, waiting []string
	for _, v := range st.Running {
		label := teamLabel(v.Team, v.Project)
		running = append(running, label)
		// Each member's place and attach command, ready-made, so the
		// greeting copies them rather than build them from Opened from.
		attach := "cadrei attach " + strings.TrimSpace(v.Team+" "+v.Project)
		var parts []string
		for _, m := range v.Members {
			parts = append(parts, m.Role+" "+m.status.brief(now))
			place := "the " + m.Role + " on " + v.Project
			if v.Project == "" {
				place = "the " + m.Role + " in " + v.Team
			}
			switch m.status.State {
			case session.SafeguardStopped:
				stopped = append(stopped, place+" ("+m.Name+"), since "+clock(m.status.Since, now)+", opened with "+attach)
			case session.Waiting:
				if w := waitingIn([]sessionView{{Team: v.Team, Project: v.Project, Members: []memberView{m}}}); len(w) == 1 {
					waiting = append(waiting, w[0].note()+" Its window: "+attach)
				} else {
					waiting = append(waiting, place+", for "+waitingFor(m.status.WaitingFor)+", opened with "+attach)
				}
			}
		}
		members = append(members, label+": "+strings.Join(parts, ", "))
	}
	line("Teams", orNone(strings.Join(teams, ", "))+"; running: "+orNone(strings.Join(running, ", ")))
	line("Members", orNoneRunning(strings.Join(members, "; ")))
	if d := describe(crewChoice(r.Path, false)); d != "" {
		line("Team default", d)
	}
	line("Stopped by safeguards", orNone(strings.Join(stopped, "; ")))
	line("Waiting on the user", orNone(strings.Join(waiting, "; ")))
	line("One-time grants left over", fmt.Sprint(onceGrants(rt, r.Path)))
	line("Every-chat hook", everyChatHook(rt))
	line("Git identity", identity(r.Path))
	return b.String()
}

// waitingFor says what a waiting member waits for.
func waitingFor(what string) string {
	if what == session.ForQuestion {
		return "an answer to its question"
	}
	return "an approval"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orNoneRunning(s string) string {
	if s == "" {
		return "none running"
	}
	return s
}

// teamLabel is "dev on my-app", or "dev" for a team without a project.
func teamLabel(team, project string) string {
	if project != "" {
		return team + " on " + project
	}
	return team
}

// opensIn says where the orchestrator runs.
func opensIn(in string) string {
	switch in {
	case "tmux", "tmux-inside":
		_, short := session.Default().Prefix()
		where := "the user is not inside another tmux"
		if in == "tmux-inside" {
			where = "the user switched to it from their own tmux session"
		}
		return "tmux (prefix " + short + "; " + where + ")"
	case "hook":
		return "a chat the user started (the every-chat hook)"
	}
	return "this terminal"
}

// openedFrom says where the user ran cadrei.
func openedFrom(r *cadreis.Resolved, o opening) string {
	dir := o.from
	if p := o.proj; p != "" {
		if d, err := projectDir(r, p); err == nil {
			return "the project " + p + " (" + display(d) + ")"
		}
	}
	if dir == "" {
		return "another folder"
	}
	dir = paths.Real(dir)
	if dir == paths.Real(paths.Home()) {
		return "your home folder"
	}
	if !cadreis.Inside(dir) {
		if top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil && len(top) > 0 {
			return "a git repo that isn't a project: " + display(strings.TrimSpace(string(top)))
		}
	}
	return "another folder: " + display(dir)
}

// onceGrants counts the one-time grants left in a cadrei's grants.
func onceGrants(rt runtime.Runtime, cadrei string) int {
	if _, err := os.Stat(rt.Permissions().GrantsFile(cadrei)); err != nil {
		return 0
	}
	store, err := rt.Permissions().Open(cadrei, false)
	if err != nil {
		return 0
	}
	n := 0
	for _, g := range store.List() {
		if g.Once {
			n++
		}
	}
	return n
}

// everyChatHook is the every-chat hook's state: on, or off and why.
func everyChatHook(rt runtime.Runtime) string {
	switch s, _ := everyChat(rt); s {
	case hookOn:
		return "on"
	case hookSkipped:
		return "off (the user said no)"
	}
	return "off (never offered)"
}

// identity is "set" when git has a name and an email for dir.
func identity(dir string) string {
	if hasIdentity(dir) {
		return "set"
	}
	return "missing"
}

func hasIdentity(dir string) bool {
	for _, key := range []string{"user.name", "user.email"} {
		out, err := exec.Command("git", "-C", dir, "config", key).Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return false
		}
	}
	return true
}

// runHookSnapshot is the hidden cadrei hook snapshot: the session start
// hook in the orchestrator's settings, which gives it a fresh snapshot on
// startup, on resume and after /clear and /compact. It prints nothing it
// can't build, and never fails.
func runHookSnapshot(e *env) int {
	defer func() { recover() }()
	if inMember() || os.Getenv("CADREI_ORCHESTRATOR") == "" {
		return 0
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return 0
	}
	wd, _ := paths.Getwd()
	r, err := cadreis.Resolve(wd, nil)
	if err != nil {
		return 0
	}
	e.stdout.Write(rt.Hooks().Output(snapshotOf(rt, r, openingFromEnv(), time.Now())))
	return 0
}

// snapshotSettings writes the orchestrator's settings file, with the
// snapshot hook, when this binary may be named in a hook. Otherwise it
// returns "", and the snapshot goes into the prompt file instead (fresh
// at startup only).
func (e *env) snapshotSettings(rt runtime.Runtime, build string) string {
	bin := framework.Binary()
	if e.hookPlaced(bin) != nil {
		return ""
	}
	path, err := rt.Hooks().SnapshotSettings(build, bin)
	if err != nil {
		os.Remove(filepath.Join(build, "orchestrator-settings.json"))
		return ""
	}
	return path
}
