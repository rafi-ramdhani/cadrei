//go:build !windows

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
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
// set), it refuses cadrei's uninstall and restore, cadrei answer and
// cadrei compact, in any form it can see. In the orchestrator, it asks the
// user before cadrei answer <code> <choice> in its strict form, in the
// permission modes known to prompt, and refuses every other form of it.
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
	case guardLine(tmuxRule{}, call.Command, 0):
		return guardRefuses(e, typingRefused)
	case guardLine(keystrokeRule{}, call.Command, 0):
		return guardRefuses(e, keystrokeRefused)
	}
	if inMember() {
		switch {
		case removesCrews(call.Command):
			return guardRefuses(e, "Team members can't run cadrei uninstall or cadrei restore, in any form. Ask the user to do it from the orchestrator.")
		case guardLine(answerRule, call.Command, 0):
			return guardRefuses(e, memberAnswerRefused)
		case guardLine(compactRule, call.Command, 0):
			return guardRefuses(e, "Team members can't run cadrei compact. Ask the user to do it from the orchestrator.")
		}
		return 0
	}
	if !guardLine(answerRule, call.Command, 0) || answerListing.MatchString(call.Command) {
		return 0
	}
	m := strictAnswer.FindStringSubmatch(call.Command)
	if m == nil {
		return guardRefuses(e, strictRefused)
	}
	code, choice := m[1], m[2]
	if !promptModes[call.Mode] {
		return guardRefuses(e, rt.Title()+" can't ask the user in this permission mode. Ask them to type ! cadrei answer "+code+" "+choice+" themselves, or to answer in the member's window.")
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
	e.stdout.Write(rt.Sessions().Ask(askReason(w, choice)))
	return 0
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
	strictRefused       = "Run cadrei answer <code> <choice> on its own, with nothing around it, so cadrei can ask you."
	typingRefused       = "Cadrei sessions can't type into tmux windows. To compact a member, run cadrei compact <team>/<role> [project]."
	keystrokeRefused    = "Cadrei sessions can't press keys in other apps."
)

// promptModes are the permission modes in which the runtime is known to
// show a hook's ask to the user. In any other a prompt might never show,
// or pass on its own.
var promptModes = setOf("default", "acceptEdits", "auto")

// strictAnswer is cadrei answer <code> <choice> as one simple command:
// cadrei by its name or a full path, literal words, nothing around it.
// answerListing is cadrei answer with nothing after it.
var (
	strictAnswer  = regexp.MustCompile(`^\s*(?:cadrei|/[A-Za-z0-9_./-]*/cadrei)[ \t]+answer[ \t]+([a-z0-9]{3})[ \t]+(yes|no|[0-9]{1,2})\s*$`)
	answerListing = regexp.MustCompile(`^\s*(?:cadrei|/[A-Za-z0-9_./-]*/cadrei)[ \t]+answer\s*$`)
)

// tmuxRule looks for a tmux command that isn't read-only: one that types,
// pastes, runs commands or takes a pane over, on any server.
type tmuxRule struct{}

func (tmuxRule) runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool {
	k, ok := programAt(words, p, "tmux")
	switch {
	case !ok:
		return false
	case k != p && wordName(words[p]) == "xargs":
		return true // xargs may add words of its own
	}
	return tmuxWrites(words[k+1:])
}

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

// calls: code that runs tmux with a command outside the read-only list,
// named in full or by its alias, within the next words.
func (tmuxRule) calls(text string) bool {
	words := bluntWords(text)
	for i, w := range words {
		if w != "tmux" {
			continue
		}
		for _, v := range words[i+1 : min(i+7, len(words))] {
			if name, ok := tmuxExact(v); ok && !tmuxReadOnly[name] {
				return true
			}
		}
	}
	return false
}

func (r tmuxRule) mentions(text string) bool { return r.calls(text) }

func (r tmuxRule) blunt(text string) bool { return r.calls(text) }

// tmuxWrites reports whether tmux's arguments run any command outside the
// read-only list, or one it can't read. tmux takes a sequence of commands
// split by a ; word (\; in a shell), and unique prefixes and aliases of
// their names.
func tmuxWrites(args []shWord) bool {
	i := 0
	// tmux's own flags: -C (control mode) and -c (a shell command) run
	// commands; -f, -L, -S and -T take a value.
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
