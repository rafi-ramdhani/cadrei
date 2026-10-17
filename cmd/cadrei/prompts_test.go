//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// 0.2.3 (PR A): how cadrei records what a member asks the user, so every
// prompt gets a code. The hook inputs are Claude Code 2.1.301's, as the
// hook spike captured them.

// traceOf is the engineer's trace lines in crew c.
func traceOf(t *testing.T, c string) []session.TraceLine {
	t.Helper()
	raw, _ := os.ReadFile(session.TracePath(c+"/.claude/build", "work-dev-engineer"))
	var lines []session.TraceLine
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var line session.TraceLine
		if l != "" && json.Unmarshal([]byte(l), &line) == nil {
			lines = append(lines, line)
		}
	}
	return lines
}

// Each hook run notes a start and an end line in the member's trace, with
// the hook's name, the tool, the call's id, the state before and after
// and the codes: never what the tool got.
func TestTheStateHookTracesEachRun(t *testing.T) {
	c := teamOf(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit","prompt":"deploy with TEST_TOKEN_PLACEHOLDER"}`)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"echo TEST_TOKEN_PLACEHOLDER"}}`)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`)
	lines := traceOf(t, c)
	if len(lines) != 6 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	for i, want := range []struct{ phase, hook, before, after string }{
		{session.TraceStart, "UserPromptSubmit", "", ""}, {session.TraceEnd, "UserPromptSubmit", "starting/0", "working/0"},
		{session.TraceStart, "PermissionRequest", "", ""}, {session.TraceEnd, "PermissionRequest", "working/0", "waiting/1"},
		{session.TraceStart, "Notification", "", ""}, {session.TraceEnd, "Notification", "waiting/1", "waiting/1"},
	} {
		l := lines[i]
		if l.Phase != want.phase || l.Hook != want.hook || l.Before != want.before || l.After != want.after || l.Run != lines[i/2*2].Run {
			t.Errorf("line %d: %+v", i, l)
		}
	}
	st := session.ReadState(c + "/.claude/build/state/work-dev-engineer.json")
	if lines[3].Tool != "Bash" || len(lines[3].Codes) != 1 || lines[3].Codes[0] != st.Current().Code || lines[5].Notice != "permission_prompt" {
		t.Errorf("the request's run: %+v %+v", lines[3], lines[5])
	}
	raw, _ := os.ReadFile(session.TracePath(c+"/.claude/build", "work-dev-engineer"))
	if strings.Contains(string(raw), "TEST_TOKEN_PLACEHOLDER") || strings.Contains(string(raw), "echo") || strings.Contains(string(raw), "needs your") {
		t.Errorf("the trace holds what the hook got:\n%s", raw)
	}
	if n := session.CountTrace(session.TracePath(c+"/.claude/build", "work-dev-engineer"), time.Now()); n != (session.TraceCounts{Runs: 3}) {
		t.Errorf("counts %+v", n)
	}
}

// A hook waits for the member's lock, which another hook holds while it
// changes the state file. One that can't get it in time changes nothing,
// and says so in the trace, which counts it as failed.
func TestTheStateHookTakesTheMembersLock(t *testing.T) {
	c := teamOf(t)
	build := c + "/.claude/build"
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	lock, err := fsx.Acquire(session.LockPath(build, "work-dev-engineer"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stateLockWait = 200 * time.Millisecond
	t.Cleanup(func() { stateLockWait = 2 * time.Second })
	feed(t, c, "work-dev-engineer", mkdirRequest)
	if st := session.ReadState(build + "/state/work-dev-engineer.json"); st.State != session.Working {
		t.Errorf("changed without the lock: %+v", st)
	}
	lines := traceOf(t, c)
	if last := lines[len(lines)-1]; last.Phase != session.TraceEnd || last.Hook != "PermissionRequest" || last.Err == "" {
		t.Errorf("trace: %+v", last)
	}
	if n := session.CountTrace(session.TracePath(build, "work-dev-engineer"), time.Now()); n.Failed != 1 {
		t.Errorf("counts %+v", n)
	}
	// Released, the next hook gets it.
	lock.Release()
	feed(t, c, "work-dev-engineer", mkdirRequest)
	if st := session.ReadState(build + "/state/work-dev-engineer.json"); st.State != session.Waiting {
		t.Errorf("after the lock: %+v", st)
	}
}

// The hook inputs of item 10's turn, with tool_use_id as 2.1.301 gives it:
// SendMessage and a question side by side.
const (
	sendStart   = `{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_use_id":"toolu_s","tool_input":{"to":"uds:/tmp/x.sock","message":"Waiting on the user's yes"}}`
	sendEnd     = `{"hook_event_name":"PostToolUse","tool_name":"SendMessage","tool_use_id":"toolu_s","tool_input":{"to":"uds:/tmp/x.sock","message":"Waiting on the user's yes"},"tool_response":{"success":true}}`
	shipInput   = `{"questions":[{"question":"Ship it?","header":"Ship","options":[{"label":"Yes","description":"Ship now."},{"label":"No","description":"Wait."}],"multiSelect":false}]}`
	shipStart   = `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_use_id":"toolu_q","tool_input":` + shipInput + `}`
	shipAsk     = `{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":` + shipInput + `}`
	shipEnd     = `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"toolu_q","tool_input":` + shipInput + `}`
	mkdirStart  = `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_m","tool_input":{"command":"mkdir e2e-yes"}}`
	mkdirFailed = `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_use_id":"toolu_m","tool_input":{"command":"mkdir e2e-yes"},"error":"denied"}`
)

// stateOf is the engineer's state in crew c.
func stateOf(c string) *session.State {
	return session.ReadState(c + "/.claude/build/state/work-dev-engineer.json")
}

// Item 10: a question asked side by side with a SendMessage shows as
// waiting, with its code, from its call's start, in either order, and the
// code doesn't change when its permission request comes.
func TestAQuestionBesideASendMessageWaits(t *testing.T) {
	c := teamOf(t)
	for name, events := range map[string][]string{
		"one after the other": {sendStart, sendEnd, shipStart},
		"side by side":        {sendStart, shipStart, sendEnd},
	} {
		feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
		for _, ev := range events {
			feed(t, c, "work-dev-engineer", ev)
		}
		st := stateOf(c)
		if st.State != session.Waiting || st.Current() == nil || st.Current().Question != "Ship it?" {
			t.Fatalf("%s: %+v", name, st)
		}
		code := st.Current().Code
		if r := row(t, "engineer"); !strings.Contains(r, "waiting on you · it asked you something: cadrei answer") {
			t.Errorf("%s: row %q", name, r)
		}
		feed(t, c, "work-dev-engineer", shipAsk)
		if st := stateOf(c); len(st.Requests()) != 1 || st.Current().Code != code {
			t.Errorf("%s: after its permission request: %+v", name, st.Requests())
		}
		feed(t, c, "work-dev-engineer", shipEnd)
		if st := stateOf(c); st.State != session.Working || st.Current() != nil {
			t.Errorf("%s: answered in its window: %+v", name, st)
		}
	}
}

// Two prompts at once each get a code, and the listing shows both.
func TestTwoPromptsAtOnceBothShow(t *testing.T) {
	c := teamOf(t)
	for _, ev := range []string{`{"hook_event_name":"UserPromptSubmit"}`, mkdirStart, mkdirRequest, shipStart} {
		feed(t, c, "work-dev-engineer", ev)
	}
	st := stateOf(c)
	if len(st.Requests()) != 2 || st.Requests()[0].Code == st.Requests()[1].Code {
		t.Fatalf("requests: %+v", st.Requests())
	}
	ownTerminal(t)
	_, out, _ := call("answer")
	for _, r := range st.Requests() {
		if !strings.Contains(out, "  "+r.Code+"  ") {
			t.Errorf("the listing lacks %s:\n%s", r.Code, out)
		}
	}
	// The Bash prompt failing (a no in its window) ends it alone.
	feed(t, c, "work-dev-engineer", mkdirFailed)
	if st := stateOf(c); st.State != session.Waiting || len(st.Requests()) != 1 || st.Current().Kind != session.ForQuestion {
		t.Errorf("after the Bash call failed: %+v", st)
	}
}

// Hooks firing at once, as parallel tool calls make them, each get their
// change in: the member's lock keeps one from undoing another's.
func TestHooksAtOnceAllCount(t *testing.T) {
	c := teamOf(t)
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CADREI_HOME", c)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("toolu_%02d", i)
			in := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"` + id + `","tool_input":{"command":"make t` + id + `"}}`
			writeState(rt, c, "work-dev-engineer", []byte(in), time.Now())
			writeState(rt, c, "work-dev-engineer", []byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"make t`+id+`"}}`), time.Now())
		}(i)
	}
	wg.Wait()
	st := stateOf(c)
	if len(st.Calls) != 20 || len(st.Requests()) != session.MaxRequests {
		t.Errorf("%d calls, %d requests", len(st.Calls), len(st.Requests()))
	}
	for _, r := range st.Requests() {
		if r.Call == "" || !strings.HasSuffix(r.Summary, strings.TrimPrefix(r.Call, "toolu_")) {
			t.Errorf("request %+v isn't its call's", r)
		}
	}
}

// Answering one of two open prompts ends that one alone: the member still
// waits on the other, under its code.
func TestAnsweringOneOfTwoLeavesTheOther(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	for _, ev := range []string{mkdirStart, mkdirRequest, shipStart} {
		feed(t, c, "work-dev-engineer", ev)
	}
	st := stateOf(c)
	if len(st.Requests()) != 2 {
		t.Fatalf("requests: %+v", st.Requests())
	}
	bash, question := st.Requests()[0], st.Requests()[1]
	ownTerminal(t)
	if n, out, errOut := call("answer", bash.Code, "yes"); n != 0 {
		t.Fatalf("exit %d: %s%s", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
	if st := stateOf(c); st.State != session.Waiting || len(st.Requests()) != 1 || st.Current().Code != question.Code || st.WaitingFor != session.ForQuestion {
		t.Errorf("after the answer: %+v %+v", st, st.Requests())
	}
	// The prompt log: two opened, one answered by its code; the turn's
	// end cancels the other.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	if n := session.CountPrompts(c + "/.claude/build"); n != (session.PromptCounts{Opened: 2, ByCode: 1, Cancelled: 1}) {
		t.Errorf("prompt log: %+v", n)
	}
}

// A prompt answered in the member's window (its call ends with no answer
// by code) is the miss the prompt log counts.
func TestThePromptLogCountsAnswersInTheWindow(t *testing.T) {
	c := teamOf(t)
	for _, ev := range []string{`{"hook_event_name":"UserPromptSubmit"}`, mkdirStart, mkdirRequest, mkdirFailed, shipStart, shipEnd} {
		feed(t, c, "work-dev-engineer", ev)
	}
	if n := session.CountPrompts(c + "/.claude/build"); n != (session.PromptCounts{Opened: 2, InWindow: 2}) {
		t.Errorf("prompt log: %+v", n)
	}
}

// The catch-all's trigger, as 2.1.301 sends it about 6 seconds after any
// prompt shows.
const promptShown = `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission","title":"Claude Code"}`

// feedInPane runs the state hook as the member's own hooks run: inside
// its tmux pane, with $TMUX and $TMUX_PANE naming it.
func feedInPane(t *testing.T, c, role, payload string) {
	t.Helper()
	out, err := exec.Command("tmux", "-S", session.Default().Path(), "display", "-p", "-t", "=cadrei-work-dev:="+role, "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", session.Default().Path()+",1,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(out)))
	feed(t, c, "work-dev-"+role, payload)
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
}

// A prompt no hook recorded (here an MCP tool's, with its call's start
// lost) is read off the member's screen when the runtime says a prompt is
// up: its box and its options, under a code. The answer checks the screen
// shows that same prompt, then presses the option's number: only the one
// labeled Yes or the one labeled No, never one that allows more.
func TestAPromptOffTheScreenGetsACode(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "mcp-live.txt")
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feedInPane(t, c, "engineer", promptShown)
	st := stateOf(c)
	r := st.Current()
	if st.State != session.Waiting || st.WaitingFor != session.ForPrompt || r == nil || r.Source != session.FromScreen || r.Multi ||
		len(r.Options) != 3 || r.Options[0] != "Yes" || r.Options[2] != "No" || !strings.Contains(r.Question, `path: "resume.pdf"`) || !strings.Contains(r.Question, "Do you want to proceed?") {
		t.Fatalf("recorded: %+v %+v", st, r)
	}
	if row := row(t, "engineer"); !strings.Contains(row, "waiting on you · it shows a prompt: cadrei answer") {
		t.Errorf("row %q", row)
	}
	ownTerminal(t)
	_, out, _ := call("answer")
	if !strings.Contains(out, "  "+r.Code+"  The engineer in dev shows a prompt:") || !strings.Contains(out, "         1. Yes\n         3. No\n") || strings.Contains(out, "2. Yes, and") {
		t.Errorf("listing:\n%s", out)
	}
	// Finding 2: the choice that would allow it for good isn't pressed.
	if n, _, errOut := call("answer", r.Code, "2"); n == 0 || !strings.Contains(flat(errOut), "only presses 1 or 3 in that prompt") {
		t.Errorf("option 2: %d %q", n, errOut)
	}
	if k := key(t, home, "engineer"); k != "" {
		t.Fatalf("pressed %q for option 2", k)
	}
	// What the screen shows must be what was recorded: a changed record
	// presses nothing.
	session.ChangeState(c+"/.claude/build", "work-dev-engineer", time.Second, func(s *session.State) *session.State {
		changed := *s
		other := *s.Current()
		other.Question += " (changed)"
		changed.Open = []*session.Request{&other}
		return &changed
	})
	if n, _, errOut := call("answer", r.Code, "1"); n == 0 || !strings.Contains(errOut, "I couldn't find that prompt on the engineer's screen") {
		t.Errorf("changed: %d %q", n, errOut)
	}
	if k := key(t, home, "engineer"); k != "" {
		t.Fatalf("pressed %q for a changed prompt", k)
	}
	session.ChangeState(c+"/.claude/build", "work-dev-engineer", time.Second, func(*session.State) *session.State { return st })
	n, out, errOut := call("answer", r.Code, "1")
	if n != 0 || flat(out) != "+ Answered the engineer in dev: 1. Yes" {
		t.Errorf("answer: %d %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "1" {
		t.Errorf("key %q", k)
	}
	if st := stateOf(c); st.State != session.Working || st.Current() != nil {
		t.Errorf("after the answer: %+v", st)
	}
	raw, _ := os.ReadFile(session.PromptLogPath(c + "/.claude/build"))
	if !strings.Contains(string(raw), `"source":"screen","event":"opened"`) || session.CountPrompts(c+"/.claude/build").ByCode != 1 {
		t.Errorf("prompt log:\n%s", raw)
	}
}

// With a request the hooks recorded, the catch-all leaves it: no request
// is read off the screen in its place, and it only gains its choices
// (W6, PR B). Outside a pane, or with no prompt on the screen, nothing is
// recorded.
func TestTheCatchAllOnlyFillsAGap(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	feed(t, c, "work-dev-engineer", mkdirStart)
	feed(t, c, "work-dev-engineer", mkdirRequest)
	before := stateOf(c)
	feedInPane(t, c, "engineer", promptShown)
	after := stateOf(c)
	before.Current().Options = bashChoices
	if !session.Same(before, after) {
		t.Errorf("a recorded request changed: %+v", after.Current())
	}
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feed(t, c, "work-dev-engineer", promptShown) // no $TMUX_PANE
	if st := stateOf(c); st.State != session.Working || st.Current() != nil {
		t.Errorf("outside a pane: %+v", st)
	}
	feedInPane(t, c, "reviewer", promptShown) // no such window: nothing read
	if st := stateOf(c); st.State != session.Working {
		t.Errorf("no screen: %+v", st)
	}
}

// A prompt cadrei can't read whole (its top off the screen) is recorded
// for the member's window, so nothing offers an answer that can't work.
func TestATallPromptOffTheScreenIsLeftToItsWindow(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "tall-live.txt")
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feedInPane(t, c, "engineer", promptShown)
	st := stateOf(c)
	if st.State != session.Waiting || st.Current() == nil || !st.Current().Multi {
		t.Fatalf("tall: %+v %+v", st, st.Current())
	}
	if row := row(t, "engineer"); !strings.Contains(row, "it shows a prompt in its window") {
		t.Errorf("row %q", row)
	}
	ownTerminal(t)
	if n, _, errOut := call("answer", st.Current().Code, "1"); n == 0 || !strings.Contains(errOut, "couldn't read that prompt whole") {
		t.Errorf("answer: %d %q", n, errOut)
	}
}

// Review finding 1 and 2: the catch-all offers only what one key answers.
// A multi-select question (checkboxes), two questions at once (a Submit
// tab) and a prompt with no plain Yes or No go to the window. A question
// offers its own options, not the two the runtime adds; an approval only
// Yes and No. A label that wraps is read whole.
func TestTheCatchAllOffersOnlyWhatOneKeyAnswers(t *testing.T) {
	two := strings.Replace(rawFixture(t, "question-live.txt"), " ☐ Upload", "←  ☐ Upload  ☐ Size  ✔ Submit  →", 1)
	noPlain := strings.NewReplacer("❯ 1. Yes\n", "❯ 1. Yes, and keep going\n", "   4. No\n", "   4. No, and say why\n").Replace(rawFixture(t, "bash-prompt-live.txt"))
	for name, screen := range map[string]string{"multi-select": rawFixture(t, "multi-live.txt"), "two questions": two, "no plain yes or no": noPlain} {
		if r := screenRequest(screen); r == nil || !r.Multi {
			t.Errorf("%s: %+v", name, r)
		}
	}
	q := screenRequest(rawFixture(t, "question-live.txt"))
	if q == nil || q.Multi || len(q.Options) != 4 || fmt.Sprint(picks(q.Options)) != "[1 2]" {
		t.Errorf("a question: %+v %v", q, picks(q.Options))
	}
	b := screenRequest(rawFixture(t, "bash-prompt-live.txt"))
	if b == nil || b.Multi || b.Options[1] != "Yes, and always allow access to <work> from this project" || fmt.Sprint(picks(b.Options)) != "[1 4]" {
		t.Errorf("an approval: %+v %v", b, picks(b.Options))
	}
	opts, _, _ := screenPrompt(rawFixture(t, "write-live.txt"))
	if len(opts) != 3 || opts[1].label != "Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)" {
		t.Errorf("a wrapped label: %+v", opts)
	}
	// A question's descriptions aren't read as its labels.
	if opts, _, _ := screenPrompt(rawFixture(t, "question-live.txt")); opts[0].label != "Yes" || opts[1].label != "No" {
		t.Errorf("a question's labels: %+v", opts)
	}
}

// Review finding 1, end to end: a multi-select question the hooks lost is
// recorded for its window, and an answer by number presses nothing (one
// key would tick a box and leave the dialog up).
func TestAMultiSelectOffTheScreenIsLeftToItsWindow(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "multi-live.txt")
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feedInPane(t, c, "engineer", promptShown)
	st := stateOf(c)
	if st.State != session.Waiting || st.Current() == nil || !st.Current().Multi {
		t.Fatalf("multi-select: %+v %+v", st, st.Current())
	}
	ownTerminal(t)
	if n, _, errOut := call("answer", st.Current().Code, "1"); n == 0 || !strings.Contains(errOut, "couldn't read that prompt whole") {
		t.Errorf("answer: %d %q", n, errOut)
	}
	if k := key(t, home, "engineer"); k != "" {
		t.Errorf("pressed %q", k)
	}
}

// Review finding 3: the tool's own end can come between the key and the
// answer dropping the code. The mark put on before the key makes the
// prompt log count it as answered by its code, not in the window. A no
// still leaves the member idle, though the tool's failure took the code.
// With the mark taken off (the key didn't take), it's the window's.
func TestAnAnswerRacingItsToolsEndIsByCode(t *testing.T) {
	c := teamOf(t)
	build := c + "/.claude/build"
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feed(t, c, "work-dev-engineer", shipStart)
	w := &waitingOn{team: "dev", role: "engineer", name: "work-dev-engineer", req: stateOf(c).Current()}
	if open, err := answering(build, w, true); !open || err != nil || !stateOf(c).Current().ByCode(time.Now()) {
		t.Fatalf("not marked: %+v", stateOf(c).Current())
	}
	pressed := time.Now()
	feed(t, c, "work-dev-engineer", shipEnd) // first, as it often is
	answered(build, w, "1", pressed)
	if st := stateOf(c); st.State != session.Working || st.Current() != nil {
		t.Errorf("after a pick: %+v", st)
	}
	if n := session.CountPrompts(build); n != (session.PromptCounts{Opened: 1, ByCode: 1}) {
		t.Errorf("a pick: %+v", n)
	}
	// A no, with the call's failure first.
	feed(t, c, "work-dev-engineer", mkdirStart)
	feed(t, c, "work-dev-engineer", mkdirRequest)
	w.req = stateOf(c).Current()
	answering(build, w, true)
	pressed = time.Now()
	feed(t, c, "work-dev-engineer", mkdirFailed)
	answered(build, w, "no", pressed)
	if st := stateOf(c); st.State != session.Idle || st.Current() != nil {
		t.Errorf("after a no: %+v", st)
	}
	if n := session.CountPrompts(build); n != (session.PromptCounts{Opened: 2, ByCode: 2}) {
		t.Errorf("a no: %+v", n)
	}
	// The key didn't take: the mark is off, and its end is the window's.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	feed(t, c, "work-dev-engineer", mkdirStart)
	feed(t, c, "work-dev-engineer", mkdirRequest)
	w.req = stateOf(c).Current()
	answering(build, w, true)
	answering(build, w, false)
	feed(t, c, "work-dev-engineer", mkdirFailed)
	if n := session.CountPrompts(build); n != (session.PromptCounts{Opened: 3, ByCode: 2, InWindow: 1}) {
		t.Errorf("unmarked: %+v", n)
	}
	// Gone before the mark: nothing's waiting under it.
	if open, err := answering(build, w, true); open || err != nil {
		t.Errorf("marked a request that's gone: %v", err)
	}
}

// Low items A and C (PR B): a request that may be one of two Bash calls'
// ends with its own call, when that ends first and the member's screen
// shows no prompt, though an old one's footer is still above its empty
// input line. The prompt log counts it in the window.
func TestARequestEndsWithItsCallWhenTheScreenShowsNone(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	stepsMember(t, home, "engineer", rawFixture(t, "bash-prompt-live.txt")+"\n⏺ Made it.\n\n"+
		"────────────────────────────────────────\n❯ \n────────────────────────────────────────\n  ? for shortcuts\n")
	for _, ev := range []string{`{"hook_event_name":"UserPromptSubmit"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"make"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_2","tool_input":{"command":"make test"}}`,
		`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"make deploy"}}`} {
		feed(t, c, "work-dev-engineer", ev)
	}
	if r := stateOf(c).Current(); r == nil || len(r.Maybe) != 2 {
		t.Fatalf("not one of two: %+v", r)
	}
	feedInPane(t, c, "engineer", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_1"}`)
	if st := stateOf(c); st.State != session.Working || st.Current() != nil {
		t.Errorf("handed on: %+v", st.Current())
	}
	if n := session.CountPrompts(c + "/.claude/build"); n.InWindow != 1 {
		t.Errorf("prompt log: %+v", n)
	}
}

// Low item B (PR B): with the member's lock busy, an answer says so, not
// that nothing's waiting, and presses nothing.
func TestAnAnswerWithTheLockBusySaysSo(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	code := request(t, c, "engineer", mkdirRequest)
	lock, err := fsx.Acquire(session.LockPath(c+"/.claude/build", "work-dev-engineer"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ownTerminal(t)
	if n, out, errOut := call("answer", code, "yes"); n != 1 || out != "" || flat(errOut) != "cadrei: the engineer's record is busy right now, so I didn't press anything. Try again in a moment." {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
	if k := key(t, home, "engineer"); k != "" {
		t.Errorf("pressed %q", k)
	}
}

// A screen prompt's No ends the member's turn as an approval's no does:
// idle, with every prompt in it ended.
func TestAScreenPromptsNoLeavesTheMemberIdle(t *testing.T) {
	c := teamOf(t)
	build := c + "/.claude/build"
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	r := screenRequest(rawFixture(t, "bash-prompt-live.txt"))
	ev := runtime.StateEvent{Name: runtime.EvPromptShown, Request: r}
	session.ChangeState(build, "work-dev-engineer", time.Second, func(s *session.State) *session.State { return session.Apply(s, ev, time.Now()) })
	w := &waitingOn{team: "dev", role: "engineer", name: "work-dev-engineer", req: stateOf(c).Current()}
	if !saysNo(w, "4") || saysNo(w, "1") || saysNo(w, "2") {
		t.Errorf("which is a no: %+v", w.req.Options)
	}
	answered(build, w, "4", time.Now())
	if st := stateOf(c); st.State != session.Idle || st.Current() != nil {
		t.Errorf("after its No: %+v", st)
	}
	// A question's own No is only an answer.
	q := &waitingOn{req: &session.Request{Kind: session.ForPrompt, Options: []string{"Yes", "No", "Type something.", "Chat about this"}}}
	if saysNo(q, "2") {
		t.Error("a question's No ended the turn")
	}
}

// The runtime's idle reminder, as 2.1.301 sends it.
const idleReminder = `{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"Claude is waiting for your input"}`

// Review finding 4: a no or Esc in the member's window ends its prompt
// with no hook to say so. The idle reminder after it reads the member's
// screen: with the prompt still up, the request stays; with it gone, the
// member is idle and the prompt log counts it as answered in the window.
func TestAnIdleReminderEndsAPromptGoneFromTheScreen(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	promptMember(t, home, "engineer", "bash-prompt-live.txt")
	for _, ev := range []string{`{"hook_event_name":"UserPromptSubmit"}`, mkdirStart, mkdirRequest} {
		feed(t, c, "work-dev-engineer", ev)
	}
	// Any prompt the runtime draws counts, with or without an "Esc to
	// cancel" line: a fetch's and a plan's have none.
	for _, name := range []string{"fetch-live.txt", "plan-live.txt", "multi-live.txt", "bash-prompt-live.txt"} {
		if noPrompt(rawFixture(t, name)) {
			t.Errorf("%s reads as no prompt", name)
		}
	}
	if noPrompt("") || !noPrompt("⏺ Done.\n\n────\n❯ \n────\n  ? for shortcuts\n") {
		t.Error("noPrompt")
	}
	// Low item C (PR B): only the bottom of the screen counts, where the
	// runtime draws a prompt. The empty input line there means none is up,
	// whatever earlier output shows above it: an old prompt's footer, a
	// prompt the user echoed, or a reply that runs long.
	idle := "\n────────────────────────────────────────\n❯ \n────────────────────────────────────────\n  ? for shortcuts\n"
	for name, screen := range map[string]string{
		"an old prompt above": rawFixture(t, "bash-prompt-live.txt") + "\n⏺ Okay, I won't.\n" + idle,
		"an echoed prompt":    "❯ 1. Yes, go on\n\n⏺ Going on.\n" + idle,
		"a long reply":        " Esc to cancel\n" + strings.Repeat("⏺ more\n", maxPromptLines) + "  (no input line)\n",
	} {
		if !noPrompt(screen) {
			t.Errorf("%s reads as a prompt", name)
		}
	}
	feedInPane(t, c, "engineer", idleReminder)
	if st := stateOf(c); st.State != session.Waiting || st.Current() == nil {
		t.Fatalf("with the prompt up: %+v", st)
	}
	feed(t, c, "work-dev-engineer", idleReminder) // a screen it can't read
	if st := stateOf(c); st.State != session.Waiting {
		t.Fatalf("with no screen: %+v", st)
	}
	// Esc in its window: the stub takes the key, and its window shows the
	// runtime's input line again.
	os.WriteFile(home+"/after-engineer", []byte("⏺ Okay, I won't make it.\n\n────────────────────────────────────────\n❯ \n────────────────────────────────────────\n  ? for shortcuts\n"), 0o600)
	if err := session.Default().SendKeys(session.SessionName("work", "dev"), "engineer", false, "Escape"); err != nil {
		t.Fatal(err)
	}
	if k := key(t, home, "engineer"); k == "" {
		t.Fatal("no key")
	}
	for i := 0; i < 40 && !strings.Contains(session.Default().Screen(session.SessionName("work", "dev"), "engineer"), "for shortcuts"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	feedInPane(t, c, "engineer", idleReminder)
	if st := stateOf(c); st.State != session.Idle || st.Current() != nil {
		t.Errorf("with the prompt gone: %+v", st)
	}
	if n := session.CountPrompts(c + "/.claude/build"); n != (session.PromptCounts{Opened: 1, InWindow: 1}) {
		t.Errorf("prompt log: %+v", n)
	}
}
