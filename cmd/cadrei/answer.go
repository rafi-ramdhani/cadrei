//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	var out []waitingOn
	for _, v := range views {
		for _, m := range v.Members {
			if m.status.State != session.Waiting || m.status.Request == nil {
				continue
			}
			out = append(out, waitingOn{team: v.Team, project: v.Project, role: m.Role, session: v.info.Name, window: m.window,
				name: m.name, since: m.status.Since, req: m.status.Request})
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

// note says a request for the orchestrator, from cadrei's record: who,
// its code, what it wants or asks, and how it's answered.
func (w *waitingOn) note() string {
	who := capital(w.place()) + " (" + w.req.Code + ")"
	switch {
	case w.req.Kind == session.ForApproval:
		verb, object, _ := act(w.req)
		return who + " wants to " + verb + ": " + object + ". Answer with cadrei answer " + w.req.Code + " yes, or no."
	case w.req.Kind == session.ForQuestion && !w.req.Multi:
		var opts []string
		for i, o := range w.req.Options {
			opts = append(opts, fmt.Sprintf("%d. %s", i+1, o))
		}
		return who + " asks: " + w.req.Question + " Options: " + strings.Join(opts, ", ") + ". The user picks one by typing ! cadrei answer " + w.req.Code + " <n>."
	case w.req.Kind == session.ForQuestion:
		return who + " asks: " + w.req.Question + " Only its window can answer that: " + w.attach() + "."
	}
	return who + " waits on something only its window can answer: " + w.attach() + "."
}

// nothingWaiting says a code names no request.
func nothingWaiting(code string, c func(string) string) string {
	return "Nothing's waiting under " + printable(code) + " anymore. Run " + c("cadrei answer") + " to see what's waiting now."
}

// checkChoice says why choice can't answer w's request, or "". c formats
// a command (cmd for the screen, plain text for the guard).
func checkChoice(w *waitingOn, code, choice string, c func(string) string) string {
	switch w.req.Kind {
	case session.ForApproval:
		if choice != "yes" && choice != "no" {
			return code + " is a yes-or-no prompt. Use " + c("cadrei answer "+code+" yes") + " or " + c("cadrei answer "+code+" no") + "."
		}
	case session.ForQuestion:
		if w.req.Multi {
			return "This question needs more than one pick, so answer it in its window: " + c(w.attach())
		}
		n, err := strconv.Atoi(choice)
		if err != nil || strconv.Itoa(n) != choice || n < 1 || n > len(w.req.Options) {
			return fmt.Sprintf("%s has options 1 to %d. Use %s, for example.", code, len(w.req.Options), c("cadrei answer "+code+" 1"))
		}
	default:
		return "Cadrei can't answer this kind of request, so answer it in its window: " + c(w.attach())
	}
	return ""
}

// askReason is the guard's text in the runtime's permission prompt: what
// a yes there does, built from the record only.
func askReason(w *waitingOn, choice string) string {
	if w.req.Kind == session.ForQuestion {
		n, _ := strconv.Atoi(choice)
		return "Cadrei: answer " + w.place() + " with \"" + choice + ". " + w.req.Options[n-1] + "\"? It asks: " + w.req.Question
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
	key, why := keyFor(t.Screen(w.session, w.window), w, choice)
	if why != "" {
		return e.failWith(why)
	}
	if err := t.SendKeys(w.session, w.window, false, key); err != nil {
		return e.fail("%s", err)
	}
	gone := false
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		if !promptShows(t.Screen(w.session, w.window), w.req) {
			gone = true
			break
		}
		if time.Since(start) >= answerWait {
			break
		}
	}
	if !gone {
		e.warn("", "I pressed your answer, but the prompt is still on the "+w.role+"'s screen. Check its window: "+cmd(w.attach()))
		return 1
	}
	verb, object, _ := act(w.req)
	switch {
	case w.req.Kind == session.ForQuestion:
		n, _ := strconv.Atoi(choice)
		e.note("", markDone, "Answered "+w.place()+": "+choice+". "+w.req.Options[n-1])
	case choice == "yes":
		e.note("", markDone, "Allowed once: "+w.place()+" can "+verb+" "+object+".")
	default:
		// A no stops the member's turn, and no hook says so: it's idle,
		// with nothing to answer.
		if path := session.StatePath(rt.BuildDir(r.Path), w.name); path != "" {
			if st := session.ReadState(path); st != nil && st.Request != nil && st.Request.Code == w.req.Code {
				st.State, st.Since, st.WaitingFor, st.Request = session.Idle, time.Now(), "", nil
				session.WriteState(path, st)
			}
		}
		e.note("", markDone, "Told "+w.place()+" no. It won't "+verb+" "+object+", and it's waiting for what to do instead.")
	}
	return 0
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
			e.say("%s", "         "+object)
			e.say("%s", "       "+pad("Allow it once:")+cmd("cadrei answer "+code+" yes"))
			e.say("%s", "       "+pad("Say no:")+cmd("cadrei answer "+code+" no"))
		case w.req.Kind == session.ForQuestion && !w.req.Multi:
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			for i, o := range w.req.Options {
				e.say("%s", fmt.Sprintf("         %d. %s", i+1, o))
			}
			e.say("%s", "       "+pad("Answer:")+cmd("cadrei answer "+code+" 1"))
		case w.req.Kind == session.ForQuestion:
			e.say("%s", "  "+code+"  "+capital(w.place())+" asks: "+w.req.Question)
			e.say("%s", "       This needs more than one pick. Answer it in its window: "+cmd(w.attach()))
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
}

// optionLine is a numbered option: the pointer at the chosen one, the
// number, the label.
var optionLine = regexp.MustCompile(`^\s*(?:[❯›>]\s*)?([1-9])\.\s+(.*?)\s*$`)

// screenPrompt reads the prompt at the bottom of a member's screen: the
// numbered options above its last "Esc to cancel" line, numbered from 1,
// and the lines right above them (up to a blank line or a rule), which
// hold the command or the question.
func screenPrompt(screen string) (opts []promptOption, above string, ok bool) {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	foot := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "Esc to cancel") {
			foot = i
			break
		}
	}
	if foot < 0 {
		return nil, "", false
	}
	first := -1
	for i := foot - 1; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			if len(opts) == 0 {
				continue
			}
			break
		}
		// A question's own options are ruled off from the two it adds.
		if strings.Trim(strings.TrimSpace(line), "─━") == "" {
			continue
		}
		if m := optionLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if len(opts) > 0 && n != opts[0].n-1 {
				return nil, "", false
			}
			opts = append([]promptOption{{n, m[2]}}, opts...)
			if n == 1 {
				first = i
				break
			}
			continue
		}
		// A description under an option is indented deeper than it.
		if len(opts) > 0 && indent(line) > indent(lines[i+1]) {
			continue
		}
		if len(opts) > 0 {
			break
		}
	}
	if first < 0 || len(opts) == 0 || opts[0].n != 1 {
		return nil, "", false
	}
	// The prompt's box starts after a solid rule; dashed ones (╌) frame
	// the command inside it.
	// A question's text can sit a blank line above its options.
	var region []string
	top := first - 1
	for top >= 0 && strings.TrimSpace(lines[top]) == "" {
		top--
	}
	for i := top; i >= 0 && first-i <= 40; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.Trim(t, "─━") == "" {
			break
		}
		region = append([]string{lines[i]}, region...)
	}
	return opts, strings.Join(region, "\n"), true
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

// promptShows reports whether a member's screen shows req's prompt: its
// options, and right above them its command or question.
func promptShows(screen string, req *session.Request) bool {
	opts, above, ok := screenPrompt(screen)
	if !ok {
		return false
	}
	want := req.Summary
	switch req.Tool {
	case "Edit", "MultiEdit", "Write", "Read", "NotebookEdit":
		// The prompt names the file from the member's folder ("Create
		// file notes.txt"): its name is what it surely shows.
		want = filepath.Base(req.Summary)
	}
	if req.Kind == session.ForQuestion {
		want = req.Question
		if len(opts) < len(req.Options) {
			return false
		}
		for i, o := range req.Options {
			if !sameLabel(opts[i].label, o) {
				return false
			}
		}
	}
	return squeeze(want) != "" && strings.Contains(squeeze(above), squeeze(want))
}

// sameLabel compares a label on the screen with the recorded one, which
// may be cut short.
func sameLabel(screen, recorded string) bool {
	if cut, ok := strings.CutSuffix(recorded, "…"); ok {
		return strings.HasPrefix(screen, cut)
	}
	return screen == recorded
}

// keyFor is the one key that gives choice in w's prompt, checked against
// the screen first, or why no key is pressed. yes is only the option
// labeled exactly Yes (never one that allows more, or changes the mode);
// no is the option labeled exactly No, else Escape.
func keyFor(screen string, w *waitingOn, choice string) (key, why string) {
	lost := "I couldn't find that prompt on the " + w.role + "'s screen, so I didn't press anything. Answer it in its window: " + cmd(w.attach())
	if !promptShows(screen, w.req) {
		return "", lost
	}
	opts, _, _ := screenPrompt(screen)
	if w.req.Kind == session.ForQuestion {
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
	return "", "This prompt has no plain \"Yes\", only choices that would allow it for good, so answer it in its window: " + cmd(w.attach())
}

// underOwnOrchestrator reports whether this run may answer for crew r: the
// closest of the runtime's sessions it runs under is r's own orchestrator,
// started with its settings file (which holds the guard), or it runs under
// none (the user's own terminal). The every-chat orchestrator and the
// user's other chats have no cadrei guard, so they don't get to.
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
	return true
}
