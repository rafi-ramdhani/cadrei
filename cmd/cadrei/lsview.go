//go:build !windows

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// printStatus is cadrei ls's screen for one cadrei: its orchestrator, what
// each running member is doing, the teams that aren't running, its
// projects, and problems last. several says other cadreis exist, so the
// header says which one this is.
func (e *env) printStatus(s cadreiStatus, several bool) {
	styled := streamStyled(e.stdout)
	w := streamWidth(e.stdout)
	now := time.Now()
	out := func(text string) { e.say("%s", render(text, styled)) }
	head := bold(s.Cadrei.Name) + "  " + dim(unbroken(display(s.Cadrei.Path)))
	if several {
		switch {
		case s.Cadrei.From == "default" || (s.Cadrei.From == "" && s.Cadrei.Default):
			head += dim(" · default")
		case strings.HasPrefix(s.Cadrei.From, "from the project "):
			head += dim(" · from " + strings.TrimPrefix(s.Cadrei.From, "from the project "))
		}
	}
	out(head)
	out("")
	switch o := s.Orchestrator; {
	case !o.Running:
		out(bold("Orchestrator") + "  not running · open it with " + cmd("cadrei"))
	case o.Mode == orchestrator.Terminal && o.Since != nil:
		out(bold("Orchestrator") + "  open in a terminal since " + o.Since.Format("15:04"))
	case o.Since != nil:
		out(bold("Orchestrator") + "  open in tmux since " + o.Since.Format("15:04") + " · reopen with " + cmd("cadrei"))
	default:
		out(bold("Orchestrator") + "  open in tmux · reopen with " + cmd("cadrei"))
	}
	teamDefault := session.ChoiceFrom(s.Cadrei.Model, s.Cadrei.Effort, session.FromCrew)
	if d := describe(teamDefault); d != "" {
		out(bold("Team default") + "  " + d)
	}
	out(bold("Teams"))
	running := map[string]bool{}
	for _, v := range s.Running {
		running[v.Team] = true
	}
	if len(s.Running) == 0 {
		out("  Nothing running. Ask your orchestrator to start a team.")
	}
	for _, v := range s.Running {
		label := teamLabel(v.Team, v.Project)
		if v.Legacy {
			label += " (from 0.1.x)"
		}
		// The attach command in its own column (column 51 at 80 wide),
		// dropped under 70 columns.
		attach := "cadrei attach " + strings.TrimSpace(v.Team+" "+v.Project)
		col := w - 29
		if col+len(attach) > w {
			col = w - len(attach)
		}
		if pad := col - 2 - visible(label); w >= 70 && pad >= 2 && !v.Legacy {
			out("  " + label + strings.Repeat(" ", pad) + cmd(attach))
		} else {
			out("  " + label)
		}
		// Every member of the team, running or not.
		byRole := map[string]memberView{}
		for _, m := range v.Members {
			byRole[m.Role] = m
		}
		roles := []string{}
		for _, m := range s.Teams[v.Team] {
			roles = append(roles, m.Role)
		}
		for _, m := range v.Members {
			if !contains(roles, m.Role) {
				roles = append(roles, m.Role)
			}
		}
		width := 10
		for _, r := range roles {
			if len(r) > width {
				width = len(r)
			}
		}
		for _, role := range roles {
			m, ok := byRole[role]
			if !ok {
				// The mark's own color would end the dim: the whole row is dim.
				glyph := markStopped.plain
				if styled {
					glyph = markStopped.styled
				}
				out("    " + dim(glyph+" "+fmt.Sprintf("%-*s", width, role)+" not running"))
				continue
			}
			first := "    " + m.status.mark().text(styled) + " " + fmt.Sprintf("%-*s", width, role) + " "
			rest := strings.Repeat(" ", 4+2+width+1)
			note := modelNote(m.choice(), teamDefault)
			text := m.status.text(now)
			if note != "" && w >= 60 {
				text += dim(" · " + note)
			}
			for _, l := range wrap(text, w, first, rest) {
				out(l)
			}
			// Under 60 columns, the note goes on its own line.
			if note != "" && w < 60 {
				out(rest + dim(note))
			}
			if m.status.State == session.SafeguardStopped {
				out(rest + dim(safeguardHint))
			}
		}
	}
	var idle []string
	teams := make([]string, 0, len(s.Teams))
	for team := range s.Teams {
		teams = append(teams, team)
	}
	sort.Strings(teams)
	for _, team := range teams {
		if running[team] {
			continue
		}
		var roles []string
		for _, m := range s.Teams[team] {
			roles = append(roles, m.Role)
		}
		idle = append(idle, team+" ("+strings.Join(roles, ", ")+")")
	}
	// With nothing running, the empty line says it all.
	if len(idle) > 0 && len(s.Running) > 0 {
		for _, l := range wrap("Not running: "+strings.Join(idle, ", "), w, "  ", "    ") {
			out(l)
		}
	}
	out(bold("Projects"))
	if len(s.Projects) == 0 {
		out("  None yet. Ask your orchestrator to add one.")
	}
	width := 0
	for _, p := range s.Projects {
		if len(p.Name) > width {
			width = len(p.Name)
		}
	}
	for _, p := range s.Projects {
		var where string
		switch p.State {
		case "present":
			where = display(p.Path)
		case "not here":
			where = "not on this machine · ask your orchestrator to clone it"
		case "missing":
			where = "folder is gone · ask your orchestrator to find it"
		case "drive":
			where = "on a drive that isn't connected"
		}
		out("  " + fmt.Sprintf("%-*s", width, p.Name) + "   " + where)
	}
	for _, p := range s.Problems {
		for _, l := range wrap(sentence(p), w, markWarn.text(styled)+" ", "  ") {
			out(l)
		}
	}
}

// sentence ends a text with a period, and starts it with a capital when it
// begins with an ordinary word. A name (a project, a crew, a file, a
// command) keeps how it's written: "my-app has no GitHub repo".
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	word, _, _ := strings.Cut(s, " ")
	if ordinaryStarts[word] {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// clause is a sentence that follows a colon: its first word small when
// it's an ordinary word ("The model ..."), names and the like as they are.
func clause(s string) string {
	word, _, _ := strings.Cut(s, " ")
	if ordinaryStarts[strings.ToLower(word)] {
		return strings.ToLower(s[:1]) + s[1:]
	}
	return s
}

// ordinaryStarts are the words cadrei's messages begin with that aren't
// names, so they take a capital at the start of a sentence.
var ordinaryStarts = map[string]bool{
	"a": true, "an": true, "the": true, "another": true, "there": true, "there's": true, "this": true, "that": true,
	"it": true, "its": true, "it's": true, "one": true, "no": true, "your": true, "some": true, "every": true,
	"can't": true, "couldn't": true, "cannot": true, "could": true, "nothing": true, "runtime": true, "refused": true,
	"members": true, "team": true,
}

// printOthers is ls --all's line for each other cadrei.
func (e *env) printOthers(list []otherView) {
	if len(list) == 0 {
		return
	}
	styled := streamStyled(e.stdout)
	e.say("%s", render(bold("Other crews"), styled))
	width := 0
	for _, o := range list {
		if len(o.Name) > width {
			width = len(o.Name)
		}
	}
	for _, o := range list {
		what := fmt.Sprintf("%d running", o.Running)
		if o.Missing {
			what = "folder is gone"
		}
		e.say("%s", render("  "+bold(fmt.Sprintf("%-*s", width, o.Name))+"  "+dim(unbroken(display(o.Path)))+"  "+what, styled))
	}
}

// printSessions is a section of ls --all for sessions of no known cadrei.
func (e *env) printSessions(heading string, list []sessionView) {
	if len(list) == 0 {
		return
	}
	styled := streamStyled(e.stdout)
	e.say("%s", render(bold(heading), styled))
	sort.Slice(list, func(a, b int) bool { return list[a].Session < list[b].Session })
	for _, v := range list {
		var names []string
		for _, p := range v.Members {
			names = append(names, p.Name)
		}
		e.say("  %s  %s", v.Session, strings.Join(names, ", "))
	}
}

// modelNote is a member row's note on its model and effort (7.11.4): what
// cadrei passed it that the team default doesn't say, and (this start
// only) for a value given just for this start.
func modelNote(c, teamDefault session.Choice) string {
	if c.Model.Value == teamDefault.Model.Value {
		c.Model = session.Setting{}
	}
	if c.Effort.Value == teamDefault.Effort.Value {
		c.Effort = session.Setting{}
	}
	note := describe(c)
	if note != "" && (c.Model.From == session.FromStart || c.Effort.From == session.FromStart) {
		note += " (this start only)"
	}
	return note
}
