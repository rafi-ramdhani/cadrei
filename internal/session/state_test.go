package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

func TestStateFollowsTheHooks(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)
	ev := func(name string) runtime.StateEvent { return runtime.StateEvent{Name: name, Session: "s1"} }
	var s *State
	steps := []struct {
		ev    runtime.StateEvent
		after time.Duration
		state string
		since time.Duration // from t0
	}{
		{ev(runtime.EvStart), 0, Idle, 0},
		{ev(runtime.EvPrompt), time.Minute, Working, time.Minute},
		{ev(runtime.EvToolUse), 2 * time.Minute, Working, time.Minute}, // still the same turn
		{runtime.StateEvent{Name: runtime.EvPermission, Question: true}, 3 * time.Minute, Waiting, 3 * time.Minute},
		{ev(runtime.EvToolUse), 4 * time.Minute, Working, 4 * time.Minute},
		{ev(runtime.EvStop), 5 * time.Minute, Idle, 5 * time.Minute},
		{runtime.StateEvent{}, 6 * time.Minute, Idle, 5 * time.Minute}, // a notification that changes nothing
		{ev(runtime.EvIdleReminder), 7 * time.Minute, Idle, 5 * time.Minute},
		{ev(runtime.EvPrompt), 8 * time.Minute, Working, 8 * time.Minute},
		// A queued prompt's Stop from the turn before: still working.
		{ev(runtime.EvStop), 8*time.Minute + 300*time.Millisecond, Working, 8 * time.Minute},
		{ev(runtime.EvStopFailure), 9 * time.Minute, Idle, 9 * time.Minute},
	}
	for i, st := range steps {
		s = Apply(s, st.ev, t0.Add(st.after))
		if s == nil || s.State != st.state || !s.Since.Equal(t0.Add(st.since)) {
			t.Fatalf("step %d: %+v, want %s since +%v", i, s, st.state, st.since)
		}
		if i == 3 && s.WaitingFor != ForQuestion {
			t.Errorf("waiting for %q", s.WaitingFor)
		}
	}
	if !s.Error || s.Session != "s1" {
		t.Errorf("after StopFailure: %+v", s)
	}
	if Apply(s, ev(runtime.EvEnd), t0) != nil {
		t.Error("the session ended, and the state stayed")
	}
	dir := t.TempDir()
	path := StatePath(dir, "work-dev-engineer")
	if err := WriteState(path, s); err != nil || ReadState(path) == nil || ReadState(path).State != Idle {
		t.Errorf("write and read: %v", err)
	}
	WriteState(path, nil)
	if ReadState(path) != nil || StatePath(dir, "../x") != "" || filepath.Base(filepath.Dir(path)) != "state" {
		t.Error("state paths")
	}
}

// countingOps counts the transcript reads behind Safeguard.
type countingOps struct {
	resumableOps
	reads *int
}

func (o countingOps) SafeguardStop(string) (time.Time, bool) {
	*o.reads++
	return time.Time{}, false
}

// Audit qual-tests-06 (7.7): Safeguard reads a transcript again only when
// its size or time changed; its cache answers otherwise.
func TestSafeguardReadsAnUnchangedTranscriptOnce(t *testing.T) {
	build := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o600)
	reads := 0
	ops := countingOps{reads: &reads}
	flushes := fsx.Syncs()
	Safeguard(ops, build, "work-dev-engineer", transcript)
	// The cache is written, but not flushed (perf-hooks-1): it's throwaway.
	if fsx.Syncs() != flushes {
		t.Error("the safeguard cache was flushed to disk")
	}
	Safeguard(ops, build, "work-dev-engineer", transcript)
	if reads != 1 {
		t.Errorf("an unchanged transcript was read %d times", reads)
	}
	f, _ := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write([]byte("x"))
	f.Close()
	Safeguard(ops, build, "work-dev-engineer", transcript)
	if reads != 2 {
		t.Errorf("a grown transcript: %d reads", reads)
	}
}

// 0.2.2 (6.4): a permission request records what the runtime passed,
// waiting for a code; leaving waiting drops it; a new request is a new
// request.
func TestARequestLastsWhileItWaits(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	ask := runtime.StateEvent{Name: runtime.EvPermission, Request: &runtime.Request{Tool: "Bash", Summary: "gh pr merge 42 --squash"}}
	s := Apply(nil, ask, now)
	if s.State != Waiting || s.Request == nil || !Same(&State{Request: s.Request}, &State{Request: &Request{Kind: ForApproval, Tool: "Bash", Summary: "gh pr merge 42 --squash"}}) {
		t.Fatalf("approval: %+v %+v", s, s.Request)
	}
	q := runtime.StateEvent{Name: runtime.EvPermission, Question: true, Request: &runtime.Request{Tool: "AskUserQuestion", Question: "Ship?", Options: []string{"Yes", "No"}}}
	s = Apply(s, q, now)
	if s.Request.Kind != ForQuestion || s.Request.Question != "Ship?" || len(s.Request.Options) != 2 || s.Request.Code != "" {
		t.Errorf("question: %+v", s.Request)
	}
	for _, name := range []string{runtime.EvToolUse, runtime.EvStop, runtime.EvPrompt, runtime.EvStopFailure, runtime.EvIdleReminder} {
		if next := Apply(s, runtime.StateEvent{Name: name}, now.Add(time.Minute)); next.Request != nil {
			t.Errorf("%s kept the request", name)
		}
	}
}

// Same compares states, their requests too.
func TestSameComparesRequests(t *testing.T) {
	a := &State{State: Waiting, Request: &Request{Code: "7f3", Kind: ForQuestion, Options: []string{"Yes", "No"}}}
	b := &State{State: Waiting, Request: &Request{Code: "7f3", Kind: ForQuestion, Options: []string{"Yes", "No"}}}
	if !Same(a, b) || !Same(nil, nil) || Same(a, nil) {
		t.Error("the same")
	}
	for _, change := range []func(*State){
		func(s *State) { s.Request.Code = "k2m" },
		func(s *State) { s.Request.Options = []string{"Yes"} },
		func(s *State) { s.Request.Options = []string{"No", "Yes"} },
		func(s *State) { s.Request = nil },
		func(s *State) { s.State = Idle },
	} {
		c := *b
		r := *b.Request
		c.Request = &r
		change(&c)
		if Same(a, &c) {
			t.Errorf("a change went unseen: %+v", c.Request)
		}
	}
}

// A new request gets a code of three of a-z and 2-9 without 0, 1, l and o,
// that no other state file in the folder has.
func TestARequestsCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w-dev-engineer.json")
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := &State{State: Waiting, Request: &Request{Kind: ForApproval}}
		GiveCode(path, s)
		if !CodeRule.MatchString(s.Request.Code) || strings.ContainsAny(s.Request.Code, "01lo") {
			t.Fatalf("code %q", s.Request.Code)
		}
		seen[s.Request.Code] = true
	}
	if len(seen) < 150 {
		t.Errorf("only %d codes in 200", len(seen))
	}
	// Taken by another member: never given again.
	for code := range seen {
		WriteState(filepath.Join(dir, "w-dev-"+code+".json"), &State{State: Waiting, Request: &Request{Code: code, Kind: ForApproval}})
	}
	for i := 0; i < 200; i++ {
		s := &State{State: Waiting, Request: &Request{Kind: ForApproval}}
		GiveCode(path, s)
		if seen[s.Request.Code] {
			t.Fatalf("%s was taken", s.Request.Code)
		}
	}
	// A request with a code keeps it.
	s := &State{State: Waiting, Request: &Request{Code: "7f3", Kind: ForApproval}}
	GiveCode(path, s)
	if s.Request.Code != "7f3" {
		t.Errorf("code %q", s.Request.Code)
	}
}
