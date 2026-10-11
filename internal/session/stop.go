package session

import (
	"fmt"
	"io"
	"os"
)

// StopResult is what stopping a team or a member did.
type StopResult struct {
	Stopped  bool
	Unmarked string // a session without cadrei's markers holds the name, and why it was left
	Instead  string // another team runs under the session name, and was left as it is
}

// StopTeam stops the session that runs key for this cadrei (its own, else
// a legacy one), and says so when a session without cadrei's markers holds
// the name.
func (s Scope) StopTeam(team, project string) StopResult {
	if live := s.Live(team, project); live != "" && s.T.KillSession(live) == nil {
		return StopResult{Stopped: true}
	}
	if m := s.Unmarked(team, project); m != "" {
		return StopResult{Unmarked: m}
	}
	return StopResult{Instead: s.Instead(team, project)}
}

// StopRole stops one member: its window in this cadrei's session, or in a
// legacy one.
func (s Scope) StopRole(team, project, role string) StopResult {
	key := Key(team, project)
	for _, session := range []string{s.mine(team, project), s.legacy(key)} {
		if session != "" && s.T.KillWindow(session, role) == nil {
			return StopResult{Stopped: true}
		}
	}
	if m := s.Unmarked(team, project); m != "" {
		return StopResult{Unmarked: m}
	}
	return StopResult{Instead: s.Instead(team, project)}
}

// Stopping stops a list of sessions, leaving the one this command runs in
// for Last, so the caller can print its summary first.
type Stopping struct {
	T      Tmux
	own    string
	Failed []string
	// Report, when set, is told about each session in place of the plain
	// lines: stopped or not, and last for the command's own.
	Report func(session string, stopped, last bool)
}

// Stop stops every session in names except the command's own, printing a
// line each. It can be called for several lists: the command's own session
// found in any of them stays held for Last, with only the command's own
// window left in it, so its other members stop now, before anything
// moves. A session that ended on its own meanwhile counts as stopped; one
// tmux can't be asked about doesn't.
func (st *Stopping) Stop(out io.Writer, names []string) {
	self := st.T.Own()
	for _, n := range names {
		if n == self {
			st.own = n
			// Its other members stop now; one that won't is reported as a
			// session that won't, so nothing moves while it runs.
			if !st.T.KeepOnly(n, os.Getenv("TMUX_PANE")) {
				st.Failed = append(st.Failed, n)
				if st.Report != nil {
					st.Report(n, false, false)
				} else {
					fmt.Fprintf(out, "  %s could not be stopped\n", n)
				}
			}
			continue
		}
		ok := st.T.KillSession(n) == nil || st.T.Gone(n)
		if !ok {
			st.Failed = append(st.Failed, n)
		}
		switch {
		case st.Report != nil:
			st.Report(n, ok, false)
		case ok:
			fmt.Fprintf(out, "  %s stopped\n", n)
		default:
			fmt.Fprintf(out, "  %s could not be stopped\n", n)
		}
	}
}

// Last stops the command's own session, when it was in the list.
func (st *Stopping) Last(out io.Writer) {
	if st.own == "" {
		return
	}
	if st.Report != nil {
		st.Report(st.own, true, true)
	} else {
		fmt.Fprintf(out, "  stopping %s last (this command runs in it)\n", st.own)
	}
	st.T.KillSession(st.own)
}
