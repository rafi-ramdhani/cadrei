package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// 0.2.3: the prompt log notes each request opening and ending, and how it
// ended, with nothing of what it asked.
func TestThePromptLogNotesOpensAndEnds(t *testing.T) {
	build := t.TempDir()
	now := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	a := &Request{Code: "7f3", Kind: ForApproval, Tool: "Bash", Summary: "echo TEST_TOKEN_PLACEHOLDER", Source: FromHook}
	b := &Request{Code: "k2m", Kind: ForPrompt, Question: "Do you want to proceed?", Source: FromScreen}
	one := &State{State: Waiting, Open: []*Request{a}}
	two := &State{State: Waiting, Open: []*Request{a, b}}
	LogPrompts(build, "w-dev-engineer", nil, one, Ending(runtime.EvPermission), now)
	LogPrompts(build, "w-dev-engineer", one, two, Ending(runtime.EvPromptShown), now)
	LogPrompts(build, "w-dev-engineer", two, two, Ending(runtime.EvToolUse), now) // nothing changed
	LogPrompts(build, "w-dev-engineer", two, &State{State: Waiting, Open: []*Request{b}}, Ending(runtime.EvToolDone), now)
	LogPrompts(build, "w-dev-engineer", &State{State: Waiting, Open: []*Request{b}}, &State{State: Idle}, Ending(runtime.EvStop), now)
	if c := CountPrompts(build); c != (PromptCounts{Opened: 2, InWindow: 1, Cancelled: 1}) {
		t.Errorf("counts %+v", c)
	}
	raw, _ := os.ReadFile(PromptLogPath(build))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], `"code":"7f3"`) || !strings.Contains(lines[1], `"source":"screen"`) ||
		!strings.Contains(lines[2], `"ended":"in_window"`) || !strings.Contains(lines[3], `"ended":"cancelled"`) {
		t.Errorf("log:\n%s", raw)
	}
	if strings.Contains(string(raw), "TEST_TOKEN_PLACEHOLDER") || strings.Contains(string(raw), "proceed") {
		t.Errorf("the log holds what was asked:\n%s", raw)
	}
}

// Past its limit the log moves aside, replacing the one before, so it
// keeps two files at most; the counts read both.
func TestThePromptLogIsBounded(t *testing.T) {
	build := t.TempDir()
	now := time.Now()
	r := &Request{Code: "7f3", Kind: ForApproval, Tool: "Bash"}
	open, none := &State{State: Waiting, Open: []*Request{r}}, &State{State: Working}
	for i := 0; i < 6000; i++ {
		LogPrompts(build, "w-dev-engineer", none, open, Ending(runtime.EvPermission), now)
		LogPrompts(build, "w-dev-engineer", open, none, Ending(runtime.EvToolDone), now)
	}
	path := PromptLogPath(build)
	old, err1 := os.Stat(path + ".1")
	cur, err2 := os.Stat(path)
	if err1 != nil || err2 != nil || old.Size() > promptLogLimit+1024 || cur.Size() > promptLogLimit+1024 {
		t.Fatalf("files: %v %v", err1, err2)
	}
	raw, _ := os.ReadFile(path)
	inCurrent := strings.Count(string(raw), `"event":"opened"`)
	if c := CountPrompts(build); c.Opened <= inCurrent || c.Opened > 6000 || c.InWindow < c.Opened-1 {
		t.Errorf("counts %+v, %d opened in the current file", c, inCurrent)
	}
}
