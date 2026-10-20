package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// 0.2.3 (PR A): each prompt is its own request, matched to its tool call.
// The events are what Claude Code 2.1.301's hooks give (the hook spike's
// live runs): PreToolUse and PostToolUse name the call, PermissionRequest
// doesn't.

func pre(tool, id, key string) runtime.StateEvent {
	return runtime.StateEvent{Name: runtime.EvToolUse, Tool: tool, ToolID: id, InputKey: key}
}

func done(tool, id string) runtime.StateEvent {
	return runtime.StateEvent{Name: runtime.EvToolDone, Tool: tool, ToolID: id}
}

func approval(tool, key, summary string) runtime.StateEvent {
	return runtime.StateEvent{Name: runtime.EvPermission, Tool: tool, InputKey: key, Request: &runtime.Request{Tool: tool, Summary: summary}}
}

var ship = &runtime.Request{Tool: "AskUserQuestion", Question: "Ship?", Options: []string{"Yes", "No"}}

// run applies events in order, a millisecond apart.
func run(t *testing.T, s *State, evs ...runtime.StateEvent) *State {
	t.Helper()
	at := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	for _, ev := range evs {
		at = at.Add(time.Millisecond)
		s = Apply(s, ev, at)
	}
	return s
}

// A question is recorded at its call's start, from what the tool got,
// before its dialog draws. Its PermissionRequest, which comes next, is the
// same request (the same code), and its call's end ends it.
func TestAQuestionIsRecordedAtItsCallsStart(t *testing.T) {
	q := pre("AskUserQuestion", "toolu_q", "k-q")
	q.Question, q.Request = true, ship
	s := run(t, &State{State: Working}, q)
	if s.State != Waiting || s.WaitingFor != ForQuestion || len(s.Requests()) != 1 || s.Current().Call != "toolu_q" || s.Current().Question != "Ship?" {
		t.Fatalf("at its start: %+v %+v", s, s.Current())
	}
	GiveCode(filepath.Join(t.TempDir(), "w.json"), s)
	code := s.Current().Code
	perm := runtime.StateEvent{Name: runtime.EvPermission, Question: true, Tool: "AskUserQuestion", InputKey: "k-q", Request: ship}
	s = run(t, s, perm, q) // the PermissionRequest, and a second PreToolUse for the same call
	if len(s.Requests()) != 1 || s.Current().Code != code {
		t.Fatalf("after its permission request: %+v", s.Requests())
	}
	s = run(t, s, done("AskUserQuestion", "toolu_q"))
	if s.State != Working || s.Current() != nil || s.WaitingFor != "" || len(s.Calls) != 0 {
		t.Errorf("answered: %+v", s)
	}
}

// Item 10's two orders: SendMessage and a question in one turn. Whether
// SendMessage ends before the question starts (live run 2) or after its
// permission request (the order first suspected), the question stays
// open until its own call ends.
func TestAnotherCallEndingLeavesAQuestionOpen(t *testing.T) {
	q := pre("AskUserQuestion", "toolu_q", "k-q")
	q.Question, q.Request = true, ship
	perm := runtime.StateEvent{Name: runtime.EvPermission, Question: true, Tool: "AskUserQuestion", InputKey: "k-q", Request: ship}
	for name, evs := range map[string][]runtime.StateEvent{
		"one after the other": {pre("SendMessage", "toolu_s", "k-s"), done("SendMessage", "toolu_s"), q, perm},
		"side by side":        {pre("SendMessage", "toolu_s", "k-s"), q, perm, done("SendMessage", "toolu_s")},
	} {
		s := run(t, &State{State: Working}, evs...)
		if s.State != Waiting || len(s.Requests()) != 1 || s.Current().Call != "toolu_q" {
			t.Errorf("%s: %+v %+v", name, s, s.Requests())
		}
	}
}

// A permission request is matched to the open call with the same tool and
// input; each one's end ends only its own request.
func TestAPermissionRequestFindsItsCall(t *testing.T) {
	s := run(t, &State{State: Working},
		pre("Bash", "toolu_a", "k-a"), pre("Bash", "toolu_b", "k-b"), pre("Read", "toolu_r", "k-r"),
		approval("Bash", "k-b", "rm -r build"))
	if s.State != Waiting || len(s.Requests()) != 1 || s.Current().Call != "toolu_b" {
		t.Fatalf("matched: %+v", s.Requests())
	}
	s = run(t, s, done("Bash", "toolu_a"), done("Read", "toolu_r"))
	if s.State != Waiting || len(s.Requests()) != 1 {
		t.Errorf("other calls ended it: %+v", s)
	}
	s = run(t, s, done("Bash", "toolu_b"))
	if s.State != Working || s.Current() != nil {
		t.Errorf("its call ended: %+v", s)
	}
	// Two identical calls: each permission request takes its own.
	s = run(t, &State{State: Working}, pre("Bash", "toolu_1", "k"), pre("Bash", "toolu_2", "k"), approval("Bash", "k", "make"), approval("Bash", "k", "make"))
	if r := s.Requests(); len(r) != 2 || r[0].Call != "toolu_1" || r[1].Call != "toolu_2" {
		t.Errorf("identical calls: %+v %+v", r[0], r[1])
	}
	// The input changed on the way (a hook's updatedInput): the one open
	// call of that tool.
	s = run(t, &State{State: Working}, pre("Write", "toolu_w", "k-old"), approval("Write", "k-new", "/w/a.txt"))
	if s.Current().Call != "toolu_w" {
		t.Errorf("one call of the tool: %+v", s.Current())
	}
}

// Two prompts at once are two requests, with two codes; answering or
// ending one leaves the other, and the oldest says what it waits for.
func TestTwoPromptsAtOnce(t *testing.T) {
	q := pre("AskUserQuestion", "toolu_q", "k-q")
	q.Question, q.Request = true, ship
	s := run(t, &State{State: Working}, pre("Bash", "toolu_b", "k-b"), approval("Bash", "k-b", "make deploy"), q)
	GiveCode(filepath.Join(t.TempDir(), "w.json"), s)
	r := s.Requests()
	if len(r) != 2 || r[0].Code == r[1].Code || s.WaitingFor != ForApproval {
		t.Fatalf("two prompts: %+v %+v %q", r[0], r[1], s.WaitingFor)
	}
	s = run(t, s, done("Bash", "toolu_b"))
	if s.State != Waiting || len(s.Requests()) != 1 || s.WaitingFor != ForQuestion {
		t.Errorf("one left: %+v", s)
	}
	if !s.Drop(s.Current().Code) || s.Drop("zzz") || s.Current() != nil {
		t.Error("Drop")
	}
}

// A permission request that came without its call's start (a lost hook,
// or a member started before 0.2.3) names no call. A call of the same
// tool ending ends it, so it doesn't wait on after its prompt went.
func TestARequestWithoutItsCallEndsWithTheTool(t *testing.T) {
	s := run(t, &State{State: Working}, approval("Bash", "k", "make"))
	if s.Current().Call != "" {
		t.Fatalf("call %q", s.Current().Call)
	}
	if s = run(t, s, done("Read", "toolu_r")); s.State != Waiting {
		t.Errorf("another tool ended it: %+v", s)
	}
	if s = run(t, s, done("Bash", "toolu_b")); s.State != Working || s.Current() != nil {
		t.Errorf("its tool ended: %+v", s)
	}
}

// The catch-all: the runtime says a prompt is up and no hook recorded one,
// so the one read off the screen stands for it, belonging to the one call
// with nothing recorded. With a request open, it changes nothing.
func TestAPromptOffTheScreen(t *testing.T) {
	shown := runtime.StateEvent{Name: runtime.EvPromptShown, Request: &runtime.Request{Question: "Do you want to proceed?", Options: []string{"Yes", "No"}}}
	s := run(t, &State{State: Working}, pre("WebFetch", "toolu_f", "k-f"), shown)
	if s.State != Waiting || s.WaitingFor != ForPrompt || s.Current().Source != FromScreen || s.Current().Call != "toolu_f" {
		t.Fatalf("screen prompt: %+v %+v", s, s.Current())
	}
	if s = run(t, s, done("WebFetch", "toolu_f")); s.Current() != nil {
		t.Errorf("its call ended: %+v", s)
	}
	busy := run(t, &State{State: Working}, approval("Bash", "k", "make"))
	if next := Apply(busy, shown, time.Now()); next != busy {
		t.Errorf("over a recorded request: %+v", next)
	}
	if next := Apply(busy, runtime.StateEvent{Name: runtime.EvPromptShown}, time.Now()); next != busy {
		t.Errorf("with nothing read: %+v", next)
	}
}

// Review finding 5: a call's end never ends another call's prompt. A
// request answered by its code is gone before its own call ends, and that
// end leaves a request of the same tool that names no call (here a 0.2.2
// one). With two calls a request may be, the first to end isn't its own
// (a call waiting on a prompt doesn't end), so it's the other's.
func TestACallsEndEndsOnlyItsOwnPrompt(t *testing.T) {
	s := run(t, &State{State: Working}, pre("Bash", "toolu_a", "k-a"), approval("Bash", "k-a", "make"))
	s.Open = append(s.Open, &Request{Code: "7f3", Kind: ForApproval, Tool: "Bash", Summary: "make deploy"})
	GiveCode(filepath.Join(t.TempDir(), "w.json"), s)
	s.Drop(s.Current().Code) // answered by its code
	if s = run(t, s, done("Bash", "toolu_a")); s.State != Waiting || len(s.Requests()) != 1 || s.Current().Code != "7f3" {
		t.Errorf("an answered call's end ended another: %+v", s.Requests())
	}
	// Two Bash calls, and a permission request whose input matches neither.
	s = run(t, &State{State: Working}, pre("Bash", "toolu_1", "k-1"), pre("Bash", "toolu_2", "k-2"), approval("Bash", "k-other", "make"))
	if r := s.Current(); r == nil || r.Call != "" || len(r.Maybe) != 2 {
		t.Fatalf("unmatched: %+v", r)
	}
	before := s.Current()
	if s = run(t, s, done("Bash", "toolu_2")); s.State != Waiting || s.Current() == nil || s.Current().Call != "toolu_1" || len(s.Current().Maybe) != 0 {
		t.Errorf("the other call's end: %+v", s.Current())
	}
	if before.Call != "" || len(before.Maybe) != 2 {
		t.Errorf("the state before changed: %+v", before)
	}
	if s = run(t, s, done("Bash", "toolu_1")); s.State != Working || s.Current() != nil {
		t.Errorf("its own call's end: %+v", s)
	}
	// Low item A (PR B): its own call's end comes first, while the other
	// runs. The screen shows no prompt, so it ends, rather than go on
	// under the running call's code.
	s = run(t, &State{State: Working}, pre("Bash", "toolu_1", "k-1"), pre("Bash", "toolu_2", "k-2"), approval("Bash", "k-other", "make"))
	end := done("Bash", "toolu_1")
	end.NoPrompt = true
	if s = run(t, s, end); s.State != Working || s.Current() != nil || len(s.Calls) != 1 {
		t.Errorf("with no prompt on the screen: %+v", s)
	}
}

// A state keeps at most MaxRequests requests and maxCalls calls: the
// oldest go first.
func TestAStateIsBounded(t *testing.T) {
	s := &State{State: Working}
	for i := 0; i < 50; i++ {
		id := "toolu_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		s = run(t, s, pre("Bash", id, "k-"+id), approval("Bash", "k-"+id, "make"))
	}
	if len(s.Requests()) != MaxRequests || len(s.Calls) != maxCalls || s.Requests()[MaxRequests-1].Call != "toolu_xb" {
		t.Errorf("%d requests, %d calls, last %q", len(s.Requests()), len(s.Calls), s.Requests()[MaxRequests-1].Call)
	}
	path := filepath.Join(t.TempDir(), "w-dev-engineer.json")
	GiveCode(path, s)
	if err := WriteState(path, s); err != nil || len(ReadState(path).Requests()) != MaxRequests {
		t.Errorf("a full state doesn't read back: %v", err)
	}
}

// W3 (PR B): a request the hook had to cut short keeps its whole text in
// a file of its own, written with the state that offers its code, and
// gone once the request ends. A file that doesn't go with the request
// (its start isn't the summary) isn't read as its text.
func TestARequestsWholeTextHasItsOwnFile(t *testing.T) {
	build := t.TempDir()
	whole := strings.Repeat("echo one && ", 40) + "echo done"
	long := approval("Bash", "k1", runtime.RequestText(whole, runtime.SummaryLimit))
	long.Request.Full = whole
	change := func(evs ...runtime.StateEvent) *State {
		t.Helper()
		_, next, err := ChangeState(build, "w-dev-engineer", time.Second, func(s *State) *State { return run(t, s, evs...) })
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	s := change(pre("Bash", "toolu_1", "k1"), long, pre("Bash", "toolu_2", "k2"), approval("Bash", "k2", "make"))
	cut, short := s.Requests()[0], s.Requests()[1]
	if got := ReadFull(build, "w-dev-engineer", cut); got != whole {
		t.Errorf("the whole text: %q", got)
	}
	if raw, _ := os.ReadFile(StatePath(build, "w-dev-engineer")); strings.Contains(string(raw), "echo done") {
		t.Errorf("the state file holds it: %s", raw)
	}
	if _, err := os.Stat(FullPath(build, "w-dev-engineer", short.Code)); err == nil || ReadFull(build, "w-dev-engineer", short) != "" {
		t.Errorf("a short request has a file")
	}
	os.WriteFile(FullPath(build, "w-dev-engineer", cut.Code), []byte("rm -rf / && "+whole), 0o600)
	if got := ReadFull(build, "w-dev-engineer", cut); got != "" {
		t.Errorf("another text: %q", got)
	}
	change(done("Bash", "toolu_1"))
	if _, err := os.Stat(FullPath(build, "w-dev-engineer", cut.Code)); !os.IsNotExist(err) {
		t.Errorf("the file outlived its request: %v", err)
	}

	// A start or a stop writes the state file outside ChangeState: the
	// files of the requests it held go with them, and no other member's.
	for _, s := range []*State{{State: Starting, Since: time.Now()}, nil} {
		rs := change(pre("Bash", "toolu_3", "k1"), long).Requests()
		cut = rs[len(rs)-1]
		if ReadFull(build, "w-dev-engineer", cut) != whole {
			t.Fatalf("no whole text for %+v", cut)
		}
		other := FullPath(build, "w-dev-engineer2", cut.Code)
		os.WriteFile(other, []byte(whole), 0o600)
		if err := ResetState(StatePath(build, "w-dev-engineer"), s); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(FullPath(build, "w-dev-engineer", cut.Code)); !os.IsNotExist(err) {
			t.Errorf("reset to %+v: the file outlived its request: %v", s, err)
		}
		if _, err := os.Stat(other); err != nil {
			t.Errorf("reset to %+v: another member's file went: %v", s, err)
		}
		if got := ReadState(StatePath(build, "w-dev-engineer")); (got == nil) != (s == nil) {
			t.Errorf("reset to %+v: %+v", s, got)
		}
	}
}

// A 0.2.2 state file's one request reads as the first open one.
func TestA022StateFileReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w-dev-engineer.json")
	os.WriteFile(path, []byte(`{"state":"waiting","since":"2026-10-15T10:00:00Z","waiting_for":"approval","request":{"code":"7f3","kind":"approval","tool":"Bash","summary":"make"}}`+"\n"), 0o600)
	s := ReadState(path)
	if s == nil || len(s.Requests()) != 1 || s.Current().Code != "7f3" || s.Legacy != nil {
		t.Fatalf("read: %+v", s)
	}
	WriteState(path, s)
	if raw, _ := os.ReadFile(path); string(raw) == "" || ReadState(path).Current().Code != "7f3" {
		t.Errorf("written back: %s", raw)
	}
}

var trustScreen = &runtime.StartScreen{Kind: runtime.StartTrust, Title: "Do you trust the files in this folder?", Options: []string{"No, exit", "Yes, I trust this folder"}}

// N5: a starting member's start-up screen is its request, and it waits on
// it; the same screen keeps its code, wherever the pointer is; another
// takes its place; none left means it's starting again. A member a hook
// wrote for isn't looked at.
func TestAStartUpScreenIsARequest(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	start := &State{State: Starting, Since: at, Session: "s1"}
	s := SeeStart(start, trustScreen, at)
	if !Starts(s) || s.State != Waiting || s.WaitingFor != ForStart || len(s.Open) != 1 || s.Current().Tool != runtime.StartTrust || s.Current().Source != FromScreen {
		t.Fatalf("seen: %+v", s)
	}
	s.Open[0].Code = "s7k"
	moved := *trustScreen
	moved.At = 1
	if again := SeeStart(s, &moved, at.Add(time.Second)); again != s {
		t.Errorf("the same screen changed it: %+v", again)
	}
	login := &runtime.StartScreen{Kind: runtime.StartLogin, Title: "Select login method", Options: []string{"a", "b"}}
	if next := SeeStart(s, login, at); next.Current().Tool != runtime.StartLogin || next.Current().Code != "" {
		t.Errorf("another screen: %+v", next.Current())
	}
	if gone := SeeStart(s, nil, at); gone.State != Starting || len(gone.Open) != 0 || gone.WaitingFor != "" || gone.Session != "s1" {
		t.Errorf("gone: %+v", gone)
	}
	if same := SeeStart(start, nil, at); same != start {
		t.Errorf("nothing to see: %+v", same)
	}
	for _, other := range []*State{nil, {State: Working}, {State: Idle}, {State: Waiting, Open: []*Request{{Kind: ForApproval}}}} {
		if got := SeeStart(other, trustScreen, at); got != other {
			t.Errorf("%+v looked at: %+v", other, got)
		}
	}
}

// Any hook means the start-up screens are behind it: a start-up request
// still open was answered in its window, and goes.
func TestAHookEndsAStartUpRequest(t *testing.T) {
	s := SeeStart(&State{State: Starting}, trustScreen, time.Now())
	for _, ev := range []runtime.StateEvent{{Name: runtime.EvPrompt}, pre("Bash", "toolu_1", "k"), {Name: runtime.EvStop}} {
		if next := run(t, s, ev); len(next.Open) != 0 || next.State == Waiting {
			t.Errorf("%s: %+v", ev.Name, next)
		}
	}
}

// A start-up screen that ends with no answer by its code was answered in
// its window, whatever ended its request: never cancelled.
func TestAStartUpScreenEndsInItsWindow(t *testing.T) {
	build := t.TempDir()
	s := SeeStart(&State{State: Starting}, trustScreen, time.Now())
	s.Open[0].Code = "s7k"
	LogPrompts(build, "work-dev-engineer", s, run(t, s, runtime.StateEvent{Name: runtime.EvPrompt}), Ending(runtime.EvPrompt), time.Now())
	raw, _ := os.ReadFile(PromptLogPath(build))
	if !strings.Contains(string(raw), `"code":"s7k","kind":"start","tool":"trust","source":"screen","event":"ended","ended":"in_window"`) {
		t.Errorf("log: %s", raw)
	}
}
