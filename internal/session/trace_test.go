package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 0.2.3: a trace counts the hook runs, those that never ended (killed by
// the runtime's timeout, or crashed) once they're a minute old, and those
// that ended without doing their work.
func TestATraceCountsHookRuns(t *testing.T) {
	path := TracePath(t.TempDir(), "w-dev-engineer")
	now := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)
	for _, l := range []TraceLine{
		{T: now.Add(-3 * time.Minute), Run: "a", Phase: TraceStart, Hook: "PreToolUse"},
		{T: now.Add(-3 * time.Minute), Run: "a", Phase: TraceEnd, Hook: "PreToolUse", Before: "working/0", After: "working/0"},
		{T: now.Add(-2 * time.Minute), Run: "b", Phase: TraceStart, Hook: "PermissionRequest"}, // killed
		{T: now.Add(-time.Minute), Run: "c", Phase: TraceStart, Hook: "PostToolUse"},
		{T: now.Add(-time.Minute), Run: "c", Phase: TraceEnd, Hook: "PostToolUse", Err: "busy"},
		{T: now.Add(-time.Second), Run: "d", Phase: TraceStart, Hook: "Stop"}, // still running
	} {
		if err := AppendTrace(path, l); err != nil {
			t.Fatal(err)
		}
	}
	if c := CountTrace(path, now); c != (TraceCounts{Runs: 4, Unfinished: 1, Failed: 1}) {
		t.Errorf("counts %+v", c)
	}
	if c := CountTrace(filepath.Join(t.TempDir(), "none.trace"), now); c != (TraceCounts{}) {
		t.Errorf("no trace: %+v", c)
	}
}

// A trace past its limit moves aside, replacing the one before, so a
// member keeps two files at most; the counts read both.
func TestATraceIsBounded(t *testing.T) {
	path := TracePath(t.TempDir(), "w-dev-engineer")
	now := time.Now()
	for i := 0; i < 3; i++ {
		for st, _ := os.Stat(path); st == nil || st.Size() <= traceLimit; st, _ = os.Stat(path) {
			AppendTrace(path, TraceLine{T: now, Run: "r" + strings.Repeat("x", 200), Phase: TraceEnd})
		}
		RotateTrace(path)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("round %d: not moved aside", i)
		}
	}
	AppendTrace(path, TraceLine{T: now, Run: "new", Phase: TraceStart})
	files, _ := filepath.Glob(path + "*")
	if len(files) != 2 {
		t.Errorf("files %v", files)
	}
	if st, _ := os.Stat(path + ".1"); st.Size() > traceLimit+1024 {
		t.Errorf("the old trace is %d bytes", st.Size())
	}
	if c := CountTrace(path, now); c.Runs != 1 {
		t.Errorf("counts %+v", c)
	}
	// Under the limit, it stays.
	RotateTrace(path)
	if _, err := os.Stat(path); err != nil {
		t.Error("a small trace moved")
	}
}

// A trace is never written through a symlink.
func TestATraceFollowsNoLink(t *testing.T) {
	dir := t.TempDir()
	path := TracePath(dir, "w-dev-engineer")
	os.MkdirAll(filepath.Dir(path), 0o755)
	target := filepath.Join(dir, "elsewhere")
	os.WriteFile(target, nil, 0o600)
	os.Symlink(target, path)
	if AppendTrace(path, TraceLine{Run: "a", Phase: TraceStart}) == nil {
		t.Error("wrote through a link")
	}
	if raw, _ := os.ReadFile(target); len(raw) != 0 {
		t.Errorf("the link's target got %q", raw)
	}
	if TracePath(dir, "../x") != "" || LockPath(dir, "../x") != "" {
		t.Error("paths for a name cadrei doesn't make")
	}
}
