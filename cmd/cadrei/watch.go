//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// cadrei ls --json --watch (7.13.3, 7.13.4): the state of cadrei ls --json
// as a stream of JSON lines, a whole snapshot first and then each change.
// It reads only what cadrei ls reads, in rounds: a stat of the files that
// say what members do every round, one tmux listing every other round,
// the projects' folders now and then, and a whole snapshot every few
// minutes. It never runs the runtime, never reads a pane and never sends
// keys.

// watchTimes are the stream's timings (7.13.4); tests shorten them.
type watchTimes struct {
	round     time.Duration // a stat round
	tmux      time.Duration // one tmux listing
	projects  time.Duration // a stat of each project's folder
	heartbeat time.Duration // a line after this long with none
	periodic  time.Duration // a whole snapshot
	late      time.Duration // a round this late starts over (the machine slept)
}

var watchTiming = watchTimes{round: time.Second, tmux: 2 * time.Second, projects: 10 * time.Second,
	heartbeat: 15 * time.Second, periodic: 5 * time.Minute, late: 10 * time.Second}

// watchWakeups counts the stream's rounds, for the idle budget's test.
var watchWakeups atomic.Int64

// watchReads counts the members the stream read again, for its test.
var watchReads atomic.Int64

// watchStall, when set, is called at the start of every round (a test
// makes a round late with it).
var watchStall func()

// The stream's line types, in the order they come within a round.
const (
	lineSnapshot      = "snapshot"
	lineHeartbeat     = "heartbeat"
	lineOrchestrator  = "orchestrator"
	lineTeamStopped   = "team_stopped"
	lineMemberStopped = "member_stopped"
	lineTeamStarted   = "team_started"
	lineMemberStarted = "member_started"
	lineMemberState   = "member_state"
	lineProjectState  = "project_state"
)

var lineOrder = []string{lineOrchestrator, lineTeamStopped, lineMemberStopped, lineTeamStarted, lineMemberStarted, lineMemberState, lineProjectState}

// watchLine is one line of the stream. Project is always a project's
// name; a project_state line has the whole project in ProjectInfo.
type watchLine struct {
	Type         string            `json:"type"`
	Seq          int               `json:"seq"`
	At           time.Time         `json:"at"`
	Reason       string            `json:"reason,omitempty"`
	Status       any               `json:"status,omitempty"`
	Crew         string            `json:"crew,omitempty"`
	Session      string            `json:"session,omitempty"`
	Team         string            `json:"team,omitempty"`
	Project      string            `json:"project,omitempty"`
	ProjectInfo  *projectView      `json:"project_info,omitempty"`
	Orchestrator *orchestratorView `json:"orchestrator,omitempty"`
	Members      []memberView      `json:"members,omitempty"`
	Legacy       bool              `json:"legacy,omitempty"` // team_started: a team from cadre 0.1.x
	Member       any               `json:"member,omitempty"`
}

// stoppedMember is a member_stopped line's member: its name and role.
type stoppedMember struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// crewWatch is the stream's picture of one crew.
type crewWatch struct {
	c        cadreis.Cadrei
	gone     bool
	files    string // the crew's own files, as stats
	orch     orchestratorView
	teams    []*teamWatch // in session order, as ls --json lists them
	projects []projectView
}

// teamWatch is one running team instance.
type teamWatch struct {
	info    session.Info
	members []*memberWatch // in window order
}

// memberWatch is one running member, with what its state was read from.
type memberWatch struct {
	view       memberView // as the stream writes it: names printable
	name       string     // the member's name as tmux has it, for reads
	window     string     // its window's name as tmux has it
	stamps     string     // its state file and transcript, as stats
	transcript string
	// next is when its view can change with time alone (a safeguard
	// stop's wait ending, the no-activity note), or zero for never.
	next time.Time
	// goneAt is when its state file was first seen gone, while the
	// stream waits for a tmux listing taken after that.
	goneAt time.Time
}

// watcher is one stream.
type watcher struct {
	e     *env
	all   bool
	rt    runtime.Runtime
	t     session.Tmux
	out   io.Writer
	r     *cadreis.Resolved // the crew followed, without --all
	crews []*crewWatch
	seq   int

	lastLine, lastSnapshot, lastTmux, lastProjects time.Time
	crewList, strays                               string // --all: the crews and the sessions of none, as fingerprints
}

// errReaderGone is a write that found the reader gone: the stream ends
// quietly.
var errReaderGone = errors.New("the reader left")

func runLsWatch(e *env, all bool) (code int) {
	defer func() {
		if recover() != nil {
			code = e.failWith("cadrei ls --watch hit a problem and stopped.")
		}
	}()
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	w := &watcher{e: e, all: all, rt: rt, t: session.Default(), out: e.stdout}
	if !all {
		// The crew is found once, as cadrei ls --json finds it, and then
		// followed by its path.
		r, ok := e.resolve()
		if !ok {
			return 1
		}
		w.r = r
	}
	// SIGPIPE is noted rather than fatal, so a closed reader shows as a
	// write error, and the stream ends with 0.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	pipe := make(chan os.Signal, 1)
	signal.Notify(pipe, syscall.SIGPIPE)
	defer signal.Stop(stop)
	defer signal.Stop(pipe)

	if err := w.snapshot("start", time.Now()); err != nil {
		return w.ended(err)
	}
	ticker := time.NewTicker(watchTiming.round)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-stop:
			return 0
		case <-ticker.C:
		}
		watchWakeups.Add(1)
		if watchStall != nil {
			watchStall()
		}
		now := time.Now()
		// Late on either clock: the machine slept (the monotonic clock
		// stops then on macOS), or cadrei fell behind. Start over.
		late := now.Sub(last) > watchTiming.late || now.Round(0).Sub(last.Round(0)) > watchTiming.late
		last = now
		var err error
		switch {
		case late:
			err = w.snapshot("woke", now)
		case now.Sub(w.lastSnapshot) >= watchTiming.periodic:
			err = w.snapshot("periodic", now)
		default:
			err = w.round(now)
		}
		if err != nil {
			return w.ended(err)
		}
	}
}

// ended is how the stream ends after a write error: quietly when the
// reader left, with a message for anything else.
func (w *watcher) ended(err error) int {
	if errors.Is(err, errReaderGone) {
		return 0
	}
	return w.e.failWith("Couldn't write the stream: " + strings.TrimSuffix(err.Error(), ".") + ".")
}

// write sends one line, compact and in one write call.
func (w *watcher) write(l watchLine, now time.Time) error {
	w.seq++
	l.Seq, l.At = w.seq, now
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return err
	}
	if _, err := w.out.Write(b.Bytes()); err != nil {
		if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) {
			return errReaderGone
		}
		return err
	}
	w.lastLine = now
	return nil
}

// snapshot sends a whole snapshot and starts the picture over from it.
func (w *watcher) snapshot(reason string, now time.Time) error {
	// The stats are taken first: a change while the snapshot is built
	// shows as a change next round, never as nothing.
	var status any
	if w.all {
		st, _ := allJSON(w.t, false)
		w.crews = nil
		for i := range st.Cadreis {
			w.crews = append(w.crews, w.pictureOf(&st.Cadreis[i]))
		}
		w.crewList, w.strays = crewListPrint(), w.straysPrint(w.t.AllWindows())
		status = st
	} else {
		c := w.r.Cadrei
		var st cadreiStatus
		if _, err := os.Stat(c.Path); err != nil {
			st = goneStatus(w.r)
		} else {
			st = crewJSON(w.r, w.t, false)
		}
		cw := w.pictureOf(&st)
		cw.c = c
		w.crews = []*crewWatch{cw}
		status = st
	}
	w.lastSnapshot, w.lastTmux, w.lastProjects = now, now, now
	return w.write(watchLine{Type: lineSnapshot, Reason: reason, Status: status}, now)
}

// goneStatus is a snapshot's status for a crew whose folder is gone.
func goneStatus(r *cadreis.Resolved) cadreiStatus {
	return cadreiStatus{Version: jsonVersion, OpensIn: cadreis.OpensInTerminal, Hook: hookState(),
		Cadrei:  cadreiView{Name: r.Name, Path: r.Path, Default: r.Name == r.Default, From: r.From, Project: r.Project, Missing: true},
		Running: []sessionView{}, Projects: []projectView{}, Teams: map[string][]memberView{}, Problems: []string{}}
}

// pictureOf starts a crew's picture from what a snapshot holds.
func (w *watcher) pictureOf(st *cadreiStatus) *crewWatch {
	cw := &crewWatch{c: cadreis.Cadrei{Name: st.Cadrei.Name, Path: st.Cadrei.Path}, gone: st.Cadrei.Missing,
		orch: st.Orchestrator, projects: append([]projectView{}, st.Projects...)}
	cw.files = crewFiles(cw.c)
	for _, v := range st.Running {
		home := cw.c.Path
		if v.Legacy {
			home = ""
		}
		// The session and windows as tmux has them, so the first tmux
		// round matches a name printable changed.
		info := v.info
		if info.Name == "" {
			info = session.Info{Name: v.Session, Team: v.Team, Project: v.Project}
		}
		info.Home = home
		tw := &teamWatch{info: info}
		for _, m := range v.Members {
			mw := &memberWatch{view: m, name: m.name, window: m.window}
			if mw.name == "" {
				mw.name, mw.window = m.Name, m.Role
			}
			mw.stamps, mw.transcript = w.memberStamps(cw.c.Path, mw.name, nil)
			mw.next = w.nextChange(cw.c.Path, mw)
			tw.members = append(tw.members, mw)
		}
		cw.teams = append(cw.teams, tw)
	}
	return cw
}

// crewReadsForTests runs between a round's two reads of a crew, in tests.
var crewReadsForTests func()

// round is one round of looking: crew folders and files, the tmux listing
// when it's due, every member's state, and the projects when they're due.
// It writes what changed, in 7.13.3's order, or a heartbeat.
func (w *watcher) round(now time.Time) error {
	tmuxDue := now.Sub(w.lastTmux) >= watchTiming.tmux
	projectsDue := now.Sub(w.lastProjects) >= watchTiming.projects
	var windows []session.Window
	if tmuxDue {
		windows = w.t.AllWindows()
		w.lastTmux = now
	}
	if w.all && tmuxDue {
		// A crew made, removed or plugged in, its folder gone or back, the
		// default changed, or the sessions of no crew changed.
		if crewListPrint() != w.crewList || w.straysPrint(windows) != w.strays {
			return w.snapshot("crews_changed", now)
		}
	}
	for _, cw := range w.crews {
		// The files first, then the folder: a crew moved away between the
		// two reads is gone, never only changed.
		files := crewFiles(cw.c)
		if crewReadsForTests != nil {
			crewReadsForTests()
		}
		_, err := os.Stat(cw.c.Path)
		switch gone := err != nil; {
		case gone != cw.gone && w.all:
			return w.snapshot("crews_changed", now)
		case gone && !cw.gone:
			return w.snapshot("crew_gone", now)
		case !gone && cw.gone:
			return w.snapshot("crew_back", now)
		case gone:
			continue
		}
		if files != cw.files {
			return w.snapshot("crew_changed", now)
		}
	}
	lines := map[string][]watchLine{}
	for _, cw := range w.crews {
		if cw.gone {
			continue
		}
		if tmuxDue {
			w.teamsRound(cw, windows, now, lines)
		}
		w.membersRound(cw, now, tmuxDue, lines)
		if projectsDue {
			if changed := w.projectsRound(cw, lines); changed {
				return w.snapshot("crew_changed", now)
			}
		}
	}
	if projectsDue {
		w.lastProjects = now
	}
	wrote := false
	for _, t := range lineOrder {
		for _, l := range lines[t] {
			if err := w.write(l, now); err != nil {
				return err
			}
			wrote = true
		}
	}
	if !wrote && now.Sub(w.lastLine) >= watchTiming.heartbeat {
		return w.write(watchLine{Type: lineHeartbeat}, now)
	}
	return nil
}

// streamLine is a line about a team instance of a crew.
func streamLine(typ string, cw *crewWatch, i session.Info) watchLine {
	// Named as ls --json names them (printable), so a reader's picture
	// stays the same as ls --json.
	l := watchLine{Type: typ, Crew: printable(cw.c.Name), Session: printable(i.Name), Team: printable(i.Team)}
	if l.Team == "" {
		l.Team = printable(session.Bare(i.Name))
	}
	if i.Project != "" {
		l.Project = printable(i.Project)
	}
	return l
}

// teamsRound compares a crew's teams and orchestrator with the tmux
// listing.
func (w *watcher) teamsRound(cw *crewWatch, windows []session.Window, now time.Time, lines map[string][]watchLine) {
	def := cadreis.Default()
	scope := session.Scope{Name: cw.c.Name, Path: cw.c.Path, Default: cw.c.Name == def, T: w.t}
	// The crew's sessions, in name order, each with its windows in order.
	bySession := map[string][]session.Window{}
	var names []string
	for _, win := range windows {
		i := win.Session
		if !scope.Ours(i) || i.Role == "orchestrator" {
			continue
		}
		if _, ok := bySession[i.Name]; !ok {
			names = append(names, i.Name)
		}
		bySession[i.Name] = append(bySession[i.Name], win)
	}
	sort.Strings(names)
	if o := w.orchestratorOf(cw, windows); !sameJSON(o, cw.orch) {
		cw.orch = o
		lines[lineOrchestrator] = append(lines[lineOrchestrator], watchLine{Type: lineOrchestrator, Crew: cw.c.Name, Orchestrator: &o})
	}
	old := map[string]*teamWatch{}
	for _, tw := range cw.teams {
		old[tw.info.Name] = tw
		if _, ok := bySession[tw.info.Name]; !ok {
			lines[lineTeamStopped] = append(lines[lineTeamStopped], streamLine(lineTeamStopped, cw, tw.info))
		}
	}
	var teams []*teamWatch
	for _, name := range names {
		wins := bySession[name]
		i := wins[0].Session
		tw, ok := old[name]
		if !ok {
			tw = &teamWatch{info: i}
			l := streamLine(lineTeamStarted, cw, i)
			l.Legacy = i.Legacy()
			for _, win := range wins {
				mw := w.newMember(i, win, now)
				tw.members = append(tw.members, mw)
				l.Members = append(l.Members, mw.view)
			}
			lines[lineTeamStarted] = append(lines[lineTeamStarted], l)
			teams = append(teams, tw)
			continue
		}
		have := map[string]*memberWatch{}
		for _, mw := range tw.members {
			have[mw.window] = mw
		}
		alive := map[string]bool{}
		var members []*memberWatch
		for _, win := range wins {
			alive[win.Name] = true
			if mw, ok := have[win.Name]; ok {
				members = append(members, mw)
				continue
			}
			mw := w.newMember(i, win, time.Now())
			members = append(members, mw)
			l := streamLine(lineMemberStarted, cw, i)
			l.Member = mw.view
			lines[lineMemberStarted] = append(lines[lineMemberStarted], l)
		}
		for _, mw := range tw.members {
			if !alive[mw.window] {
				l := streamLine(lineMemberStopped, cw, tw.info)
				l.Member = stoppedMember{Name: mw.view.Name, Role: mw.view.Role}
				lines[lineMemberStopped] = append(lines[lineMemberStopped], l)
			}
		}
		tw.members = members
		teams = append(teams, tw)
	}
	cw.teams = teams
}

// newMember reads a member that just showed up.
func (w *watcher) newMember(i session.Info, win session.Window, now time.Time) *memberWatch {
	mw := &memberWatch{name: memberName(i, win), window: win.Name}
	mw.stamps, mw.transcript = w.memberStamps(i.Home, mw.name, nil)
	mw.read(i.Home, now)
	mw.next = w.nextChange(i.Home, mw)
	return mw
}

// read reads a member's view by its name as tmux has it, and names it as
// ls --json does (printable).
func (mw *memberWatch) read(home string, now time.Time) {
	mw.view.Name, mw.view.Role = mw.name, mw.window
	mw.view.read(home, now)
	mw.view.Name, mw.view.Role = printable(mw.name), printable(mw.window)
}

// membersRound reads again each member whose files changed, and each one
// whose view can change with time alone (a safeguard stop's wait, the
// no-activity note) once that time comes (7.13.4), and notes those whose
// view changed.
func (w *watcher) membersRound(cw *crewWatch, now time.Time, tmuxDue bool, lines map[string][]watchLine) {
	for _, tw := range cw.teams {
		for _, mw := range tw.members {
			stamps, transcript := w.memberStamps(tw.info.Home, mw.name, mw)
			if stamps == mw.stamps && (mw.next.IsZero() || now.Before(mw.next)) {
				continue
			}
			was := mw.view
			watchReads.Add(1)
			mw.read(tw.info.Home, now)
			// A state file that's there but doesn't parse (T-W5) keeps the
			// last value, and is read again next round. One that's gone
			// waits for a tmux listing taken after it was first seen gone:
			// a stopped member loses its state file as its window closes,
			// and is member_stopped, not unknown. A listing from earlier in
			// the same round can still hold the window that just closed.
			if mw.view.State == session.Unknown && was.State != session.Unknown {
				if unreadable(w.rt, tw.info.Home, mw.name) {
					mw.view = was
					continue
				}
				if mw.goneAt.IsZero() {
					mw.goneAt = now
				}
				if !tmuxDue || !w.lastTmux.After(mw.goneAt) {
					mw.view = was
					continue
				}
			}
			mw.goneAt = time.Time{}
			mw.stamps, mw.transcript = stamps, transcript
			mw.next = w.nextChange(tw.info.Home, mw)
			if !sameJSON(was, mw.view) {
				l := streamLine(lineMemberState, cw, tw.info)
				l.Member = mw.view
				lines[lineMemberState] = append(lines[lineMemberState], l)
			}
		}
	}
}

// nextChange is when a member's view can next change with time alone: a
// safeguard stop's wait ending, or the no-activity note for a working or
// waiting member whose transcript stays as it is; zero for never. Both
// come from what the stream already has: the safeguard cache and the
// transcript's time.
func (w *watcher) nextChange(home string, mw *memberWatch) time.Time {
	var next time.Time
	soonest := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	if home == "" || mw.transcript == "" {
		return next
	}
	if mw.view.State != session.SafeguardStopped {
		if at, stopped := session.Safeguard(w.rt.Sessions(), w.rt.BuildDir(home), mw.name, mw.transcript); stopped {
			soonest(at.Add(safeguardWait))
		}
	}
	if (mw.view.State == session.Working || mw.view.State == session.Waiting) && mw.view.NoActivitySince == nil {
		if fi, err := os.Stat(mw.transcript); err == nil {
			soonest(fi.ModTime().Add(quietAfter))
		}
	}
	return next
}

// memberStamps are a member's state file, record and transcript, as stats,
// and its transcript. The transcript's path is read again only when the
// state file or the record changed since prev: a member gets a transcript
// with its first prompt, which changes its state file too.
func (w *watcher) memberStamps(home, name string, prev *memberWatch) (stamps, transcript string) {
	if home == "" {
		return "", ""
	}
	build := w.rt.BuildDir(home)
	stateFile, record := session.StatePath(build, name), session.RecordPath(build, name)
	head := stat(stateFile) + stat(record)
	// A member finds its transcript through its state file, or through its
	// record once the runtime writes it, which changes neither file: while
	// it has none, whatever its state, it's looked for every round.
	if prev != nil && prev.transcript != "" && strings.HasPrefix(prev.stamps, head) {
		transcript = prev.transcript
	} else if st := session.ReadState(stateFile); st != nil && st.Transcript != "" {
		transcript = st.Transcript
	} else if rec := session.ReadRecord(record); rec != nil {
		transcript = w.rt.Sessions().TranscriptFile(rec.ID, rec.Dir)
	}
	return head + "|" + stat(transcript), transcript
}

// unreadable reports whether a member's state file is there but can't be
// read.
func unreadable(rt runtime.Runtime, home, name string) bool {
	path := session.StatePath(rt.BuildDir(home), name)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return session.ReadState(path) == nil
}

// projectsRound looks at each project's folder again. It reports true
// when the projects themselves changed (one added or removed), which takes
// a snapshot.
func (w *watcher) projectsRound(cw *crewWatch, lines map[string][]watchLine) bool {
	reg, err := registry.Load(cw.c.Registry())
	if err != nil {
		return false
	}
	entries := reg.Entries()
	if len(entries) != len(cw.projects) {
		return true
	}
	for k, entry := range entries {
		p := &cw.projects[k]
		if p.Name != entry.Name {
			return true
		}
		d := cadreis.ProjectDir(cw.c, entry)
		state := cadreis.Where(d)
		if state == p.State && d == p.Path {
			continue
		}
		p.State, p.Path, p.Cloned = state, d, state == "present"
		if p.Cloned {
			p.Found = ""
		}
		v := *p
		lines[lineProjectState] = append(lines[lineProjectState], watchLine{Type: lineProjectState, Crew: cw.c.Name, Project: v.Name, ProjectInfo: &v})
	}
	return false
}

// orchestratorOf is a crew's orchestrator, as orchestratorOf has it, from
// its lock and the tmux listing.
func (w *watcher) orchestratorOf(cw *crewWatch, windows []session.Window) orchestratorView {
	name := orchestrator.SessionName(cw.c.Name)
	var ours bool
	pane := 0
	for _, win := range windows {
		if win.Session.Name == name && win.Session.Role == "orchestrator" && win.Session.Home == cw.c.Path {
			ours = true
			if win.Name == "orchestrator" {
				pane = win.PanePID
			}
		}
	}
	if l := orchestrator.ReadLock(orchestrator.LockPath(w.rt.BuildDir(cw.c.Path))); l != nil {
		since := l.Since
		switch {
		case l.Mode != orchestrator.Tmux:
			return orchestratorView{Running: true, Mode: l.Mode, Session: l.Session, Since: &since}
		case l.Session == name && ours && (l.PID <= 0 || pane == l.PID):
			return orchestratorView{Running: true, Mode: l.Mode, Session: l.Session, Since: &since}
		}
	}
	if ours {
		return orchestratorView{Running: true, Mode: orchestrator.Tmux, Session: name}
	}
	return orchestratorView{}
}

// crewFiles is a crew's own files, as stats: its member files and team
// folders, its projects, and its settings. A change takes a snapshot.
func crewFiles(c cadreis.Cadrei) string {
	var b strings.Builder
	members := filepath.Join(c.Path, "members")
	b.WriteString(stat(members))
	teams, _ := os.ReadDir(members)
	for _, t := range teams {
		dir := filepath.Join(members, t.Name())
		b.WriteString(stat(dir))
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			b.WriteString(stat(filepath.Join(dir, f.Name())))
		}
	}
	for _, f := range []string{c.Registry(), cadreis.PlacesFile(c), filepath.Join(c.Path, "cadrei.conf"), filepath.Join(cadreis.LocalDir(c.Path), "cadrei.conf")} {
		b.WriteString(stat(f))
	}
	return b.String()
}

// crewListPrint is the crews on this machine and the default, for --all.
func crewListPrint() string {
	list, _ := cadreis.List()
	var b strings.Builder
	b.WriteString(cadreis.Default())
	for _, c := range list {
		fmt.Fprintf(&b, "|%s=%s %v", c.Name, c.Path, c.Present())
	}
	return b.String()
}

// straysPrint is the sessions of no crew (0.1.x ones, unmarked ones, and
// those of crews cadrei doesn't know), for --all, whose changes take a
// snapshot.
func (w *watcher) straysPrint(windows []session.Window) string {
	known := map[string]bool{}
	list, _ := cadreis.List()
	for _, c := range list {
		known[c.Path] = true
	}
	var b strings.Builder
	for _, win := range windows {
		i := win.Session
		if i.Role != "orchestrator" && (i.Legacy() || !known[i.Home]) {
			fmt.Fprintf(&b, "%s/%s=%s;", i.Name, win.Name, win.Member)
		}
	}
	return b.String()
}

// stat is a file's time and size, or nothing for no file.
func stat(path string) string {
	if path == "" {
		return "-"
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d:%d;", fi.ModTime().UnixNano(), fi.Size())
}

// sameJSON reports whether two values give the same JSON.
func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
