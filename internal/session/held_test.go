package session

import (
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// PR D (N6): a call the guard refused or auto mode denied is over when its
// record opens. The record stays through the member's turns (a new
// prompt, a turn's end, an idle reminder, another tool's end), and the
// member never waits on it. A new session ends it, and so does HeldFor.
func TestAHeldRecordOutlivesTheTurn(t *testing.T) {
	t0 := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	s := Apply(nil, runtime.StateEvent{Name: runtime.EvStart, Session: "s1"}, t0)
	s = Apply(s, runtime.StateEvent{Name: runtime.EvPrompt}, t0.Add(time.Second))
	s = Apply(s, runtime.StateEvent{Name: runtime.EvToolUse, Tool: "Bash", ToolID: "toolu_1", InputKey: "k1"}, t0.Add(2*time.Second))
	s = Apply(s, runtime.StateEvent{Name: runtime.EvDenied, Tool: "Bash", ToolID: "toolu_1", Reason: "Pushing over history needs the user's consent.", Dir: "/src/app",
		Request: &runtime.Request{Tool: "Bash", Summary: "git push --force"}}, t0.Add(3*time.Second))
	if s.State != Working || s.WaitingFor != "" || s.Current() != nil || len(s.Calls) != 0 {
		t.Fatalf("after the denial: %+v", s)
	}
	held := s.HeldOnes()
	if len(held) != 1 || held[0].Kind != ForDenied || held[0].Summary != "git push --force" || held[0].Reason != "Pushing over history needs the user's consent." ||
		held[0].Dir != "/src/app" || !held[0].At.Equal(t0.Add(3*time.Second)) || held[0].Call != "" {
		t.Fatalf("held: %+v", held)
	}
	GiveCode(t.TempDir()+"/m.json", s)
	code := held[0].Code
	for i, ev := range []runtime.StateEvent{
		{Name: runtime.EvToolUse, Tool: "Bash", ToolID: "toolu_2"},
		// A call whose start was never seen ends the oldest same-tool
		// request with no call, but never a held one.
		{Name: runtime.EvToolDone, Tool: "Bash", ToolID: "toolu_x"},
		{Name: runtime.EvToolDone, Tool: "Bash", ToolID: "toolu_2"},
		{Name: runtime.EvStop},
		{Name: runtime.EvIdleReminder},
		{Name: runtime.EvPrompt},
		{Name: runtime.EvStopFailure},
	} {
		s = Apply(s, ev, t0.Add(time.Duration(i+4)*time.Minute))
		if h := s.HeldOnes(); len(h) != 1 || h[0].Code != code {
			t.Fatalf("event %d (%s) took the record: %+v", i, ev.Name, s)
		}
		if s.State == Waiting {
			t.Fatalf("event %d (%s): waiting on a held record", i, ev.Name)
		}
	}
	// A prompt it waits on comes first, and the catch-all still reads one
	// off the screen.
	s = Apply(s, runtime.StateEvent{Name: runtime.EvPromptShown, Request: &runtime.Request{Question: "Do it?", Options: []string{"Yes", "No"}}}, t0.Add(20*time.Minute))
	if s.State != Waiting || s.Current() == nil || s.Current().Kind != ForPrompt || len(s.Open) != 2 || len(s.Live()) != 1 {
		t.Fatalf("a screen prompt over a held record: %+v", s)
	}
	// A permission request that names no call takes the place of another
	// that names none, but not of a held record's.
	s = Apply(s, runtime.StateEvent{Name: runtime.EvPermission, Tool: "Write", Request: &runtime.Request{Tool: "Write", Summary: "/a"}}, t0.Add(21*time.Minute))
	if len(s.HeldOnes()) != 1 {
		t.Fatalf("a permission request took the held record: %+v", s)
	}
	// HeldFor after it opened, it's gone.
	s = Apply(s, runtime.StateEvent{Name: runtime.EvStop}, t0.Add(3*time.Second+HeldFor))
	if len(s.HeldOnes()) != 0 {
		t.Fatalf("still held past HeldFor: %+v", s)
	}
	// A new session ends a held record and an allowance.
	s.Open = []*Request{{Code: "abc", Kind: ForGuard, At: t0.Add(time.Hour)}}
	s.Allowed = []Allowance{{Code: "def", Key: "k", Until: t0.Add(2 * time.Hour)}}
	if s = Apply(s, runtime.StateEvent{Name: runtime.EvStart}, t0.Add(time.Hour+time.Minute)); len(s.Open) != 0 || len(s.Allowed) != 0 {
		t.Fatalf("a new session kept them: %+v", s)
	}
}

// A guard's refusal opens a held record, once for the same command; the
// member's own state stays as it is. A yes by its code turns it into an
// allowance for that command alone, used up by one run, and gone after
// AllowedFor.
func TestARefusalIsAllowedOnce(t *testing.T) {
	t0 := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	if s := Refuse(nil, Refusal("cadrei compact dev/pm", "No.", "/src", "compact"), t0); s.State != Unknown || len(s.HeldOnes()) != 1 {
		t.Fatalf("with no state: %+v", s)
	}
	idle := &State{State: Idle, Since: t0}
	key := CommandKey("/src", "cadrei compact dev/pm")
	s := Refuse(idle, Refusal("cadrei compact dev/pm", "No.", "/src", "compact"), t0.Add(time.Minute))
	if s.State != Idle || !s.Since.Equal(t0) || len(s.Open) != 1 || idle.Open != nil {
		t.Fatalf("refused: %+v (prev %+v)", s, idle)
	}
	r := s.Open[0]
	if r.Kind != ForGuard || r.Tool != "Bash" || r.Summary != "cadrei compact dev/pm" || r.Reason != "No." || r.Rule != "compact" || r.Key != key || r.Dir != "/src" {
		t.Fatalf("record: %+v", r)
	}
	GiveCode(t.TempDir()+"/m.json", s)
	code := r.Code
	// The same command refused again: the same record. Another: its own.
	s = Refuse(s, Refusal("cadrei compact dev/pm", "No.", "/src", "compact"), t0.Add(2*time.Minute))
	if len(s.Open) != 1 || s.Open[0].Code != code || !s.Open[0].At.Equal(t0.Add(time.Minute)) {
		t.Fatalf("again: %+v", s.Open)
	}
	s = Refuse(s, Refusal("cadrei compact dev/qa", "No.", "/src", "compact"), t0.Add(2*time.Minute))
	if len(s.Open) != 2 {
		t.Fatalf("another: %+v", s.Open)
	}
	// The same command in another folder is another record, under its
	// own key (review M3).
	if o := Refusal("cadrei compact dev/pm", "No.", "/other", "compact"); o.Key == key {
		t.Fatal("another folder, the same key")
	}
	if s.TakeAllowance(key, t0.Add(3*time.Minute)) {
		t.Fatal("an allowance before any yes")
	}
	if s.Allow("zzz", t0) || !s.Allow(code, t0.Add(3*time.Minute)) {
		t.Fatal("Allow")
	}
	if len(s.Open) != 1 || len(s.Allowed) != 1 || s.Allowed[0].Key != key || !s.Allowed[0].Until.Equal(t0.Add(3*time.Minute+AllowedFor)) {
		t.Fatalf("allowed: %+v", s)
	}
	if s.TakeAllowance(CommandKey("/src", "cadrei compact dev/pm "), t0.Add(4*time.Minute)) {
		t.Fatal("another command took the allowance")
	}
	if s.TakeAllowance(CommandKey("/other", "cadrei compact dev/pm"), t0.Add(4*time.Minute)) {
		t.Fatal("another folder took the allowance")
	}
	if !s.TakeAllowance(key, t0.Add(4*time.Minute)) || s.TakeAllowance(key, t0.Add(4*time.Minute)) {
		t.Fatal("the allowance isn't once")
	}
	s.Allowed = []Allowance{{Code: code, Key: key, Until: t0.Add(5 * time.Minute)}}
	if s.TakeAllowance(key, t0.Add(5*time.Minute)) {
		t.Fatal("an allowance past its time")
	}
	// Only a guard's refusal with a command's digest can be allowed.
	d := &State{Open: []*Request{{Code: "ddd", Kind: ForDenied, At: t0}, {Code: "ggg", Kind: ForGuard, At: t0}}}
	if d.Allow("ddd", t0) || d.Allow("ggg", t0) {
		t.Fatal("allowed a denial, or a refusal with no digest")
	}
}

// Held records and prompts count apart: refusals over and over drop the
// oldest refusal, never the prompt the member waits on.
func TestRefusalsNeverPushOutAPrompt(t *testing.T) {
	t0 := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	s := Apply(nil, runtime.StateEvent{Name: runtime.EvPermission, Tool: "Bash", Request: &runtime.Request{Tool: "Bash", Summary: "gh pr merge 54"}}, t0)
	for i := 0; i < MaxRequests+3; i++ {
		cmd := "cadrei compact dev/r" + strings.Repeat("x", i)
		s = Refuse(s, Refusal(cmd, "No.", "", "compact"), t0.Add(time.Duration(i)*time.Second))
	}
	if len(s.Live()) != 1 || s.Current().Summary != "gh pr merge 54" || len(s.HeldOnes()) != MaxRequests || s.State != Waiting {
		t.Fatalf("live %+v, %d held", s.Live(), len(s.HeldOnes()))
	}
	if s.HeldOnes()[0].Summary != "cadrei compact dev/rxxx" {
		t.Errorf("the oldest kept: %q", s.HeldOnes()[0].Summary)
	}
}

// A long command's record is cut as a hook cuts a summary, and kept whole
// in its own file, as an approval's is.
func TestARefusalKeepsALongCommandWhole(t *testing.T) {
	long := "echo " + strings.Repeat("x", runtime.SummaryLimit)
	r := Refusal(long, "No.", "relative/dir", "memory")
	if !strings.HasSuffix(r.Summary, "…") || r.full != long || r.Dir != "" || r.Altered {
		t.Fatalf("%+v", r)
	}
	// A tab or a control character shows otherwise, so the record says
	// its text isn't the command itself (review L3). A line break doesn't.
	for cmd, altered := range map[string]bool{"rm -f out\x01.o": true, "printf 'a\tb'": true, "echo a\necho b": false, "echo ✓": false} {
		if r := Refusal(cmd, "No.", "/src", "memory"); r.Altered != altered {
			t.Errorf("%q: altered %v", cmd, r.Altered)
		}
	}
}

// Expired drops what's past its time, as of now, and returns the same
// state when nothing is.
func TestExpiredDropsWhatsPastItsTime(t *testing.T) {
	t0 := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	s := &State{State: Idle, Open: []*Request{{Code: "abc", Kind: ForGuard, At: t0}, {Code: "def", Kind: ForApproval}},
		Allowed: []Allowance{{Code: "ghi", Key: "k", Until: t0.Add(AllowedFor)}}}
	if Expired(s, t0.Add(time.Minute)) != s {
		t.Error("changed with nothing past its time")
	}
	e := Expired(s, t0.Add(HeldFor))
	if e == s || len(e.Open) != 1 || e.Open[0].Code != "def" || len(e.Allowed) != 0 || len(s.Open) != 2 || len(s.Allowed) != 1 {
		t.Errorf("%+v (from %+v)", e, s)
	}
	if Expired(nil, t0) != nil {
		t.Error("nil")
	}
}
