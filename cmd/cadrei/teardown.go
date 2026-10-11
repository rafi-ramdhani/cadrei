//go:build !windows

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// teardown is what both uninstalls stop and take out before anything else
// moves: cadrei's sessions, its instructions link and every-chat hook where
// they point at this cadrei, and the crews' pre-push checks. Sessions
// started by 0.1.x are not this cadrei's to stop: on a machine that runs
// 0.1.x too, they are the user's live sessions. Nor are sessions without
// cadrei's markers, which cadrei did not make in full, or another setup's
// (8.3.5).
type teardown struct {
	rt  runtime.Runtime
	t   session.Tmux
	bin string
	// sessions are the teams' sessions to stop, named in plans by team
	// (teams: dev on my-app) or by member (members: main-dev-engineer).
	sessions, teams, members []string
	orchestrators            []string // the crews' orchestrators in tmux
	legacy, unmarked, others []string // sessions left running, as plans name them
	skillOurs                bool     // the instructions link points at this cadrei
	hookOurs, hookOthers     bool     // the every-chat hook runs this cadrei, or another one
	guarded                  []cadreis.Cadrei
	// unlisted is tmux's error when it couldn't list the sessions, so the
	// plan can't know which teams run: nothing is moved or deleted then.
	unlisted error
	// elsewhere are the crews' teams on another tmux server than this
	// command's (servers): it can't stop them, so nothing goes ahead.
	elsewhere []string
}

// planTeardown finds what a teardown of the crews in list would do.
func planTeardown(rt runtime.Runtime, list []cadreis.Cadrei) teardown {
	td := teardown{rt: rt, t: session.Default(), bin: framework.Binary()}
	known := knownCadreis()
	all, err := td.t.List()
	td.unlisted = err
	for _, i := range session.Members(all, known) {
		switch {
		case i.Legacy():
			td.legacy = append(td.legacy, i.Name)
		case i.Home == "":
			td.unmarked = append(td.unmarked, i.Name)
		case known[i.Home] == "":
			td.others = append(td.others, runningLabel(i)+" in the crew "+printable(filepath.Base(i.Home)))
		default:
			td.sessions = append(td.sessions, i.Name)
			td.teams = append(td.teams, runningLabel(i))
			td.members = append(td.members, td.t.MemberNames(i)...)
		}
	}
	for _, i := range ofCrews(all, known, false) {
		if i.Role == "orchestrator" {
			td.orchestrators = append(td.orchestrators, i.Name)
		}
	}
	// The environment may name another server than the teams run on.
	for _, s := range servers(list)[1:] {
		there, err := s.List()
		if err != nil && td.unlisted == nil {
			td.unlisted = fmt.Errorf("%s (%s)", err, s.Path())
		}
		for _, i := range ofCrews(there, known, false) {
			td.elsewhere = append(td.elsewhere, printable(i.Name)+" ("+printable(s.Path())+")")
		}
	}
	if target, err := rt.Instructions().Target(); err == nil && paths.Real(target) == paths.Real(framework.SkillDir()) {
		td.skillOurs = true
	}
	if progs, err := rt.Hooks().Find(); err == nil {
		for _, p := range progs {
			if paths.Real(p) == paths.Real(td.bin) {
				td.hookOurs = true
			} else {
				td.hookOthers = true
			}
		}
	}
	for _, c := range list {
		if backup.Installed(c.Path) {
			td.guarded = append(td.guarded, c)
		}
	}
	return td
}

// unlistedRefusal says why nothing goes ahead when tmux couldn't list the
// sessions, and is "" when it could.
func (td teardown) unlistedRefusal() string {
	switch {
	case td.unlisted != nil:
		return "Couldn't see which teams run: tmux says " + strings.TrimSuffix(printable(td.unlisted.Error()), ".") + "."
	case len(td.elsewhere) > 0:
		return "Your crews' sessions run on another tmux server than this command uses, so it can't stop them: " + strings.Join(td.elsewhere, ", ") + "."
	}
	return ""
}

// refuseUnlisted stops a teardown that can't see which teams run: a team
// that still runs would go on writing into a crew that's gone.
func (td teardown) refuseUnlisted(e *env) int {
	return e.failWith(td.unlistedRefusal()+" Nothing was changed, since a team that still runs would go on writing into its crew.",
		"Check that tmux works ("+cmd("tmux ls")+"), and run this where your teams were started, without another "+cmd("CADREI_TMUX_SOCKET")+" or "+cmd("TMUX_TMPDIR")+". Then run this again.")
}

// linkSteps writes the plan's lines for the link, the hook and the
// pre-push checks.
func (td teardown) linkSteps(step func(string)) {
	if td.skillOurs {
		step("remove cadrei's instructions link (" + unbroken(cadreis.Tilde(td.rt.Instructions().Path())) + ")")
	}
	if td.hookOurs {
		step("remove the every-chat hook from " + unbroken(cadreis.Tilde(td.rt.Hooks().File())) + " (your other hooks stay)")
	}
	if len(td.guarded) > 0 {
		var names []string
		for _, c := range td.guarded {
			names = append(names, c.Name)
		}
		step("remove cadrei's pre-push check from " + andList(names))
	}
}

// stop stops the teams and then the orchestrators, but the session this
// command runs in, which the caller stops last (Stopping.Last). A session
// that won't stop is in the result's Failed, and warned about when warn is
// set (a caller that stops on it names it itself).
func (td teardown) stop(e *env, w io.Writer, warn bool) *session.Stopping {
	report := e.stopReporter(nil)
	st := &session.Stopping{T: td.t, Report: func(name string, stopped, last bool) {
		if stopped || warn {
			report(name, stopped, last)
		}
	}}
	st.Stop(w, td.sessions)
	st.Stop(w, td.orchestrators)
	return st
}

// unlink takes out the link, the hook and the pre-push checks. It says
// "Removed cadrei's links and hooks" when it took any out, and returns
// what it couldn't, each as a line for a warning.
func (td teardown) unlink(e *env) []string {
	var failed []string
	removed := false
	did := func(changed bool, err error, what string) {
		switch {
		case err != nil:
			failed = append(failed, "Couldn't "+what+": "+strings.TrimSuffix(err.Error(), ".")+".")
		case changed:
			removed = true
		}
	}
	if td.skillOurs {
		changed, err := td.rt.Instructions().Unlink(framework.SkillDir())
		did(changed, err, "remove cadrei's instructions link")
	}
	if td.hookOurs {
		changed, err := td.rt.Hooks().Remove(td.bin)
		did(changed, err, "remove the every-chat hook")
	}
	for _, c := range td.guarded {
		changed, err := backup.Remove(c.Path)
		did(changed, err, "remove the pre-push check from "+c.Name)
	}
	if removed {
		e.note("", markDone, "Removed cadrei's links and hooks")
	}
	return failed
}
