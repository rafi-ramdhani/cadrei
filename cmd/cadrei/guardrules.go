//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// runHookGuard is cadrei hook guard: a member's runtime, and the
// orchestrator cadrei opens, run it before each shell command. A command
// it refuses doesn't run (exit 2, the reason on stderr), and one it asks
// about waits for the user's yes in the runtime's own prompt (exit 0 with
// the ask on stdout). It never allows: with nothing to say it says
// nothing.
//
// In both, it refuses typing into tmux windows (any tmux command outside a
// read-only list) and osascript keystrokes. In a member (CADREI_MEMBER is
// set), it refuses cadrei's uninstall and restore, cadrei answer, cadrei
// compact and cadrei hook, in any form it can see. In the orchestrator, it asks the
// user before cadrei answer <code> <choice> in its strict form, in the
// permission modes known to prompt, and refuses every other form of it.
//
// A member's refusal is recorded for the user, with a code (memberRefused).
// A refused cadrei compact, or a write into a .memory folder, the user can
// allow once by that code: the guard then lets that exact command by, once,
// and the member's own permission mode and deny rules still decide.
//
// It errs on the side of refusing, and refuses anything it can't read.
// It's one of the layers: the members' deny rules come first, and the
// commands themselves refuse a member's window (memberAnyway) and, for
// cadrei answer, any session but this crew's orchestrator. A command it
// can't see into, such as a script file a session wrote, is left to those,
// and to the instructions.
func runHookGuard(e *env) int {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return guardRefuses(e, "cadrei can't check this command ("+err.Error()+"), so it's blocked.")
	}
	call, err := rt.Sessions().ShellCall(e.stdin)
	switch {
	case err != nil:
		return guardRefuses(e, "cadrei couldn't read this command ("+err.Error()+"), so it's blocked.")
	case !call.Shell:
		return 0
	case inMember():
		if rule, why := memberRule(call.Command); rule != "" {
			return memberRefused(e, rt, call, rule, why)
		}
		return 0
	case guardLine(tmuxRule{}, call.Command, 0):
		return guardRefuses(e, typingRefused)
	case guardLine(keystrokeRule{}, call.Command, 0):
		return guardRefuses(e, keystrokeRefused)
	}
	if !guardLine(answerRule, call.Command, 0) || answerListing.MatchString(call.Command) {
		return 0
	}
	m := strictAnswer.FindStringSubmatch(call.Command)
	if m == nil {
		return guardRefuses(e, strictRefused)
	}
	// By a full path, it must be cadrei: cadrei's text in the ask vouches
	// for the program that runs.
	if m[1] != "cadrei" && !isThisCadrei(m[1]) {
		return guardRefuses(e, "Run cadrei answer <code> <choice> with cadrei by its name, so cadrei can ask you.")
	}
	code, choice := m[2], strings.Join(strings.Fields(m[3]), " ")
	if !promptModes[call.Mode] {
		return guardRefuses(e, rt.Title()+" can't ask the user in this permission mode. Ask them to type ! cadrei answer "+code+" "+choice+" themselves.")
	}
	var w *waitingOn
	if home := os.Getenv("CADREI_HOME"); home != "" {
		if r, err := cadreis.Resolve(paths.Real(home), nil); err == nil {
			w = findWaiting(r, session.Default(), code)
		}
	}
	if w == nil {
		return guardRefuses(e, nothingWaiting(code, plain))
	}
	if why := checkChoice(w, code, choice, plain); why != "" {
		return guardRefuses(e, why)
	}
	// The prompt must be on the member's screen now, as cadrei answer
	// checks it again after the user's yes: a yes for one it can't find
	// would come to nothing.
	end := guardDeadline(e, w)
	screen, restore := guardScreen(w)
	_, why := keyOn(screen, w, choice, plain)
	end()
	restore()
	if why != "" {
		return guardRefuses(e, why)
	}
	e.stdout.Write(rt.Sessions().Ask(askReason(w, choice)))
	return 0
}

// The rules the guard refuses a member's command by, as its record names
// them. The ones in allowable the user can allow once by the record's
// code; the others never: uninstall and restore are the user's to run, and
// cadrei answer, typing into tmux windows and keystrokes could press
// another member's approval.
const (
	ruleTyping  = "typing"
	ruleKeys    = "keystrokes"
	ruleRemoval = "removal"
	ruleAnswer  = "answer"
	ruleCompact = "compact"
	ruleMemory  = "memory"
	ruleHook    = "hook"
	ruleOpaque  = "unread" // cadrei with a command the guard can't read
)

var allowable = setOf(ruleCompact, ruleMemory)

// memberRule is the rule that refuses command in a member, and why, or ""
// when none does. The ones the user can never allow come first, so a
// command one of them refuses is never let by as another rule's.
func memberRule(command string) (rule, why string) {
	switch {
	case guardLine(tmuxRule{}, command, 0):
		return ruleTyping, typingRefused
	case guardLine(keystrokeRule{}, command, 0):
		return ruleKeys, keystrokeRefused
	case removesCrews(command):
		return ruleRemoval, removalRefused
	case guardLine(answerRule, command, 0):
		return ruleAnswer, memberAnswerRefused
	case guardLine(hookRule, command, 0):
		return ruleHook, hookRefused
	case guardLine(opaqueRule{}, command, 0):
		return ruleOpaque, opaqueRefused
	case guardLine(compactRule, command, 0):
		return ruleCompact, compactRefused
	case guardLine(memoryRule{}, command, 0):
		return ruleMemory, memoryRefused
	}
	return "", ""
}

// guardLockWait is how long the guard waits for a member's lock: well
// under the runtime's hook timeout.
var guardLockWait = 2 * time.Second

// memberRefused refuses a member's command that rule refuses, and records
// it in the member's state, for the user to see from the orchestrator with
// a code; the reason the member sees names the code. One the user allowed
// once by its code goes by instead, this once (exit 0, nothing said), when
// rule is one they can allow. Without a crew and member cadrei knows, it
// only refuses.
func memberRefused(e *env, rt runtime.Runtime, call runtime.ShellCall, rule, why string) int {
	name, home := os.Getenv("CADREI_MEMBER"), os.Getenv("CADREI_HOME")
	if name == "" || home == "" {
		return guardRefuses(e, why)
	}
	if c := hookCrew(home); c == nil || name == orchestrator.Name(c.Name) || !memberOf(*c, name) {
		return guardRefuses(e, why)
	}
	build := rt.BuildDir(paths.Real(home))
	if st, err := os.Stat(build); err != nil || !st.IsDir() {
		return guardRefuses(e, why)
	}
	// The allowance is for this command in this folder. It's used up in
	// the state file before the command goes by: a write that failed
	// would leave it there for another run.
	key := session.CommandKey(call.Dir, call.Command)
	if allowable[rule] && call.Dir != "" {
		taken := false
		_, _, err := session.ChangeState(build, name, guardLockWait, func(prev *session.State) *session.State {
			if prev == nil {
				return nil
			}
			next := *prev
			if taken = next.TakeAllowance(key, time.Now()); taken {
				return &next
			}
			return prev
		})
		if err == nil && taken {
			return 0
		}
	}
	now := time.Now()
	r := session.Refusal(call.Command, why, call.Dir, rule)
	prev, next, err := session.ChangeState(build, name, guardLockWait, func(prev *session.State) *session.State { return session.Refuse(prev, r, now) })
	if err != nil {
		return guardRefuses(e, why)
	}
	session.LogPrompts(build, name, prev, next, session.Ending(""), now)
	for _, o := range next.HeldOnes() {
		if o.Kind == session.ForGuard && o.Rule == rule && o.Key == key {
			return guardRefuses(e, why+refusalNote(rule, o.Code))
		}
	}
	return guardRefuses(e, why)
}

// refusalNote is what a member's refusal adds to its reason: the code the
// user sees it by, and whether they can allow it.
func refusalNote(rule, code string) string {
	if allowable[rule] {
		return " Cadrei noted it for the user under code " + code + ": they can allow it once from the orchestrator. If they do, run the exact same command again, once."
	}
	return " Cadrei noted it for the user under code " + code + ", but it can't be allowed from the orchestrator."
}

// guardScreen is what the member's window shows, as the guard reads it
// (grown for a moment when the prompt doesn't fit), and what puts the
// window's size back.
var guardScreen = func(w *waitingOn) (string, func()) { return promptScreen(session.Default(), w) }

// guardLimit is how long the guard's look at a member's screen may take:
// well under the 10 seconds the runtime gives the hook before it kills it.
// guardExit ends the guard (variables for tests).
var (
	guardLimit = 6 * time.Second
	guardExit  = os.Exit
)

// guardDeadline watches the guard's look at w's screen until end is
// called. A look that runs out of time, or a guard stopped by a signal
// (the runtime ending the hook), puts a window the look grew back to its
// size and refuses, rather than leave the member's window grown.
func guardDeadline(e *env, w *waitingOn) (end func()) {
	stop := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	timer := time.NewTimer(guardLimit)
	go func() {
		select {
		case <-stop:
		case <-timer.C:
		case <-done:
			return
		}
		session.Default().PutBack(w.session, w.window)
		fmt.Fprintln(e.stderr, "Cadrei ran out of time checking the "+w.role+"'s screen, so I didn't ask. Try again in a moment.")
		guardExit(2)
	}()
	return func() {
		signal.Stop(stop)
		timer.Stop()
		close(done)
	}
}

// isThisCadrei reports whether path is this program, or the cadrei the
// PATH finds.
func isThisCadrei(path string) bool {
	real := paths.Real(path)
	if self, err := os.Executable(); err == nil && paths.Real(self) == real {
		return true
	}
	found, err := exec.LookPath("cadrei")
	return err == nil && paths.Real(found) == real
}

// guardRefuses blocks the command, saying why where the runtime shows it.
func guardRefuses(e *env, why string) int {
	fmt.Fprintln(e.stderr, why)
	return 2
}

// plain formats a command for text the runtime shows a model: as it is.
func plain(s string) string { return s }

// The guard's reasons, shown to the session it refused.
const (
	memberAnswerRefused = "Only the user can answer for a member. Ask the user to do it from the orchestrator."
	removalRefused      = "Team members can't run cadrei uninstall or cadrei restore, in any form. Ask the user to do it from the orchestrator."
	compactRefused      = "Team members can't run cadrei compact. Ask the user to do it from the orchestrator."
	hookRefused         = "Team members can't run cadrei hook: cadrei's hooks run on their own."
	opaqueRefused       = "Team members can't run cadrei with a command cadrei can't read, like cadrei $x or cadrei $(cat f). Write the command out."
	strictRefused       = "Run cadrei answer <code> <choice> on its own, with nothing around it, so cadrei can ask you."
	typingRefused       = "Cadrei sessions can't type into tmux windows. To compact a member, run cadrei compact <team>/<role> [project]."
	keystrokeRefused    = "Cadrei sessions can't press keys in other apps."
	memoryRefused       = "Team members can't write into a .memory folder: it holds the orchestrator's memory."
)

// promptModes are the permission modes in which the runtime is known to
// show a hook's ask to the user. In any other a prompt might never show,
// or pass on its own.
var promptModes = setOf("default", "acceptEdits", "auto")

// strictAnswer is cadrei answer <code> <choice> as one simple command:
// cadrei by its name or a full path, literal words, nothing around it.
// The choice is yes, no, or a pick for each of up to four questions (a
// number, or numbers joined by commas). answerListing is cadrei answer
// that answers nothing: with nothing after it, a code alone, or --wait
// (with a --timeout of letters and digits), as one simple command.
const picksRule = `[0-9]{1,2}(?:,[0-9]{1,2}){0,5}`

// stopModelRule is 1 and a model, a safeguard stop's answer: a model name
// as session.GoodModel takes it, but for [ and ], which the shell can
// expand.
const stopModelRule = `1[ \t]+[A-Za-z0-9._:/@][A-Za-z0-9._:/@-]{0,199}`

var (
	strictAnswer  = regexp.MustCompile(`^\s*(cadrei|/[A-Za-z0-9_./-]*/cadrei)[ \t]+answer[ \t]+([a-z0-9]{3})[ \t]+(yes|no|` + stopModelRule + `|` + picksRule + `(?:[ \t]+` + picksRule + `){0,3})\s*$`)
	answerListing = regexp.MustCompile(`^\s*(?:cadrei|/[A-Za-z0-9_./-]*/cadrei)[ \t]+answer(?:[ \t]+[a-z0-9]{3}|[ \t]+--wait(?:[ \t]+--timeout[ \t]+[0-9][0-9a-z.]{0,15})?)?\s*$`)
)

// tmuxRule looks for a tmux command that isn't read-only: one that types,
// pastes, runs commands or takes a pane over, on any server.
type tmuxRule struct{}

func (tmuxRule) runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool {
	k, ok := programAt(words, p, "tmux")
	if !ok {
		// tmux after a program that runs the rest (arch, flock, script,
		// ssh): tmux, then one of its flags or commands. man tmux and brew
		// install tmux run nothing.
		for j := p + 1; j+1 < len(words); j++ {
			if wordName(words[j]) == "tmux" && tmuxNext(words[j+1].bare) {
				k, ok = j, true
				break
			}
		}
	}
	switch {
	case !ok:
		return false
	case k != p && wordName(words[p]) == "xargs":
		return true // xargs may add words of its own
	case k == p && onPrivateServer(words[k+1:], line):
		return false
	}
	return tmuxWrites(words[k+1:])
}

// onPrivateServer reports whether tmux's arguments run only commands that
// type nothing, on a server of the session's own that holds no cadrei
// window: one named with -L or -S, written out, that is neither tmux's
// default server nor cadrei's nor the one the session runs in, and whose
// socket isn't a link to one. The rest of the line must make no link or
// move no file (so it can't point that socket at another server first),
// and no command string in it may type into tmux itself. Typing, pasting,
// running a shell command or a config file, and hooks stay refused on
// every server. A link made between this check and the run, by something
// already running, is left to the other layers, as a script file is.
func onPrivateServer(args []shWord, line *lineText) bool {
	socket, i := "", 0
	for ; i < len(args); i++ {
		a := args[i]
		if a.expands || a.pattern {
			return false
		}
		if a.text == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a.text, "-") || a.text == "-" {
			break
		}
		flags := a.text[1:]
		for j := 0; j < len(flags); j++ {
			switch f := flags[j]; {
			case strings.IndexByte("2DhlNuv", f) >= 0:
			case f == 'L' || f == 'S' || f == 'T':
				value := flags[j+1:]
				if value == "" {
					if i+1 >= len(args) || args[i+1].expands || args[i+1].pattern {
						return false
					}
					i++
					value = args[i].text
				}
				if f != 'T' {
					if socket != "" {
						return false // two servers named: read neither
					}
					if socket = privateSocket(f, value); socket == "" {
						return false
					}
				}
				j = len(flags)
			default:
				return false // -f, -c, -C, or one it doesn't know
			}
		}
	}
	if socket == "" || i >= len(args) || !privateLine(line.text) {
		return false
	}
	// Its commands, split by a ; word or a word that ends with one.
	for i < len(args) {
		w := args[i]
		i++
		if w.text == ";" {
			continue
		}
		name, ok := tmuxCommand(w.text)
		if !ok || privateNever[name] {
			return false
		}
		for end := strings.HasSuffix(w.text, ";"); !end && i < len(args) && args[i].text != ";"; i++ {
			a := args[i]
			if a.expands || a.pattern && !a.quoted || strings.Contains(a.text, "#(") || reformats.MatchString(a.text) {
				return false
			}
			// A command string (new-session's, set-option's) is code the
			// private server's shell runs.
			if !strings.HasPrefix(a.text, "-") && guardLine(tmuxRule{}, a.text, 1) {
				return false
			}
			end = strings.HasSuffix(a.text, ";")
		}
	}
	return true
}

// privateNever are the tmux commands refused on every server: they type
// or paste into a pane, or run a shell command or a file the guard can't
// read.
var privateNever = setOf("send-keys", "send-prefix", "paste-buffer", "set-buffer", "load-buffer", "run-shell", "if-shell", "source-file", "set-hook",
	"pipe-pane", "command-prompt", "confirm-before", "display-popup", "display-menu", "bind-key")

// privateSocket is the socket that -L name or -S path names, when it can
// hold no cadrei window, or "".
func privateSocket(flag byte, value string) string {
	var path string
	switch {
	case flag == 'L' && value != "" && value != "default" && !strings.ContainsAny(value, "/") && value != "." && value != "..":
		path = session.Tmux{Socket: value}.Path()
	case flag == 'S' && filepath.IsAbs(value):
		path = filepath.Clean(value)
		if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			path = filepath.Join(real, filepath.Base(path))
		}
	default:
		return ""
	}
	known := []string{session.Tmux{}.Path(), session.Default().Path()}
	if t := os.Getenv("TMUX"); t != "" {
		known = append(known, strings.SplitN(t, ",", 2)[0])
	}
	st, err := os.Lstat(path)
	if err == nil && st.Mode()&os.ModeSocket == 0 {
		return "" // a link (whose own mode is a link's), or not a socket: it could lead anywhere
	}
	for _, k := range known {
		if k == path {
			return ""
		}
		if other, err2 := os.Stat(k); err == nil && err2 == nil && os.SameFile(st, other) {
			return ""
		}
	}
	return path
}

// privateLine reports whether a line with tmux on a private server does
// nothing else that could point its socket elsewhere: it reads whole, sets
// no TMUX_TMPDIR, runs no substitution, and its other commands are plain
// ones that make no link and move no file.
func privateLine(text string) bool {
	if strings.Contains(text, "TMUX_TMPDIR") {
		return false
	}
	line, ok := parseShell(text)
	if !ok || len(line.subs) > 0 {
		return false
	}
	for _, pipe := range line.pipes {
		for _, c := range pipe {
			words := c.words
			for len(words) > 0 && assignment.MatchString(words[0].text) {
				words = words[1:]
			}
			if len(words) > 0 && !privateAround[wordName(words[0])] {
				return false
			}
		}
	}
	return true
}

// privateAround are the programs a line with tmux on a private server may
// also run.
var privateAround = setOf("tmux", "sleep", "echo", "printf", "true", "false", "test", "[", "cat", "grep", "head", "tail", "wc", "cd", "pwd", "ls", "mkdir", "rm")

// programAt finds program name run by a command whose program is
// words[p]: that one, the word after a wrapper option's value (sudo -u
// someone tmux), or one that xargs, find -exec or parallel runs. tmux in
// brew install tmux or man tmux runs nothing.
func programAt(words []shWord, p int, name string) (int, bool) {
	if wordName(words[p]) == name {
		return p, true
	}
	wrapped := false
	for _, w := range words[:p] {
		wrapped = wrapped || runsAnother[wordName(w)]
	}
	if wrapped && p+1 < len(words) && wordName(words[p+1]) == name {
		return p + 1, true
	}
	if runners[wordName(words[p])] {
		for k := p + 1; k < len(words); k++ {
			if wordName(words[k]) == name {
				return k, true
			}
		}
	}
	return 0, false
}

// runners run a program named in their arguments.
var runners = setOf("xargs", "find", "parallel", "fd")

// tmuxNext reports whether a word after tmux makes it a command: one of
// tmux's own flags, or one of its commands.
func tmuxNext(word string) bool {
	if len(word) > 1 && word[0] == '-' {
		return strings.Trim(word[1:], "2CDLNSTVcfhluv") == ""
	}
	_, ok := tmuxCommand(word)
	return ok
}

// calls: code another program runs (python's -c, a heredoc to node), or
// any other argument, that names tmux with a command outside the
// read-only list.
func (r tmuxRule) calls(text string) bool { return r.mentions(text) }

// mentions: text that becomes a shell's code, or that a program which is
// itself an expansion may run: tmux with a command outside the read-only
// list, in full or by its alias, in the next words or after a ; that
// follows a read-only one; or tmux, and a format that runs a job.
func (tmuxRule) mentions(text string) bool {
	words := splitWords(text)
	for i, w := range words {
		if w != "tmux" {
			continue
		}
		if strings.Contains(text, "#(") || reformats.MatchString(text) || tmuxChain(words[i+1:]) {
			return true
		}
		for _, v := range words[i+1 : min(i+7, len(words))] {
			if name, ok := tmuxExact(v); ok && !tmuxReadOnly[name] {
				return true
			}
		}
	}
	return false
}

func (r tmuxRule) blunt(text string) bool { return r.mentions(text) }

// tmuxChain reads words as tmux's arguments, as far as they look like
// them: its flags, then commands split by ; words. It reports whether one
// of the commands isn't read-only, or is display-message without -p or
// with -I.
func tmuxChain(words []string) bool {
	k := 0
	for ; k < len(words) && strings.HasPrefix(words[k], "-"); k++ {
		f := words[k][1:]
		if strings.Contains(f, "c") {
			return true // -c or -C, in lower case
		}
		if f != "" && strings.ContainsAny(f[len(f)-1:], "flst") {
			k++ // its value
		}
	}
	for k < len(words) {
		name, ok := tmuxCommand(words[k])
		if !ok {
			return false
		}
		if !tmuxReadOnly[name] {
			return true
		}
		printing, n := false, 0
		for k++; k < len(words) && words[k] != ";"; k++ {
			if n++; n > 12 || words[k] == "tmux" {
				return false
			}
			if f, ok := strings.CutPrefix(words[k], "-"); ok {
				printing = printing || strings.Contains(f, "p")
				if name == "display-message" && strings.Contains(f, "i") {
					return true
				}
			}
		}
		if name == "display-message" && !printing {
			return true
		}
		k++
	}
	return false
}

// tmuxWrites reports whether tmux's arguments run any command outside the
// read-only list, or one it can't read. tmux takes a sequence of commands
// split by a ; word (\; in a shell), and unique prefixes and aliases of
// their names.
func tmuxWrites(args []shWord) bool {
	i := 0
	// tmux's own flags: -C (control mode) and -c (a shell command) run
	// commands; -f, -L, -S and -T take a value.
	// A format's #( starts a shell job in the tmux server, as run-shell
	// does, outside the session's own processes: in any argument.
	// The E: and T: modifiers expand a value as a format again, so a value
	// the session set (its pane's title) can hold one.
	for _, a := range args {
		if strings.Contains(a.text, "#(") || reformats.MatchString(a.text) {
			return true
		}
	}
flags:
	for ; i < len(args); i++ {
		a := args[i]
		if a.expands {
			return true
		}
		if a.text == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a.text, "-") || a.text == "-" {
			break
		}
		for j, f := range a.text[1:] {
			switch {
			case strings.ContainsRune("2DhlNuv", f):
			case f == 'V':
				return false // prints its version and ends
			case strings.ContainsRune("fLST", f):
				if j == len(a.text)-2 {
					i++ // its value is the next word
				}
				continue flags
			default:
				return true // C, c, or one it doesn't know
			}
		}
	}
	if i >= len(args) {
		return true // no command: tmux starts a new session
	}
	for i < len(args) {
		w := args[i]
		if w.expands {
			return true
		}
		name, ok := tmuxCommand(w.text)
		if !ok || !tmuxReadOnly[name] {
			return true
		}
		// The rest of this command, up to a ; word or one that ends with ;.
		// display-message's -I writes into a pane.
		printing := false
		i++
		for ; i < len(args); i++ {
			if args[i].expands {
				return true
			}
			t := args[i].text
			if t == "-p" {
				printing = true
			}
			if name == "display-message" && strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "I") {
				return true
			}
			if strings.HasSuffix(t, ";") {
				i++
				break
			}
		}
		if name == "display-message" && !printing {
			return true
		}
	}
	return false
}

// reformats is a tmux format with the E: or T: modifier, alone or among
// others split by ; (#{E:pane_title}, #{=10;T:x}).
var reformats = regexp.MustCompile(`#\{(?:[^:}]*;)?[ET][;:]`)

// tmuxReadOnly are the tmux commands cadrei sessions may run: they only
// read (display-message only with -p, which prints).
var tmuxReadOnly = setOf("capture-pane", "list-sessions", "list-windows", "list-panes", "list-clients", "has-session", "show-options", "show-environment", "display-message")

// tmuxCommands are tmux's commands, each with its alias (3.2 to 3.5).
var tmuxCommands = map[string]string{
	"attach-session": "attach", "bind-key": "bind", "break-pane": "breakp", "capture-pane": "capturep",
	"choose-buffer": "", "choose-client": "", "choose-tree": "", "clear-history": "clearhist",
	"clear-prompt-history": "clearphist", "clock-mode": "", "command-prompt": "", "confirm-before": "confirm",
	"copy-mode": "", "customize-mode": "", "delete-buffer": "deleteb", "detach-client": "detach",
	"display-menu": "menu", "display-message": "display", "display-panes": "displayp", "display-popup": "popup",
	"find-window": "findw", "has-session": "has", "if-shell": "if", "join-pane": "joinp",
	"kill-pane": "killp", "kill-server": "", "kill-session": "", "kill-window": "killw",
	"last-pane": "lastp", "last-window": "last", "link-window": "linkw", "list-buffers": "lsb",
	"list-clients": "lsc", "list-commands": "lscm", "list-keys": "lsk", "list-panes": "lsp",
	"list-sessions": "ls", "list-windows": "lsw", "load-buffer": "loadb", "lock-client": "lockc",
	"lock-server": "lock", "lock-session": "locks", "move-pane": "movep", "move-window": "movew",
	"new-session": "new", "new-window": "neww", "next-layout": "nextl", "next-window": "next",
	"paste-buffer": "pasteb", "pipe-pane": "pipep", "previous-layout": "prevl", "previous-window": "prev",
	"refresh-client": "refresh", "rename-session": "rename", "rename-window": "renamew", "resize-pane": "resizep",
	"resize-window": "resizew", "respawn-pane": "respawnp", "respawn-window": "respawnw", "rotate-window": "rotatew",
	"run-shell": "run", "save-buffer": "saveb", "select-layout": "selectl", "select-pane": "selectp",
	"select-window": "selectw", "send-keys": "send", "send-prefix": "", "server-access": "",
	"set-buffer": "setb", "set-environment": "setenv", "set-hook": "", "set-option": "set",
	"set-window-option": "setw", "show-buffer": "showb", "show-environment": "showenv", "show-hooks": "",
	"show-messages": "showmsgs", "show-options": "show", "show-prompt-history": "showphist", "show-window-options": "showw",
	"source-file": "source", "split-window": "splitw", "start-server": "start", "suspend-client": "suspendc",
	"swap-pane": "swapp", "swap-window": "swapw", "switch-client": "switchc", "unbind-key": "unbind",
	"unlink-window": "unlinkw", "wait-for": "wait",
}

// tmuxExact names the command word is, in full or by its alias.
func tmuxExact(word string) (string, bool) {
	if _, ok := tmuxCommands[word]; ok {
		return word, true
	}
	for name, alias := range tmuxCommands {
		if alias != "" && alias == word {
			return name, true
		}
	}
	return "", false
}

// tmuxCommand names the command word runs, as tmux finds it: in full, by
// its alias, or by a prefix only one command has. A word that ends with ;
// is the command before a split.
func tmuxCommand(word string) (string, bool) {
	word = strings.TrimSuffix(word, ";")
	if name, ok := tmuxExact(word); ok {
		return name, true
	}
	found := ""
	for name := range tmuxCommands {
		if word != "" && strings.HasPrefix(name, word) {
			if found != "" {
				return "", false
			}
			found = name
		}
	}
	return found, found != ""
}

// keystrokeRule looks for osascript pressing keys (System Events'
// keystroke or key code), which can type into any window.
type keystrokeRule struct{}

func (keystrokeRule) runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool {
	k, ok := programAt(words, p, "osascript")
	if !ok {
		return false
	}
	for _, w := range words[k+1:] {
		if presses(w.text) {
			return true
		}
	}
	for _, in := range c.input {
		if presses(in) {
			return true
		}
	}
	return false
}

func (keystrokeRule) calls(text string) bool { return pressesAt(text) }

func (keystrokeRule) mentions(text string) bool { return pressesAt(text) }

func (keystrokeRule) blunt(text string) bool { return pressesAt(text) }

// presses reports whether AppleScript or JavaScript for osascript presses
// keys.
func presses(text string) bool {
	t := strings.ReplaceAll(strings.ToLower(text), " ", "")
	return strings.Contains(t, "keystroke") || strings.Contains(t, "keycode")
}

// pressesAt reports whether code names osascript and pressing keys.
func pressesAt(text string) bool {
	for _, w := range bluntWords(text) {
		if w == "osascript" {
			return presses(text)
		}
	}
	return false
}
