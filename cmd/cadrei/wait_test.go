//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
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

// Review M1: a wait is for the orchestrator open when it started. The
// team status stops counting it once that one is gone, and it ends on its
// next look: when that orchestrator's process ended, and when another
// opened. A new orchestrator's wait takes over then, with no "already
// running".
func TestAWaitEndsWithItsOrchestrator(t *testing.T) {
	c, _ := waitCrew(t)
	build := c + "/.claude/build"
	call("answer", "--wait") // shows the engineer's
	open := func() *exec.Cmd {
		t.Helper()
		p := exec.Command("sleep", "60")
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.Process.Kill(); p.Wait() })
		if _, err := orchestrator.WriteLock(orchestrator.LockPath(build), p.Process.Pid, orchestrator.Terminal, "", ""); err != nil {
			t.Fatal(err)
		}
		return p
	}
	start := func() chan string {
		done := make(chan string, 1)
		go func() {
			_, out, errOut := call("answer", "--wait", "--timeout", "20s")
			done <- out + errOut
		}()
		for i := 0; i < 80 && !waitRunning(build); i++ {
			time.Sleep(25 * time.Millisecond)
		}
		if !waitRunning(build) {
			t.Fatal("the wait isn't seen")
		}
		return done
	}
	ended := func(done chan string) {
		t.Helper()
		select {
		case out := <-done:
			if out != "The orchestrator this wait was for isn't open anymore, so the wait ended.\n" {
				t.Errorf("ended: %q", out)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the wait didn't end with its orchestrator")
		}
	}
	// Its orchestrator's process ends.
	first := open()
	done := start()
	first.Process.Kill()
	first.Wait()
	if waitRunning(build) {
		t.Error("a wait for an orchestrator that's gone still counts")
	}
	ended(done)
	// Another orchestrator opens: the old wait ends, the new one runs.
	open()
	done = start()
	open()
	if waitRunning(build) {
		t.Error("a wait for the orchestrator before still counts")
	}
	if n, out, errOut := call("answer", "--wait", "--timeout", "300ms"); n != 0 || out != "Nothing new came in for 300ms.\n" {
		t.Errorf("the new orchestrator's wait: %d %q %q", n, out, errOut)
	}
	ended(done)
}

// Review R2-2: a new wait takes over from one left by an orchestrator
// that's gone however long that one's look takes, with no "already
// running".
func TestAWaitTakesOverFromASlowOne(t *testing.T) {
	c, _ := waitCrew(t)
	build := c + "/.claude/build"
	call("answer", "--wait") // shows the engineer's
	// The old wait: it holds the lock for an orchestrator that's gone, and
	// its look runs far past 3 of the test's polls.
	old, err := fsx.Acquire(waitLockPath(build), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(waitOwnerPath(build), []byte("1 gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(40 * waitEvery)
		old.Release()
	}()
	if n, out, errOut := call("answer", "--wait", "--timeout", "300ms"); n != 0 || out != "Nothing new came in for 300ms.\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
	}
}

// Review L2: a wait that starts while the team status takes the lock for
// its quick look still runs, even with a wait's note left by one that was
// killed.
func TestAWaitStartsDuringTheTeamStatusLook(t *testing.T) {
	c, _ := waitCrew(t)
	build := c + "/.claude/build"
	call("answer", "--wait") // shows the engineer's
	if err := os.WriteFile(waitOwnerPath(build), []byte(waitOwner(build)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The look holds the lock longer here than a real one, and the grace
	// is longer to match, so the wait surely starts while it's held.
	old := waitGrace
	waitGrace = 3 * time.Second
	t.Cleanup(func() { waitGrace = old })
	look, err := fsx.Acquire(waitLockPath(build), 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(time.Second)
		look.Release()
	}()
	if n, out, errOut := call("answer", "--wait", "--timeout", "300ms"); n != 0 || out != "Nothing new came in for 300ms.\n" {
		t.Errorf("exit %d: %q %q", n, out, errOut)
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
		m := &answerMark{}
		if open, stopped, err := m.mark(build, w); !open || stopped || err != nil || mark().IsZero() {
			t.Fatalf("not marked: %v %v %v", open, stopped, err)
		}
		if pressed && !m.press() {
			t.Fatal("the press was refused")
		}
		restored := false
		stopAnswer(build, w, m, func() { restored = true })
		if !restored || mark().IsZero() != !pressed {
			t.Errorf("pressed %v: restored %v, mark %v", pressed, restored, mark())
		}
	}
	// Review L3: a stop that comes before the mark's write leaves no mark,
	// and the answer goes no further.
	if _, err := answering(build, findWaiting(r, session.Default(), eng), false); err != nil {
		t.Fatal(err)
	}
	w := findWaiting(r, session.Default(), eng)
	m := &answerMark{}
	stopAnswer(build, w, m, func() {})
	if _, stopped, _ := m.mark(build, w); !stopped || !mark().IsZero() {
		t.Errorf("a stop first: stopped %v, mark %v", stopped, mark())
	}
	if m.press() {
		t.Error("a key would go after the stop")
	}
}
