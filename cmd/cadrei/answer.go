//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	}
	if strings.HasPrefix(req.Tool, "mcp__") {
		return "use", req.Summary, "use a tool"
	}
	return "use", strings.TrimSpace(req.Tool + " " + req.Summary), "use a tool"
}

// screenTools are the tools whose permission prompt cadrei knows how to
// check on a member's screen, from recorded screens: a command, a file to
// write or edit. A page to fetch isn't one: its prompt (2.1.296) has no
// "Esc to cancel" line to find it by, and shows the URL its own way.
var screenTools = setOf("Bash", "Write", "Edit")

// answerable reports whether cadrei can answer req from here: a question
// with one pick, a prompt for one of screenTools with what it would run
// or touch recorded in full, or a prompt read off the screen whole, with
// an option cadrei may press. Any other is answered in the member's
// window.
func answerable(req *session.Request) bool {
	switch req.Kind {
	case session.ForQuestion:
		return !req.Multi && req.Question != "" && len(req.Options) > 0
	case session.ForPrompt:
		return !req.Multi && req.Question != "" && len(picks(req.Options)) > 0
	case session.ForApproval:
		return screenTools[req.Tool] && req.Summary != "" && !cutShort(req)
	}
	return false
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
	if req.Kind == session.ForPrompt {
		return picks(req.Options)
	}
	var out []int
	for i := range req.Options {
		out = append(out, i+1)
	}
	return out
}

// saysNo reports whether choice tells w's member no: an approval's no, or
// the option labeled No of a prompt read off the screen that isn't a
// question. Either ends the member's turn, with no hook to say so.
func saysNo(w *waitingOn, choice string) bool {
	switch w.req.Kind {
	case session.ForApproval:
		return choice == "no"
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
			return who + " wants to " + verb + ": " + object + "." + window
		}
		return who + " wants to " + verb + ": " + object + ". Answer with cadrei answer " + w.req.Code + " yes, or no."
	case w.req.Kind == session.ForQuestion && answerable(w.req):
		return who + " asks: " + w.req.Question + " Options: " + numbered(w.req) + ". The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	case w.req.Kind == session.ForQuestion:
		return who + " asks: " + w.req.Question + window
	case w.req.Kind == session.ForPrompt && answerable(w.req):
		return who + " shows a prompt: " + w.req.Question + " Options: " + numbered(w.req) + ". The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	}
	return who + " waits on something only its window can answer: " + w.attach() + "."
}

// numbered lists the options an answer may pick in req as 1. a, 2. b.
func numbered(req *session.Request) string {
	var opts []string
	for _, n := range offered(req) {
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
		return "This question needs more than one pick, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForPrompt && !answerable(w.req):
		return "Cadrei couldn't read that prompt whole off the screen, so answer it in its window: " + c(w.attach())
	case cutShort(w.req) && screenTools[w.req.Tool]:
		return "That's too long for cadrei to check whole, so answer it in its window: " + c(w.attach())
	case !answerable(w.req):
		return "Cadrei can't check this kind of prompt on the screen, so answer it in its window: " + c(w.attach())
	case w.req.Kind == session.ForApproval:
		if choice != "yes" && choice != "no" {
			return code + " is a yes-or-no prompt. Use " + c("cadrei answer "+code+" yes") + " or " + c("cadrei answer "+code+" no") + "."
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
	switch w.req.Kind {
	case session.ForQuestion:
		n, _ := strconv.Atoi(choice)
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + w.req.Options[n-1] + "\"? It asks: " + w.req.Question
	case session.ForPrompt:
		n, _ := strconv.Atoi(choice)
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + w.req.Options[n-1] + "\"? Its screen shows: " + w.req.Question
	}
	verb, object, _ := act(w.req)
	if choice == "no" {
		return "Cadrei: tell " + w.place() + " no? It wants to " + verb + ": " + object
	}
	return "Cadrei: allow once? " + capital(w.place()) + " wants to " + verb + ": " + object
}

// answerWait is how long the prompt may take to go after the key.
var answerWait = 2 * time.Second

// runAnswer is cadrei answer [<code> yes|no|<n>].
func runAnswer(e *env) int {
	if len(e.args) != 0 && len(e.args) != 2 {
		return e.failWith("Use " + cmd("cadrei answer") + " to see who's waiting on you, or " + cmd("cadrei answer <code> yes|no|<n>") + " to answer.")
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
	code, choice := e.args[0], e.args[1]
	w := findWaiting(r, t, code)
	if w == nil {
		return e.failWith(nothingWaiting(code, cmd))
	}
	if why := checkChoice(w, code, choice, cmd); why != "" {
		return e.failWith(why)
	}
	screen, restore := promptScreen(t, w)
	defer restore()
	key, why := keyFor(screen, w, choice)
	if why != "" {
		return e.failWith(why)
	}
	// Marked first: the hook of the tool's own end can come before the
	// answer below drops the code, and the prompt log tells it by the mark.
	build := rt.BuildDir(r.Path)
	if !answering(build, w, true) {
		return e.failWith(nothingWaiting(code, cmd))
	}
	pressed := time.Now()
	if err := t.SendKeys(w.session, w.window, false, key); err != nil {
		answering(build, w, false)
		return e.fail("%s", err)
	}
	gone := false
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		if !promptShows(t.Screen(w.session, w.window), w) {
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
	switch {
	case w.req.Kind == session.ForQuestion || w.req.Kind == session.ForPrompt:
		n, _ := strconv.Atoi(choice)
		e.note("", markDone, "Answered "+w.place()+": "+choice+". "+w.req.Options[n-1])
	case choice == "yes":
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
// 80x24, can be too small for a long command or path.
func promptScreen(t session.Tmux, w *waitingOn) (screen string, restore func()) {
	screen = t.Screen(w.session, w.window)
	if promptShows(screen, w) || !answerable(w.req) {
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
	if w.req.Kind != session.ForApproval {
		lines = append(strings.Split(w.req.Question, "⏎"), w.req.Options...)
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
// It reports whether the request is still open.
func answering(build string, w *waitingOn, on bool) bool {
	found := false
	session.ChangeState(build, w.name, 2*time.Second, func(st *session.State) *session.State {
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
	return found
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
			e.say("%s", "       "+pad("Allow it once:")+cmd("cadrei answer "+code+" yes"))
			e.say("%s", "       "+pad("Say no:")+cmd("cadrei answer "+code+" no"))
		case w.req.Kind == session.ForQuestion && answerable(w.req):
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			for i, o := range w.req.Options {
				e.say("%s", fmt.Sprintf("         %d. %s", i+1, o))
			}
			e.say("%s", "       "+pad("Answer:")+cmd("cadrei answer "+code+" 1"))
		case w.req.Kind == session.ForQuestion:
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			e.say("%s", "       This needs more than one pick. Answer it in its window: "+cmd(w.attach()))
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
	opts, box, ok = screenOptions(screen)
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

// screenOptions is screenPrompt, with each option's label as its own line
// has it.
func screenOptions(screen string) (opts []promptOption, box []string, ok bool) {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	foot := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "Esc to cancel") {
			foot = i
			break
		}
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

// noPrompt reports whether a member's screen, read whole, shows no
// prompt: no "Esc to cancel" line, and no option with the pointer at it.
// No screen at all is one cadrei couldn't read, so it doesn't count.
func noPrompt(screen string) bool {
	if screen == "" {
		return false
	}
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(l, "Esc to cancel") || pointerLine.MatchString(l) {
			return false
		}
	}
	return true
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
	opts, box, ok := screenPrompt(screen)
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
	switch req.Tool {
	case "Write", "Edit":
		// The box's title (Create file, Edit file), then the file, then
		// the file's text between two dashed rules, and no other.
		rules := dashedRules(box)
		return len(rules) == 2 && rules[0] == 2 && pathShown(box[1:2], req.Summary, w.dir)
	}
	return commandShown(box, req.Summary)
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
		opts, box, _ := screenPrompt(screen)
		switch {
		case len(opts) > 0 && box == nil:
			return "", "That prompt is taller than the " + w.role + "'s window, so cadrei can't see it whole." + window
		case (w.req.Tool == "Write" || w.req.Tool == "Edit") && len(box) > 1 && strings.HasPrefix(strings.TrimSpace(box[1]), "…"):
			return "", "The " + w.role + "'s window shows that file's path cut short, so cadrei can't check it." + window
		}
		return "", "I couldn't find that prompt on the " + w.role + "'s screen, so I didn't press anything." + window
	}
	opts, _, _ := screenPrompt(screen)
	if w.req.Kind == session.ForQuestion || w.req.Kind == session.ForPrompt {
		return choice, ""
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
