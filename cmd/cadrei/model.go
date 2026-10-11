//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/conf"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Model and effort per member (7.11): the crew's settings, the checks and
// the words for them.

// The cadrei.conf keys for the members' model and effort, and for the
// orchestrator's own.
const (
	keyModel              = "MODEL"
	keyEffort             = "EFFORT"
	keyOrchestratorModel  = "ORCHESTRATOR_MODEL"
	keyOrchestratorEffort = "ORCHESTRATOR_EFFORT"
)

// crewLayers are a crew's model and effort as each of its two files sets
// them: its local cadrei.conf (this machine) and its cadrei.conf. With
// orchestrator, the orchestrator's own keys.
func crewLayers(path string, orchestrator bool) (machine, crew session.Choice) {
	model, effort := keyModel, keyEffort
	if orchestrator {
		model, effort = keyOrchestratorModel, keyOrchestratorEffort
	}
	read := func(file string) map[string]string {
		raw, _ := os.ReadFile(file)
		values, _ := conf.Parse(string(raw))
		return values
	}
	local, shared := read(filepath.Join(cadreis.LocalDir(path), "cadrei.conf")), read(filepath.Join(path, "cadrei.conf"))
	return session.ChoiceFrom(local[model], local[effort], session.FromMachine),
		session.ChoiceFrom(shared[model], shared[effort], session.FromCrew)
}

// crewChoice is the crew's default: its local cadrei.conf over its
// cadrei.conf.
func crewChoice(path string, orchestrator bool) session.Choice {
	machine, crew := crewLayers(path, orchestrator)
	return machine.Over(crew)
}

// choiceCheck names whose values a check is about and where they're set:
// who is "The writer's", "Your crew's" or "The orchestrator's" (or "" for
// a flag), crew the crew's folder, member the member file, relative to the
// crew, and orchestrator the orchestrator's keys.
type choiceCheck struct {
	who, crew, member string
	orchestrator      bool
}

// check returns what's wrong with c's values, with where to fix it, or nil.
func (k choiceCheck) check(rt runtime.Runtime, c session.Choice) error {
	if v := c.Effort.Value; v != "" && !session.GoodEffort(v) {
		return &lookupError{what: k.whose("effort") + " " + strconv.Quote(v) + " isn't one " + rt.Title() + " knows.",
			fix: []string{"Use " + andOr(session.Efforts, "or") + ", " + k.where(c.Effort.From, "effort") + "."}}
	}
	if v := c.Model.Value; v != "" && !session.GoodModel(v) {
		return &lookupError{what: k.whose("model") + " " + strconv.Quote(v) + " isn't a model name.",
			fix: []string{"Use " + rt.ModelHint() + ", " + k.where(c.Model.From, "model") + "."}}
	}
	return nil
}

func (k choiceCheck) whose(what string) string {
	if k.who == "" {
		return "The " + what
	}
	return k.who + " " + what
}

// where says where a value was set: in a member file, a key in a
// cadrei.conf, or a flag.
func (k choiceCheck) where(from, what string) string {
	key := map[string]string{"model": keyModel, "effort": keyEffort}[what]
	if k.orchestrator {
		key = "ORCHESTRATOR_" + key
	}
	switch from {
	case session.FromMember:
		return "in " + unbroken(k.member)
	case session.FromMachine:
		return "for " + key + " in " + unbroken(localConf(k.crew))
	case session.FromCrew:
		return "for " + key + " in cadrei.conf"
	}
	return "with " + cmd("--"+what)
}

// localConf is a crew's local cadrei.conf, as the user finds it in the
// crew.
func localConf(crew string) string {
	rel, err := filepath.Rel(crew, filepath.Join(cadreis.LocalDir(crew), "cadrei.conf"))
	if err != nil {
		return display(filepath.Join(cadreis.LocalDir(crew), "cadrei.conf"))
	}
	return rel
}

// andOr joins words as "a, b or c".
func andOr(words []string, last string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " " + last + " " + words[len(words)-1]
}

// describe says what a choice runs on: "sonnet, high effort", "sonnet",
// "high effort", or "".
func describe(c session.Choice) string {
	var parts []string
	if c.Model.Value != "" {
		parts = append(parts, c.Model.Value)
	}
	if c.Effort.Value != "" {
		parts = append(parts, c.Effort.Value+" effort")
	}
	return strings.Join(parts, ", ")
}

// changed is what of c differs from was, described, or "".
func changed(c, was session.Choice) string {
	var d session.Choice
	if c.Model.Value != was.Model.Value {
		d.Model = c.Model
	}
	if c.Effort.Value != was.Effort.Value {
		d.Effort = c.Effort
	}
	return describe(d)
}

// fixModel is the line under a start that ended at once, when a model was
// passed: where to check it.
func fixModel(c session.Choice, crew, member string) string {
	switch c.Model.From {
	case session.FromMember:
		return "Check model: in " + unbroken(member) + "."
	case session.FromMachine:
		return "Check " + keyModel + " in " + unbroken(localConf(crew)) + "."
	case session.FromCrew:
		return "Check " + keyModel + " in cadrei.conf."
	case session.FromStart:
		return "Check the model you gave with " + cmd("--model") + "."
	}
	return ""
}

// choiceFlags takes --model and --effort (with their values, or as
// --model=x) out of args: this start's model and effort.
func (e *env) choiceFlags(args []string) (session.Choice, []string, bool) {
	var model, effort string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, eq := strings.Cut(a, "=")
		if name != "--model" && name != "--effort" {
			rest = append(rest, a)
			continue
		}
		if !eq && i+1 < len(args) {
			i++
			value = args[i]
		}
		if value == "" {
			e.failWith(cmd(name) + " needs a value, like " + cmd(name+" <"+name[2:]+">") + ".")
			return session.Choice{}, nil, false
		}
		if name == "--model" {
			model = value
		} else {
			effort = value
		}
	}
	return session.ChoiceFrom(model, effort, session.FromStart), rest, true
}

// checkChoices checks every model and effort u could start with: this
// start's, the crew's two files and each member file it starts.
func checkChoices(rt runtime.Runtime, crew string, u session.Up) error {
	if err := (choiceCheck{crew: crew}).check(rt, u.Override); err != nil {
		return err
	}
	machine, shared := crewLayers(crew, false)
	for _, c := range []session.Choice{machine, shared} {
		if err := (choiceCheck{who: "Your crew's", crew: crew}).check(rt, c); err != nil {
			return err
		}
	}
	roles := []string{u.Role}
	if u.Role == "" {
		roles = session.Roles(crew, u.Team)
	}
	for _, role := range roles {
		file := filepath.Join("members", u.Team, role+".md")
		c := session.MemberChoice(filepath.Join(crew, file))
		if err := (choiceCheck{who: "The " + role + "'s", crew: crew, member: file}).check(rt, c); err != nil {
			return err
		}
	}
	return nil
}

// fixOrchestratorModel is where to check the orchestrator's model, when
// its crew sets one.
func fixOrchestratorModel(crew string) string {
	c := crewChoice(crew, true)
	switch c.Model.From {
	case session.FromMachine:
		return "Check " + keyOrchestratorModel + " in " + unbroken(localConf(crew)) + "."
	case session.FromCrew:
		return "Check " + keyOrchestratorModel + " in cadrei.conf."
	}
	return ""
}

// choiceProblems are the bad models and efforts a crew sets anywhere: its
// two files, for the members and for the orchestrator, and every member
// file (cadrei --check).
func choiceProblems(rt runtime.Runtime, crew string) []*lookupError {
	var out []*lookupError
	add := func(k choiceCheck, c session.Choice) {
		if err := k.check(rt, c); err != nil {
			out = append(out, err.(*lookupError))
		}
	}
	for _, orch := range []bool{false, true} {
		who := "Your crew's"
		if orch {
			who = "The orchestrator's"
		}
		machine, shared := crewLayers(crew, orch)
		add(choiceCheck{who: who, crew: crew, orchestrator: orch}, machine)
		add(choiceCheck{who: who, crew: crew, orchestrator: orch}, shared)
	}
	files, _ := filepath.Glob(filepath.Join(crew, "members", "*", "*.md"))
	for _, f := range files {
		rel, _ := filepath.Rel(crew, f)
		role := strings.TrimSuffix(filepath.Base(f), ".md")
		add(choiceCheck{who: "The " + role + "'s", crew: crew, member: rel}, session.MemberChoice(f))
	}
	return out
}

// modelSettings lists the model and effort keys a crew's cadrei.conf
// files set, as KEY=value, for a restore to name (a cost the user should
// see before it applies). A value that isn't a plain name is quoted.
func modelSettings(crew string) []string {
	var out []string
	for _, orch := range []bool{false, true} {
		c := crewChoice(crew, orch)
		model, effort := keyModel, keyEffort
		if orch {
			model, effort = keyOrchestratorModel, keyOrchestratorEffort
		}
		if v := c.Model.Value; v != "" {
			if !session.GoodModel(v) {
				v = strconv.Quote(v)
			}
			out = append(out, model+"="+v)
		}
		if v := c.Effort.Value; v != "" {
			if !session.GoodEffort(v) {
				v = strconv.Quote(v)
			}
			out = append(out, effort+"="+v)
		}
	}
	return out
}
