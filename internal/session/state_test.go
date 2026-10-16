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
// request. 0.2.3: each is its own request, and a tool call starting
// leaves them open.
func TestARequestLastsWhileItWaits(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	ask := runtime.StateEvent{Name: runtime.EvPermission, Request: &runtime.Request{Tool: "Bash", Summary: "gh pr merge 42 --squash"}}
	s := Apply(nil, ask, now)
	if s.State != Waiting || len(s.Requests()) != 1 || !Same(&State{Open: s.Requests()}, &State{Open: []*Request{{Kind: ForApproval, Tool: "Bash", Summary: "gh pr merge 42 --squash", Source: FromHook}}}) {
		t.Fatalf("approval: %+v %+v", s, s.Current())
	}
	// Another request that names no call takes the place of this one.
	q := runtime.StateEvent{Name: runtime.EvPermission, Question: true, Request: &runtime.Request{Tool: "AskUserQuestion", Question: "Ship?", Options: []string{"Yes", "No"}}}
	s = Apply(s, q, now)
	if r := s.Requests(); len(r) != 1 || r[0].Kind != ForQuestion || r[0].Question != "Ship?" || len(r[0].Options) != 2 || r[0].Code != "" || s.WaitingFor != ForQuestion {
		t.Errorf("question: %+v", s.Requests())
	}
	// One of a call joins it.
	s = Apply(s, runtime.StateEvent{Name: runtime.EvToolUse, Tool: "Bash", ToolID: "toolu_b", InputKey: "k"}, now)
	s = Apply(s, runtime.StateEvent{Name: runtime.EvPermission, Tool: "Bash", InputKey: "k", Request: &runtime.Request{Tool: "Bash", Summary: "make"}}, now)
	if next := Apply(s, runtime.StateEvent{Name: runtime.EvToolUse}, now.Add(time.Minute)); next.State != Waiting || len(next.Requests()) != 2 {
		t.Errorf("a tool call starting dropped them: %+v", next)
	}
	for _, name := range []string{runtime.EvStop, runtime.EvPrompt, runtime.EvStopFailure, runtime.EvStart} {
		if next := Apply(s, runtime.StateEvent{Name: name}, now.Add(time.Minute)); next.Current() != nil || next.WaitingFor != "" {
			t.Errorf("%s kept the requests", name)
		}
	}
	// The runtime's idle reminder never ends a prompt cadrei has open.
	if next := Apply(s, runtime.StateEvent{Name: runtime.EvIdleReminder}, now.Add(time.Minute)); next.State != Waiting || len(next.Requests()) != 2 {
		t.Errorf("idle reminder: %+v", next)
	}
}

// Same compares states, their requests too.
func TestSameComparesRequests(t *testing.T) {
	a := &State{State: Waiting, Open: []*Request{{Code: "7f3", Kind: ForQuestion, Options: []string{"Yes", "No"}}}}
	b := &State{State: Waiting, Open: []*Request{{Code: "7f3", Kind: ForQuestion, Options: []string{"Yes", "No"}}}}
	if !Same(a, b) || !Same(nil, nil) || Same(a, nil) {
		t.Error("the same")
	}
	for _, change := range []func(*State){
		func(s *State) { s.Open[0].Code = "k2m" },
		func(s *State) { s.Open[0].Options = []string{"Yes"} },
		func(s *State) { s.Open[0].Options = []string{"No", "Yes"} },
		func(s *State) { s.Open[0].Call = "toolu_1" },
		func(s *State) { s.Open = nil },
		func(s *State) { s.Open = append(s.Open, &Request{Kind: ForApproval}) },
		func(s *State) { s.Calls = []Call{{ID: "toolu_1"}} },
		func(s *State) { s.State = Idle },
	} {
		c := *b
		r := *b.Open[0]
		c.Open = []*Request{&r}
		change(&c)
		if Same(a, &c) {
			t.Errorf("a change went unseen: %+v", c.Open)
		}
	}
}

// A new request gets a code of three of a-z and 2-9 without 0, 1, l and o,
// that no other state file in the folder has, nor another request in the
// same state.
func TestARequestsCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w-dev-engineer.json")
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := &State{State: Waiting, Open: []*Request{{Kind: ForApproval}}}
		GiveCode(path, s)
		if !CodeRule.MatchString(s.Current().Code) || strings.ContainsAny(s.Current().Code, "01lo") {
			t.Fatalf("code %q", s.Current().Code)
		}
		seen[s.Current().Code] = true
	}
	if len(seen) < 150 {
		t.Errorf("only %d codes in 200", len(seen))
	}
	// Taken by another member: never given again.
	for code := range seen {
		WriteState(filepath.Join(dir, "w-dev-"+code+".json"), &State{State: Waiting, Open: []*Request{{Code: code, Kind: ForApproval}}})
	}
	for i := 0; i < 200; i++ {
		s := &State{State: Waiting, Open: []*Request{{Kind: ForApproval}}}
		GiveCode(path, s)
		if seen[s.Current().Code] {
			t.Fatalf("%s was taken", s.Current().Code)
		}
	}
	// A request with a code keeps it, and the new one beside it gets
	// another.
	for i := 0; i < 50; i++ {
		s := &State{State: Waiting, Open: []*Request{{Code: "7f3", Kind: ForApproval}, {Kind: ForApproval}, {Kind: ForQuestion}}}
		GiveCode(path, s)
		r := s.Requests()
		if r[0].Code != "7f3" || r[1].Code == "" || r[1].Code == "7f3" || r[2].Code == r[1].Code || r[2].Code == "7f3" {
			t.Fatalf("codes %q %q %q", r[0].Code, r[1].Code, r[2].Code)
		}
	}
}
