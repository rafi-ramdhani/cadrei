//go:build !windows

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// What a running member is doing, for cadrei ls, its JSON, the
// orchestrator's snapshot and the team line. It is for display only:
// never consent, and never a reason to stop or restart a member.

// safeguardWait gives the runtime's own retry after a safeguard stop its
// chance before cadrei ls calls the member stopped.
const safeguardWait = 60 * time.Second

// quietAfter is how long a working or waiting member's transcript may go
// unwritten before ls says so (Esc fires no hook, so the state can stay).
const quietAfter = 10 * time.Minute

type memberStatus struct {
	State      string
	Since      time.Time
	WaitingFor string        // approval or question
	Error      bool          // idle after a turn that hit an error
	Quiet      time.Duration // working or waiting, and nothing written for this long
	QuietSince time.Time     // when the transcript was last written, with Quiet
}

// statusOfMember reads member name's state in the cadrei at home. With
// wait, a safeguard stop counts only once the runtime's retry had its
// chance.
func statusOfMember(rt runtime.Runtime, home, name string, now time.Time, wait bool) memberStatus {
	build := rt.BuildDir(home)
	st := session.ReadState(session.StatePath(build, name))
	rec := session.ReadRecord(session.RecordPath(build, name))
	transcript := ""
	switch {
	case st != nil && st.Transcript != "":
		transcript = st.Transcript
	case rec != nil:
		transcript = rt.Sessions().TranscriptFile(rec.ID, rec.Dir)
	}
	// A safeguard stop wins over every other state. The wait for the
	// runtime's own retry is over once a turn ended after the stop.
	if at, stopped := session.Safeguard(rt.Sessions(), build, name, transcript); stopped {
		retried := st != nil && st.State == session.Idle && st.EndedAt.After(at)
		if !wait || retried || now.Sub(at) >= safeguardWait {
			return memberStatus{State: session.SafeguardStopped, Since: at}
		}
	}
	if st == nil || (rec != nil && st.Session != "" && st.Session != rec.ID) {
		return memberStatus{State: session.Unknown}
	}
	m := memberStatus{State: st.State, Since: st.Since, WaitingFor: st.WaitingFor, Error: st.Error}
	if (m.State == session.Working || m.State == session.Waiting) && transcript != "" {
		if fi, err := os.Stat(transcript); err == nil && now.Sub(fi.ModTime()) >= quietAfter {
			m.Quiet, m.QuietSince = now.Sub(fi.ModTime()), fi.ModTime()
		}
	}
	return m
}

// duration is how long, as people say it: just now, 4m, 1h 5m.
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// clock is when: 13:40 today, Oct 8 before today.
func clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2")
}

// mark is a state's mark in cadrei ls.
func (m memberStatus) mark() mark {
	switch m.State {
	case session.Working:
		return markWorking
	case session.Waiting:
		return markWaiting
	case session.Idle:
		return markIdle
	case session.Starting:
		return markStarting
	case session.SafeguardStopped:
		return markFail
	}
	return markUnknown
}

// text says a state in cadrei ls's words.
func (m memberStatus) text(now time.Time) string {
	var s string
	switch m.State {
	case session.Working:
		if d := now.Sub(m.Since); d < time.Minute {
			s = "working just now"
		} else {
			s = "working for " + duration(d)
		}
	case session.Waiting:
		s = "waiting on you · approve it in its window"
		if m.WaitingFor == session.ForQuestion {
			s = "waiting on you · it asked you something in its window"
		}
	case session.Idle:
		s = "idle since " + clock(m.Since, now)
		if m.Error {
			s += " · its last turn hit an error"
		}
	case session.Starting:
		s = "starting"
	case session.SafeguardStopped:
		return "stopped by safeguards"
	default:
		return "running · can't tell what it's doing"
	}
	if m.Quiet > 0 {
		s += " · no activity for " + duration(m.Quiet)
	}
	return s
}

// forTime is "for 4m", or "just now" under a minute (never "for just
// now").
func forTime(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return "for " + duration(d)
}

// safeguardHint is the line under a member stopped by safeguards.
const safeguardHint = "Pick the model again in its window, or start it fresh."

// brief says a state for the orchestrator's snapshot: "engineer working
// for 4m", "reviewer waiting on you for 1m".
func (m memberStatus) brief(now time.Time) string {
	switch m.State {
	case session.Working:
		return "working " + forTime(now.Sub(m.Since))
	case session.Waiting:
		if m.WaitingFor == session.ForQuestion {
			return "waiting on you (a question) " + forTime(now.Sub(m.Since))
		}
		return "waiting on you " + forTime(now.Sub(m.Since))
	case session.Idle:
		if m.Error {
			return "idle since " + clock(m.Since, now) + " (its last turn hit an error)"
		}
		return "idle since " + clock(m.Since, now)
	case session.Starting:
		return "starting"
	case session.SafeguardStopped:
		return "stopped by safeguards"
	}
	return "running (state unknown)"
}
