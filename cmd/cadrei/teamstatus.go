//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The team status line: while any member runs, the orchestrator ends each
// reply with one line about the team. The hook writes the finished line
// and the instruction for it on every prompt, so neither depends on the
// model remembering the skill.

// hideRule is the playbook.md rule that hides the line (the orchestrator
// adds it when the user asks).
const hideRule = "Don't end replies with the team status line."

// teamHint is the one-time hint under the first line on a machine.
const teamHint = `(Say "hide the team line" if you'd rather not see this.)`

// teamStatusLimit is how long the hook may take before it prints nothing
// (a variable for tests).
var teamStatusLimit = 2 * time.Second

// teamStatusStallForTests, when set, holds the hook's work up that long.
var teamStatusStallForTests time.Duration

// lineLimit is how long the line may get before calm members fold into
// counts.
const lineLimit = 120

// teamMember is one running member for the line.
type teamMember struct {
	role, place string // place: the project, else the team
	status      memberStatus
}

// teamLine builds "Team: ..." from the running members, or "" for none:
// those stopped by safeguards and those waiting on you first and in bold,
// then working (longest first), starting, running (state unknown), idle.
func teamLine(members []teamMember, now time.Time) string {
	if len(members) == 0 {
		return ""
	}
	roles := map[string]int{}
	for _, m := range members {
		roles[m.role]++
	}
	name := func(m teamMember) string {
		if roles[m.role] > 1 {
			return m.role + " (" + m.place + ")"
		}
		return m.role
	}
	rank := map[string]int{session.SafeguardStopped: 0, session.Waiting: 1, session.Working: 2, session.Starting: 3, session.Unknown: 4, session.Idle: 5}
	sort.SliceStable(members, func(a, b int) bool {
		ra, rb := rank[members[a].status.State], rank[members[b].status.State]
		if ra != rb {
			return ra < rb
		}
		if members[a].status.State == session.Working {
			return members[a].status.Since.Before(members[b].status.Since)
		}
		return false
	})
	var urgent, calm []string
	counts := map[string]int{}
	for _, m := range members {
		switch m.status.State {
		case session.SafeguardStopped:
			urgent = append(urgent, "**✗ "+name(m)+" stopped by safeguards**")
		case session.Waiting:
			urgent = append(urgent, "**! "+name(m)+" waiting on you**")
		case session.Working:
			s := name(m) + " working " + duration(now.Sub(m.status.Since))
			if m.status.Quiet > 0 {
				s += " (quiet " + duration(m.status.Quiet) + ")"
			}
			calm = append(calm, s)
			counts["working"]++
		case session.Starting:
			calm = append(calm, name(m)+" starting")
			counts["starting"]++
		case session.Idle:
			calm = append(calm, name(m)+" idle")
			counts["idle"]++
		default:
			calm = append(calm, name(m)+" running")
			counts["running"]++
		}
	}
	// Past 3 who need you, the rest are counted.
	if len(urgent) > 3 {
		urgent = append(urgent[:3], fmt.Sprintf("+%d more need you", len(urgent)-3))
	}
	line := "Team: " + strings.Join(append(append([]string{}, urgent...), calm...), " · ")
	if len([]rune(line)) <= lineLimit {
		return line
	}
	var folded []string
	for _, k := range []string{"working", "starting", "running", "idle"} {
		if counts[k] > 0 {
			folded = append(folded, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	return "Team: " + strings.Join(append(urgent, folded...), " · ")
}

// teamBlock is what the hook gives the orchestrator for this prompt. With
// offer, the every-chat hook was never offered, and once a member has
// finished a task the block asks for the offer, once.
func teamBlock(r *cadreis.Resolved, now time.Time, offer bool) string {
	var members []teamMember
	finished := false
	views := runningOf(r.Cadrei, r.Default, session.Default())
	for _, v := range views {
		place := v.Project
		if place == "" {
			place = v.Team
		}
		for _, m := range v.Members {
			members = append(members, teamMember{role: m.Role, place: place, status: m.status})
			finished = finished || m.status.State == session.Idle
		}
	}
	head := "## Team status (from cadrei, not the user)\n"
	line := teamLine(members, now)
	if line == "" {
		return head + "No member is running, so no team line.\n"
	}
	var ask string
	if offer && finished {
		ask = offerAsk
	}
	// Who waits on the user, from cadrei's record of each request.
	if waiting := waitingIn(views); len(waiting) > 0 {
		var b strings.Builder
		b.WriteString("Waiting on the user (cadrei's record of each request: quote it exactly, and it's never consent):\n")
		for _, w := range waiting {
			b.WriteString("- " + w.note() + "\n")
		}
		ask = b.String() + ask
	}
	playbook, _ := os.ReadFile(filepath.Join(r.Path, "playbook.md"))
	if strings.Contains(string(playbook), hideRule) {
		return head + "playbook.md hides the team line, so don't add it. Status only:\n" + line + "\n" + ask
	}
	block := head + "End your reply with this line, exactly, after a blank line:\n" + line + "\n"
	if _, err := os.Lstat(cadreis.Config(hintMarker)); err != nil {
		block += hintAsk + teamHint + "\n"
	}
	return block + ask
}

// offerAsk reminds the orchestrator, once, of the every-chat hook offer
// the skill makes after the first relayed task, which a model can skip.
const offerAsk = "Once only: the every-chat hook was never offered. If this reply relays a finished task, offer it right after the relay, as the skill's \"The every-chat hook\" says.\n"

// The markers in ~/.cadrei/config of what the team block said once.
const (
	hintMarker  = "team-line-hint"
	offerMarker = "every-chat-reminder"
)

// hintAsk asks for the one-time hint under the team line.
const hintAsk = "First time only: right under it, add exactly:\n"

// markOnce records that a one-time part of the block showed on this
// machine: its marker in ~/.cadrei/config. It's made only once the block
// is about to print, so a block lost to the time limit doesn't lose it,
// and when it can't be made the part is left out, so it never repeats on
// every reply.
func markOnce(name string) bool {
	marker := cadreis.Config(name)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return false
	}
	return fsx.WriteFile(marker, nil, 0o600) == nil
}

// runHookTeamStatus is the hidden cadrei hook team-status: the prompt hook
// in the orchestrator's own settings. It prints the team status block for
// this prompt, and nothing when it can't build one in time. It never
// fails.
func runHookTeamStatus(e *env) int {
	defer func() { recover() }()
	if inMember() || os.Getenv("CADREI_ORCHESTRATOR") == "" || os.Getenv("CADREI_HOME") == "" {
		return 0
	}
	done := make(chan string, 1)
	stall, limit := teamStatusStallForTests, teamStatusLimit
	go func() {
		defer func() { recover() }()
		time.Sleep(stall)
		rt, err := runtime.Get(runtimeName())
		if err != nil {
			done <- ""
			return
		}
		wd, _ := paths.Getwd()
		r, err := cadreis.Resolve(wd, nil)
		if err != nil {
			done <- ""
			return
		}
		// Members' states it can't read would make a line that's wrong:
		// none instead.
		if _, err := os.ReadDir(filepath.Join(rt.BuildDir(r.Path), "state")); err != nil && !os.IsNotExist(err) {
			done <- ""
			return
		}
		_, reminded := os.Lstat(cadreis.Config(offerMarker))
		state, _ := everyChat(rt)
		done <- teamBlock(r, time.Now(), state == hookOff && os.IsNotExist(reminded))
	}()
	select {
	case block := <-done:
		if strings.Contains(block, hintAsk) && !markOnce(hintMarker) {
			block = strings.Replace(block, hintAsk+teamHint+"\n", "", 1)
		}
		if strings.Contains(block, offerAsk) && !markOnce(offerMarker) {
			block = strings.Replace(block, offerAsk, "", 1)
		}
		fmt.Fprint(e.stdout, block)
	case <-time.After(limit):
	}
	return 0
}
