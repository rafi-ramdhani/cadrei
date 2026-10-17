//go:build !windows

package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// A question that takes more than one key: one that takes several picks,
// or several questions at once (W7). The answer names a pick for each
// question, in order (cadrei answer k2m 2 1), several for one joined by
// commas (1,3). Cadrei presses them one at a time, and checks the screen
// before each key: each question as recorded, each box ticked after its
// pick, and before the last key, the review showing exactly these
// answers. What the keys do is from the recorded multi-select screen and
// Claude Code 2.1.302's own text, not seen live yet: a pick's number
// ticks its box in a multi-select question and answers any other, which
// moves on to the next; the right arrow moves on from a multi-select one;
// and after the last question comes the review, sent by its option
// "Submit answers". A screen that doesn't show what's expected stops it
// before the review is sent.

// many reports whether req is a question cadrei answers one step at a
// time.
func many(req *session.Request) bool { return req.Kind == session.ForQuestion && len(req.Parts) > 0 }

// partPicks reads choice for req's questions: one field per question, in
// order, each an option's number, or for one that takes several picks,
// numbers joined by commas, each once.
func partPicks(req *session.Request, choice string) (picks [][]int, ok bool) {
	fields := strings.Fields(choice)
	if len(fields) != len(req.Parts) {
		return nil, false
	}
	for i, f := range fields {
		var ns []int
		for _, s := range strings.Split(f, ",") {
			n, err := strconv.Atoi(s)
			if err != nil || strconv.Itoa(n) != s || n < 1 || n > len(req.Parts[i].Options) || slices.Contains(ns, n) {
				return nil, false
			}
			ns = append(ns, n)
		}
		if len(ns) > 1 && !req.Parts[i].Multi {
			return nil, false
		}
		picks = append(picks, ns)
	}
	return picks, true
}

// partsExample is an answer to req's questions: cadrei answer <code> 1,2 1.
func partsExample(req *session.Request) string {
	var example []string
	for _, p := range req.Parts {
		pick := "1"
		if p.Multi && len(p.Options) > 1 {
			pick = "1,2"
		}
		example = append(example, pick)
	}
	return "cadrei answer " + req.Code + " " + strings.Join(example, " ")
}

// partsUsage says how to answer req's questions. c formats a command.
func partsUsage(req *session.Request, code string, c func(string) string) string {
	use := c(partsExample(req))
	if len(req.Parts) == 1 {
		return fmt.Sprintf("%s takes one or more of its options 1 to %d, joined by commas. Use %s, for example.", code, len(req.Parts[0].Options), use)
	}
	return fmt.Sprintf("%s asks %d questions: give one answer for each, in order, and join several picks for one with commas. Use %s, for example.", code, len(req.Parts), use)
}

// picked says picks for req's questions: each question, then the options
// picked, word for word.
func picked(req *session.Request, picks [][]int) string {
	var out []string
	for i, p := range req.Parts {
		var labels []string
		for _, n := range picks[i] {
			labels = append(labels, fmt.Sprintf("\"%d. %s\"", n, p.Options[n-1]))
		}
		out = append(out, p.Question+" "+strings.Join(labels, ", "))
	}
	return strings.Join(out, "; ")
}

// partText says one of req's questions, with its options, for the
// orchestrator's note.
func partText(p session.Part) string {
	var opts []string
	for i, o := range p.Options {
		opts = append(opts, fmt.Sprintf("%d. %s", i+1, o))
	}
	more := ""
	if p.Multi {
		more = " (one or more)"
	}
	return p.Question + " Options" + more + ": " + strings.Join(opts, ", ") + "."
}

// checkLead is a multi-select question's option's box: empty, or ticked.
var checkLead = regexp.MustCompile(`^\[([ ✔✓xX])\]\s*`)

// unboxed is label without its box, and whether the box is ticked.
func unboxed(label string) (string, bool) {
	m := checkLead.FindStringSubmatch(label)
	if m == nil {
		return label, false
	}
	return label[len(m[0]):], m[1] != " "
}

// partShows reports whether screen shows question p: its text in the box,
// and its own options as recorded, with boxes when it takes several picks
// and none when it doesn't.
func partShows(screen string, p session.Part) bool {
	opts, box, ok := screenPrompt(screen)
	if !ok || len(opts) < len(p.Options) || !strings.Contains(squeeze(strings.Join(box, "\n")), squeeze(p.Question)) {
		return false
	}
	for i, o := range p.Options {
		shown := opts[i]
		if checkLead.MatchString(shown.label) != p.Multi {
			return false
		}
		shown.label, _ = unboxed(shown.label)
		if !labelShown(shown, o) {
			return false
		}
	}
	return true
}

// ticked reports whether option n's box on the screen is ticked.
func ticked(screen string, n int) bool {
	opts, _, _ := screenPrompt(screen)
	if n > len(opts) {
		return false
	}
	_, on := unboxed(opts[n-1].label)
	return on
}

// reviewLead is the last line of the review of a question's answers,
// before they're sent, and reviewHead its title, in Claude Code 2.1.302's
// own text.
const (
	reviewLead = "Ready to submit your answers?"
	reviewHead = "Review your answers"
)

// reviewOf is the review dialog at the bottom of screen: the lines between
// its title and its last line, and its options under that. Only the last
// review line on the screen counts, and its title must come right under
// the dialog's top (its tab bar, or a solid rule), with no rule between
// them, so nothing printed above the dialog is read as part of it.
func reviewOf(screen string) (rows []string, opts []promptOption, ok bool) {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	at := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == reviewLead {
			at = i
			break
		}
	}
	head := -1
	for i := at - 1; i >= 0 && !solidRule(lines[i]); i-- {
		if strings.TrimSpace(lines[i]) == reviewHead {
			head = i
			break
		}
	}
	if at < 0 || head < 0 {
		return nil, nil, false
	}
	top := head - 1
	for top >= 0 && strings.TrimSpace(lines[top]) == "" {
		top--
	}
	if top < 0 || !solidRule(lines[top]) && !tabBar(lines[top]) {
		return nil, nil, false
	}
	opts, _, _ = screenPromptOf(strings.Join(lines[at:], "\n"), true)
	return lines[head+1 : at], opts, len(opts) > 0
}

// tabBar reports whether a line is the tab bar of a dialog of several
// questions: it ends in a Submit tab.
func tabBar(line string) bool { return strings.Contains(line, "✔ Submit") }

// reviewShows reports whether screen shows the review of picks for req's
// questions in its dialog: each question in order, nothing before the
// first, and after each, the arrow and the labels picked, and no other of
// its labels; and the key of its option that sends them.
func reviewShows(screen string, req *session.Request, picks [][]int) (key string, ok bool) {
	rows, opts, ok := reviewOf(screen)
	if !ok {
		return "", false
	}
	// Each question is a bullet.
	text := strings.ReplaceAll(squeeze(strings.Join(rows, "\n")), "●", "")
	pos := 0
	for i, p := range req.Parts {
		q := squeeze(p.Question)
		if !strings.HasPrefix(text[pos:], q) {
			return "", false
		}
		pos += len(q)
		end := len(text)
		if i+1 < len(req.Parts) {
			next := strings.Index(text[pos:], squeeze(req.Parts[i+1].Question))
			if next < 0 {
				return "", false
			}
			end = pos + next
		}
		answer, isAnswer := strings.CutPrefix(text[pos:end], "→")
		if !isAnswer {
			return "", false
		}
		for n, o := range p.Options {
			chosen := slices.Contains(picks[i], n+1)
			if shown := strings.Contains(answer, squeeze(o)); shown != chosen && (chosen || !inAny(squeeze(o), p.Options, picks[i])) {
				return "", false
			}
		}
		pos = end
	}
	for _, o := range opts {
		if o.label == "Submit answers" {
			return strconv.Itoa(o.n), true
		}
	}
	return "", false
}

// stillAsks reports whether screen still shows req's questions: any of
// them, or the review line near its bottom. An answer isn't taken as
// sent until neither shows.
func stillAsks(screen string, req *session.Request) bool {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	for _, l := range lines[max(0, len(lines)-maxPromptLines):] {
		if strings.TrimSpace(l) == reviewLead {
			return true
		}
	}
	return slices.ContainsFunc(req.Parts, func(p session.Part) bool { return partShows(screen, p) })
}

// inAny reports whether label is part of one of the picked options' own
// labels, so it shows inside it.
func inAny(label string, options []string, picks []int) bool {
	for _, n := range picks {
		if strings.Contains(squeeze(options[n-1]), label) {
			return true
		}
	}
	return false
}

// stepWait is how long each step of a question's answer may take to show
// on the screen.
var stepWait = 2 * time.Second

// waitFor reads w's screen until ok says it shows what's next, up to
// stepWait.
func waitFor(t session.Tmux, w *waitingOn, ok func(string) bool) (string, bool) {
	for start := time.Now(); ; time.Sleep(100 * time.Millisecond) {
		if screen := t.Screen(w.session, w.window); ok(screen) {
			return screen, true
		}
		if time.Since(start) >= stepWait {
			return "", false
		}
	}
}

// unticked reports whether none of the boxes of question p on the screen
// is ticked yet.
func unticked(screen string, p session.Part) bool {
	for n := range p.Options {
		if ticked(screen, n+1) {
			return false
		}
	}
	return true
}

// answerSteps answers w's questions with picks, one key at a time, each
// once the screen shows what it should, and the review's last. A question
// that takes several picks must show every box empty before its first, so
// a pick can't untick a box an earlier try left ticked. It says why it
// stopped, or "" once the review is sent.
func answerSteps(t session.Tmux, w *waitingOn, picks [][]int) string {
	pressed := false
	stopped := func() string {
		if !pressed {
			return "The " + w.role + "'s screen didn't show that question as I expected, with its boxes empty, so I didn't press anything. Answer it in its window: " + cmd(w.attach())
		}
		return "I pressed part of your answer, but the " + w.role + "'s screen didn't show what I expected next, so I stopped before sending it. Finish it in its window: " + cmd(w.attach())
	}
	press := func(key string) bool {
		pressed = true
		return t.SendKeys(w.session, w.window, false, key) == nil
	}
	for i, p := range w.req.Parts {
		if _, ok := waitFor(t, w, func(s string) bool { return partShows(s, p) && (!p.Multi || unticked(s, p)) }); !ok {
			return stopped()
		}
		for _, n := range picks[i] {
			if !press(strconv.Itoa(n)) {
				return stopped()
			}
			if p.Multi {
				if _, ok := waitFor(t, w, func(s string) bool { return partShows(s, p) && ticked(s, n) }); !ok {
					return stopped()
				}
			}
		}
		if p.Multi && !press("Right") {
			return stopped()
		}
	}
	screen, ok := waitFor(t, w, func(s string) bool { _, ok := reviewShows(s, w.req, picks); return ok })
	if !ok {
		return stopped()
	}
	key, _ := reviewShows(screen, w.req, picks)
	if !press(key) {
		return stopped()
	}
	return ""
}
