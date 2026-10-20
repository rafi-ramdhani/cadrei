//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/conf"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The data of cadrei ls, which --json prints as it is, so the orchestrator
// never reads the human layout. Its shape is versioned: a field is added
// under the same version, and anything else changes the version.

// jsonVersion is the version of ls --json's shape.
const jsonVersion = 1

type cadreiView struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	From    string `json:"from,omitempty"`    // how this command found it
	Project string `json:"project,omitempty"` // the linked project it was found from
	Default bool   `json:"default"`
	Missing bool   `json:"missing,omitempty"`
	// The crew's model and effort for its members (7.11), when it sets
	// them.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type memberView struct {
	Name string `json:"name"` // the session name, the member's messaging address
	Role string `json:"role"`
	// What a running member is doing: starting, working, waiting, idle,
	// unknown or stopped_by_safeguards; since when; and for waiting, what
	// for (approval or question).
	State      string     `json:"state,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	WaitingFor string     `json:"waiting_for,omitempty"`
	// For working or waiting: when its transcript last changed, once that's
	// 10 minutes or more ago. For idle: its last turn ended in an error.
	NoActivitySince *time.Time `json:"no_activity_since,omitempty"`
	TurnError       bool       `json:"turn_error,omitempty"`
	// While it waits: what it waits on the user for, as the runtime
	// recorded it, with the code cadrei answer takes. Never consent.
	Request *requestView `json:"request,omitempty"`
	// How full its context is, when its transcript says.
	Context *contextView `json:"context,omitempty"`
	// The model and effort cadrei passed this member's start, and where
	// each was set: start, member, machine or crew (7.11.4).
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	ModelFrom  string `json:"model_from,omitempty"`
	EffortFrom string `json:"effort_from,omitempty"`

	status memberStatus // the same, for the human screens
	// The member and window names as tmux has them, before printable:
	// what the stream reads files by and matches windows with.
	name, window string
}

// fill sets a member's JSON state fields from its status.
func (m *memberView) fill() {
	m.State, m.WaitingFor = m.status.State, m.status.WaitingFor
	m.Since, m.NoActivitySince = nil, nil
	if !m.status.Since.IsZero() {
		since := m.status.Since
		m.Since = &since
	}
	if !m.status.QuietSince.IsZero() {
		quiet := m.status.QuietSince
		m.NoActivitySince = &quiet
	}
	m.TurnError = m.status.Error && m.status.State == session.Idle
	m.Request = nil
	if r := m.status.Request; r != nil {
		m.Request = &requestView{Code: r.Code, Kind: r.Kind, Tool: r.Tool, Summary: r.Summary, Options: r.Options, Multi: r.Multi}
		if r.Kind == session.ForQuestion || r.Kind == session.ForPrompt {
			m.Request.Summary = runtime.RequestText(r.Question, runtime.SummaryLimit)
		}
	}
	m.Context = nil
	if c := m.status.Context; m.status.HasContext {
		m.Context = &contextView{}
		if !c.Compacted.IsZero() {
			at := c.Compacted
			m.Context.Compacted = &at
		} else {
			at := c.At
			m.Context.Tokens, m.Context.Window, m.Context.At = c.Tokens, c.Window, &at
			if p := c.Percent(); p >= 0 {
				m.Context.Percent = &p
			}
		}
	}
}

// contextView is how full a member's context is, from its transcript:
// tokens is the size of its last reply's request, at when that reply
// came. window and percent are there only when its model's window is
// certain. compacted, alone, says it was compacted and hasn't replied
// since.
type contextView struct {
	Tokens    int        `json:"tokens,omitempty"`
	Window    int        `json:"window,omitempty"`
	Percent   *int       `json:"percent,omitempty"`
	At        *time.Time `json:"at,omitempty"`
	Compacted *time.Time `json:"compacted,omitempty"`
}

// requestView is a waiting member's request in ls --json: kind is approval
// or question (an open list: any other means answer it in its window);
// summary is the command, path or URL, or a question's text; options are a
// question's labels; multi means only its window can answer it.
type requestView struct {
	Code    string   `json:"code"`
	Kind    string   `json:"kind"`
	Tool    string   `json:"tool,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Options []string `json:"options,omitempty"`
	Multi   bool     `json:"multi,omitempty"`
}

// choice is the model and effort cadrei passed the member.
func (m memberView) choice() session.Choice {
	return session.Choice{Model: session.Setting{Value: m.Model, From: m.ModelFrom}, Effort: session.Setting{Value: m.Effort, From: m.EffortFrom}}
}

type sessionView struct {
	Session string       `json:"session"` // the tmux session
	Team    string       `json:"team"`
	Project string       `json:"project,omitempty"`
	Legacy  bool         `json:"legacy"` // from before session names carried the cadrei
	Members []memberView `json:"members"`
	info    session.Info // the session as tmux has it
}

// A project's state on this machine: "present"; "not here" (no folder
// recorded on this machine); "missing" (its folder is gone); "drive" (on a
// drive that is not connected). Missing projects are never unlinked by
// cadrei: the orchestrator offers to clone (project sync), link (project
// link, with found as a suggestion) or unlink.
type projectView struct {
	Name   string `json:"name"`
	Repo   string `json:"repo,omitempty"`
	Team   string `json:"team,omitempty"`
	About  string `json:"about,omitempty"`
	Path   string `json:"path"` // "" when not here
	Cloned bool   `json:"cloned"`
	State  string `json:"state"`
	Found  string `json:"found,omitempty"` // a clone of its repo in the projects folder, for a project not present
}

type otherView struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Running int    `json:"running"`
	Missing bool   `json:"missing,omitempty"`
}

type orchestratorView struct {
	Running bool       `json:"running"`
	Mode    string     `json:"mode,omitempty"`    // "terminal" or "tmux"
	Session string     `json:"session,omitempty"` // its tmux session, in tmux mode
	Since   *time.Time `json:"since,omitempty"`   // when cadrei opened it
}

type cadreiStatus struct {
	Version      int                     `json:"version,omitempty"`               // set on the top-level object
	OpensIn      string                  `json:"orchestrator_opens_in,omitempty"` // tmux or terminal, on the top-level object
	Hook         string                  `json:"hook,omitempty"`                  // the every-chat hook: on, off or skipped, on the top-level object
	Cadrei       cadreiView              `json:"cadrei"`
	Orchestrator orchestratorView        `json:"orchestrator"`
	Running      []sessionView           `json:"running"`
	Projects     []projectView           `json:"projects"`
	Teams        map[string][]memberView `json:"teams"` // every member, running or not
	Others       []otherView             `json:"other_cadreis,omitempty"`
	Problems     []string                `json:"problems"` // what keeps members from starting, and registry entries left out
}

type allStatus struct {
	Version int            `json:"version"`
	OpensIn string         `json:"orchestrator_opens_in"`
	Hook    string         `json:"hook"`
	Cadreis []cadreiStatus `json:"cadreis"`
	Legacy  []sessionView  `json:"legacy"`
	Unknown []sessionView  `json:"unknown"`
}

// problemsOf says what keeps every member of a cadrei from starting: the
// runtime is missing, or cannot run the cadrei's permission mode.
func problemsOf(c cadreis.Cadrei) []string {
	out := problemsOfCrew(c)
	// They name folders and files, which may hold anything.
	for i := range out {
		out[i] = printable(out[i])
	}
	return out
}

// problemsOfCrew is problemsOf, as written.
func problemsOfCrew(c cadreis.Cadrei) []string {
	if why := cadreis.NewerFormat(c.Path); why != "" {
		return []string{why + " " + cadreis.UpgradeFix}
	}
	out := append([]string{}, skippedEntries(c)...)
	out = append(out, unstartable(c)...)
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return append(out, err.Error())
	}
	if _, err := rt.Detect(); err != nil {
		return append(out, err.Error())
	}
	raw, _ := os.ReadFile(filepath.Join(c.Path, "cadrei.conf"))
	values, _ := conf.Parse(string(raw))
	if err := runtime.Usable(rt, modeOf(values)); err != nil {
		return append(out, err.Error())
	}
	return out
}

// skippedEntries names the registry entries cadrei leaves out because
// their name is not a project name (one that climbs out of the projects
// folder, such as ../x, or nests, such as a/b).
func skippedEntries(c cadreis.Cadrei) []string {
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range reg.Skipped() {
		out = append(out, fmt.Sprintf("projects.yaml has an entry named %q, which can't be a project name (letters, digits, ., - and _). It's left out until you rename or remove it", name))
	}
	return out
}

func viewOf(t session.Tmux, i session.Info, look bool) sessionView {
	recorded := t.WindowMembers(i.Name)
	var windows []session.Window
	for _, w := range t.Windows(i.Name) {
		windows = append(windows, session.Window{Session: i, Name: w, Member: recorded[w]})
	}
	return viewFrom(t, look, i, windows, time.Now())
}

// viewFrom is a running team's view from its windows, in tmux t. With
// look, a member still starting has its screen looked at first
// (lookAtStart).
func viewFrom(t session.Tmux, look bool, i session.Info, windows []session.Window, now time.Time) sessionView {
	v := sessionView{Session: printable(i.Name), Team: printable(i.Team), Project: printable(i.Project), Legacy: i.Legacy(), Members: []memberView{}, info: i}
	if v.Team == "" {
		// A 0.1.x session recorded no team: show its key.
		v.Team = printable(session.Bare(i.Name))
	}
	rt, _ := runtime.Get(runtimeName())
	for _, w := range windows {
		m := memberView{Name: memberName(i, w), Role: w.Name, name: memberName(i, w), window: w.Name}
		if look && rt != nil && i.Home != "" {
			lookAtStart(rt, t, rt.BuildDir(i.Home), m.name, i.Name, w.Name)
		}
		m.read(i.Home, now)
		m.Name, m.Role = printable(m.Name), printable(m.Role)
		v.Members = append(v.Members, m)
	}
	return v
}

// memberName is a window's member: the name recorded on it, else the one
// up gives it (the session name turns a project's "." into "_", the
// member's doesn't).
func memberName(i session.Info, w session.Window) string {
	switch {
	case w.Member != "":
		return w.Member
	case i.Home != "" && i.Team != "":
		return session.MemberName(filepath.Base(i.Home), session.Key(i.Team, i.Project), w.Name)
	}
	return session.Bare(i.Name) + "-" + w.Name
}

// read fills a running member's status and model from its crew at home.
func (m *memberView) read(home string, now time.Time) {
	m.status = memberStatus{State: session.Unknown}
	if rt, err := runtime.Get(runtimeName()); err == nil && home != "" {
		m.status = statusOfMember(rt, home, m.Name, now, true)
		if rec := session.ReadRecord(session.RecordPath(rt.BuildDir(home), m.Name)); rec != nil {
			m.Model, m.Effort, m.ModelFrom, m.EffortFrom = rec.Model, rec.Effort, rec.ModelFrom, rec.EffortFrom
		}
	}
	m.fill()
}

// hookState is the every-chat hook's state for ls --json: on, off, or
// skipped (the user said no).
func hookState() string {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return hookOff
	}
	s, _ := everyChat(rt)
	return s
}

// The every-chat hook's states: on, off, or off because the user said no.
const (
	hookOn      = "on"
	hookOff     = "off"
	hookSkipped = "skipped"
)

// everyChat is the every-chat hook's state, the one source for ls, the
// snapshot and cadrei hook.
func everyChat(rt runtime.Runtime) (string, error) {
	progs, err := rt.Hooks().Find()
	switch {
	case err == nil && len(progs) > 0:
		return hookOn, nil
	case cadreis.GetState(cadreis.HookOffer) == hookSkipped:
		return hookSkipped, err
	}
	return hookOff, err
}

// statusOf gathers one cadrei's status, with how full each running
// member's context is when context is set: ls --watch leaves it out.
func statusOf(c cadreis.Cadrei, def string, t session.Tmux, context bool) cadreiStatus {
	s := cadreiStatus{Cadrei: cadreiView{Name: printable(c.Name), Path: c.Path, Default: c.Name == def, Missing: !c.Present()},
		Running: []sessionView{}, Projects: []projectView{}, Teams: map[string][]memberView{}, Problems: problemsOf(c)}
	scope := session.Scope{Name: c.Name, Path: c.Path, Default: c.Name == def, T: t}
	s.Orchestrator = orchestratorOf(c, t)
	teamDefault := crewChoice(c.Path, false)
	s.Cadrei.Model, s.Cadrei.Effort = teamDefault.Model.Value, teamDefault.Effort.Value
	for _, i := range scope.Running() {
		// The stream (no context) never reads a member's screen.
		s.Running = append(s.Running, viewOf(t, i, context))
	}
	if context {
		addContext(s.Running)
	}
	if p := projectsOf(c, true); p != nil {
		s.Projects = p
	}
	s.Teams = teamsOf(c)
	return s
}

// addContext adds how full each running member's context is, for cadrei
// ls and the snapshot (withContext).
func addContext(views []sessionView) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return
	}
	for i := range views {
		home := views[i].info.Home
		if home == "" {
			continue
		}
		for j := range views[i].Members {
			m := &views[i].Members[j]
			m.status = withContext(rt, home, m.Name, m.status)
			m.fill()
		}
	}
}

// teamsOf is a crew's teams and their members, from its members folder.
func teamsOf(c cadreis.Cadrei) map[string][]memberView {
	out := map[string][]memberView{}
	teams, _ := filepath.Glob(filepath.Join(c.Path, "members", "*"))
	for _, d := range teams {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			team := filepath.Base(d)
			shown := printable(team)
			out[shown] = []memberView{}
			for _, role := range session.Roles(c.Path, team) {
				out[shown] = append(out[shown], memberView{Name: printable(session.MemberName(c.Name, team, role)), Role: printable(role)})
			}
		}
	}
	return out
}

// runningOf is only the running teams of statusOf, for the per-prompt
// team status hook and the snapshot: no orchestrator lock, projects or
// problems, and one tmux listing whatever the number of teams.
func runningOf(c cadreis.Cadrei, def string, t session.Tmux) []sessionView {
	scope := session.Scope{Name: c.Name, Path: c.Path, Default: c.Name == def, T: t}
	byTeam := map[string][]session.Window{}
	var teams []session.Info
	for _, w := range t.AllWindows() {
		if i := w.Session; scope.Ours(i) && i.Role != "orchestrator" {
			if _, ok := byTeam[i.Name]; !ok {
				teams = append(teams, i)
			}
			byTeam[i.Name] = append(byTeam[i.Name], w)
		}
	}
	sort.Slice(teams, func(a, b int) bool { return teams[a].Name < teams[b].Name })
	now := time.Now()
	var out []sessionView
	for _, i := range teams {
		out = append(out, viewFrom(t, true, i, byTeam[i.Name], now))
	}
	return out
}

// orchestratorOf says whether a cadrei's orchestrator is open: its lock
// (plain cadrei or cadrei --tmux), or its tmux session.
func orchestratorOf(c cadreis.Cadrei, t session.Tmux) orchestratorView {
	if rt, err := runtime.Get(runtimeName()); err == nil {
		if l := openOrchestrator(t, c.Name, c.Path, orchestrator.LockPath(rt.BuildDir(c.Path)), false); l != nil {
			since := l.Since
			return orchestratorView{Running: true, Mode: l.Mode, Session: l.Session, Since: &since}
		}
	}
	name := orchestrator.SessionName(c.Name)
	if t.Has(name) && t.Option(name, "@cadrei_role") == "orchestrator" && t.Option(name, "@cadrei_home") == c.Path {
		return orchestratorView{Running: true, Mode: orchestrator.Tmux, Session: name}
	}
	return orchestratorView{}
}

func runLs(e *env) int {
	all, asJSON, watch, pinned := false, false, false, false
	for i := 0; i < len(e.args); i++ {
		switch a := e.args[i]; a {
		case "--all":
			all = true
		case "--json":
			asJSON = true
		case "--watch":
			watch = true
		case "--json-version":
			// A tool pins the version it reads. Only 1 exists; a later
			// cadrei keeps printing 1 when asked for it.
			if i+1 >= len(e.args) || e.args[i+1] != "1" {
				return e.failWith("This cadrei prints version 1 of its JSON only: use --json-version 1.")
			}
			pinned = true
			i++
		default:
			return e.failWith("Use " + cmd("cadrei ls") + ", with --all or --json if you like.")
		}
	}
	if pinned && !asJSON {
		return e.failWith("--json-version only works with --json.")
	}
	// Output for programs never asks which crew (7.13.1, T-W7).
	e.noAsk = asJSON
	if watch {
		if !asJSON {
			return e.failWith("--watch only works with --json. It's for programs that read cadrei's state.", "To look now, run "+cmd("cadrei ls")+".")
		}
		return runLsWatch(e, all)
	}
	t := session.Default()
	def := cadreis.Default()
	list, _ := cadreis.List()
	if all {
		st, unmarked := allJSON(t, true)
		if asJSON {
			return e.printJSON(st)
		}
		// This cadrei's screen, then the others in a line each.
		var here *cadreiStatus
		wd, _ := paths.Getwd()
		if r, err := cadreis.Resolve(wd, nil); err == nil {
			for i := range st.Cadreis {
				if st.Cadreis[i].Cadrei.Path == r.Path {
					here = &st.Cadreis[i]
					here.Cadrei.From = r.From
				}
			}
		}
		if here == nil && len(st.Cadreis) > 0 {
			here = &st.Cadreis[0]
		}
		if here != nil {
			e.printStatus(*here, len(list) > 1)
		}
		var others []otherView
		for _, c := range list {
			if here != nil && c.Path == here.Cadrei.Path {
				continue
			}
			others = append(others, otherView{Name: c.Name, Path: c.Path, Missing: !c.Present(),
				Running: len(session.Scope{Name: c.Name, Path: c.Path, Default: c.Name == def, T: t}.Running())})
		}
		e.printOthers(others)
		e.printSessions("Teams from cadre 0.1.x (they belong to your default crew)", st.Legacy)
		var lost, unmarkedList []sessionView
		for _, u := range st.Unknown {
			if unmarked[u.Session] {
				unmarkedList = append(unmarkedList, u)
			} else {
				lost = append(lost, u)
			}
		}
		e.printSessions("Teams of a crew cadrei doesn't know anymore", lost)
		e.printSessions("tmux sessions cadrei didn't start", unmarkedList)
		return 0
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	if !asJSON {
		e.conf(r) // a status screen says when cadrei.conf has lines it ignores
	}
	st := crewJSON(r, t, true)
	if asJSON {
		return e.printJSON(st)
	}
	// Sessions without markers belong to no cadrei, so only ls --all lists
	// them; this screen points there.
	unmarked := 0
	for _, i := range session.All(t, nil) {
		if i.Home == "" && !i.Legacy() {
			unmarked++
		}
	}
	switch {
	case unmarked == 1:
		st.Problems = append(st.Problems, "There's 1 tmux session cadrei didn't start. See it with "+cmd("cadrei ls --all"))
	case unmarked > 1:
		st.Problems = append(st.Problems, fmt.Sprintf("There are %d tmux sessions cadrei didn't start. See them with %s", unmarked, cmd("cadrei ls --all")))
	}
	e.printStatus(st, len(list) > 1)
	return 0
}

// crewJSON is cadrei ls --json for the crew r, with context as statusOf
// has it.
func crewJSON(r *cadreis.Resolved, t session.Tmux, context bool) cadreiStatus {
	st := statusOf(r.Cadrei, r.Default, t, context)
	st.Version, st.Hook = jsonVersion, hookState()
	st.OpensIn, _ = cadreis.OpensIn(r.Cadrei)
	st.Cadrei.From, st.Cadrei.Project = r.From, r.Project
	list, _ := cadreis.List()
	for _, c := range list {
		if c.Path == r.Path {
			continue
		}
		o := otherView{Name: c.Name, Path: c.Path, Missing: !c.Present()}
		o.Running = len(session.Scope{Name: c.Name, Path: c.Path, Default: c.Name == r.Default, T: t}.Running())
		st.Others = append(st.Others, o)
	}
	return st
}

// allJSON is cadrei ls --json --all, and the sessions in it without
// cadrei's markers, with context as statusOf has it.
func allJSON(t session.Tmux, context bool) (allStatus, map[string]bool) {
	def := cadreis.Default()
	list, _ := cadreis.List()
	st := allStatus{Version: jsonVersion, OpensIn: opensInOf(def), Hook: hookState(), Cadreis: []cadreiStatus{}, Legacy: []sessionView{}, Unknown: []sessionView{}}
	known := map[string]bool{}
	for _, c := range list {
		known[c.Path] = true
		if c.Present() {
			st.Cadreis = append(st.Cadreis, statusOf(c, def, t, context))
		}
	}
	// A cadrei- session without markers (made by hand, or a start that
	// died early) is not legacy: it is listed with the unknown ones.
	unmarked := map[string]bool{}
	for _, i := range session.All(t, nil) {
		switch {
		case i.Legacy():
			st.Legacy = append(st.Legacy, viewOf(t, i, false))
		case i.Home == "":
			unmarked[i.Name] = true
			st.Unknown = append(st.Unknown, viewOf(t, i, false))
		case !known[i.Home]:
			st.Unknown = append(st.Unknown, viewOf(t, i, false))
		}
	}
	return st, unmarked
}

func (e *env) printJSON(v any) int {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return e.fail("%s", err)
	}
	return 0
}

// unstartable names the teams and members whose names cannot be part of a
// session name (0.1.x allowed dots, as in ml.ops): cadrei up refuses them,
// so the orchestrator should not offer to start them.
func unstartable(c cadreis.Cadrei) []string {
	var out []string
	teams, _ := os.ReadDir(filepath.Join(c.Path, "members"))
	for _, t := range teams {
		if !t.IsDir() || strings.HasPrefix(t.Name(), ".") {
			continue
		}
		if !session.CheckName(t.Name()) {
			out = append(out, fmt.Sprintf("The team %s can't start: %s. Rename its folder, members/%s", t.Name(), session.NameRule, t.Name()))
			continue
		}
		for _, role := range session.Roles(c.Path, t.Name()) {
			if !session.CheckName(role) {
				out = append(out, fmt.Sprintf("The %s in %s can't start: %s. Rename its file, members/%s/%s.md", role, t.Name(), session.NameRule, t.Name(), role))
			}
		}
	}
	return out
}

// opensInOf is where the named cadrei's orchestrator opens. With no such
// crew (none at all, say) it opens nowhere else than the terminal, as the
// stream says too: the contract's values are tmux and terminal only.
func opensInOf(name string) string {
	c, ok := cadreis.Find(name)
	if !ok {
		return cadreis.OpensInTerminal
	}
	where, _ := cadreis.OpensIn(c)
	return where
}
