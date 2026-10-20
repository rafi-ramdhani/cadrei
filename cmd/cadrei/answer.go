//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// cadrei answer: the user answers a member's permission prompt or question
// from the orchestrator. The answer is a fixed choice (yes, no or an
// option's number) for a request cadrei recorded, named by its code, never
// free text. Cadrei presses one key in the prompt the runtime drew, once
// the prompt on the member's screen matches the record. The user's own
// act is the yes: their keypress in the runtime's permission prompt in the
// orchestrator (the guard asks), or the command they typed themselves.

// waitingOn is a member waiting on the user, with the request cadrei
// recorded.
type waitingOn struct {
	team, project, role string // as shown
	session, window     string // the member's tmux window, as tmux has it
	name                string // the member's name, its state file's
	dir                 string // the folder it was started in, when known
	since               time.Time
	req                 *session.Request
}

// place names the member as the orchestrator does: the engineer on
// my-app, or the writer in docs.
func (w *waitingOn) place() string { return placeName(w.role, w.team, w.project) }

func placeName(role, team, project string) string {
	if project != "" {
		return "the " + role + " on " + project
	}
	return "the " + role + " in " + team
}

// attach is the command that opens the member's window.
func (w *waitingOn) attach() string {
	return "cadrei attach " + strings.TrimSpace(w.team+" "+w.project)
}

// capital starts text with a capital letter.
func capital(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// waitingRequests lists crew r's running members that wait on the user
// with a request cadrei recorded, oldest first.
func waitingRequests(r *cadreis.Resolved, t session.Tmux) []waitingOn {
	return waitingIn(runningOf(r.Cadrei, r.Default, t))
}

// waitingIn lists the members of views that wait on the user with a
// request cadrei recorded, oldest first.
func waitingIn(views []sessionView) []waitingOn {
	rt, _ := runtime.Get(runtimeName())
	var out []waitingOn
	for _, v := range views {
		for _, m := range v.Members {
			if m.status.State != session.Waiting {
				continue
			}
			dir, build := "", ""
			if rt != nil && v.info.Home != "" {
				build = rt.BuildDir(v.info.Home)
				if rec := session.ReadRecord(session.RecordPath(build, m.name)); rec != nil {
					dir = rec.Dir
				}
			}
			// Each open prompt is its own request, under its own code. One
			// the hook had to cut short goes by its whole text, so the user
			// sees all of what they answer, and the screen is checked for
			// all of it.
			for _, req := range m.status.Requests {
				if full := session.ReadFull(build, m.name, req); full != "" {
					whole := *req
					whole.Summary = full
					req = &whole
				}
				out = append(out, waitingOn{team: v.Team, project: v.Project, role: m.Role, session: v.info.Name, window: m.window,
					name: m.name, dir: dir, since: m.status.Since, req: req})
			}
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].since.Before(out[j-1].since); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// findWaiting is the request under code, or nil.
func findWaiting(r *cadreis.Resolved, t session.Tmux, code string) *waitingOn {
	if !session.CodeRule.MatchString(code) {
		return nil
	}
	for _, w := range waitingRequests(r, t) {
		if w.req.Code == code {
			return &w
		}
	}
	return nil
}

// act says what an approval would let the member do: the verb, its object,
// and what kind of thing that is, for the listing.
func act(req *session.Request) (verb, object, kind string) {
	switch req.Tool {
	case "Bash":
		return "run", req.Summary, "run a command"
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "edit", req.Summary, "edit a file"
	case "Read":
		return "read", req.Summary, "read a file"
	case "WebFetch":
		return "fetch", req.Summary, "fetch a page"
	case "WebSearch":
		return "search the web for", req.Summary, "search the web"
	case "ExitPlanMode":
		if plan := planText(req.Summary); plan != "" {
			return "carry out this plan", plan, "carry out a plan"
		}
	}
	if strings.HasPrefix(req.Tool, "mcp__") {
		return "use", req.Summary, "use a tool"
	}
	return "use", strings.TrimSpace(req.Tool + " " + req.Summary), "use a tool"
}

// screenTools are the tools whose permission prompt cadrei knows how to
// check on a member's screen (promptShows), besides MCP tools: from
// recorded screens, a command, a file to write or edit, a page to fetch;
// from Claude Code 2.1.302's own text, not seen live yet, a file to read
// or edit in several places, a notebook, a web search.
var screenTools = setOf("Bash", "Write", "Edit", "WebFetch", "MultiEdit", "NotebookEdit", "Read", "WebSearch", "ExitPlanMode")

// checkable reports whether cadrei can check tool's permission prompt on
// a member's screen.
func checkable(tool string) bool { return screenTools[tool] || strings.HasPrefix(tool, "mcp__") }

// planText is the plan a plan approval's record holds (the tool's input,
// {"plan": ...}, as the hook recorded it), as one printable line, or ""
// for none. Which input the hook gets for it isn't seen live yet.
func planText(summary string) string {
	var in struct {
		Plan string `json:"plan"`
	}
	if json.Unmarshal([]byte(summary), &in) != nil || strings.TrimSpace(in.Plan) == "" {
		return ""
	}
	return runtime.RequestText(strings.TrimSpace(in.Plan), runtime.FullLimit)
}

// answerable reports whether cadrei can answer req from here: a question
// with one pick, a prompt for a tool cadrei can check with what it would
// run or touch recorded in full, or a prompt or start-up screen read off
// the screen whole, with an option cadrei may pick. Any other is answered
// in the member's window.
func answerable(req *session.Request) bool {
	switch req.Kind {
	case session.ForQuestion:
		for _, p := range req.Parts {
			if p.Question == "" || len(p.Options) == 0 {
				return false
			}
		}
		return !req.Multi && req.Question != "" && len(req.Options) > 0
	case session.ForPrompt:
		return !req.Multi && req.Question != "" && len(picks(req.Options)) > 0
	case session.ForStart:
		return !req.Multi && req.Question != "" && len(startChoices(req)) > 0
	case session.ForApproval:
		return checkable(req.Tool) && req.Summary != "" && !cutShort(req) && (req.Tool != "ExitPlanMode" || planText(req.Summary) != "")
	}
	return false
}

// asksForText is an approval's option that has the user type what to do
// instead: no fixed choice answers it.
var asksForText = regexp.MustCompile(`(?i)^(no, and )?tell \S+ what to `)

// choices are the numbers of the options of an approval's prompt, read
// off the member's screen once it showed (none before), that an answer
// may pick by number: the plain Yes and No, one that stops the member
// asking again for what it names (lasting), and a plan's approval that
// keeps it asking before each edit. Never one that asks for text, one cut
// short (its end, often what it covers, isn't kept), or one that changes
// the member's permission mode (auto mode, accepting edits, bypassing
// permissions): those are answered in its window. Each is shown word for
// word, and the guard's ask says how a lasting one lasts.
func choices(req *session.Request) []int {
	var out []int
	for i, o := range req.Options {
		if plainChoice(o) || o == planManual || lasting(o) != "" {
			out = append(out, i+1)
		}
	}
	return out
}

// planManual is a plan approval's option that carries out the plan with
// the member asking before each edit, as it did before: no mode it
// didn't have.
const planManual = "Yes, manually approve edits"

// savedRule and sessionRule are an approval's options that stop the
// member asking again for what they name: saved in its settings, or for
// the rest of its session. modeWords is one that changes its permission
// mode, which no answer picks, whatever else it says.
var (
	savedRule   = regexp.MustCompile(`^Yes, and (don't ask again|always allow) `)
	sessionRule = regexp.MustCompile(`^Yes, (and )?allow .+ during this session$`)
	modeWords   = regexp.MustCompile(`(?i)\bmodes?\b|\baccept(ing)? edits\b|\bauto-|\bbypass|shift\+tab|\ball edits\b`)
)

// Ways an approval's option lasts past its prompt.
const (
	lastsSaved   = "saved"
	lastsSession = "session"
)

// lasting says how an approval's option lasts past its prompt (lastsSaved,
// lastsSession), or "" for one that doesn't, or that no answer picks.
func lasting(label string) string {
	switch {
	case strings.HasSuffix(label, "…") || modeWords.MatchString(label) || asksForText.MatchString(label):
		return ""
	case savedRule.MatchString(label):
		return lastsSaved
	case sessionRule.MatchString(label):
		return lastsSession
	}
	return ""
}

// lastsTag says in a few words how an option lasts, for the listing and
// the note, or "".
func lastsTag(label string) string {
	switch lasting(label) {
	case lastsSaved:
		return " (saved, so it lasts after this prompt)"
	case lastsSession:
		return " (lasts for the rest of its session)"
	}
	return ""
}

// lastsSentence says plainly how an option lasts, for the guard's ask, or
// "".
func lastsSentence(w *waitingOn, label string) string {
	switch lasting(label) {
	case lastsSaved:
		return " That choice is saved and lasts: " + w.place() + " won't ask again for what it names, after this prompt too."
	case lastsSession:
		return " That choice lasts for the rest of the member's session: " + w.place() + " won't ask again for what it names."
	}
	return ""
}

// otherChoices are those of choices that aren't the plain Yes or No.
func otherChoices(req *session.Request) []int {
	return slices.DeleteFunc(choices(req), func(n int) bool { return plainChoice(req.Options[n-1]) })
}

func plainChoice(label string) bool { return label == "Yes" || label == "No" }

// hasPlainYes reports whether an approval's prompt has a plain Yes, as
// far as cadrei knows: its options aren't read yet, or one is.
func hasPlainYes(req *session.Request) bool {
	return len(req.Options) == 0 || slices.Contains(req.Options, "Yes")
}

// approvalPick is an approval's choice by number: its label, when choice
// is a number of choices.
func approvalPick(req *session.Request, choice string) (label string, ok bool) {
	n, err := strconv.Atoi(choice)
	if req.Kind != session.ForApproval || err != nil || strconv.Itoa(n) != choice || !slices.Contains(choices(req), n) {
		return "", false
	}
	return req.Options[n-1], true
}

// questionExtra reports whether an option is one of the two the runtime
// adds under a question's own: a text field, and a way back to the chat.
// Neither answers it in one key.
func questionExtra(label string) bool {
	return label == "Chat about this" || strings.HasPrefix(label, "Type something")
}

// screenQuestion reports whether a prompt's options are a question's: it
// has the two the runtime adds.
func screenQuestion(options []string) bool { return slices.ContainsFunc(options, questionExtra) }

// picks are the numbers of the options cadrei may press in a prompt read
// off the screen: a question's own options, not the two the runtime adds;
// in any other prompt, only the option labeled exactly Yes and the one
// labeled exactly No, never one that allows more or changes the mode.
func picks(options []string) []int {
	question := screenQuestion(options)
	var out []int
	for i, o := range options {
		if question && !questionExtra(o) || !question && (o == "Yes" || o == "No") {
			out = append(out, i+1)
		}
	}
	return out
}

// offered are the numbers of the options an answer may pick in req.
func offered(req *session.Request) []int {
	switch req.Kind {
	case session.ForPrompt:
		return picks(req.Options)
	case session.ForStart:
		return startChoices(req)
	case session.ForApproval:
		return choices(req)
	}
	var out []int
	for i := range req.Options {
		out = append(out, i+1)
	}
	return out
}

// saysNo reports whether choice tells w's member no: an approval's no (or
// its option labeled No), or the option labeled No of a prompt read off
// the screen that isn't a question. Either ends the member's turn, with
// no hook to say so.
func saysNo(w *waitingOn, choice string) bool {
	switch w.req.Kind {
	case session.ForApproval:
		label, _ := approvalPick(w.req, choice)
		return choice == "no" || label == "No"
	case session.ForPrompt:
		n, err := strconv.Atoi(choice)
		return err == nil && n >= 1 && n <= len(w.req.Options) && w.req.Options[n-1] == "No" && !screenQuestion(w.req.Options)
	}
	return false
}

// cutShort reports whether the record of what a prompt would run or touch
// was cut, so neither the user nor the screen check would see its end.
func cutShort(req *session.Request) bool {
	return req.Kind == session.ForApproval && strings.HasSuffix(req.Summary, "…")
}

// note says a request for the orchestrator, from cadrei's record: who,
// its code, what it wants or asks, and how it's answered.
func (w *waitingOn) note() string {
	who := capital(w.place()) + " (" + w.req.Code + ")"
	window := " Only its window can answer that: " + w.attach() + "."
	switch {
	case w.req.Kind == session.ForApproval:
		verb, object, _ := act(w.req)
		if !answerable(w.req) {
			return who + " wants to " + verb + ": " + noteText(object) + "." + window
		}
		answer := " Answer with cadrei answer " + w.req.Code + " yes, or no."
		if !hasPlainYes(w.req) {
			answer = " It has no plain Yes: answer with cadrei answer " + w.req.Code + " <n> for one of its choices, or no."
		}
		if others := otherChoices(w.req); len(others) > 0 {
			var opts []string
			for _, n := range others {
				opts = append(opts, fmt.Sprintf("%d. %s%s", n, w.req.Options[n-1], lastsTag(w.req.Options[n-1])))
			}
			answer += " Its other choices, word for word: " + strings.Join(opts, ", ") +
				". The user picks one with cadrei answer " + w.req.Code + " <n>."
		}
		return who + " wants to " + verb + ": " + noteText(object) + "." + answer
	case many(w.req) && answerable(w.req):
		var parts []string
		for _, p := range w.req.Parts {
			parts = append(parts, partText(p))
		}
		lead := " asks: "
		if len(w.req.Parts) > 1 {
			lead = fmt.Sprintf(" asks %d questions: ", len(w.req.Parts))
		}
		return who + lead + strings.Join(parts, " ") + " The user answers by typing ! cadrei answer " + w.req.Code +
			" and one answer per question, in order, several picks for one joined by commas (" + partsExample(w.req) + ")."
	case w.req.Kind == session.ForQuestion && answerable(w.req):
		return who + " asks: " + w.req.Question + " Options: " + numbered(w.req) + ". The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	case w.req.Kind == session.ForQuestion:
		return who + " asks: " + w.req.Question + window
	case w.req.Kind == session.ForPrompt && answerable(w.req):
		return who + " shows a prompt: " + w.req.Question + " Options: " + numbered(w.req) + ". The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	case w.req.Kind == session.ForStart:
		ns, refused := startOffered(w)
		if !answerable(w.req) || len(ns) == 0 {
			return who + " waits on a start-up screen: " + w.req.Question + window
		}
		trust := ""
		if refused != "" {
			trust = " Cadrei won't trust its folder from here (" + refused + "): only its window can."
		}
		return who + " waits on a start-up screen: " + w.req.Question + " Options: " + numberedOf(w.req, ns) + "." + trust +
			" The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	}
	return who + " waits on something only its window can answer: " + w.attach() + "."
}

// noteText is what a request would run or touch, for the orchestrator's
// note: cut as the hook cuts a summary, so a long command's whole text
// (up to runtime.FullLimit) doesn't go in every note. The listing shows
// it whole.
func noteText(object string) string {
	if cut := runtime.RequestText(object, runtime.SummaryLimit); cut != object {
		return cut + " (cadrei answer shows it whole)"
	}
	return object
}

// numbered lists the options an answer may pick in req as 1. a, 2. b.
func numbered(req *session.Request) string { return numberedOf(req, offered(req)) }

// numberedOf lists req's options ns as 1. a, 2. b.
func numberedOf(req *session.Request, ns []int) string {
	var opts []string
	for _, n := range ns {
		opts = append(opts, fmt.Sprintf("%d. %s", n, req.Options[n-1]))
	}
	return strings.Join(opts, ", ")
}

// nothingWaiting says a code names no request.
func nothingWaiting(code string, c func(string) string) string {
	return "Nothing's waiting under " + printable(code) + " anymore. Run " + c("cadrei answer") + " to see what's waiting now."
}

// checkChoice says why choice can't answer w's request, or "". c formats
// a command (cmd for the screen, plain text for the guard).
func checkChoice(w *waitingOn, code, choice string, c func(string) string) string {
	switch {
	case w.req.Kind == session.ForQuestion && w.req.Multi:
		return "That question has more options or questions than cadrei keeps, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForPrompt && !answerable(w.req):
		return "Cadrei couldn't read that prompt whole off the screen, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForStart && !answerable(w.req):
		return "Cadrei can't answer that start-up screen from here, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForStart:
		return checkStart(w, code, choice, c)
	case cutShort(w.req) && checkable(w.req.Tool):
		return "That's too long for cadrei to check whole, so answer it in its window: " + c(w.attach())
	case !answerable(w.req):
		return "Cadrei can't check this kind of prompt on the screen, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForApproval:
		if _, ok := approvalPick(w.req, choice); ok || choice == "yes" || choice == "no" {
			break
		}
		if ns := choices(w.req); len(ns) > 0 {
			var nums []string
			for _, n := range ns {
				nums = append(nums, strconv.Itoa(n))
			}
			return code + " takes yes, no, or one of its choices: " + strings.Join(nums, ", ") + ". Use " + c("cadrei answer "+code+" no") + ", for example."
		}
		return code + " is a yes-or-no prompt. Use " + c("cadrei answer "+code+" yes") + " or " + c("cadrei answer "+code+" no") + "."
	case many(w.req):
		if _, ok := partPicks(w.req, choice); !ok {
			return partsUsage(w.req, code, c)
		}
	case w.req.Kind == session.ForQuestion:
		n, err := strconv.Atoi(choice)
		if err != nil || strconv.Itoa(n) != choice || n < 1 || n > len(w.req.Options) {
			return fmt.Sprintf("%s has options 1 to %d. Use %s, for example.", code, len(w.req.Options), c("cadrei answer "+code+" 1"))
		}
	case w.req.Kind == session.ForPrompt:
		n, err := strconv.Atoi(choice)
		ok := offered(w.req)
		if err != nil || strconv.Itoa(n) != choice || !slices.Contains(ok, n) {
			var nums []string
			for _, o := range ok {
				nums = append(nums, strconv.Itoa(o))
			}
			return "Cadrei only presses " + strings.Join(nums, " or ") + " in that prompt, never a choice that allows more or changes the mode. Use " +
				c("cadrei answer "+code+" "+nums[0]) + ", or answer it in its window: " + c(w.attach())
		}
	}
	return ""
}

// askReason is the guard's text in the runtime's permission prompt: what
// a yes there does, built from the record only.
func askReason(w *waitingOn, choice string) string {
	if picks, ok := partPicks(w.req, choice); ok && many(w.req) {
		return "Cadrei: answer " + w.place() + " with " + picked(w.req, picks) + "?"
	}
	switch w.req.Kind {
	case session.ForQuestion:
		n, _ := strconv.Atoi(choice)
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + w.req.Options[n-1] + "\"? It asks: " + w.req.Question
	case session.ForPrompt:
		n, _ := strconv.Atoi(choice)
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + w.req.Options[n-1] + "\"? Its screen shows: " + w.req.Question
	case session.ForStart:
		label := w.req.Options[startPick(choice)-1]
		return "Cadrei: answer the start-up screen of " + w.place() + " with \"" + choice + ". " + label + "\"?" + startEffect(w, label) + " It asks: " + w.req.Question
	}
	verb, object, _ := act(w.req)
	label, picked := approvalPick(w.req, choice)
	switch {
	case choice == "no" || label == "No":
		return "Cadrei: tell " + w.place() + " no? It wants to " + verb + ": " + object
	case picked && label != "Yes":
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + label + "\"?" + lastsSentence(w, label) + " It wants to " + verb + ": " + object
	}
	return "Cadrei: allow once? " + capital(w.place()) + " wants to " + verb + ": " + object
}

// answerWait is how long the prompt may take to go after the key.
var answerWait = 2 * time.Second

// runAnswer is cadrei answer [<code> yes|no|<n>].
func runAnswer(e *env) int {
	if len(e.args) == 1 {
		return e.failWith("Use " + cmd("cadrei answer") + " to see who's waiting on you, or " + cmd("cadrei answer <code> yes|no|<n>") + " to answer.")
	}
	if len(e.args) > 1+runtime.MaxParts {
		return e.failWith("Use " + cmd("cadrei answer <code> <n> <n>") + ", one answer for each question, at most " + strconv.Itoa(runtime.MaxParts) + ".")
	}
	if e.memberAnyway("answer for you") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	if !underOwnOrchestrator(rt, r) {
		return e.failWith("cadrei answer runs only in the orchestrator cadrei opened, or in your own terminal.")
	}
	t := session.Default()
	if len(e.args) == 0 {
		return e.listWaiting(r, t)
	}
	// A question asked with others takes an answer for each.
	code, choice := e.args[0], strings.Join(e.args[1:], " ")
	w := findWaiting(r, t, code)
	if w == nil {
		return e.failWith(nothingWaiting(code, cmd))
	}
	if why := checkChoice(w, code, choice, cmd); why != "" {
		return e.failWith(why)
	}
	screen, restore := promptScreen(t, w)
	defer restore()
	// Stopped partway (Esc on the orchestrator's command, a kill), a grown
	// window still goes back to its size.
	stop := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(stop)
		close(done) // the goroutine below ends with this call
	}()
	go func() {
		select {
		case <-stop:
			restore()
			os.Exit(1)
		case <-done:
		}
	}()
	key, why := keyFor(screen, w, choice)
	if why != "" {
		return e.failWith(why)
	}
	// Marked first: the hook of the tool's own end can come before the
	// answer below drops the code, and the prompt log tells it by the mark.
	build := rt.BuildDir(r.Path)
	switch open, err := answering(build, w, true); {
	case err != nil:
		return e.failWith("The " + w.role + "'s record is busy right now, so I didn't press anything. Try again in a moment.")
	case !open:
		return e.failWith(nothingWaiting(code, cmd))
	}
	pressed := time.Now()
	if picks, ok := partPicks(w.req, choice); ok && many(w.req) {
		if why := answerSteps(t, w, picks); why != "" {
			answering(build, w, false)
			e.warn("", why)
			return 1
		}
	} else if w.req.Kind == session.ForStart {
		if why := startSteps(t, w, startPick(choice)); why != "" {
			answering(build, w, false)
			e.warn("", why)
			return 1
		}
	} else if err := t.SendKeys(w.session, w.window, false, key); err != nil {
		answering(build, w, false)
		return e.fail("%s", err)
	}
	// Gone once the screen no longer shows it: for questions answered one
	// step at a time, none of them and not their review, so a review whose
	// last key didn't take isn't taken for sent.
	shows := func(screen string) bool { return promptShows(screen, w) }
	if many(w.req) {
		shows = func(screen string) bool { return stillAsks(screen, w.req) }
	}
	gone := false
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		if !shows(t.Screen(w.session, w.window)) {
			gone = true
			break
		}
		if time.Since(start) >= answerWait {
			break
		}
	}
	if !gone {
		answering(build, w, false)
		e.warn("", "I pressed your answer, but the prompt is still on the "+w.role+"'s screen. Check its window: "+cmd(w.attach()))
		return 1
	}
	answered(build, w, choice, pressed)
	verb, object, _ := act(w.req)
	label, isPick := approvalPick(w.req, choice)
	switch {
	case many(w.req):
		picks, _ := partPicks(w.req, choice)
		e.note("", markDone, "Answered "+w.place()+": "+picked(w.req, picks))
	case w.req.Kind == session.ForStart:
		label := w.req.Options[startPick(choice)-1]
		e.note("", markDone, "Answered the start-up screen of "+w.place()+": "+choice+". "+label)
		if w.req.Tool == runtime.StartLogin {
			e.show("  ", "The login goes on in its window, and may open a browser on its machine. Finish it there: "+cmd(w.attach()))
		}
	case w.req.Kind == session.ForQuestion || w.req.Kind == session.ForPrompt:
		n, _ := strconv.Atoi(choice)
		e.note("", markDone, "Answered "+w.place()+": "+choice+". "+w.req.Options[n-1])
	case isPick && !plainChoice(label):
		e.note("", markDone, "Answered "+w.place()+": "+choice+". "+label)
	case choice == "yes" || label == "Yes":
		e.note("", markDone, "Allowed once: "+w.place()+" can "+verb+" "+object+".")
	default:
		e.note("", markDone, "Told "+w.place()+" no. It won't "+verb+" "+object+", and it's waiting for what to do instead.")
	}
	return 0
}

// growWait is how long a window cadrei grew may take to show the prompt
// again: the runtime draws it anew at the new size.
var growWait = 1500 * time.Millisecond

// promptScreen is what w's window shows, for a check of w's prompt: as it
// is, or when the prompt doesn't show whole there, with the window grown
// for a moment to a size it fits in (restore puts the size back). A window
// someone has open on a small screen, or one cadrei 0.2.2 started at
// 80x24, can be too small for a long command or path. A window an earlier
// check left grown (it was killed first) is put back too.
func promptScreen(t session.Tmux, w *waitingOn) (screen string, restore func()) {
	screen = t.Screen(w.session, w.window)
	// A start-up screen is read as it is: one cadrei can't read whole is
	// answered in its window.
	if !answerable(w.req) || w.req.Kind == session.ForStart || promptShows(screen, w) && !t.Grown(w.session, w.window) {
		return screen, func() {}
	}
	width, height := fitSize(w)
	restore, grown := t.Grow(w.session, w.window, width, height)
	for start := time.Now(); grown && time.Since(start) < growWait; {
		time.Sleep(100 * time.Millisecond)
		if screen = t.Screen(w.session, w.window); promptShows(screen, w) {
			break
		}
	}
	return screen, restore
}

// fitSize is a window size w's prompt fits in whole, from its record:
// wide enough for each line of what it shows (a command, a path, a
// question and its options) to stay on one line, and tall enough for
// all of them and the rest of the prompt around them.
func fitSize(w *waitingOn) (width, height int) {
	lines := strings.Split(w.req.Summary, "⏎")
	if w.req.Tool == "ExitPlanMode" {
		lines = strings.Split(planText(w.req.Summary), "⏎")
	}
	if w.req.Kind != session.ForApproval {
		lines = append(strings.Split(w.req.Question, "⏎"), w.req.Options...)
	}
	for _, p := range w.req.Parts[min(1, len(w.req.Parts)):] {
		lines = append(append(lines, strings.Split(p.Question, "⏎")...), p.Options...)
	}
	longest := 0
	for _, l := range lines {
		longest = max(longest, utf8.RuneCountInString(l))
	}
	// Room for the frame and the option numbers, and for the title, the
	// options with a line each for a description, and the footer.
	return max(session.WindowWidth, longest+12), max(session.WindowHeight, len(lines)+2*len(w.req.Options)+30)
}

// answering marks w's request in its member's state as being answered by
// its code, or with on false takes the mark off, under the member's lock.
// It reports whether the request is still open, and an error when the
// state couldn't be changed (its lock stayed busy, say).
func answering(build string, w *waitingOn, on bool) (bool, error) {
	found := false
	_, _, err := session.ChangeState(build, w.name, 2*time.Second, func(st *session.State) *session.State {
		i := slices.IndexFunc(st.Requests(), func(r *session.Request) bool { return r.Code == w.req.Code })
		if i < 0 {
			return st
		}
		found = true
		next := *st
		next.Open = slices.Clone(st.Open)
		marked := *st.Open[i]
		marked.Answering = time.Time{}
		if on {
			marked.Answering = time.Now()
		}
		next.Open[i] = &marked
		return &next
	})
	return found && err == nil, err
}

// answered ends w's request in its member's state, under the member's
// lock, so nothing offers the code again. A yes or a pick lets the member
// go on: working (its next hook comes only when the tool ends), or still
// waiting when another prompt is open. A no stops its turn, and every
// prompt in it, with no hook to say so: idle, even when the tool's own
// end came first and took the code, unless a prompt came after the key
// was pressed. The prompt log notes it as answered by its code, and any
// other a no ended as cancelled.
func answered(build string, w *waitingOn, choice string, pressed time.Time) {
	no := saysNo(w, choice)
	prev, next, err := session.ChangeState(build, w.name, 2*time.Second, func(st *session.State) *session.State {
		open := slices.ContainsFunc(st.Requests(), func(r *session.Request) bool { return r.Code == w.req.Code })
		if st == nil || !open && (!no || st.State == session.Idle || st.PromptAt.After(pressed)) {
			return st
		}
		next := *st
		next.Open = slices.Clone(st.Open)
		next.Drop(w.req.Code)
		state := session.Working
		switch {
		case no:
			next.Open, next.Calls, state = nil, nil, session.Idle
		case len(next.Open) > 0:
			state = session.Waiting
		case w.req.Kind == session.ForStart:
			// Still before its first hook: the next look reads what it
			// shows now.
			state = session.Starting
		}
		if next.State != state {
			next.Since = time.Now()
		}
		next.State, next.WaitingFor = state, ""
		if r := next.Current(); r != nil {
			next.WaitingFor = r.Kind
		}
		return &next
	})
	if err == nil {
		session.LogPrompts(build, w.name, prev, next, func(code string) string {
			if code == w.req.Code {
				return session.EndedByCode
			}
			return session.EndedCancelled
		}, time.Now())
	}
}

// listWaiting is cadrei answer with no code: who's waiting, with the line
// that answers each.
func (e *env) listWaiting(r *cadreis.Resolved, t session.Tmux) int {
	list := waitingRequests(r, t)
	if len(list) == 0 {
		e.say("Nobody's waiting on you right now.")
		return 0
	}
	e.say("Waiting on you:")
	pad := func(label string) string { return label + spaces(16-len(label)) }
	for _, w := range list {
		e.say("")
		code := w.req.Code
		switch {
		case w.req.Kind == session.ForApproval:
			_, object, kind := act(w.req)
			e.say("%s", "  "+code+"  "+capital(w.place())+" wants to "+kind+":")
			// A command of several lines, line by line.
			for _, l := range strings.Split(object, "⏎") {
				e.say("%s", "         "+l)
			}
			if !answerable(w.req) {
				e.say("%s", "       Answer it in its window: "+cmd(w.attach()))
				continue
			}
			if hasPlainYes(w.req) {
				e.say("%s", "       "+pad("Allow it once:")+cmd("cadrei answer "+code+" yes"))
			}
			e.say("%s", "       "+pad("Say no:")+cmd("cadrei answer "+code+" no"))
			// Its other choices, as its screen showed them.
			if others := otherChoices(w.req); len(others) > 0 {
				e.say("%s", "       Its other choices:")
				for _, n := range others {
					e.say("%s", fmt.Sprintf("         %d. %s%s", n, w.req.Options[n-1], lastsTag(w.req.Options[n-1])))
				}
				e.say("%s", "       "+pad("Pick one:")+cmd("cadrei answer "+code+" "+strconv.Itoa(others[0])))
			}
		case many(w.req) && answerable(w.req):
			lead := " asks:"
			if len(w.req.Parts) > 1 {
				lead = fmt.Sprintf(" asks %d questions:", len(w.req.Parts))
			}
			e.say("%s", "  "+code+"  "+capital(w.place())+lead)
			for _, p := range w.req.Parts {
				more := ""
				if p.Multi {
					more = " (pick one or more)"
				}
				e.say("%s", "         "+p.Question+more)
				for i, o := range p.Options {
					e.say("%s", fmt.Sprintf("           %d. %s", i+1, o))
				}
			}
			e.say("%s", "       "+pad("Answer:")+cmd(partsExample(w.req)))
		case w.req.Kind == session.ForQuestion && answerable(w.req):
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			for i, o := range w.req.Options {
				e.say("%s", fmt.Sprintf("         %d. %s", i+1, o))
			}
			e.say("%s", "       "+pad("Answer:")+cmd("cadrei answer "+code+" 1"))
		case w.req.Kind == session.ForQuestion:
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			e.say("%s", "       It has more options or questions than cadrei keeps. Answer it in its window: "+cmd(w.attach()))
		case w.req.Kind == session.ForPrompt && answerable(w.req):
			e.say("%s", "  "+code+"  "+capital(w.place())+" shows a prompt:")
			for _, l := range strings.Split(w.req.Question, "⏎") {
				e.say("%s", "         "+strings.TrimSpace(l))
			}
			ok := offered(w.req)
			for _, n := range ok {
				e.say("%s", fmt.Sprintf("         %d. %s", n, w.req.Options[n-1]))
			}
			e.say("%s", "       "+pad("Answer:")+cmd("cadrei answer "+code+" "+strconv.Itoa(ok[0])))
		case w.req.Kind == session.ForStart:
			e.say("%s", "  "+code+"  "+capital(w.place())+" waits on a start-up screen:")
			e.say("%s", "         "+w.req.Question)
			ns, refused := startOffered(&w)
			if !answerable(w.req) || len(ns) == 0 {
				e.say("%s", "       Answer it in its window: "+cmd(w.attach()))
				continue
			}
			for i, o := range w.req.Options {
				switch n := i + 1; {
				case slices.Contains(ns, n):
					e.say("%s", fmt.Sprintf("         %d. %s", n, o))
				case refused != "" && trustYes(w.req, o):
					e.say("%s", fmt.Sprintf("         %d. %s (only in its window: %s)", n, o, refused))
				default:
					e.say("%s", fmt.Sprintf("         %d. %s (only in its window)", n, o))
				}
			}
			e.say("%s", "       "+pad("Answer:")+cmd("cadrei answer "+code+" "+strconv.Itoa(ns[0])))
		default:
			e.say("%s", "  "+code+"  "+capital(w.place())+" is waiting on you.")
			e.say("%s", "       Answer it in its window: "+cmd(w.attach()))
		}
	}
	return 0
}

// promptOption is one numbered option of a prompt on a member's screen.
type promptOption struct {
	n     int
	label string
	digit int      // the column of its number
	col   int      // the column its label starts at
	under []string // the lines under it, before the next option
}

// optionLine is a numbered option: the pointer at the chosen one, the
// number, the label.
var optionLine = regexp.MustCompile(`^(\s*(?:[❯›>]\s*)?)([1-9])\.\s+(.*?)\s*$`)

// screenPrompt reads the prompt at the bottom of a member's screen: the
// numbered options above its last "Esc to cancel" line, numbered from 1,
// and the prompt's box above them, back to the solid rule it starts
// after, which holds its header, the command or the question. A prompt
// taller than the pane has its options, and no box: its top isn't on the
// screen (the runtime draws on the whole screen, with nothing kept above).
// A label that wraps is read whole, with the lines it goes on to.
func screenPrompt(screen string) (opts []promptOption, box []string, ok bool) {
	return screenPromptOf(screen, false)
}

// openTools are the tools whose permission prompt has no "Esc to cancel"
// line under its options, recorded live: a page fetch's and a plan's.
var openTools = setOf("WebFetch", "ExitPlanMode")

// promptOf is screenPrompt for w's prompt: one of openTools' is also read
// with no "Esc to cancel" line (openFoot). Any other must have one.
func promptOf(screen string, w *waitingOn) (opts []promptOption, box []string, ok bool) {
	return screenPromptOf(screen, w.req.Kind == session.ForApproval && openTools[w.req.Tool])
}

// screenPromptOf is screenPrompt, and with open, also a prompt with no
// "Esc to cancel" line.
func screenPromptOf(screen string, open bool) (opts []promptOption, box []string, ok bool) {
	opts, box, ok = screenOptions(screen, open)
	// What goes on where a label starts is the rest of it, except in a
	// question, whose options have their descriptions there.
	if !slices.ContainsFunc(opts, func(o promptOption) bool { return questionExtra(o.label) }) {
		for i, o := range opts {
			for _, u := range o.under {
				if indent(u) != o.col {
					break
				}
				opts[i].label += " " + strings.TrimSpace(u)
			}
		}
	}
	return opts, box, ok
}

// screenOptions is screenPromptOf, with each option's label as its own
// line has it.
func screenOptions(screen string, open bool) (opts []promptOption, box []string, ok bool) {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	foot := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "Esc to cancel") {
			foot = i
			break
		}
	}
	if foot < 0 && open {
		foot = openFoot(lines)
	}
	if foot < 0 {
		return nil, nil, false
	}
	first := -1
	var under []string // the lines under the option above, top down
	for i := foot - 1; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			if len(opts) == 0 {
				continue
			}
			break
		}
		// A question's own options are ruled off from the two it adds.
		if solidRule(line) {
			continue
		}
		if m := optionLine.FindStringSubmatchIndex(line); m != nil {
			n := int(line[m[4]] - '0')
			if len(opts) > 0 && n != opts[0].n-1 {
				return nil, nil, false
			}
			o := promptOption{n: n, label: line[m[6]:m[7]], digit: utf8.RuneCountInString(line[:m[4]]), col: utf8.RuneCountInString(line[:m[6]]), under: under}
			opts, under = append([]promptOption{o}, opts...), nil
			if n == 1 {
				first = i
				break
			}
			continue
		}
		// A description under an option, or the rest of a label that
		// wraps, is indented past the number of the option below it.
		if len(opts) == 0 || indent(line) > opts[0].digit {
			under = append([]string{line}, under...)
			continue
		}
		break
	}
	if first < 0 || len(opts) == 0 || opts[0].n != 1 {
		return nil, nil, false
	}
	// The box: back to the solid rule above it (blank lines, as in a
	// command's own text, don't end it). With no rule there, the box's top
	// isn't on the screen.
	for i := first - 1; ; i-- {
		switch {
		case i < 0:
			return opts, nil, false
		case solidRule(lines[i]):
			return opts, box, true
		}
		box = append([]string{lines[i]}, box...)
	}
}

// openFoot is where a prompt drawn with no "Esc to cancel" line under it
// ends (a page fetch's, a plan's, both recorded live): its options are the
// last on the screen, one with the pointer, and under the last one's own
// lines come at most footLines more, after a blank line. It's the index
// of the line after the options, or -1 for none.
func openFoot(lines []string) int {
	at := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-maxPromptLines; i-- {
		if pointerLine.MatchString(lines[i]) {
			at = i
			break
		}
	}
	if at < 0 {
		return -1
	}
	end := at + 1
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	rest := 0
	for _, l := range lines[end:] {
		if strings.TrimSpace(l) != "" {
			rest++
		}
	}
	if rest > footLines {
		return -1
	}
	return end
}

// Bounds on a prompt with no "Esc to cancel" line: how far up from the
// screen's bottom its pointer may be, and how many lines may follow it.
const (
	maxPromptLines = 30
	footLines      = 3
)

// checkbox is how a multi-select question's options start.
var checkbox = regexp.MustCompile(`^\[[ ✔✓xX]\]`)

// takesMore reports whether a prompt on the screen takes more than one
// key to answer: a multi-select question (its options have checkboxes),
// or more than one question at once (its tab bar has a Submit tab, or a
// tab for each). One key there only ticks a box or moves on, and the
// dialog stays.
func takesMore(box []string, opts []promptOption) bool {
	for _, l := range box {
		if strings.Contains(l, "✔ Submit") || strings.Count(l, "☐")+strings.Count(l, "☒") > 1 {
			return true
		}
	}
	return slices.ContainsFunc(opts, func(o promptOption) bool { return checkbox.MatchString(o.label) })
}

// pointerLine is the option the pointer is at, in any prompt the runtime
// draws, with or without an "Esc to cancel" line under it.
var pointerLine = regexp.MustCompile(`^\s*❯\s*[1-9]\.\s`)

// noPrompt reports whether a member's screen shows no prompt at its
// bottom, where the runtime draws one: the empty input line, framed by
// solid rules, near the bottom, or in the lines a prompt's options take
// there, no "Esc to cancel" line and no option with the pointer at it.
// Earlier output above (an old "Esc to cancel", an echoed "❯ 1." line)
// doesn't count. No screen at all is one cadrei couldn't read, so it
// doesn't count either.
func noPrompt(screen string) bool {
	if screen == "" {
		return false
	}
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	bottom := lines[max(0, len(lines)-maxPromptLines):]
	if idleInput(lines) {
		return true
	}
	for _, l := range bottom {
		if strings.Contains(l, "Esc to cancel") || pointerLine.MatchString(l) {
			return false
		}
	}
	return true
}

// inputLine is the runtime's input line: the pointer, then what's typed.
var inputLine = regexp.MustCompile(`^\s*❯(\s|$)`)

// idleInput reports whether the bottom lines of a screen hold the input
// line the runtime shows when no prompt is up, a solid rule right above
// it and one just under it.
func idleInput(lines []string) bool {
	for i := len(lines) - 1; i >= 1 && i >= len(lines)-footLines-3; i-- {
		if !inputLine.MatchString(lines[i]) || pointerLine.MatchString(lines[i]) || !solidRule(lines[i-1]) {
			continue
		}
		for _, l := range lines[i+1 : min(len(lines), i+3)] {
			if solidRule(l) {
				return true
			}
		}
	}
	return false
}

// solidRule reports whether a line is a rule of ─, as the prompt's box
// starts after. Dashed ones (╌) frame a command or a file's text in it.
func solidRule(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && strings.Trim(t, "─━") == ""
}

// dashedRule reports whether a line is a rule of ╌, as frames a command or
// a file's text in a prompt's box.
func dashedRule(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && strings.Trim(t, "╌") == ""
}

func indent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// squeeze drops what a screen's wrapping and framing changes: spaces, line
// breaks, the frame, and the record's line-break mark and ellipsis.
func squeeze(text string) string {
	text = strings.TrimSuffix(text, "…")
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '│', '┃', '⏎':
			return -1
		}
		return r
	}, text)
}

// promptShows reports whether a member's screen shows w's prompt: its
// options, and in its box, exactly what the record says. A question's
// text and every one of its labels; a command as a whole, and nothing
// else, between the dashed rules; a file by its path from the member's
// folder (or in full), on the line under the box's title.
func promptShows(screen string, w *waitingOn) bool {
	if w.req.Kind == session.ForStart {
		_, ok := startShows(screen, w.req)
		return ok && answerable(w.req)
	}
	if many(w.req) {
		// Before its first key, its first question.
		return answerable(w.req) && partShows(screen, w.req.Parts[0])
	}
	opts, box, ok := promptOf(screen, w)
	if !ok || !answerable(w.req) {
		return false
	}
	req := w.req
	switch req.Kind {
	case session.ForPrompt:
		// Read off the screen: the same options, and the same box, whole.
		if len(opts) != len(req.Options) {
			return false
		}
		for i, o := range req.Options {
			if !sameLabel(opts[i].label, o) {
				return false
			}
		}
		return squeeze(strings.Join(box, "\n")) == squeeze(req.Question)
	case session.ForQuestion:
		if len(opts) < len(req.Options) {
			return false
		}
		for i, o := range req.Options {
			if !labelShown(opts[i], o) {
				return false
			}
		}
		return strings.Contains(squeeze(strings.Join(box, "\n")), squeeze(req.Question))
	}
	rules := dashedRules(box)
	switch req.Tool {
	case "Write", "Edit":
		// The box's title (Create file, Edit file), then the file, then
		// the file's text between two dashed rules, and no other.
		return len(rules) == 2 && rules[0] == 2 && pathShown(box[1:2], req.Summary, w.dir)
	case "MultiEdit", "NotebookEdit":
		// Not seen live: as an Edit's, under the title Claude Code gives
		// it, with its changes between dashed rules.
		return titled(box, fileTitles[req.Tool]) && len(rules) >= 2 && rules[0] == 2 && pathShown(box[1:2], req.Summary, w.dir)
	case "Read":
		// Not seen live: the title, then the file.
		return titled(box, fileTitles[req.Tool]) && len(box) > 1 && pathShown(box[1:2], req.Summary, w.dir)
	case "WebFetch":
		return fetchShown(box, req.Summary)
	case "WebSearch":
		return searchShown(box, req.Summary)
	case "ExitPlanMode":
		return planShown(screen, planText(req.Summary))
	case "Bash":
		return commandShown(box, req.Summary)
	}
	return strings.HasPrefix(req.Tool, "mcp__") && mcpShown(box, req.Tool, req.Summary)
}

// planShown reports whether a plan approval's screen (recorded live,
// 2.1.296) shows plan: right above the prompt's own box, the plan between
// two dashed rules, under the line that ends "plan:", whole. Its markdown
// is drawn without some of its marks, so both are read squeezed and
// without them.
func planShown(screen, plan string) bool {
	if plan == "" {
		return false
	}
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	// Up from the pointer to the solid rule the prompt's box starts after.
	rule := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if pointerLine.MatchString(lines[i]) {
			for rule = i - 1; rule >= 0 && !solidRule(lines[rule]); rule-- {
			}
			break
		}
	}
	// Right above it, the plan's closing dashed rule; above that, its
	// opening one, with no solid rule between.
	end := rule - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	if rule < 0 || end < 0 || !dashedRule(lines[end]) {
		return false
	}
	start := end - 1
	for start >= 0 && !dashedRule(lines[start]) {
		if solidRule(lines[start]) {
			return false
		}
		start--
	}
	if start < 1 || !strings.HasSuffix(strings.TrimSpace(lines[start-1]), "plan:") {
		return false
	}
	return markless(strings.Join(lines[start+1:end], "\n")) == markless(plan)
}

// markless is text squeezed, without the marks markdown is drawn without
// (#, *, _, `, >) and a list's bullets, which it may draw another way.
func markless(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '#', '*', '_', '`', '>', '-', '•':
			return -1
		}
		return r
	}, squeeze(text))
}

// fileTitles are the titles of the prompts of file tools cadrei hasn't
// seen live, from Claude Code 2.1.302's own text.
var fileTitles = map[string][]string{
	"MultiEdit":    {"Edit file"},
	"NotebookEdit": {"Edit notebook"},
	"Read":         {"Read file"},
}

// titled reports whether box starts with one of titles.
func titled(box []string, titles []string) bool {
	return len(box) > 0 && slices.Contains(titles, strings.TrimSpace(box[0]))
}

// framed is what box has between its first two dashed rules, or nil.
func framed(box []string) []string {
	rules := dashedRules(box)
	if len(rules) < 2 {
		return nil
	}
	return box[rules[0]+1 : rules[1]]
}

// fetchShown reports whether a page fetch's box (recorded live, 2.1.296)
// is for url: under its title, Fetch, and the line naming the site, the
// framed lines hold "url: " and the URL, whole (a long one wrapped onto
// the lines under it), then the prompt for the page. Claude Code shows a
// URL with no path with a slash after its host.
func fetchShown(box []string, url string) bool {
	if rules := dashedRules(box); !titled(box, []string{"Fetch"}) || len(rules) < 2 || rules[0] != 2 {
		return false
	}
	var shown []string
	for _, l := range framed(box) {
		l = strings.TrimSpace(strings.Trim(l, " │┃"))
		if strings.HasPrefix(l, "prompt:") {
			break
		}
		shown = append(shown, l)
	}
	got, ok := strings.CutPrefix(strings.Join(shown, ""), "url:")
	got = strings.TrimSpace(got)
	if !ok || url == "" {
		return false
	}
	if got == url {
		return true
	}
	u, err := neturl.Parse(url)
	return err == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && got == url+"/"
}

// searchLead is how a web search's prompt says what it searches for,
// after the runtime's name, in Claude Code 2.1.302's own text (not seen
// live).
const searchLead = " wants to search the web for: "

// searchShown reports whether a web search's box is for query: the line
// under its title that says so, with the query whole after it (wrapped
// onto the lines under it, up to a blank line or a rule), and nothing
// more, like a list of the only sites it may search.
func searchShown(box []string, query string) bool {
	if !titled(box, []string{"Web Search"}) || len(box) < 2 {
		return false
	}
	_, after, ok := strings.Cut(box[1], searchLead)
	if !ok || query == "" {
		return false
	}
	got := squeeze(after)
	for _, m := range box[2:] {
		if strings.TrimSpace(m) == "" || dashedRule(m) || solidRule(m) {
			break
		}
		got += squeeze(m)
	}
	return got == squeeze(query)
}

// mcpShown reports whether an MCP tool's box (recorded live, 2.1.296) is
// for tool, called with what summary records: its title, Tool use, then
// the server's name, a dash, the tool's own and "(MCP)", then each of its
// arguments, in order, as "name: value" (the value as JSON) between
// dashed rules, and no other.
func mcpShown(box []string, tool, summary string) bool {
	parts := strings.SplitN(tool, "__", 3)
	input, ok := strings.CutPrefix(summary, tool+" ")
	if len(parts) != 3 || parts[1] == "" || !ok || !titled(box, []string{"Tool use"}) || len(box) < 2 {
		return false
	}
	head, rules := strings.TrimSpace(box[1]), dashedRules(box)
	if !strings.HasPrefix(head, parts[1]+" - ") || !strings.HasSuffix(head, "(MCP)") || len(rules) < 2 || rules[0] != 2 {
		return false
	}
	args, ok := argLines(input)
	return ok && len(args) > 0 && squeeze(strings.Join(framed(box), "")) == squeeze(strings.Join(args, ""))
}

// argLines are a tool call's arguments as an MCP tool's prompt shows them,
// in order: "name: value", the value as compact JSON. ok is false for
// input that isn't one JSON object.
func argLines(input string) (lines []string, ok bool) {
	dec := json.NewDecoder(strings.NewReader(input))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		t, err := dec.Token()
		name, isName := t.(string)
		var raw json.RawMessage
		if err != nil || !isName || dec.Decode(&raw) != nil {
			return nil, false
		}
		var value bytes.Buffer
		if json.Compact(&value, raw) != nil {
			return nil, false
		}
		lines = append(lines, name+": "+value.String())
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') || dec.More() {
		return nil, false
	}
	return lines, true
}

// dashedRules are the indexes of box's dashed rules.
func dashedRules(box []string) []int {
	var rules []int
	for i, line := range box {
		if dashedRule(line) {
			rules = append(rules, i)
		}
	}
	return rules
}

// commandShown reports whether the lines between the box's first two
// dashed rules, read squeezed, are want exactly: the command, whole, and
// not the description above it.
func commandShown(box []string, want string) bool {
	rules := dashedRules(box)
	w := squeeze(want)
	return len(rules) >= 2 && w != "" && squeeze(strings.Join(box[rules[0]+1:rules[1]], "")) == w
}

// pathShown reports whether one of the lines holds path alone: as it is,
// or from dir, the member's folder, when it's inside it.
func pathShown(lines []string, path, dir string) bool {
	want := map[string]bool{path: true}
	if dir != "" {
		for _, d := range []string{dir, paths.Real(dir)} {
			for _, p := range []string{path, paths.Real(path)} {
				if rel, err := filepath.Rel(d, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
					want[rel] = true
				}
			}
		}
	}
	for _, line := range lines {
		if want[strings.TrimSpace(strings.Trim(line, " │┃"))] {
			return true
		}
	}
	return false
}

// sameLabel compares a label on the screen with the recorded one, which
// may be cut short.
func sameLabel(screen, recorded string) bool {
	if cut, ok := strings.CutSuffix(recorded, "…"); ok {
		return strings.HasPrefix(screen, cut)
	}
	return screen == recorded
}

// labelShown reports whether a question's option on the screen shows the
// recorded label: on its own line, or going on to the lines under it, as
// a label too long for the window wraps. A description starts at the
// same column, so a line goes with the label only while the label, read
// squeezed, goes on with it, and the whole label must be there.
func labelShown(o promptOption, recorded string) bool {
	if sameLabel(o.label, recorded) {
		return true
	}
	want, _ := strings.CutSuffix(recorded, "…")
	want = squeeze(want)
	got := squeeze(o.label)
	for _, u := range o.under {
		if got == "" || !strings.HasPrefix(want, got) || indent(u) != o.col {
			return false
		}
		got += squeeze(u)
		if got == want || strings.HasSuffix(recorded, "…") && strings.HasPrefix(got, want) {
			return true
		}
	}
	return false
}

// keyFor is the one key that gives choice in w's prompt, checked against
// the screen first, or why no key is pressed. yes is only the option
// labeled exactly Yes (never one that allows more, or changes the mode);
// no is the option labeled exactly No, else Escape.
func keyFor(screen string, w *waitingOn, choice string) (key, why string) {
	return keyOn(screen, w, choice, cmd)
}

// keyOn is keyFor, with c to format a command (cmd for the screen, plain
// text for the guard).
func keyOn(screen string, w *waitingOn, choice string, c func(string) string) (key, why string) {
	window := " Answer it in its window: " + c(w.attach())
	if !promptShows(screen, w) {
		opts, box, _ := promptOf(screen, w)
		switch {
		case len(opts) > 0 && box == nil:
			return "", "That prompt is taller than the " + w.role + "'s window, so cadrei can't see it whole." + window
		case slices.Contains([]string{"Write", "Edit", "MultiEdit", "NotebookEdit", "Read"}, w.req.Tool) && len(box) > 1 && strings.HasPrefix(strings.TrimSpace(box[1]), "…"):
			return "", "The " + w.role + "'s window shows that file's path cut short, so cadrei can't check it." + window
		}
		return "", "I couldn't find that prompt on the " + w.role + "'s screen, so I didn't press anything." + window
	}
	opts, _, _ := promptOf(screen, w)
	if w.req.Kind == session.ForQuestion || w.req.Kind == session.ForPrompt || w.req.Kind == session.ForStart {
		return choice, ""
	}
	if _, ok := approvalPick(w.req, choice); ok {
		// By number: the screen's options must be the ones recorded, so the
		// number gives the label the user was shown.
		same := len(opts) == len(w.req.Options)
		for i := 0; same && i < len(opts); i++ {
			same = sameLabel(opts[i].label, w.req.Options[i])
		}
		if !same {
			return "", "I couldn't find that prompt on the " + w.role + "'s screen, so I didn't press anything." + window
		}
		return choice, ""
	}
	if others := otherChoices(w.req); choice == "yes" && !hasPlainYes(w.req) && len(others) > 0 {
		return "", "This prompt has no plain \"Yes\". Pick one of its choices with " + c("cadrei answer "+w.req.Code+" <n>") + " (" + numberedOf(w.req, others) + "), or answer it in its window: " + c(w.attach())
	}
	label := "Yes"
	if choice == "no" {
		label = "No"
	}
	for _, o := range opts {
		if o.label == label {
			return strconv.Itoa(o.n), ""
		}
	}
	if choice == "no" {
		return "Escape", ""
	}
	return "", "This prompt has no plain \"Yes\", only choices that would allow it for good, so answer it in its window: " + c(w.attach())
}

// underOwnOrchestrator reports whether this run may answer for crew r: the
// closest of the runtime's sessions it runs under is r's own orchestrator,
// started with its settings file (which holds the guard), or it runs under
// none (the user's own terminal). The every-chat orchestrator and the
// user's other chats have no cadrei guard, so they don't get to. A run
// under no session the process list shows, with the runtime's own marks in
// its environment, runs under one cadrei doesn't recognize: it doesn't
// either.
func underOwnOrchestrator(rt runtime.Runtime, r *cadreis.Resolved) bool {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,args=").Output()
	if err != nil {
		return false
	}
	parent, args := map[int]int{}, map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		var p, pp int
		if n, _ := fmt.Sscan(line, &p, &pp); n != 2 {
			continue
		}
		parent[p] = pp
		fields := strings.Fields(line)
		args[p] = strings.Join(fields[2:], " ")
	}
	own := filepath.Join(rt.BuildDir(r.Path), "orchestrator-settings.json")
	for p, n := parent[selfPID()], 0; p > 1 && n < 256; p, n = parent[p], n+1 {
		settings, ok := rt.Sessions().SessionSettings(args[p])
		if !ok {
			continue
		}
		return settings != "" && (settings == own || paths.Real(settings) == paths.Real(own))
	}
	return !rt.Sessions().InSession()
}
