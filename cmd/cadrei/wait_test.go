//go:build !windows

package main

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// waitCrew starts team dev in crew work with stub members, the engineer
// waiting on a command, and has cadrei run from the user's own terminal.
// It returns the crew's folder and the engineer's code.
func waitCrew(t *testing.T) (c, eng string) {
	t.Helper()
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c = home + "/.cadrei/work"
	must(t, "up", "dev")
	eng = request(t, c, "engineer", mkdirRequest)
	ownTerminal(t)
	old := waitEvery
	waitEvery = 50 * time.Millisecond
	t.Cleanup(func() { waitEvery = old })
	return c, eng
}

// ask gives role a question, as a hook would record it, with no hook run
// (one in the test's own process would change its environment while a
// wait runs). It returns the question's code.
func ask(t *testing.T, c, role, question string) string {
	t.Helper()
	_, next, err := session.ChangeState(c+"/.claude/build", "work-dev-"+role, time.Second, func(prev *session.State) *session.State {
		return &session.State{State: session.Waiting, WaitingFor: session.ForQuestion, Since: time.Now(),
			Open: []*session.Request{{Kind: session.ForQuestion, Tool: "AskUserQuestion", Question: question, Options: []string{"Yes", "No"}, Source: session.FromHook}}}
	})
	if err != nil || next.Current() == nil {
		t.Fatalf("no question: %v", err)
	}
	return next.Current().Code
}

// Item 6: a code alone shows that one request as the listing does, with
// the line that answers it, and presses nothing. A code that's gone says
// so.
func TestAnswerACodeAloneShowsIt(t *testing.T) {
	_, eng := waitCrew(t)
	want := "  " + eng + "  The engineer in dev wants to run a command:\n" +
		"         mkdir e2e-yes\n" +
		"       Allow it once:  cadrei answer " + eng + " yes\n" +
		"       Say no:         cadrei answer " + eng + " no\n"
	if n, out, errOut := call("answer", eng); n != 0 || out != want || errOut != "" {
		t.Errorf("exit %d:\n%s\nwant:\n%s%s", n, out, want, errOut)
	}
	if n, out, _ := call("answer"); n != 0 || !strings.Contains(out, eng) {
		t.Errorf("the look ended the request: %q", out)
	}
	if n, _, errOut := call("answer", "zz9"); n != 1 || flat(errOut) != "cadrei: nothing's waiting under zz9 anymore. Run cadrei answer to see what's waiting now." {
		t.Errorf("gone: %d %q", n, errOut)
	}
	if n, _, errOut := call("answer", "--now"); n != 1 || !strings.Contains(flat(errOut), "cadrei answer --wait to wait for the next") {
		t.Errorf("a flag it doesn't know: %d %q", n, errOut)
	}
}

// N8: a wait shows a request no wait showed yet, and ends: at once for one
// that came in before it started, and once one comes in while it waits.
// One it showed doesn't end the next wait, and a code given again to
// another request counts as new.
func TestTheWaitShowsWhatsNew(t *testing.T) {
	c, eng := waitCrew(t)
	n, out, errOut := call("answer", "--wait")
	if n != 0 || !strings.HasPrefix(out, "New and waiting on you:\n\n  "+eng+"  The engineer in dev wants to run a command:\n") || errOut != "" {
		t.Fatalf("first wait: %d %q %q", n, out, errOut)
	}
	if n, out, _ := call("answer", "--wait", "--timeout", "300ms"); n != 0 || out != "Nothing new came in for 300ms.\n" {
		t.Errorf("shown before: %d %q", n, out)
	}
	type result struct {
		n        int
		out, err string
	}
	done := make(chan result, 1)
	go func() {
		n, out, errOut := call("answer", "--wait", "--timeout", "10s")
		done <- result{n, out, errOut}
	}()
	time.Sleep(300 * time.Millisecond)
	select {
	case r := <-done:
		t.Fatalf("it ended with nothing new: %+v", r)
	default:
	}
	rev := ask(t, c, "reviewer", "Upload the test file?")
	select {
	case r := <-done:
		want := "New and waiting on you:\n\n" +
			"  " + rev + "  The reviewer in dev asks: Upload the test file?\n" +
			"         1. Yes\n" +
			"         2. No\n" +
			"       Answer:         cadrei answer " + rev + " 1\n\n" +
			"One more, shown before, still waits. See them all with cadrei answer.\n"
		if r.n != 0 || r.out != want {
			t.Errorf("exit %d:\n%s\nwant:\n%s%s", r.n, r.out, want, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait didn't end on a new request")
	}
	raw, _ := os.ReadFile(waitedPath(c + "/.claude/build"))
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 2 {
		t.Errorf("waited file: %q", raw)
	}
	// The reviewer's question ends; another, under the same code, is new.
	session.ChangeState(c+"/.claude/build", "work-dev-reviewer", time.Second, func(prev *session.State) *session.State {
		return &session.State{State: session.Working, Since: time.Now()}
	})
	if n, out, _ := call("answer", "--wait", "--timeout", "300ms"); n != 0 || out != "Nothing new came in for 300ms.\n" {
		t.Errorf("after it ended: %d %q", n, out)
	}
	session.ChangeState(c+"/.claude/build", "work-dev-reviewer", time.Second, func(prev *session.State) *session.State {
		return &session.State{State: session.Waiting, WaitingFor: session.ForQuestion, Since: time.Now(),
			Open: []*session.Request{{Code: rev, Kind: session.ForQuestion, Tool: "AskUserQuestion", Question: "Delete the test file?", Options: []string{"Yes", "No"}, Source: session.FromHook}}}
	})
	if n, out, _ := call("answer", "--wait", "--timeout", "5s"); n != 0 || !strings.Contains(out, rev+"  The reviewer in dev asks: Delete the test file?") {
		t.Errorf("a code given again: %d %q", n, out)
	}
}

// A wait holds its lock while it runs, so the team status can tell, and a
// second one for the crew isn't started. With no member running it ends
// at once, and its options are checked.
func TestOnlyOneWaitRuns(t *testing.T) {
	c, _ := waitCrew(t)
	build := c + "/.claude/build"
	call("answer", "--wait") // shows the engineer's
	if waitRunning(build) {
		t.Fatal("a wait runs before any started")
	}
	done := make(chan int, 1)
	go func() {
		n, _, _ := call("answer", "--wait", "--timeout", "2s")
		done <- n
	}()
	for i := 0; i < 40 && !waitRunning(build); i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if !waitRunning(build) {
		t.Fatal("the running wait isn't seen")
	}
	if n, _, errOut := call("answer", "--wait"); n != 1 || flat(errOut) != "cadrei: a cadrei answer --wait is already running for this crew, so this one isn't needed." {
		t.Errorf("a second wait: %d %q", n, errOut)
	}
	if n := <-done; n != 0 || waitRunning(build) {
		t.Errorf("after it ended: exit %d, running %v", n, waitRunning(build))
	}
	for _, args := range [][]string{{"--timeout", "soon"}, {"--timeout"}, {"7f3"}, {"--timeout", "-1s"}} {
		if n, _, _ := call(append([]string{"answer", "--wait"}, args...)...); n != 1 {
			t.Errorf("%v: exit %d", args, n)
		}
	}
	must(t, "stop", "dev", "--yes")
	if n, out, _ := call("answer", "--wait"); n != 0 || out != "No member is running, so there's nothing to wait on.\n" {
		t.Errorf("no member: %d %q", n, out)
	}
}

// PR B's re-review (low): an answer stopped before it pressed a key (Ctrl-C
// on the orchestrator's command) takes its mark off the request, so an
// answer then given in the window isn't logged as one by its code. One
// stopped after a key went keeps it.
func TestAnAnswerStoppedEarlyTakesItsMarkOff(t *testing.T) {
	c, eng := waitCrew(t)
	r, err := cadreis.Resolve(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	build := c + "/.claude/build"
	mark := func() time.Time {
		t.Helper()
		st := session.ReadState(session.StatePath(build, "work-dev-engineer"))
		if st == nil || st.Current() == nil {
			t.Fatalf("no request: %+v", st)
		}
		return st.Current().Answering
	}
	for _, pressed := range []bool{false, true} {
		w := findWaiting(r, session.Default(), eng)
		if w == nil {
			t.Fatal("not found")
		}
		if open, err := answering(build, w, true); !open || err != nil || mark().IsZero() {
			t.Fatalf("not marked: %v %v", open, err)
		}
		var marked, pressing atomic.Bool
		marked.Store(true)
		pressing.Store(pressed)
		restored := false
		stopAnswer(build, w, &marked, &pressing, func() { restored = true })
		if !restored || mark().IsZero() != !pressed {
			t.Errorf("pressed %v: restored %v, mark %v", pressed, restored, mark())
		}
	}
}
