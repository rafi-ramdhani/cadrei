//go:build !windows

package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// Start-up screens: the screens a runtime shows before its prompt that
// wait on the user (a folder's trust, a login, an MCP server from the
// project). No hook runs until they're behind it, so cadrei reads them off
// the member's screen while it starts, gives each a code, and answers one
// by moving its pointer to the option picked and pressing Enter, checking
// the screen before each key.

// lookAtStart looks at member name's screen, in window win of tmux session
// sess, while no hook has written its state yet, and records a start-up
// screen there as a request with a code, or ends the one no longer there:
// cadrei ls, cadrei answer and the team status look on each read. Any
// other member's screen isn't read.
func lookAtStart(rt runtime.Runtime, t session.Tmux, build, name, sess, win string) {
	prev := session.ReadState(session.StatePath(build, name))
	if !session.Starts(prev) {
		return
	}
	screen := t.Screen(sess, win)
	if screen == "" {
		// A screen tmux couldn't give says nothing.
		return
	}
	var s *runtime.StartScreen
	if got, ok := rt.Sessions().StartScreen(screen); ok {
		s = &got
	}
	if session.SeeStart(prev, s, time.Now()) == prev {
		return
	}
	session.SeeStartOf(build, name, s)
}

// startShows reports whether screen shows req's start-up screen as cadrei
// recorded it, read whole: the same kind, heading and options. It gives
// the screen as read, for where its pointer is.
func startShows(screen string, req *session.Request) (runtime.StartScreen, bool) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return runtime.StartScreen{}, false
	}
	s, ok := rt.Sessions().StartScreen(screen)
	return s, ok && !s.Multi && s.Kind == req.Tool && s.Title == req.Question && slices.Equal(s.Options, req.Options)
}

// startChoices are the numbers of the options of a start-up screen an
// answer may pick: on a screen cadrei knows, any but one that turns on
// bypass mode (a mode switch, which stays in the member's window) or one
// cut short; on one it doesn't, only the option labeled exactly Yes and
// the one labeled exactly No.
func startChoices(req *session.Request) []int {
	var out []int
	for i, o := range req.Options {
		switch {
		case strings.HasSuffix(o, "…"):
		case req.Tool == runtime.StartOther:
			if o == "Yes" || o == "No" {
				out = append(out, i+1)
			}
		case req.Tool == runtime.StartBypass && !strings.HasPrefix(o, "No"):
		default:
			out = append(out, i+1)
		}
	}
	return out
}

// trustYes reports whether option label of start-up screen req trusts the
// member's folder.
func trustYes(req *session.Request, label string) bool {
	return req.Tool == runtime.StartTrust && strings.HasPrefix(label, "Yes")
}

// trustRefused says why cadrei won't trust w's folder for the user, as
// cadrei trust would refuse it, or "" when it may.
func trustRefused(w *waitingOn) string {
	rt, err := runtime.Get(runtimeName())
	switch {
	case err != nil:
		return "cadrei can't check it"
	case w.dir == "":
		return "cadrei doesn't know which folder it runs in"
	}
	return project.TrustRefusal(paths.Real(w.dir), rt.Trust().Protected())
}

// startOffered are startChoices less a trust yes cadrei refuses (refused
// says why, or ""): what the user can pick from here.
func startOffered(w *waitingOn) (ns []int, refused string) {
	for _, n := range startChoices(w.req) {
		if trustYes(w.req, w.req.Options[n-1]) {
			if refused = trustRefused(w); refused != "" {
				continue
			}
		}
		ns = append(ns, n)
	}
	return ns, refused
}

// unknownScreen says cadrei can't tell what a pick on a start-up screen
// it doesn't know does.
const unknownScreen = "cadrei doesn't know this screen, so it can't say what a pick does past what the screen says"

// startEffect says what picking label on w's start-up screen does past
// the pick, for the guard's ask, or "".
func startEffect(w *waitingOn, label string) string {
	switch {
	case w.req.Tool == runtime.StartOther:
		return " " + capital(unknownScreen) + "."
	case trustYes(w.req, label):
		return " That's saved: " + display(w.dir) + " is trusted from now on, with its own settings and hooks."
	case w.req.Tool == runtime.StartServer && strings.HasPrefix(label, "Use this and all future"):
		return " That's saved: it won't ask about this project's MCP servers again."
	case w.req.Tool == runtime.StartServer && strings.HasPrefix(label, "Use "):
		return " That's saved: the project uses that server from now on."
	case w.req.Tool == runtime.StartServer && strings.HasPrefix(label, "Continue without"):
		return " That's saved: the project leaves that server off from now on."
	case w.req.Tool == runtime.StartImports && strings.HasPrefix(label, "Yes"):
		return " That's saved: its instructions can import files from outside its folder from now on."
	case w.req.Tool == runtime.StartAPIKey && label == "Yes":
		return " It then runs on that API key, and its use is billed to the key. That's saved: your own sessions with that key use it from now on too, without asking."
	case w.req.Tool == runtime.StartAPIKey && strings.HasPrefix(label, "No"):
		return " That's saved: it won't use that key from now on, nor will your own sessions, and they won't ask about it again."
	case w.req.Tool == runtime.StartStyle:
		return " That's saved: every session you run uses that text style from now on."
	case w.req.Tool == runtime.StartLogin:
		return " The login goes on in its window, and may open a browser on its machine."
	}
	return ""
}

// startSteps answers w's start-up screen with option n: it moves the
// pointer there a key at a time, and presses Enter once the pointer is
// on it. Before each key the screen must still show the same screen, with
// the pointer where the last key left it, so a screen that changed gets
// no Enter. It says why it stopped, or "" once Enter is pressed.
func startSteps(t session.Tmux, w *waitingOn, n int) string {
	stopped := "The " + w.role + "'s screen didn't move as I expected, so I stopped before picking anything. Answer it in its window: " + cmd(w.attach())
	at := -1
	read := func(s string) bool {
		got, ok := startShows(s, w.req)
		if ok && (at < 0 || got.At == at) {
			at = got.At
			return true
		}
		return false
	}
	if _, ok := waitFor(t, w, read); !ok {
		return stopped
	}
	for at != n-1 {
		key, next := "Down", at+1
		if at > n-1 {
			key, next = "Up", at-1
		}
		if t.SendKeys(w.session, w.window, false, key) != nil {
			return stopped
		}
		at = next
		if _, ok := waitFor(t, w, read); !ok {
			return stopped
		}
	}
	if t.SendKeys(w.session, w.window, false, "Enter") != nil {
		return stopped
	}
	return ""
}

// startPick is the option number choice names, or 0.
func startPick(choice string) int {
	n, err := strconv.Atoi(choice)
	if err != nil || strconv.Itoa(n) != choice {
		return 0
	}
	return n
}

// checkStart says why choice can't answer w's start-up screen, or "": it
// must be one of its options' numbers, one cadrei may pick (never bypass
// mode's yes), and for a trust screen's yes, its folder one cadrei trust
// would trust. c formats a command.
func checkStart(w *waitingOn, code, choice string, c func(string) string) string {
	window := c(w.attach())
	n := startPick(choice)
	if n < 1 || n > len(w.req.Options) {
		return code + " has options 1 to " + strconv.Itoa(len(w.req.Options)) + ". Use " + c("cadrei answer "+code+" 1") + ", for example."
	}
	ns, refused := startOffered(w)
	switch {
	case slices.Contains(ns, n):
		return ""
	case w.req.Tool == runtime.StartBypass:
		return "Cadrei never turns on bypass mode for you: that choice stays in the " + w.role + "'s window: " + window
	case refused != "" && trustYes(w.req, w.req.Options[n-1]):
		return "Cadrei won't trust " + display(w.dir) + " for you: " + refused + ". If you're sure, answer it in its window: " + window
	}
	var nums []string
	for _, o := range ns {
		nums = append(nums, strconv.Itoa(o))
	}
	if len(nums) == 0 {
		return "Cadrei can't answer that start-up screen from here, so answer it in its window: " + window
	}
	return "Cadrei only picks " + strings.Join(nums, " or ") + " on that screen. Use " + c("cadrei answer "+code+" "+nums[0]) + ", or answer it in its window: " + window
}
