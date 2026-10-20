//go:build !windows

package main

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Safeguard stops: the runtime's safeguards stopped a member's last
// reply, so it won't answer on that model. Cadrei gives the stop a code
// once the runtime's own retry had its chance, and the code takes one of
// two answers. 1 <model> stops the member and starts it again on its chat
// with that model for this start only, as cadrei up --model does: the
// runtime's own advice is another model. 2 starts it fresh, in a new chat.
// Picking the model in its window stays off the code: Claude Code 2.1.304
// saves a model picked by /model as the default for every new session
// (its picker does too, unless s is pressed), which would change the
// user's own settings.

// lookAtStop records the safeguard stop status m shows for member name,
// in build, as a request with a code, or ends the one m no longer shows.
// It reports whether the member's state changed.
func lookAtStop(build, name string, m memberStatus) bool {
	stopped := m.State == session.SafeguardStopped
	prev := session.ReadState(session.StatePath(build, name))
	if session.SeeStop(prev, m.Since, stopped) == prev {
		return false
	}
	session.SeeStopOf(build, name, m.Since, stopped)
	return true
}

// modelName is the runtime's model's name, whose safeguards stop a reply.
func modelName() string {
	if rt, err := runtime.Get(runtimeName()); err == nil {
		return rt.Model()
	}
	return "The model"
}

// modelHint is how the runtime's models are named.
func modelHint() string {
	if rt, err := runtime.Get(runtimeName()); err == nil {
		return rt.ModelHint()
	}
	return "a model's name"
}

// stopModel is the model in choice 1 <model>, or "" for any other choice.
// A model name has a letter in it, so 1 2 (two picks) names none.
func stopModel(choice string) string {
	n, model, ok := strings.Cut(choice, " ")
	if n != "1" || !ok || !strings.ContainsFunc(model, unicode.IsLetter) {
		return ""
	}
	return model
}

// checkStop says why choice can't answer w's safeguard stop, or "".
func checkStop(w *waitingOn, code, choice string, c func(string) string) string {
	switch model := stopModel(choice); {
	case choice == "2":
		return ""
	case model != "" && session.GoodModel(model):
		return ""
	case model != "":
		return "\"" + model + "\" isn't a model name. Use " + modelHint() + ", like " + c("cadrei answer "+code+" 1 sonnet") + "."
	case choice == "1":
		return "Say which model it starts again on, like " + c("cadrei answer "+code+" 1 sonnet") + ". Use " + modelHint() + "."
	}
	return code + " takes 1 and a model (start it again on another model) or 2 (start it fresh). Use " +
		c("cadrei answer "+code+" 1 <model>") + " or " + c("cadrei answer "+code+" 2") + "."
}

// stopAsk is the guard's text for answering w's safeguard stop with choice.
func stopAsk(w *waitingOn, choice string) string {
	after := " after " + modelName() + "'s safeguards stopped its last reply."
	if model := stopModel(choice); model != "" {
		return "Cadrei: start " + w.place() + " again on " + model + "? It stops, and starts again on its chat with " + model +
			" for this start only (your default model stays as it is)," + after
	}
	return "Cadrei: start " + w.place() + " fresh? It stops, and starts again in a new chat that won't remember this one," + after
}

// answerStop answers w's safeguard stop with choice, its request ended as
// answered by its code first: as cadrei stop and cadrei up --model do, on
// its chat, for 1 <model>, or as cadrei stop --fresh and cadrei up do, for
// 2.
func (e *env) answerStop(w *waitingOn, build, choice string) int {
	answered(build, w, choice, time.Now())
	model := stopModel(choice)
	member := w.team + "/" + w.role
	stop, up := []string{member}, []string{member}
	if w.project != "" {
		stop = append(stop, w.project)
		up = append(up, w.project)
		if target := session.Default().Option(w.session, "@cadrei_target"); target != "" {
			up[1] = target
		}
	}
	saved := e.args
	defer func() { e.args = saved }()
	e.args = slices.Clone(stop)
	if model == "" {
		e.args = append(e.args, "--fresh")
	}
	if code := runStop(e); code != 0 {
		return code
	}
	if model == "" {
		e.args = up
		return runUp(e)
	}
	// Its chat still ends in the stop until its next reply: marked past
	// first, so up's own check doesn't show it again.
	session.PassStop(build, w.name, w.req.At)
	e.args = append(up, "--model", model)
	if code := runUp(e); code != 0 {
		session.PassStop(build, w.name, time.Time{})
		return code
	}
	e.say("%s", "It picks up its chat where the safeguards stopped it, and waits for its next message.")
	e.say("%s", "If they stop it again, it gets a new code. Start it fresh with that code and 2: "+cmd("cadrei answer <code> 2")+".")
	return 0
}
